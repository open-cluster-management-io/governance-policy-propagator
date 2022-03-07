// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

func policySetMapper(c client.Client) handler.MapFunc {
	return func(object client.Object) []reconcile.Request {
		log := log.WithValues("policySetName", object.GetName(), "namespace", object.GetNamespace())
		log.V(2).Info("Reconcile Request for PolicySet")

		var result []reconcile.Request

		for _, plc := range object.(*policiesv1beta1.PolicySet).Spec.Policies {
			log.V(2).Info("Found reconciliation request from a policyset", "policyName", string(plc))

			request := reconcile.Request{NamespacedName: types.NamespacedName{
				Name:      string(plc),
				Namespace: object.GetNamespace(),
			}}
			result = append(result, request)
		}

		return result
	}
}
