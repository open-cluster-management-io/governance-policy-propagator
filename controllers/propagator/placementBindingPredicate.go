// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

// we only want to watch for pb contains policy and policyset as subjects
var pbPredicateFuncs = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		//nolint:forcetypeassert
		pbObjNew := e.ObjectNew.(*policiesv1.PlacementBinding)
		//nolint:forcetypeassert
		pbObjOld := e.ObjectOld.(*policiesv1.PlacementBinding)

		return common.IsForPolicyOrPolicySet(pbObjNew) || common.IsForPolicyOrPolicySet(pbObjOld)
	},
	CreateFunc: func(e event.CreateEvent) bool {
		//nolint:forcetypeassert
		pbObj := e.Object.(*policiesv1.PlacementBinding)

		return common.IsForPolicyOrPolicySet(pbObj)
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		//nolint:forcetypeassert
		pbObj := e.Object.(*policiesv1.PlacementBinding)

		return common.IsForPolicyOrPolicySet(pbObj)
	},
}
