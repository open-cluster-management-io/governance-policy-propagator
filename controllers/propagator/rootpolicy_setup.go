// Copyright (c) 2023 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/finalizers,verbs=update
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=placementbindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policysets,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters;placementdecisions;placements,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=*,resources=*,verbs=get;list;watch

// SetupWithManager sets up the controller with the Manager.
func (r *RootPolicyReconciler) SetupWithManager(mgr ctrl.Manager, maxConcurrentReconciles uint) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: int(maxConcurrentReconciles)}).
		Named("root-policy-spec").
		For(
			&policiesv1.Policy{},
			builder.WithPredicates(rootPolicyNonStatusUpdates())).
		Watches(
			&policiesv1beta1.PolicySet{},
			handler.EnqueueRequestsFromMapFunc(mapPolicySetToPolicies),
			builder.WithPredicates(policySetPolicyListChanged())).
		Complete(r)
}

// policyNonStatusUpdates triggers reconciliation if the Policy has had a change that is not just
// a status update.
func rootPolicyNonStatusUpdates() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			_, isReplicated := e.Object.GetLabels()[common.RootPolicyLabel]

			return !isReplicated
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, isReplicated := e.Object.GetLabels()[common.RootPolicyLabel]

			return !isReplicated
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			_, newIsReplicated := e.ObjectNew.GetLabels()[common.RootPolicyLabel]
			_, oldIsReplicated := e.ObjectOld.GetLabels()[common.RootPolicyLabel]

			// if either has the label, it is a replicated policy
			if oldIsReplicated || newIsReplicated {
				return false
			}

			// Ignore pure status updates since those are handled by a separate controller
			return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() ||
				!equality.Semantic.DeepEqual(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels()) ||
				!equality.Semantic.DeepEqual(e.ObjectOld.GetAnnotations(), e.ObjectNew.GetAnnotations())
		},
	}
}

// mapPolicySetToPolicies maps a PolicySet to all the Policies in its policies list.
func mapPolicySetToPolicies(_ context.Context, object client.Object) []reconcile.Request {
	log := log.WithValues("policySetName", object.GetName(), "namespace", object.GetNamespace())
	log.V(2).Info("Reconcile Request for PolicySet")

	var result []reconcile.Request

	//nolint:forcetypeassert
	policySet := object.(*policiesv1beta1.PolicySet)

	for _, plc := range policySet.Spec.Policies {
		log.V(2).Info("Found reconciliation request from a policyset", "policyName", string(plc))

		request := reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      string(plc),
			Namespace: object.GetNamespace(),
		}}
		result = append(result, request)
	}

	return result
}

// policySetPolicyListChanged triggers reconciliation if the list of policies in the PolicySet has
// changed, or if the PolicySet was just created or deleted.
func policySetPolicyListChanged() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			//nolint:forcetypeassert
			policySetObjNew := e.ObjectNew.(*policiesv1beta1.PolicySet)
			//nolint:forcetypeassert
			policySetObjOld := e.ObjectOld.(*policiesv1beta1.PolicySet)

			return !equality.Semantic.DeepEqual(policySetObjNew.Spec.Policies, policySetObjOld.Spec.Policies)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
	}
}
