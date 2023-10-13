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

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

// EnqueueReqsFromMapFuncByDecision maps a PlacementDecision
// to all RepPolicies that are in namespace as a decision cluster name.
// The name of repPolicy is rootpolicy name + namespace which is in Placementbinding subject
func EnqueueReqsFromMapFuncByDecision(c client.Client) handler.EventHandler {
	return &enqueueReqsFromMapFuncByDecision{
		c,
	}
}

type (
	enqueueReqsFromMapFuncByDecision struct {
		c client.Client
	}
)

// Create implements EventHandler.
func (e *enqueueReqsFromMapFuncByDecision) Create(ctx context.Context,
	evt event.CreateEvent, q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Update implements EventHandler. Update only targeted(modified) objects
func (e *enqueueReqsFromMapFuncByDecision) Update(ctx context.Context,
	evt event.UpdateEvent, q workqueue.RateLimitingInterface,
) {
	log.Info("Detect placementDecision update and targeting replicated-policy to be changed")
	//nolint:forcetypeassert
	newObj := evt.ObjectNew.(*clusterv1beta1.PlacementDecision)
	//nolint:forcetypeassert
	oldObj := evt.ObjectOld.(*clusterv1beta1.PlacementDecision)

	newOjbDecisions := newObj.Status.Decisions
	oldObjDecisions := oldObj.Status.Decisions

	// Select only affected(Deleted in old or Created in new) objs
	affectedDecisions := common.GetAffectedOjbs(newOjbDecisions, oldObjDecisions)

	// These is no change. Nothing to reconcile
	if len(affectedDecisions) == 0 {
		return
	}

	placementName := newObj.GetLabels()["cluster.open-cluster-management.io/placement"]
	if placementName == "" {
		return
	}

	pbList := &policiesv1.PlacementBindingList{}
	// Find pb in the same namespace of placementrule
	lopts := &client.ListOptions{Namespace: newObj.GetNamespace()}
	opts := client.MatchingFields{"placementRef.name": placementName}
	opts.ApplyToList(lopts)

	err := e.c.List(ctx, pbList, lopts)
	if err != nil {
		log.Error(err, "Getting placementbinding list has error in watching placedecision in replicatedpolicy_setup")

		return
	}

	// Collect all rootPolicies that relate to this placementRule
	var rootPolicyResults []reconcile.Request

	for i, pb := range pbList.Items {
		if pb.PlacementRef.APIGroup != clusterv1beta1.SchemeGroupVersion.Group ||
			pb.PlacementRef.Kind != "Placement" || pb.PlacementRef.Name != placementName {
			continue
		}
		// GetPoliciesInPlacementBinding only pick root-policy name
		rootPolicyResults = append(rootPolicyResults,
			common.GetPoliciesInPlacementBinding(ctx, e.c, &pbList.Items[i])...)
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
func (e *enqueueReqsFromMapFuncByDecision) Delete(ctx context.Context,
	evt event.DeleteEvent, q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Generic implements EventHandler.
func (e *enqueueReqsFromMapFuncByDecision) Generic(ctx context.Context,
	evt event.GenericEvent, q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

func (e *enqueueReqsFromMapFuncByDecision) mapAndEnqueue(ctx context.Context,
	q workqueue.RateLimitingInterface, obj client.Object,
) {
	pDecision := obj.(*clusterv1beta1.PlacementDecision)
	reqs := e.getMappedReplicatedPolicy(ctx, pDecision)

	for _, req := range reqs {
		q.Add(req)
	}
}

func (e *enqueueReqsFromMapFuncByDecision) getMappedReplicatedPolicy(ctx context.Context,
	pDecision *clusterv1beta1.PlacementDecision,
) []reconcile.Request {
	if len(pDecision.Status.Decisions) == 0 {
		return nil
	}

	placementName := pDecision.GetLabels()["cluster.open-cluster-management.io/placement"]
	if placementName == "" {
		return nil
	}

	pbList := &policiesv1.PlacementBindingList{}
	// Find pb in the same namespace of placementrule
	lopts := &client.ListOptions{Namespace: pDecision.GetNamespace()}
	opts := client.MatchingFields{"placementRef.name": placementName}
	opts.ApplyToList(lopts)

	err := e.c.List(ctx, pbList, lopts)
	if err != nil {
		log.Error(err, "Getting placementbinding list has error in watching placedecision in replicatedpolicy_setup")

		return nil
	}

	var rootPolicyResults []reconcile.Request

	for i, pb := range pbList.Items {
		if pb.PlacementRef.APIGroup != clusterv1beta1.SchemeGroupVersion.Group ||
			pb.PlacementRef.Kind != "Placement" || pb.PlacementRef.Name != placementName {
			continue
		}
		// GetPoliciesInPlacementBinding only pick root-policy name
		rootPolicyResults = append(rootPolicyResults,
			common.GetPoliciesInPlacementBinding(ctx, e.c, &pbList.Items[i])...)
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
