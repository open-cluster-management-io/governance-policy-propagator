// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"

	clusterv1alpha1 "github.com/open-cluster-management/api/cluster/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/api/v1"
)

func placementDecisionMapper(c client.Client) handler.MapFunc {
	return func(object client.Object) []reconcile.Request {
		log.Info("Reconcile Request for PlacementDecision %s in namespace %s", object.GetName(), object.GetNamespace())

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
		// loop through pb to find if current placement is used for policy
		for _, pb := range pbList.Items {
			if pb.PlacementRef.APIGroup != clusterv1alpha1.SchemeGroupVersion.Group ||
				pb.PlacementRef.Kind != "Placement" || pb.PlacementRef.Name != placementName {
				continue
			}

			// found matching placement rule in pb -- check if it is for policy
			subjects := pb.Subjects
			for _, subject := range subjects {
				if subject.APIGroup != policiesv1.SchemeGroupVersion.Group || subject.Kind != policiesv1.Kind {
					continue
				}

				log.Info("Found reconciliation request from placement decision...", "Namespace", object.GetNamespace(),
					"Name", object.GetName(), "Policy-Name", subject.Name)

				// generate reconcile request for policy referenced by pb
				request := reconcile.Request{NamespacedName: types.NamespacedName{
					Name:      subject.Name,
					Namespace: object.GetNamespace(),
				}}
				result = append(result, request)
			}
		}

		return result
	}
}
