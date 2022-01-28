// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func placementRuleMapper(c client.Client) handler.MapFunc {
	return func(object client.Object) []reconcile.Request {
		log := log.WithValues("placementRuleName", object.GetName(), "namespace", object.GetNamespace())

		log.V(2).Info("Reconcile Request for PlacementRule")

		// list pb
		pbList := &policiesv1.PlacementBindingList{}

		// find pb in the same namespace of placementrule
		err := c.List(context.TODO(), pbList, &client.ListOptions{Namespace: object.GetNamespace()})
		if err != nil {
			return nil
		}

		var result []reconcile.Request
		// loop through pb to find if current placementrule is used for policy set
		for _, pb := range pbList.Items {
			// found matching placement rule in pb
			if pb.PlacementRef.APIGroup == appsv1.SchemeGroupVersion.Group &&
				pb.PlacementRef.Kind == "PlacementRule" && pb.PlacementRef.Name == object.GetName() {
				// check if it is for policy set
				subjects := pb.Subjects
				for _, subject := range subjects {
					if subject.APIGroup == policiesv1.SchemeGroupVersion.Group {
						if subject.Kind == policiesv1.PolicySetKind {
							log.V(2).Info("Found reconciliation request from policyset placement rule",
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
		}

		return result
	}
}
