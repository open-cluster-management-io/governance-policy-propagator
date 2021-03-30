// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type placementBindingMapper struct {
	client.Client
}

func (mapper *placementBindingMapper) Map(obj handler.MapObject) []reconcile.Request {
	object := obj.Object.(*policiesv1.PlacementBinding)
	var result []reconcile.Request
	subjects := object.Subjects
	for _, subject := range subjects {
		if subject.APIGroup == policiesv1.SchemeGroupVersion.Group && subject.Kind == policiesv1.Kind {
			log.Info("Found reconciliation request from placement binding...",
				"Namespace", object.GetNamespace(), "Name", object.GetName(), "Policy-Name", subject.Name)
			request := reconcile.Request{NamespacedName: types.NamespacedName{
				Name:      subject.Name,
				Namespace: object.GetNamespace(),
			}}
			result = append(result, request)
		}
	}
	return result
}
