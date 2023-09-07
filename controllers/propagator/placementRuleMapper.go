// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"

	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

func placementRuleMapper(c client.Client) handler.MapFunc {
	return func(ctx context.Context, object client.Object) []reconcile.Request {
		log := log.WithValues("placementRuleName", object.GetName(), "namespace", object.GetNamespace())

		log.V(2).Info("Reconcile Request for PlacementRule")

		// list pb
		pbList := &policiesv1.PlacementBindingList{}

		// find pb in the same namespace of placementrule
		err := c.List(ctx, pbList, &client.ListOptions{Namespace: object.GetNamespace()})
		if err != nil {
			return nil
		}

		var result []reconcile.Request
		// loop through pbs and collect policies from each matching one.
		for _, pb := range pbList.Items {
			if pb.PlacementRef.APIGroup != appsv1.SchemeGroupVersion.Group ||
				pb.PlacementRef.Kind != "PlacementRule" || pb.PlacementRef.Name != object.GetName() {
				continue
			}

			result = append(result, common.GetPoliciesInPlacementBinding(ctx, c, &pb)...)
		}

		return result
	}
}
