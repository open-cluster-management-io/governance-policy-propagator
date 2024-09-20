package propagator

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

// HandlerForRule maps a PlacementRule to all replicated policies which are in the namespace as
// PlacementRule status.decisions. This finds placementBindings, of which placementRef is the placementRule,
// then collects all rootPolicies in placementBindings. Replicated policies are determined
// from decisions in the placementRule and a rootPolicy name
func HandlerForRule(c client.Client) handler.EventHandler {
	return &handlerForRule{
		c,
	}
}

type handlerForRule struct {
	c client.Client
}

// Create implements EventHandler.
func (e *handlerForRule) Create(ctx context.Context,
	evt event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Update implements EventHandler. Update only targeted(modified) objects
func (e *handlerForRule) Update(ctx context.Context,
	evt event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	log.Info("Detect placementDecision and update targeted replicated-policies")
	//nolint:forcetypeassert
	newObj := evt.ObjectNew.(*appsv1.PlacementRule)
	//nolint:forcetypeassert
	oldObj := evt.ObjectOld.(*appsv1.PlacementRule)

	newOjbDecisions := newObj.Status.Decisions
	oldObjDecisions := oldObj.Status.Decisions

	// Select only affected(Deleted in old or Created in new) objs
	affectedDecisions := common.GetAffectedObjs(newOjbDecisions, oldObjDecisions)

	// These is no status.decision change. Nothing to reconcile
	if len(affectedDecisions) == 0 {
		return
	}

	rootPolicyResults, err := common.GetRootPolicyRequests(ctx, e.c,
		newObj.GetNamespace(), newObj.GetName(), common.PlacementRule)
	if err != nil {
		log.Error(err, "Failed to get RootPolicyResults to update by Rule event")

		return
	}

	for _, pr := range rootPolicyResults {
		for _, decision := range affectedDecisions {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: decision.ClusterNamespace,
				Name:      pr.Namespace + "." + pr.Name,
			}})
		}
	}
}

// Delete implements EventHandler.
func (e *handlerForRule) Delete(ctx context.Context,
	evt event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Generic implements EventHandler.
func (e *handlerForRule) Generic(ctx context.Context,
	evt event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

func (e *handlerForRule) mapAndEnqueue(ctx context.Context,
	q workqueue.TypedRateLimitingInterface[reconcile.Request], obj client.Object,
) {
	pRule := obj.(*appsv1.PlacementRule)
	reqs := e.getMappedReplicatedPolicy(ctx, pRule)

	for _, req := range reqs {
		q.Add(req)
	}
}

func (e *handlerForRule) getMappedReplicatedPolicy(ctx context.Context,
	pRule *appsv1.PlacementRule,
) []reconcile.Request {
	if len(pRule.Status.Decisions) == 0 {
		return nil
	}

	rootPolicyResults, err := common.GetRootPolicyRequests(ctx, e.c,
		pRule.GetNamespace(), pRule.GetName(), common.PlacementRule)
	if err != nil {
		log.Error(err, "Failed to get RootPolicyResults By Rule event")

		return nil
	}

	var result []reconcile.Request

	for _, pr := range rootPolicyResults {
		for _, decision := range pRule.Status.Decisions {
			result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: decision.ClusterNamespace,
				Name:      pr.Namespace + "." + pr.Name,
			}})
		}
	}

	return result
}
