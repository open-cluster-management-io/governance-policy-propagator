// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"

	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/api/v1"
	appsv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func placementRuleMapper(c client.Client) handler.MapFunc {
	return func(object client.Object) []reconcile.Request {
		// log.Info("Reconcile Request for PlacementRule %s in namespace %s", object.GetName(), object.GetNamespace())
		// list pb
		pbList := &policiesv1.PlacementBindingList{}
		// find pb in the same namespace of placementrule
		err := c.List(context.TODO(), pbList, &client.ListOptions{Namespace: object.GetNamespace()})
		if err != nil {
			return nil
		}
		var result []reconcile.Request
		// loop through pb to find if current placementrule is used for policy
		for _, pb := range pbList.Items {
			// found matching placement rule in pb
			if pb.PlacementRef.APIGroup == appsv1.SchemeGroupVersion.Group &&
				pb.PlacementRef.Kind == "PlacementRule" && pb.PlacementRef.Name == object.GetName() {
				// check if it is for policy
				subjects := pb.Subjects
				for _, subject := range subjects {
					if subject.APIGroup == policiesv1.SchemeGroupVersion.Group && subject.Kind == policiesv1.Kind {
						log.Info("Found reconciliation request from placement rule...", "Namespace", object.GetNamespace(),
							"Name", object.GetName(), "Policy-Name", subject.Name)
						// generate reconcile request for policy referenced by pb
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
