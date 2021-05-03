// Copyright Contributors to the Open Cluster Management project

package automation

import (
	"context"

	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
	policyv1beta1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1beta1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type policyAutomationMapper struct {
	client.Client
}

func (mapper *policyAutomationMapper) Map(obj handler.MapObject) []reconcile.Request {
	policyAutomation := obj.Object.(*policyv1beta1.PolicyAutomation)
	var result []reconcile.Request
	policyRef := policyAutomation.Spec.PolicyRef
	if policyRef != "" {
		policy := &policiesv1.Policy{}
		err := mapper.Client.Get(context.TODO(), types.NamespacedName{
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
