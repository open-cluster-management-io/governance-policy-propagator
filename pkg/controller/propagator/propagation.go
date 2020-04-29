package propagator

import (
	"context"
	"strings"

	appsv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/apps/v1"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ReconcilePolicy) handleRootPolicy(instance *policiesv1.Policy) error {
	// flow -- if triggerred by user creating a new policy or updateing existing policy
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
			// plr found, update status.placement
			// placement := policiesv1.Placement{PlacementBinding: decision.}
			// r.client.Status().Patch(context.TODO())
			// plr found, checking decision
			decisions := plr.Status.Decisions
			if decisions == nil || len(decisions) == 0 {
				// no decision, skipping
				return nil
			}
			for _, decision := range decisions {
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
						return r.client.Create(context.TODO(), replicatedPlc)
					}
					// failed to get replicated object, requeue
					return err
				}
				// replicated policy already created, need to compare and patch
				return nil
			}
		}
	}

	// 2. check if placementrule and decision exists

	// 3. replicate
	// 4. update status with binding and placementrule

	// flow -- if triggerred by user updateing existing policy
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
		for _, compliancePerClusterStatus := range status {
			if compliancePerClusterStatus.ClusterName == instance.GetLabels()["cluster-name"] && compliancePerClusterStatus.ClusterNamespace == instance.GetLabels()["cluster-namespace"] {
				// found existing entry, check if it needs updating
				if compliancePerClusterStatus.ComplianceState != instance.Status.ComplianceState {
					compliancePerClusterStatus.ComplianceState = instance.Status.ComplianceState
				}
				break
			}
		}
	}
	rootPls.Status.Status = status
	err = r.client.Status().Update(context.TODO(), rootPls)
	if err != nil {
		return err
	}
	return nil
}
