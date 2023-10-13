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

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

// EnqueueReqsFromMapFuncByRule maps a PlacementRule to all RepPolicies which are in the namespace as
// PlacementRule status.decisions. This finds placementBindings, of which placementRef is the placementRule,
// then collects all rootPolicies in placementBindings. Replicated policies are determined
// from decisions in the placementRule and a rootPolicy name
func EnqueueReqsFromMapFuncByRule(c client.Client) handler.EventHandler {
	return &enqueueReqsFromMapFuncByRule{
		c,
	}
}

type (
	enqueueReqsFromMapFuncByRule struct {
		c client.Client
	}
)

// Create implements EventHandler.
func (e *enqueueReqsFromMapFuncByRule) Create(ctx context.Context,
	evt event.CreateEvent, q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Update implements EventHandler. Update only targeted(modified) objects
func (e *enqueueReqsFromMapFuncByRule) Update(ctx context.Context,
	evt event.UpdateEvent, q workqueue.RateLimitingInterface,
) {
	log.Info("Detect placementDecision update and targeting replicated-policy to be changed")
	//nolint:forcetypeassert
	newObj := evt.ObjectNew.(*appsv1.PlacementRule)
	//nolint:forcetypeassert
	oldObj := evt.ObjectOld.(*appsv1.PlacementRule)

	newOjbDecisions := newObj.Status.Decisions
	oldObjDecisions := oldObj.Status.Decisions

	// Select only affected(Deleted in old or Created in new) objs
	affectedDecisions := common.GetAffectedOjbs(newOjbDecisions, oldObjDecisions)

	// These is no status.decision change. Nothing to reconcile
	if len(affectedDecisions) == 0 {
		return
	}

	pbList := &policiesv1.PlacementBindingList{}
	// Find pb in the same namespace of placementrule
	lopts := &client.ListOptions{Namespace: newObj.GetNamespace()}
	opts := client.MatchingFields{"placementRef.name": newObj.GetName()}
	opts.ApplyToList(lopts)

	err := e.c.List(ctx, pbList, lopts)
	if err != nil {
		log.Error(err, "Getting placementbinding list has error in watching placedecision in replicatedpolicy_setup")

		return
	}

	var rootPolicyResults []reconcile.Request

	for i, pb := range pbList.Items {
		if pb.PlacementRef.APIGroup != appsv1.SchemeGroupVersion.Group ||
			pb.PlacementRef.Kind != "PlacementRule" || pb.PlacementRef.Name != newObj.GetName() {
			continue
		}
		// GetPoliciesInPlacementBinding only pick root-policy name
		rootPolicyResults = append(rootPolicyResults,
			common.GetPoliciesInPlacementBinding(ctx, e.c, &pbList.Items[i])...)
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
func (e *enqueueReqsFromMapFuncByRule) Delete(ctx context.Context,
	evt event.DeleteEvent, q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Generic implements EventHandler.
func (e *enqueueReqsFromMapFuncByRule) Generic(ctx context.Context,
	evt event.GenericEvent, q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

func (e *enqueueReqsFromMapFuncByRule) mapAndEnqueue(ctx context.Context,
	q workqueue.RateLimitingInterface, obj client.Object,
) {
	pRule := obj.(*appsv1.PlacementRule)
	reqs := e.getMappedReplicatedPolicy(ctx, pRule)

	for _, req := range reqs {
		q.Add(req)
	}
}

func (e *enqueueReqsFromMapFuncByRule) getMappedReplicatedPolicy(ctx context.Context,
	pRule *appsv1.PlacementRule,
) []reconcile.Request {
	if len(pRule.Status.Decisions) == 0 {
		return nil
	}

	pbList := &policiesv1.PlacementBindingList{}
	// Find pb in the same namespace of placementrule
	lopts := &client.ListOptions{Namespace: pRule.GetNamespace()}
	opts := client.MatchingFields{"placementRef.name": pRule.GetName()}
	opts.ApplyToList(lopts)

	err := e.c.List(ctx, pbList, lopts)
	if err != nil {
		log.Error(err, "Getting placementbinding list has error in watching placedecision in replicatedpolicy_setup")

		return nil
	}

	var rootPolicyResults []reconcile.Request

	for i, pb := range pbList.Items {
		if pb.PlacementRef.APIGroup != appsv1.SchemeGroupVersion.Group ||
			pb.PlacementRef.Kind != "PlacementRule" || pb.PlacementRef.Name != pRule.GetName() {
			continue
		}
		// GetPoliciesInPlacementBinding only picks root-policy name
		rootPolicyResults = append(rootPolicyResults,
			common.GetPoliciesInPlacementBinding(ctx, e.c, &pbList.Items[i])...)
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

///////////////////////////////////
