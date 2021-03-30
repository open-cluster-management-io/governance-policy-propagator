// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"fmt"
	"sort"

	appsv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/apps/v1"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
	"github.com/open-cluster-management/governance-policy-propagator/pkg/controller/common"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ReconcilePolicy) handleRootPolicy(instance *policiesv1.Policy) error {
	reqLogger := log.WithValues("Policy-Namespace", instance.GetNamespace(), "Policy-Name", instance.GetName())
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
	allDecisions := []appsv1.PlacementDecision{}
	for _, pb := range pbList.Items {
		subjects := pb.Subjects
		for _, subject := range subjects {
			if subject.APIGroup == policiesv1.SchemeGroupVersion.Group &&
				subject.Kind == policiesv1.Kind && subject.Name == instance.GetName() {
				plr := &appsv1.PlacementRule{}
				err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: instance.GetNamespace(),
					Name: pb.PlacementRef.Name}, plr)
				if err != nil && !errors.IsNotFound(err) {
					reqLogger.Error(err, "Failed to get plr...", "Namespace", instance.GetNamespace(), "Name",
						pb.PlacementRef.Name)
					return err
				}
				// plr found, add current plcmnt to placement
				placement = append(placement, &policiesv1.Placement{
					PlacementBinding: pb.GetName(),
					PlacementRule:    plr.GetName(),
					// Decisions:        plr.Status.Decisions,
				})
				// only handle replicate policy when policy is not disabled
				if !instance.Spec.Disabled {
					// plr found, checking decision
					decisions := plr.Status.Decisions
					for _, decision := range decisions {
						allDecisions = append(allDecisions, decision)
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
			if status == nil {
				status = []*policiesv1.CompliancePerClusterStatus{
					{
						ComplianceState:  rPlc.Status.ComplianceState,
						ClusterName:      rPlc.GetLabels()[common.ClusterNameLabel],
						ClusterNamespace: rPlc.GetLabels()[common.ClusterNamespaceLabel],
					},
				}
			} else {
				// loop through status and update
				found := false
				for _, compliancePerClusterStatus := range status {
					if compliancePerClusterStatus.ClusterName == rPlc.GetLabels()[common.ClusterNameLabel] {
						// found existing entry, check if it needs updating
						found = true
						compliancePerClusterStatus.ClusterNamespace = rPlc.GetLabels()[common.ClusterNamespaceLabel]
						compliancePerClusterStatus.ComplianceState = rPlc.Status.ComplianceState
						break
					}
				}
				// not found, it's a CompliancePerClusterStatus, add it
				if !found {
					status = append(status, &policiesv1.CompliancePerClusterStatus{
						ComplianceState:  rPlc.Status.ComplianceState,
						ClusterName:      rPlc.GetLabels()[common.ClusterNameLabel],
						ClusterNamespace: rPlc.GetLabels()[common.ClusterNamespaceLabel],
					})
				}
			}
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
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil && !errors.IsNotFound(err) {
		// failed to update instance.spec.placement, requeue
		reqLogger.Error(err, "Failed to update root policy status...")
		return err
	}

	// remove stale replicated policy based on allDecisions and status.status
	// if cluster exists in status.status but doesn't exist in allDecisions, then it's stale cluster.
	// we need to remove this replicated policy
	for _, cluster := range instance.Status.Status {
		found := false
		for _, decision := range allDecisions {
			if decision.ClusterName == cluster.ClusterName &&
				decision.ClusterNamespace == cluster.ClusterNamespace {
				found = true
				break
			}
		}
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
			labels := replicatedPlc.GetLabels()
			if labels == nil {
				labels = map[string]string{}
			}
			labels[common.ClusterNameLabel] = decision.ClusterName
			labels[common.ClusterNamespaceLabel] = decision.ClusterNamespace
			labels[common.RootPolicyLabel] = common.FullNameForPolicy(instance)
			replicatedPlc.SetLabels(labels)
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
