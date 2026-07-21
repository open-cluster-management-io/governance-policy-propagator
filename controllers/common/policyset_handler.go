// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
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
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	for _, policy := range mapPolicySetToRequests(evt.Object) {
		q.Add(policy)
	}
}

// Update implements EventHandler
// Enqueues the diff between the new and old policy sets in the UpdateEvent
func (e *EnqueueRequestsFromPolicySet) Update(_ context.Context, evt event.UpdateEvent,
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	//nolint:forcetypeassert
	newPolicySet := evt.ObjectNew.(*policiesv1beta1.PolicySet)
	//nolint:forcetypeassert
	oldPolicySet := evt.ObjectOld.(*policiesv1beta1.PolicySet)

	diffPolicies := getPolicySetDiffs(oldPolicySet, newPolicySet)

	for _, policyName := range diffPolicies {
		log.V(2).Info("Found reconciliation request from a policyset", "policyName", policyName)
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      policyName,
			Namespace: newPolicySet.GetNamespace(),
		}})
	}
}

func getPolicySetDiffs(oldPolicySet, newPolicySet *policiesv1beta1.PolicySet) []string {
	newPoliciesMap := make(map[string]bool)
	oldPoliciesMap := make(map[string]bool)
	diffPolicies := make(map[string]bool)

	for _, plc := range newPolicySet.Spec.Policies {
		newPoliciesMap[string(plc)] = true
	}

	for _, plc := range oldPolicySet.Spec.Policies {
		oldPoliciesMap[string(plc)] = true
	}

	oldExclusions := make(map[string]policiesv1beta1.PolicySetExclusion, len(oldPolicySet.Spec.Exclusions))
	for _, exclusion := range oldPolicySet.Spec.Exclusions {
		oldExclusions[string(exclusion.PolicyName)] = exclusion
	}

	newExclusions := make(map[string]policiesv1beta1.PolicySetExclusion, len(newPolicySet.Spec.Exclusions))
	for _, exclusion := range newPolicySet.Spec.Exclusions {
		newExclusions[string(exclusion.PolicyName)] = exclusion
	}

	for _, plc := range oldPolicySet.Spec.Policies {
		if !newPoliciesMap[string(plc)] {
			diffPolicies[string(plc)] = true
		}
	}

	for _, plc := range newPolicySet.Spec.Policies {
		if !oldPoliciesMap[string(plc)] {
			diffPolicies[string(plc)] = true
		}
	}

	// exclusion added or updated
	for policyName, newExclusion := range newExclusions {
		oldExclusion, ok := oldExclusions[policyName]
		if !ok || !equality.Semantic.DeepEqual(newExclusion, oldExclusion) {
			diffPolicies[policyName] = true
		}
	}

	// exclusion removed
	for policyName := range oldExclusions {
		if _, ok := newExclusions[policyName]; !ok {
			diffPolicies[policyName] = true
		}
	}

	diff := make([]string, 0, len(diffPolicies))
	for policyName := range diffPolicies {
		diff = append(diff, policyName)
	}

	return diff
}

// Delete implements EventHandler
func (e *EnqueueRequestsFromPolicySet) Delete(_ context.Context, evt event.DeleteEvent,
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	for _, policy := range mapPolicySetToRequests(evt.Object) {
		q.Add(policy)
	}
}

// Generic implements EventHandler
func (e *EnqueueRequestsFromPolicySet) Generic(_ context.Context, evt event.GenericEvent,
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	for _, policy := range mapPolicySetToRequests(evt.Object) {
		q.Add(policy)
	}
}
