package propagator

import (
	"context"
	"strings"

	appsv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/apps/v1"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ReconcilePolicy) handleRootPolicy(instance *policiesv1.Policy) error {
	// flow -- if triggerred by user creating a new policy or updateing existing policy
	if instance.Spec.Disabled {
		// do nothing, clean up replicated policy
		// deleteReplicatedPolicy
		return nil
	}
	// 1. get binding
	pbList := &policiesv1.PlacementBindingList{}
	err := r.client.List(context.TODO(), pbList, &client.ListOptions{Namespace: instance.GetNamespace()})
	if err != nil {
		// reqLogger.Info("Failed to list pb, going to retry...")
		return err
	}
	// 1.1 if doesn't exist -> skip
	if pbList == nil {
		return nil
	}
	// 1.2 if exists -> step 2
	placement := []*policiesv1.Placement{}
	allDecisions := []appsv1.PlacementDecision{}
	for _, pb := range pbList.Items {
		if pb.Spec.Subject.APIGroup == policiesv1.SchemeGroupVersion.Group && pb.Spec.Subject.Kind == policiesv1.Kind && pb.Spec.Subject.Name == instance.GetName() {
			plr := &appsv1.PlacementRule{}
			err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: instance.GetNamespace(), Name: pb.Spec.PlacementRef.Name}, plr)
			if err != nil {
				if errors.IsNotFound(err) {
					// not found doing nothing
				}
				// reqLogger.Info("Failed to get plr, going to retry...")
				return err
			}
			// plr found, add current plcmnt to placement
			placement = append(placement, &policiesv1.Placement{
				PlacementBinding: pb.GetName(),
				PlacementRule:    plr.GetName(),
			})
			// plr found, checking decision
			decisions := plr.Status.Decisions
			for _, decision := range decisions {
				allDecisions = append(allDecisions, decision)
				// retrieve replicated policy in cluster namespace
				replicatedPlc := &policiesv1.Policy{}
				err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: decision.ClusterNamespace, Name: instance.GetNamespace() + "." + instance.GetName()}, replicatedPlc)
				if err != nil {
					if errors.IsNotFound(err) {
						// not replicated, need to create
						replicatedPlc = instance.DeepCopy()
						replicatedPlc.SetName(instance.GetNamespace() + "." + instance.GetName())
						replicatedPlc.SetNamespace(decision.ClusterNamespace)
						replicatedPlc.SetResourceVersion("")
						ownerReferences := []metav1.OwnerReference{
							*metav1.NewControllerRef(instance, schema.GroupVersionKind{
								Group:   policiesv1.SchemeGroupVersion.Group,
								Version: policiesv1.SchemeGroupVersion.Version,
								Kind:    policiesv1.Kind,
							}),
						}
						replicatedPlc.SetOwnerReferences(ownerReferences)
						labels := replicatedPlc.GetLabels()
						if labels == nil {
							labels = map[string]string{"cluster-name": decision.ClusterName, "cluster-namespace": decision.ClusterNamespace}
						} else {
							labels["cluster-name"] = decision.ClusterName
							labels["cluster-namespace"] = decision.ClusterNamespace
						}
						replicatedPlc.SetLabels(labels)
						err = r.client.Create(context.TODO(), replicatedPlc)
						if err != nil {
							return err
						}
					} else {
						// failed to get replicated object, requeue
						return err
					}

				}
				// replicated policy already created, need to compare and patch
				// compare annotation
				if !equality.Semantic.DeepEqual(instance.GetAnnotations(), replicatedPlc.GetAnnotations()) || !equality.Semantic.DeepEqual(instance.Spec, replicatedPlc.Spec) {
					// update needed
					replicatedPlc.SetAnnotations(instance.GetAnnotations())
					replicatedPlc.Spec = instance.Spec
					err = r.client.Update(context.TODO(), replicatedPlc)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	// looped through all pb, update status.placement
	instance.Status.Placement = placement
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// not found doing nothing, why? shouldn't happen?
		}
		// failed to update instance.spec.placement, requeue
		return err
	}

	// remove stale replicated policy based on allDecisions and status.status
	// if cluster exists in status.status but doesn't exist in allDecistion, then it's stale cluster, we need to remove this replicated policy
	for _, cluster := range instance.Status.Status {
		found := false
		for _, decision := range allDecisions {
			if decision.ClusterName == cluster.ClusterName && decision.ClusterNamespace == cluster.ClusterNamespace {
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
					Name:      instance.GetNamespace() + "." + instance.GetName(),
					Namespace: cluster.ClusterNamespace,
				},
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *ReconcilePolicy) handleReplicatedPolicy(instance *policiesv1.Policy) error {
	// flow -- triggered by status of replicated policy was updated
	// 1. check if status has change
	// 2. aggregate status in root policy
	nameSplit := strings.Split(instance.GetName(), ".")
	if len(nameSplit) != 2 {
		// skipping, this replicated policy didn't follow %{namespace}.%{name} nameing convention
		// doing nothing, skipping
		return nil
	}
	rootPlsNs := nameSplit[0]
	rootPlsName := nameSplit[1]
	rootPls := &policiesv1.Policy{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: rootPlsNs, Name: rootPlsName}, rootPls)
	if err != nil {
		if errors.IsNotFound(err) {
			// root policy doesn't exist
			// impossible situation
			// skipping
			return nil
		}
		// other error
		return err
	}
	// rootPls found, update status
	status := rootPls.Status.Status
	if status == nil {
		status = []*policiesv1.CompliancePerClusterStatus{
			&policiesv1.CompliancePerClusterStatus{
				ComplianceState:  instance.Status.ComplianceState,
				ClusterName:      instance.GetLabels()["cluster-name"],
				ClusterNamespace: instance.GetLabels()["cluster-namespace"],
			},
		}
	} else {
		// loop through status and update
		found := false
		for _, compliancePerClusterStatus := range status {
			if compliancePerClusterStatus.ClusterName == instance.GetLabels()["cluster-name"] && compliancePerClusterStatus.ClusterNamespace == instance.GetLabels()["cluster-namespace"] {
				// found existing entry, check if it needs updating
				found = true
				if compliancePerClusterStatus.ComplianceState != instance.Status.ComplianceState {
					compliancePerClusterStatus.ComplianceState = instance.Status.ComplianceState
				}
				break
			}
		}
		// not found, it's a CompliancePerClusterStatus, add it
		if !found {
			status = append(status, &policiesv1.CompliancePerClusterStatus{
				ComplianceState:  instance.Status.ComplianceState,
				ClusterName:      instance.GetLabels()["cluster-name"],
				ClusterNamespace: instance.GetLabels()["cluster-namespace"],
			})
		}
	}
	rootPls.Status.Status = status
	err = r.client.Status().Update(context.TODO(), rootPls)
	if err != nil {
		return err
	}
	return nil
}
