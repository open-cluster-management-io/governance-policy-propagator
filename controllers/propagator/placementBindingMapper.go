// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

func placementBindingMapper(c client.Client) handler.MapFunc {
	return func(obj client.Object) []reconcile.Request {
		// nolint: forcetypeassert
		object := obj.(*policiesv1.PlacementBinding)
		var result []reconcile.Request

		log := log.WithValues("placementBindingName", object.GetName(), "namespace", object.GetNamespace())

		log.V(2).Info("Reconcile request for a PlacementBinding")

		subjects := object.Subjects
		for _, subject := range subjects {
			if subject.APIGroup == policiesv1.SchemeGroupVersion.Group {
				if subject.Kind == policiesv1.Kind {
					log.V(2).Info("Found reconciliation request from policy placement binding",
						"policyName", subject.Name)

					request := reconcile.Request{NamespacedName: types.NamespacedName{
						Name:      subject.Name,
						Namespace: object.GetNamespace(),
					}}
					result = append(result, request)
				} else if subject.Kind == policiesv1.PolicySetKind {
					policySetNamespacedName := types.NamespacedName{
						Name:      subject.Name,
						Namespace: object.GetNamespace(),
					}
					policySet := &policiesv1beta1.PolicySet{}
					err := c.Get(context.TODO(), policySetNamespacedName, policySet)
					if err != nil {
						log.V(2).Info("Failed to retrieve policyset referenced in placementbinding",
							"policySetName", subject.Name, "error", err)

						continue
					}
					policies := policySet.Spec.Policies
					for _, plc := range policies {
						log.V(2).Info("Found reconciliation request from policyset placement bindng",
							"policySetName", subject.Name, "policyName", string(plc))
						request := reconcile.Request{NamespacedName: types.NamespacedName{
							Name:      string(plc),
							Namespace: object.GetNamespace(),
						}}
						result = append(result, request)
					}
				}
			}
		}

		return result
	}
}
