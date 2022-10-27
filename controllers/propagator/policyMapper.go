// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

// policyMapper looks at object and returns a slice of reconcile.Request to reconcile
// owners of object from label: policy.open-cluster-management.io/root-policy
func policyMapper(c client.Client) handler.MapFunc {
	return func(object client.Object) []reconcile.Request {
		log := log.WithValues("name", object.GetName(), "namespace", object.GetNamespace())

		log.V(2).Info("Reconcile request for a policy")

		rootPlcName := object.GetLabels()[common.RootPolicyLabel]
		var name string
		var namespace string

		if rootPlcName != "" {
			// policy.open-cluster-management.io/root-policy exists, should be a replicated policy
			log.V(2).Info("Found reconciliation request from replicated policy")

			var err error

			name, namespace, err = common.ParseRootPolicyLabel(rootPlcName)
			if err != nil {
				log.Error(err, "Unable to parse name and namespace of root policy, ignoring replicated policy",
					"rootPlcName", rootPlcName)

				return nil
			}

			clusterList := &clusterv1.ManagedClusterList{}

			err = c.List(context.TODO(), clusterList, &client.ListOptions{})
			if err != nil {
				log.Error(err, "failed to list ManagedCluster objects")

				return nil
			}
			// do not handle a replicated policy which does not belong to the current cluster
			if !common.IsInClusterNamespace(object.GetNamespace(), clusterList.Items) {
				log.V(2).Info("Found a replicated policy in non-cluster namespace, skipping it")

				return nil
			}
		} else {
			// policy.open-cluster-management.io/root-policy doesn't exist, should be a root policy
			log.V(2).Info("Found reconciliation request from root policy")

			name = object.GetName()
			namespace = object.GetNamespace()
		}

		request := reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}}

		return []reconcile.Request{request}
	}
}
