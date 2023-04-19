// Copyright Contributors to the Open Cluster Management project

package policystatus

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

// policyStatusPredicate will filter out all policy events except for updates where the generation is the same, which
// implies the status has been updated. If the generation changes, the main policy controller will handle it.
func policyStatusPredicate() predicate.Funcs {
	return predicate.Funcs{
		// Creations are handled by the main policy controller.
		CreateFunc: func(e event.CreateEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			//nolint:forcetypeassert
			oldPolicy := e.ObjectOld.(*policiesv1.Policy)
			//nolint:forcetypeassert
			updatedPolicy := e.ObjectNew.(*policiesv1.Policy)

			// If there was an update and the generation is the same, the status must have changed.
			return oldPolicy.Generation == updatedPolicy.Generation
		},
		// Deletions are handled by the main policy controller.
		DeleteFunc: func(e event.DeleteEvent) bool { return false },
	}
}
