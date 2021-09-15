// Copyright Contributors to the Open Cluster Management project

package automation

import (
	policyv1beta1 "github.com/open-cluster-management/governance-policy-propagator/api/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// we only want to watch for pb contains policy as subjects
var policyAuomtationPredicateFuncs = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		policyAutomationNew := e.ObjectNew.(*policyv1beta1.PolicyAutomation)
		policyAutomationOld := e.ObjectOld.(*policyv1beta1.PolicyAutomation)
		if policyAutomationNew.Spec.PolicyRef == "" {
			return false
		}
		if policyAutomationNew.ObjectMeta.Annotations["policy.open-cluster-management.io/rerun"] == "true" {
			return true
		}
		return !equality.Semantic.DeepEqual(policyAutomationNew.Spec, policyAutomationOld.Spec)
	},
	CreateFunc: func(e event.CreateEvent) bool {
		policyAutomationNew := e.Object.(*policyv1beta1.PolicyAutomation)
		return policyAutomationNew.Spec.PolicyRef != ""
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
}
