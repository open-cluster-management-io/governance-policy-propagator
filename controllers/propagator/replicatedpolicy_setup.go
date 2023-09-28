package propagator

import (
	"k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

func (r *ReplicatedPolicyReconciler) SetupWithManager(mgr ctrl.Manager, additionalSources ...source.Source) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		Named("replicated-policy").
		For(
			&policiesv1.Policy{},
			builder.WithPredicates(replicatedPolicyPredicates()),
		)

	for _, source := range additionalSources {
		builder.WatchesRawSource(source, &handler.EnqueueRequestForObject{})
	}

	return builder.Complete(r)
}

// replicatedPolicyPredicates triggers reconciliation if the policy is a replicated policy, and is
// not a pure status update.
func replicatedPolicyPredicates() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			_, isReplicated := e.Object.GetLabels()[common.RootPolicyLabel]

			// NOTE: can we ignore the create event from when this controller creates the resource?
			return isReplicated
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, isReplicated := e.Object.GetLabels()[common.RootPolicyLabel]

			// NOTE: can we ignore the delete event from when this controller deletes the resource?
			return isReplicated
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			_, newIsReplicated := e.ObjectNew.GetLabels()[common.RootPolicyLabel]
			_, oldIsReplicated := e.ObjectOld.GetLabels()[common.RootPolicyLabel]

			// if neither has the label, it is not a replicated policy
			if !(oldIsReplicated || newIsReplicated) {
				return false
			}

			// NOTE: can we ignore updates where we've already reconciled the new ResourceVersion?
			// Ignore pure status updates since those are handled by a separate controller
			return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() ||
				!equality.Semantic.DeepEqual(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels()) ||
				!equality.Semantic.DeepEqual(e.ObjectOld.GetAnnotations(), e.ObjectNew.GetAnnotations())
		},
	}
}
