package propagator

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

// HandlerForDecision maps a PlacementDecision
// to all replicated policies that are in namespace as a decision cluster name.
// The name of replicated policy is rootpolicy name + namespace which is in Placementbinding subject
func HandlerForDecision(c client.Client) handler.EventHandler {
	return &handlerForDecision{
		c,
	}
}

type handlerForDecision struct {
	c client.Client
}

// Create implements EventHandler.
func (e *handlerForDecision) Create(ctx context.Context,
	evt event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Update implements EventHandler. Update only targeted(modified) objects
func (e *handlerForDecision) Update(ctx context.Context,
	evt event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	log.V(1).Info("Detect placementDecision and update targeted replicated-policies")
	//nolint:forcetypeassert
	newObj := evt.ObjectNew.(*clusterv1beta1.PlacementDecision)
	//nolint:forcetypeassert
	oldObj := evt.ObjectOld.(*clusterv1beta1.PlacementDecision)

	placementName := newObj.GetLabels()["cluster.open-cluster-management.io/placement"]
	if placementName == "" {
		return
	}

	newOjbDecisions := newObj.Status.Decisions
	oldObjDecisions := oldObj.Status.Decisions

	// Select only affected(Deleted in old or Created in new) objs
	affectedDecisions := common.GetAffectedObjs(newOjbDecisions, oldObjDecisions)

	// These is no change. Nothing to reconcile
	if len(affectedDecisions) == 0 {
		return
	}

	rootPolicyResults, err := common.GetRootPolicyRequests(ctx, e.c,
		newObj.GetNamespace(), placementName, common.Placement)
	if err != nil {
		log.Error(err, "Failed to get RootPolicyResults to update by decision event")

		return
	}

	for _, pr := range rootPolicyResults {
		for _, decision := range affectedDecisions {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: decision.ClusterName,
				Name:      pr.Namespace + "." + pr.Name,
			}})
		}
	}
}

// Delete implements EventHandler.
func (e *handlerForDecision) Delete(ctx context.Context,
	evt event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Generic implements EventHandler.
func (e *handlerForDecision) Generic(ctx context.Context,
	evt event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

func (e *handlerForDecision) mapAndEnqueue(ctx context.Context,
	q workqueue.TypedRateLimitingInterface[reconcile.Request], obj client.Object,
) {
	pDecision := obj.(*clusterv1beta1.PlacementDecision)
	reqs := e.getMappedReplicatedPolicy(ctx, pDecision)

	for _, req := range reqs {
		q.Add(req)
	}
}

func (e *handlerForDecision) getMappedReplicatedPolicy(ctx context.Context,
	pDecision *clusterv1beta1.PlacementDecision,
) []reconcile.Request {
	if len(pDecision.Status.Decisions) == 0 {
		return nil
	}

	placementName := pDecision.GetLabels()["cluster.open-cluster-management.io/placement"]
	if placementName == "" {
		return nil
	}

	rootPolicyResults, err := common.GetRootPolicyRequests(ctx, e.c,
		pDecision.GetNamespace(), placementName, common.Placement)
	if err != nil {
		log.Error(err, "Failed to get RootPolicyResults by decision events")

		return nil
	}

	var result []reconcile.Request

	for _, pr := range rootPolicyResults {
		for _, decision := range pDecision.Status.Decisions {
			result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: decision.ClusterName,
				Name:      pr.Namespace + "." + pr.Name,
			}})
		}
	}

	return result
}
