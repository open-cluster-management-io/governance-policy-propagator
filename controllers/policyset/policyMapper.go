// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func policyMapper(c client.Client) handler.MapFunc {
	return func(object client.Object) []reconcile.Request {
		log := log.WithValues("policyName", object.GetName(), "namespace", object.GetNamespace())
		log.V(2).Info("Reconcile Request for Policy")

		var result []reconcile.Request

		for _, plcmt := range object.(*policiesv1.Policy).Status.Placement {
			// iterate through placement looking for policyset
			if plcmt.PolicySet != "" {
				log.V(2).Info("Found reconciliation request from a policy", "policySetName", plcmt.PolicySet)

				request := reconcile.Request{NamespacedName: types.NamespacedName{
					Name:      plcmt.PolicySet,
					Namespace: object.GetNamespace(),
				}}
				result = append(result, request)
			}
		}

		return result
	}
}
