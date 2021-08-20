// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/apps/v1"
	clusterv1alpha1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/cluster/v1alpha1"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
	"github.com/open-cluster-management/governance-policy-propagator/pkg/controller/common"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ReconcilePolicy) handleRootPolicy(instance *policiesv1.Policy) error {
	entry_ts := time.Now()
	defer func() {
		now := time.Now()
		elapsed := now.Sub(entry_ts).Seconds()
		roothandlerMeasure.Observe(elapsed)
	}()

	reqLogger := log.WithValues("Policy-Namespace", instance.GetNamespace(), "Policy-Name", instance.GetName())
	originalInstance := instance.DeepCopy()
	// flow -- if triggerred by user creating a new policy or updateing existing policy
	if instance.Spec.Disabled {
		// do nothing, clean up replicated policy
		// deleteReplicatedPolicy
		reqLogger.Info("Policy is disabled, doing clean up...")
		replicatedPlcList := &policiesv1.PolicyList{}
		err := r.client.List(context.TODO(), replicatedPlcList, client.MatchingLabels(common.LabelsForRootPolicy(instance)))
		if err != nil {
			// there was an error, requeue
			reqLogger.Error(err, "Failed to list replicated policy...")
			return err
		}
		for _, plc := range replicatedPlcList.Items {
			// #nosec G601 -- no memory addresses are stored in collections
			err := r.client.Delete(context.TODO(), &plc)
			if err != nil && !errors.IsNotFound(err) {
				reqLogger.Error(err, "Failed to delete replicated policy...", "Namespace", plc.GetNamespace(),
					"Name", plc.GetName())
				return err
			}
		}
		r.recorder.Event(instance, "Normal", "PolicyPropagation",
			fmt.Sprintf("Policy %s/%s was disabled", instance.GetNamespace(), instance.GetName()))
	}
	// get binding
	pbList := &policiesv1.PlacementBindingList{}
	err := r.client.List(context.TODO(), pbList, &client.ListOptions{Namespace: instance.GetNamespace()})
	if err != nil {
		reqLogger.Error(err, "Failed to list pb...")
		return err
	}
	// get placement
	placement := []*policiesv1.Placement{}
	// a set in the format of `namespace/name`
	allDecisions := map[string]struct{}{}
	for _, pb := range pbList.Items {
		subjects := pb.Subjects
		for _, subject := range subjects {
			if subject.APIGroup == policiesv1.SchemeGroupVersion.Group &&
				subject.Kind == policiesv1.Kind && subject.Name == instance.GetName() {
				decisions, p, err := getPlacementDecisions(r.client, pb, instance)
				if err != nil {
					return err
				}
				placement = append(placement, p)
				// only handle replicate policy when policy is not disabled
				if !instance.Spec.Disabled {
					// plr found, checking decision
					for _, decision := range decisions {
						key := fmt.Sprintf("%s/%s", decision.ClusterNamespace, decision.ClusterName)
						allDecisions[key] = struct{}{}
						// create/update replicated policy for each decision
						err := r.handleDecision(instance, decision)
						if err != nil {
							return err
						}
					}
				}
				// only handle first match in pb.spec.subjects
				break
			}
		}
	}
	status := []*policiesv1.CompliancePerClusterStatus{}
	if !instance.Spec.Disabled {
		// loop through all replciated policy, update status.status
		replicatedPlcList := &policiesv1.PolicyList{}
		err = r.client.List(context.TODO(), replicatedPlcList,
			client.MatchingLabels(common.LabelsForRootPolicy(instance)))
		if err != nil {
			// there was an error, requeue
			reqLogger.Error(err, "Failed to list replicated policy...",
				"MatchingLables", common.LabelsForRootPolicy(instance))
			return err
		}
		for _, rPlc := range replicatedPlcList.Items {
			status = append(status, &policiesv1.CompliancePerClusterStatus{
				ComplianceState:  rPlc.Status.ComplianceState,
				ClusterName:      rPlc.GetLabels()[common.ClusterNameLabel],
				ClusterNamespace: rPlc.GetLabels()[common.ClusterNamespaceLabel],
			})
		}
		sort.Slice(status, func(i, j int) bool {
			return status[i].ClusterName < status[j].ClusterName
		})
	}

	instance.Status.Status = status
	//loop through status and set ComplianceState
	instance.Status.ComplianceState = ""
	isCompliant := true
	for _, cpcs := range status {
		if cpcs.ComplianceState == "NonCompliant" {
			instance.Status.ComplianceState = policiesv1.NonCompliant
			isCompliant = false
			break
		} else if cpcs.ComplianceState == "" {
			isCompliant = false
		}
	}
	// set to compliant only when all status are compliant
	if len(status) > 0 && isCompliant {
		instance.Status.ComplianceState = policiesv1.Compliant
	}
	// looped through all pb, update status.placement
	sort.Slice(placement, func(i, j int) bool {
		return placement[i].PlacementBinding < placement[j].PlacementBinding
	})
	instance.Status.Placement = placement
	err = r.client.Status().Patch(context.TODO(), instance, client.MergeFrom(originalInstance))
	if err != nil && !errors.IsNotFound(err) {
		// failed to update instance.spec.placement, requeue
		reqLogger.Error(err, "Failed to update root policy status...")
		return err
	}

	// remove stale replicated policy based on allDecisions and status.status
	// if cluster exists in status.status but doesn't exist in allDecisions, then it's stale cluster.
	// we need to remove this replicated policy
	for _, cluster := range instance.Status.Status {
		key := fmt.Sprintf("%s/%s", cluster.ClusterNamespace, cluster.ClusterName)
		_, found := allDecisions[key]
		// not found in allDecision, orphan, delete it
		if !found {
			err := r.client.Delete(context.TODO(), &policiesv1.Policy{
				TypeMeta: metav1.TypeMeta{
					Kind:       policiesv1.Kind,
					APIVersion: policiesv1.SchemeGroupVersion.Group,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      common.FullNameForPolicy(instance),
					Namespace: cluster.ClusterNamespace,
				},
			})
			if err != nil && !errors.IsNotFound(err) {
				reqLogger.Error(err, "Failed to delete orphan policy...",
					"Namespace", cluster.ClusterNamespace, "Name", common.FullNameForPolicy(instance))
			}
		}
	}
	reqLogger.Info("Reconciliation complete.")
	return nil
}

// getApplicationPlacementDecisions return the placement decisions from an application
// lifecycle placementrule
func getApplicationPlacementDecisions(c client.Client, pb policiesv1.PlacementBinding, instance *policiesv1.Policy) ([]appsv1.PlacementDecision, *policiesv1.Placement, error) {
	plr := &appsv1.PlacementRule{}
	err := c.Get(context.TODO(), types.NamespacedName{Namespace: instance.GetNamespace(),
		Name: pb.PlacementRef.Name}, plr)
	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Failed to get plr...", "Namespace", instance.GetNamespace(), "Name",
			pb.PlacementRef.Name)
		return nil, nil, err
	}
	// plr found, add current plcmnt to placement
	placement := &policiesv1.Placement{
		PlacementBinding: pb.GetName(),
		PlacementRule:    plr.GetName(),
		// Decisions:        plr.Status.Decisions,
	}
	return plr.Status.Decisions, placement, nil
}

// getClusterPlacementDecisions return the placement decisions from cluster
// placement decisions
func getClusterPlacementDecisions(c client.Client, pb policiesv1.PlacementBinding, instance *policiesv1.Policy) ([]appsv1.PlacementDecision, *policiesv1.Placement, error) {
	plr := &clusterv1alpha1.Placement{}
	err := c.Get(context.TODO(), types.NamespacedName{Namespace: instance.GetNamespace(),
		Name: pb.PlacementRef.Name}, plr)
	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Failed to get plr...", "Namespace", instance.GetNamespace(), "Name",
			pb.PlacementRef.Name)
		return nil, nil, err
	}
	// plr found, add current plcmnt to placement
	placement := &policiesv1.Placement{
		PlacementBinding: pb.GetName(),
		Placement:        plr.GetName(),
		// Decisions:        plr.Status.Decisions,
	}
	list := &clusterv1alpha1.PlacementDecisionList{}
	lopts := &client.ListOptions{Namespace: instance.GetNamespace()}

	opts := client.MatchingLabels{"cluster.open-cluster-management.io/placement": plr.GetName()}
	opts.ApplyToList(lopts)
	err = c.List(context.TODO(), list, lopts)
	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Failed to get plr...", "Namespace", instance.GetNamespace(), "Name",
			pb.PlacementRef.Name)
		return nil, nil, err
	}
	var decisions []appsv1.PlacementDecision
	decisions = make([]appsv1.PlacementDecision, 0, 100)
	for _, item := range list.Items {
		for _, cluster := range item.Status.Decisions {
			decided := &appsv1.PlacementDecision{
				ClusterName:      cluster.ClusterName,
				ClusterNamespace: cluster.ClusterName,
			}
			decisions = append(decisions, *decided)
		}
	}
	return decisions, placement, nil
}

// getPlacementDecisions gets the PlacementDecisions for a PlacementBinding
func getPlacementDecisions(c client.Client, pb policiesv1.PlacementBinding,
	instance *policiesv1.Policy) ([]appsv1.PlacementDecision, *policiesv1.Placement, error) {
	if pb.PlacementRef.APIGroup == appsv1.SchemeGroupVersion.Group &&
		pb.PlacementRef.Kind == appsv1.Kind {
		d, placement, err := getApplicationPlacementDecisions(c, pb, instance)
		if err != nil {
			return nil, nil, err
		}
		return d, placement, nil
	} else if pb.PlacementRef.APIGroup == clusterv1alpha1.SchemeGroupVersion.Group &&
		pb.PlacementRef.Kind == clusterv1alpha1.Kind {
		d, placement, err := getClusterPlacementDecisions(c, pb, instance)
		if err != nil {
			return nil, nil, err
		}
		return d, placement, nil
	}
	return nil, nil, fmt.Errorf("Placement binding %s/%s reference is not valid", pb.Name, pb.Namespace)
}

func (r *ReconcilePolicy) handleDecision(instance *policiesv1.Policy, decision appsv1.PlacementDecision) error {
	reqLogger := log.WithValues("Policy-Namespace", instance.GetNamespace(), "Policy-Name", instance.GetName())
	// retrieve replicated policy in cluster namespace
	replicatedPlc := &policiesv1.Policy{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: decision.ClusterNamespace,
		Name: common.FullNameForPolicy(instance)}, replicatedPlc)
	if err != nil {
		if errors.IsNotFound(err) {
			// not replicated, need to create
			replicatedPlc = instance.DeepCopy()
			replicatedPlc.SetName(common.FullNameForPolicy(instance))
			replicatedPlc.SetNamespace(decision.ClusterNamespace)
			replicatedPlc.SetResourceVersion("")
			replicatedPlc.SetFinalizers(nil)
			labels := replicatedPlc.GetLabels()
			if labels == nil {
				labels = map[string]string{}
			}
			labels[common.ClusterNameLabel] = decision.ClusterName
			labels[common.ClusterNamespaceLabel] = decision.ClusterNamespace
			labels[common.RootPolicyLabel] = common.FullNameForPolicy(instance)
			replicatedPlc.SetLabels(labels)

			// Make sure the Owner Reference is cleared
			replicatedPlc.SetOwnerReferences(nil)

			reqLogger.Info("Creating replicated policy...", "Namespace", decision.ClusterNamespace,
				"Name", common.FullNameForPolicy(instance))
			err = r.client.Create(context.TODO(), replicatedPlc)
			if err != nil {
				// failed to create replicated object, requeue
				reqLogger.Error(err, "Failed to create replicated policy...", "Namespace", decision.ClusterNamespace,
					"Name", common.FullNameForPolicy(instance))
				return err
			}
			r.recorder.Event(instance, "Normal", "PolicyPropagation",
				fmt.Sprintf("Policy %s/%s was propagated to cluster %s/%s", instance.GetNamespace(),
					instance.GetName(), decision.ClusterNamespace, decision.ClusterName))
		} else {
			// failed to get replicated object, requeue
			reqLogger.Error(err, "Failed to get replicated policy...", "Namespace", decision.ClusterNamespace,
				"Name", common.FullNameForPolicy(instance))
			return err
		}

	}
	// replicated policy already created, need to compare and patch
	// compare annotation
	if !common.CompareSpecAndAnnotation(instance, replicatedPlc) {
		// update needed
		reqLogger.Info("Root policy and Replicated policy mismatch, updating replicated policy...",
			"Namespace", replicatedPlc.GetNamespace(), "Name", replicatedPlc.GetName())
		replicatedPlc.SetAnnotations(instance.GetAnnotations())
		replicatedPlc.Spec = instance.Spec
		err = r.client.Update(context.TODO(), replicatedPlc)
		if err != nil {
			reqLogger.Error(err, "Failed to update replicated policy...",
				"Namespace", replicatedPlc.GetNamespace(), "Name", replicatedPlc.GetName())
			return err
		}
		r.recorder.Event(instance, "Normal", "PolicyPropagation",
			fmt.Sprintf("Policy %s/%s was updated for cluster %s/%s", instance.GetNamespace(),
				instance.GetName(), decision.ClusterNamespace, decision.ClusterName))
	}
	return nil
}
