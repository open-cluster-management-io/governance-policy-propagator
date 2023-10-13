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

// EnqueueReqsFromMapFuncByBinding maps a PlacementBinding to the targeted RepPolicies that are either directly in its
// subjects list, or are in a PolicySet which is a subject of this PlacementBinding.
func EnqueueReqsFromMapFuncByBinding(c client.Client) handler.EventHandler {
	return &enqueueReqsFromMapFuncByBinding{
		c,
	}
}

type (
	enqueueReqsFromMapFuncByBinding struct {
		c client.Client
	}
)

// Create implements EventHandler.
func (e *enqueueReqsFromMapFuncByBinding) Create(ctx context.Context,
	evt event.CreateEvent, q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Update implements EventHandler. Update only targeted(modified) objects
func (e *enqueueReqsFromMapFuncByBinding) Update(ctx context.Context,
	evt event.UpdateEvent, q workqueue.RateLimitingInterface,
) {
	log.Info("Detect placementBinding update and targeting replicated-policy to be changed")
	//nolint:forcetypeassert
	newObj := evt.ObjectNew.(*policiesv1.PlacementBinding)
	//nolint:forcetypeassert
	oldObj := evt.ObjectOld.(*policiesv1.PlacementBinding)

	if !(common.IsForPolicyOrPolicySet(newObj) || common.IsForPolicyOrPolicySet(oldObj)) {
		return
	}

	oldRepPolcies := common.GetRepPoliciesInPlacementBinding(ctx, e.c, oldObj)
	newRepPolcies := common.GetRepPoliciesInPlacementBinding(ctx, e.c, newObj)

	// Send only affected replicated policies
	affectedRepPolicies := common.GetAffectedOjbs(oldRepPolcies, newRepPolcies)
	for _, obj := range affectedRepPolicies {
		q.Add(obj)
	}
}

// Delete implements EventHandler.
func (e *enqueueReqsFromMapFuncByBinding) Delete(ctx context.Context,
	evt event.DeleteEvent, q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Generic implements EventHandler.
func (e *enqueueReqsFromMapFuncByBinding) Generic(ctx context.Context,
	evt event.GenericEvent, q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

func (e *enqueueReqsFromMapFuncByBinding) mapAndEnqueue(ctx context.Context,
	q workqueue.RateLimitingInterface, obj client.Object,
) {
	pBinding := obj.(*policiesv1.PlacementBinding)
	reqs := e.getMappedReplicatedPolicy(ctx, pBinding)

	for _, req := range reqs {
		q.Add(req)
	}
}

func (e *enqueueReqsFromMapFuncByBinding) getMappedReplicatedPolicy(ctx context.Context,
	pBinding *policiesv1.PlacementBinding,
) []reconcile.Request {
	if !(common.IsForPolicyOrPolicySet(pBinding)) {
		return nil
	}

	return common.GetRepPoliciesInPlacementBinding(ctx, e.c, pBinding)
}
