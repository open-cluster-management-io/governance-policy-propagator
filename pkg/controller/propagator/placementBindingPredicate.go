// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
	"github.com/open-cluster-management/governance-policy-propagator/pkg/controller/common"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// we only want to watch for pb contains policy as subjects
var pbPredicateFuncs = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		pbObjNew := e.ObjectNew.(*policiesv1.PlacementBinding)
		pbObjOld := e.ObjectOld.(*policiesv1.PlacementBinding)
		return common.IsPbForPoicy(pbObjNew) || common.IsPbForPoicy(pbObjOld)
	},
	CreateFunc: func(e event.CreateEvent) bool {
		pbObj := e.Object.(*policiesv1.PlacementBinding)
		return common.IsPbForPoicy(pbObj)
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		pbObj := e.Object.(*policiesv1.PlacementBinding)
		return common.IsPbForPoicy(pbObj)
	},
}
