// Copyright Contributors to the Open Cluster Management project

package automation

import (
	"context"

	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/api/v1"
	policyv1beta1 "github.com/open-cluster-management/governance-policy-propagator/api/v1beta1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func policyAutomationMapper(c client.Client) handler.MapFunc {
	return func(obj client.Object) []reconcile.Request {
		policyAutomation := obj.(*policyv1beta1.PolicyAutomation)
		var result []reconcile.Request
		policyRef := policyAutomation.Spec.PolicyRef
		if policyRef != "" {
			policy := &policiesv1.Policy{}
			err := c.Get(context.TODO(), types.NamespacedName{
				Name:      policyRef,
				Namespace: policyAutomation.GetNamespace(),
			}, policy)
			if err == nil {
				log.Info("Found reconciliation request from PolicyAutomation ...",
					"Namespace", policyAutomation.GetNamespace(), "Name", policyAutomation.GetName(), "Policy-Name", policyRef)
				request := reconcile.Request{NamespacedName: types.NamespacedName{
					Name:      policyAutomation.GetName(),
					Namespace: policyAutomation.GetNamespace(),
				}}
				result = append(result, request)
			} else {
				log.Info("Failed to retrieve policyRef from PolicyAutomation...ignoring it...",
					"Namespace", policyAutomation.GetNamespace(), "Name", policyAutomation.GetName(), "Policy-Name", policyRef)
			}
		}
		return result
	}
}
