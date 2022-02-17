// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func placementDecisionMapper(c client.Client) handler.MapFunc {
	return func(object client.Object) []reconcile.Request {
		log := log.WithValues("placementDecisionName", object.GetName(), "namespace", object.GetNamespace())

		log.V(2).Info("Reconcile request for a placement decision")

		// get the placement name from the placementdecision
		placementName := object.GetLabels()["cluster.open-cluster-management.io/placement"]
		if placementName == "" {
			return nil
		}

		pbList := &policiesv1.PlacementBindingList{}
		// find pb in the same namespace of placementrule
		lopts := &client.ListOptions{Namespace: object.GetNamespace()}
		opts := client.MatchingFields{"placementRef.name": placementName}
		opts.ApplyToList(lopts)

		err := c.List(context.TODO(), pbList, lopts)
		if err != nil {
			return nil
		}

		var result []reconcile.Request
		// loop through pb to find if current placement is used for policy set
		for _, pb := range pbList.Items {
			if pb.PlacementRef.APIGroup != clusterv1beta1.SchemeGroupVersion.Group ||
				pb.PlacementRef.Kind != "Placement" || pb.PlacementRef.Name != placementName {
				continue
			}

			// found matching placement in pb -- check if it is for policyset
			subjects := pb.Subjects
			for _, subject := range subjects {
				if subject.APIGroup == policiesv1.SchemeGroupVersion.Group {
					if subject.Kind == policiesv1.PolicySetKind {
						log.V(2).Info("Found reconciliation request from policyset placement decision",
							"policySetName", subject.Name)

						request := reconcile.Request{NamespacedName: types.NamespacedName{
							Name:      subject.Name,
							Namespace: object.GetNamespace(),
						}}
						result = append(result, request)
					}
				}
			}
		}

		return result
	}
}
