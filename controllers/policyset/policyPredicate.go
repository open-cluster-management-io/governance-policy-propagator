// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

var policyPredicateFuncs = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		// nolint: forcetypeassert
		policyObjNew := e.ObjectNew.(*policiesv1.Policy)
		// nolint: forcetypeassert
		policyObjOld := e.ObjectOld.(*policiesv1.Policy)

		return !equality.Semantic.DeepEqual(policyObjNew.Status, policyObjOld.Status)
	},
	CreateFunc: func(e event.CreateEvent) bool {
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return true
	},
}
