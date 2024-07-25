// Copyright (c) 2023 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

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
//+kubebuilder:rbac:groups=core,resources=serviceaccounts/token,verbs=create
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
			&common.EnqueueRequestsFromPolicySet{}).
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
