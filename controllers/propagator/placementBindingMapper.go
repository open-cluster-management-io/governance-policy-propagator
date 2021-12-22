// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func placementBindingMapper(c client.Client) handler.MapFunc {
	return func(obj client.Object) []reconcile.Request {
		// nolint: forcetypeassert
		object := obj.(*policiesv1.PlacementBinding)
		var result []reconcile.Request

		log := log.WithValues("name", object.GetName(), "namespace", object.GetNamespace())

		log.V(2).Info("Reconcile request for a PlacementBinding")

		subjects := object.Subjects
		for _, subject := range subjects {
			if subject.APIGroup == policiesv1.SchemeGroupVersion.Group && subject.Kind == policiesv1.Kind {
				log.V(2).Info("Found reconciliation request from placement binding", "policyName", subject.Name)

				request := reconcile.Request{NamespacedName: types.NamespacedName{
					Name:      subject.Name,
					Namespace: object.GetNamespace(),
				}}
				result = append(result, request)
			}
		}

		return result
	}
}
