// Copyright Contributors to the Open Cluster Management project

package automation

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policyv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

func policyMapper(c client.Client) handler.MapFunc {
	return func(obj client.Object) []reconcile.Request {
		// nolint: forcetypeassert
		policy := obj.(*policiesv1.Policy)

		var result []reconcile.Request

		policyAutomationList := &policyv1beta1.PolicyAutomationList{}

		err := c.List(context.TODO(), policyAutomationList, &client.ListOptions{Namespace: policy.GetNamespace()})
		if err != nil {
			return nil
		}

		found := false
		policyAutomation := policyv1beta1.PolicyAutomation{}

		for _, policyAutomationTemp := range policyAutomationList.Items {
			if policyAutomationTemp.Spec.PolicyRef == policy.GetName() {
				found = true
				policyAutomation = policyAutomationTemp

				break
			}
		}

		if found {
			modeType := policyAutomation.Spec.Mode
			if modeType == "scan" {
				// scan mode, do not queue
			} else if modeType == policyv1beta1.Once || modeType == policyv1beta1.EveryEvent {
				// The same policyAutomation mapping logic for once and everyEvent mode
				request := reconcile.Request{NamespacedName: types.NamespacedName{
					Name:      policyAutomation.GetName(),
					Namespace: policyAutomation.GetNamespace(),
				}}
				result = append(result, request)
			}
		}

		return result
	}
}
