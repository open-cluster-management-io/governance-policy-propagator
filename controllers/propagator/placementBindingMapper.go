// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

func placementBindingMapper(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		//nolint:forcetypeassert
		pb := obj.(*policiesv1.PlacementBinding)

		log := log.WithValues("placementBindingName", pb.GetName(), "namespace", pb.GetNamespace())
		log.V(2).Info("Reconcile request for a PlacementBinding")

		return common.GetPoliciesInPlacementBinding(ctx, c, pb)
	}
}
