package propagator

import (
	"context"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

// HandlerForBinding maps a PlacementBinding to the targeted RepPolicies that are either directly in its
// subjects list, or are in a PolicySet which is a subject of this PlacementBinding.
func HandlerForBinding(c client.Client) handler.EventHandler {
	return &handlerForBinding{
		c,
	}
}

type handlerForBinding struct {
	c client.Client
}

// Create implements EventHandler.
func (e *handlerForBinding) Create(ctx context.Context,
	evt event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Update implements EventHandler. Update only targeted(modified) objects
func (e *handlerForBinding) Update(ctx context.Context,
	evt event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	log.V(1).Info("Detect placementBinding and update targeted replicated-policies")
	//nolint:forcetypeassert
	newObj := evt.ObjectNew.(*policiesv1.PlacementBinding)
	//nolint:forcetypeassert
	oldObj := evt.ObjectOld.(*policiesv1.PlacementBinding)

	if !(common.IsForPolicyOrPolicySet(newObj) || common.IsForPolicyOrPolicySet(oldObj)) {
		return
	}

	oldRepPolcies := common.GetRepPoliciesInPlacementBinding(ctx, e.c, oldObj)
	newRepPolcies := common.GetRepPoliciesInPlacementBinding(ctx, e.c, newObj)

	// If the BindingOverrides or subFilter has changed, all new and old policies need to be checked
	if newObj.BindingOverrides.RemediationAction != oldObj.BindingOverrides.RemediationAction ||
		newObj.SubFilter != oldObj.SubFilter {
		for _, obj := range newRepPolcies {
			q.Add(obj)
		}

		// The workqueue will handle any de-duplication.
		for _, obj := range oldRepPolcies {
			q.Add(obj)
		}

		return
	}

	// Otherwise send only affected replicated policies
	affectedRepPolicies := common.GetAffectedObjs(oldRepPolcies, newRepPolcies)
	for _, obj := range affectedRepPolicies {
		q.Add(obj)
	}
}

// Delete implements EventHandler.
func (e *handlerForBinding) Delete(ctx context.Context,
	evt event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Generic implements EventHandler.
func (e *handlerForBinding) Generic(ctx context.Context,
	evt event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

func (e *handlerForBinding) mapAndEnqueue(ctx context.Context,
	q workqueue.TypedRateLimitingInterface[reconcile.Request], obj client.Object,
) {
	pBinding := obj.(*policiesv1.PlacementBinding)
	reqs := e.getMappedReplicatedPolicy(ctx, pBinding)

	for _, req := range reqs {
		q.Add(req)
	}
}

func (e *handlerForBinding) getMappedReplicatedPolicy(ctx context.Context,
	pBinding *policiesv1.PlacementBinding,
) []reconcile.Request {
	if !(common.IsForPolicyOrPolicySet(pBinding)) {
		return nil
	}

	return common.GetRepPoliciesInPlacementBinding(ctx, e.c, pBinding)
}
