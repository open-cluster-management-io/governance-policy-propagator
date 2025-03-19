// Copyright Contributors to the Open Cluster Management project

package automation

import (
	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

// we only want to watch for pb contains policy as subjects
var policyPredicateFuncs = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		//nolint:forcetypeassert
		plcObjNew := e.ObjectNew.(*policiesv1.Policy)
		if _, ok := plcObjNew.Labels["policy.open-cluster-management.io/root-policy"]; ok {
			return false
		}

		//nolint:forcetypeassert
		plcObjOld := e.ObjectOld.(*policiesv1.Policy)

		return !cmp.Equal(plcObjNew.Status.Status, plcObjOld.Status.Status)
	},
	CreateFunc: func(_ event.CreateEvent) bool {
		return false
	},
	DeleteFunc: func(_ event.DeleteEvent) bool {
		return false
	},
}
