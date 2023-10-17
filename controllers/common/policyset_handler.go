// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

// EnqueueRequestsFromPolicySet adds reconcile requests for every policy in the policy set,
// except on updates, it'll only add the diff between the old and new sets.
type EnqueueRequestsFromPolicySet struct{}

// mapPolicySetToRequests maps a PolicySet to all the Policies in its policies list.
func mapPolicySetToRequests(object client.Object) []reconcile.Request {
	log := log.WithValues("policySetName", object.GetName(), "namespace", object.GetNamespace())
	log.V(2).Info("Reconcile Request for PolicySet")

	var result []reconcile.Request

	//nolint:forcetypeassert
	policySet := object.(*policiesv1beta1.PolicySet)

	for _, plc := range policySet.Spec.Policies {
		log.V(2).Info("Found reconciliation request from a policyset", "policyName", string(plc))

		request := reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      string(plc),
			Namespace: object.GetNamespace(),
		}}
		result = append(result, request)
	}

	return result
}

// Create implements EventHandler
func (e *EnqueueRequestsFromPolicySet) Create(_ context.Context, evt event.CreateEvent,
	q workqueue.RateLimitingInterface,
) {
	for _, policy := range mapPolicySetToRequests(evt.Object) {
		q.Add(policy)
	}
}

// Update implements EventHandler
// Enqueues the diff between the new and old policy sets in the UpdateEvent
func (e *EnqueueRequestsFromPolicySet) Update(_ context.Context, evt event.UpdateEvent,
	q workqueue.RateLimitingInterface,
) {
	//nolint:forcetypeassert
	newPolicySet := evt.ObjectNew.(*policiesv1beta1.PolicySet)
	//nolint:forcetypeassert
	oldPolicySet := evt.ObjectOld.(*policiesv1beta1.PolicySet)

	newPoliciesMap := make(map[string]bool)
	oldPoliciesMap := make(map[string]bool)
	diffPolicies := []policiesv1beta1.NonEmptyString{}

	for _, plc := range newPolicySet.Spec.Policies {
		newPoliciesMap[string(plc)] = true
	}

	for _, plc := range oldPolicySet.Spec.Policies {
		oldPoliciesMap[string(plc)] = true
	}

	for _, plc := range oldPolicySet.Spec.Policies {
		if !newPoliciesMap[string(plc)] {
			diffPolicies = append(diffPolicies, plc)
		}
	}

	for _, plc := range newPolicySet.Spec.Policies {
		if !oldPoliciesMap[string(plc)] {
			diffPolicies = append(diffPolicies, plc)
		}
	}

	for _, plc := range diffPolicies {
		log.V(2).Info("Found reconciliation request from a policyset", "policyName", string(plc))

		request := reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      string(plc),
			Namespace: newPolicySet.GetNamespace(),
		}}
		q.Add(request)
	}
}

// Delete implements EventHandler
func (e *EnqueueRequestsFromPolicySet) Delete(_ context.Context, evt event.DeleteEvent,
	q workqueue.RateLimitingInterface,
) {
	for _, policy := range mapPolicySetToRequests(evt.Object) {
		q.Add(policy)
	}
}

// Generic implements EventHandler
func (e *EnqueueRequestsFromPolicySet) Generic(_ context.Context, evt event.GenericEvent,
	q workqueue.RateLimitingInterface,
) {
	for _, policy := range mapPolicySetToRequests(evt.Object) {
		q.Add(policy)
	}
}
