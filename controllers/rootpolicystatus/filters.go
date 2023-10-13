// Copyright Contributors to the Open Cluster Management project

package policystatus

import (
	"context"

	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

// policyStatusPredicate will filter out all policy events except for updates where the generation is the same, which
// implies the status has been updated. If the generation changes, the main policy controller will handle it.
func policyStatusPredicate() predicate.Funcs {
	return predicate.Funcs{
		// Creations are handled by the main policy controller.
		CreateFunc: func(e event.CreateEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			//nolint:forcetypeassert
			oldPolicy := e.ObjectOld.(*policiesv1.Policy)
			//nolint:forcetypeassert
			updatedPolicy := e.ObjectNew.(*policiesv1.Policy)

			// If there was an update and the generation is the same, the status must have changed.
			return oldPolicy.Generation == updatedPolicy.Generation
		},
		// Deletions are handled by the main policy controller.
		DeleteFunc: func(e event.DeleteEvent) bool { return false },
	}
}

// mapBindingToPolicies maps a placementBinding to all the Policies in its policies list.
func mapBindingToPolicies(c client.Client) handler.MapFunc {
	return func(ctx context.Context, object client.Object) []reconcile.Request {
		log := log.WithValues("placementBinding", object.GetName(), "namespace", object.GetNamespace())
		log.V(2).Info("Reconcile Request for placementBinding")

		//nolint:forcetypeassert
		pb := object.(*policiesv1.PlacementBinding)

		return common.GetPoliciesInPlacementBinding(ctx, c, pb)
	}
}

// mapPlacementRuleToPolicies maps a PlacementRule to all the Policies in its policies list.
func mapRuleToPolicies(c client.Client) handler.MapFunc {
	return func(ctx context.Context, object client.Object) []reconcile.Request {
		log := log.WithValues("PlacementRule", object.GetName(), "namespace", object.GetNamespace())
		log.V(2).Info("Reconcile Request for PlacementRule")

		//nolint:forcetypeassert
		pr := object.(*appsv1.PlacementRule)
		pbList := &policiesv1.PlacementBindingList{}
		// Find pb in the same namespace of placementrule
		lopts := &client.ListOptions{Namespace: pr.GetNamespace()}
		opts := client.MatchingFields{"placementRef.name": pr.GetName()}
		opts.ApplyToList(lopts)

		err := c.List(ctx, pbList, lopts)
		if err != nil {
			return nil
		}

		result := []reconcile.Request{}

		for _, pb := range pbList.Items {
			policy := common.GetPoliciesInPlacementBinding(ctx, c, &pb)
			result = append(result, policy...)
		}

		return result
	}
}

// mapPlacementDecisionToPolicies maps a PlacementDecision to all the Policies in its policies list.
func mapDecisionToPolicies(c client.Client) handler.MapFunc {
	return func(ctx context.Context, object client.Object) []reconcile.Request {
		log := log.WithValues("placementDecision", object.GetName(), "namespace", object.GetNamespace())
		log.V(2).Info("Reconcile Request for placementDecision")

		pd := object.(*clusterv1beta1.PlacementDecision)
		placementName := pd.GetLabels()["cluster.open-cluster-management.io/placement"]
		pbList := &policiesv1.PlacementBindingList{}
		// Find pb in the same namespace of placementrule
		lopts := &client.ListOptions{Namespace: pd.GetNamespace()}
		opts := client.MatchingFields{"placementRef.name": placementName}
		opts.ApplyToList(lopts)

		err := c.List(ctx, pbList, lopts)
		if err != nil {
			log.Error(err,
				"Getting placementbinding list has error in watching placedecision in replicatedpolicy_setup")

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
				common.GetPoliciesInPlacementBinding(ctx, c, &pbList.Items[i])...)
		}

		return rootPolicyResults
	}
}
