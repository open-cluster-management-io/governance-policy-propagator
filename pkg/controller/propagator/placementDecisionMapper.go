// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"

	clusterv1alpha1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/cluster/v1alpha1"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type placementDecisionMapper struct {
	client.Client
}

func (mapper *placementDecisionMapper) Map(obj handler.MapObject) []reconcile.Request {
	object := obj.Meta
	log.Info("Reconcile Request for PlacementDecision %s in namespace %s", object.GetName(), object.GetNamespace())

	// get the placement name from the placementdecision
	placementName := object.GetLabels()["cluster.open-cluster-management.io/placement"]
	if len(placementName) == 0 {
		return nil
	}

	pbList := &policiesv1.PlacementBindingList{}
	// find pb in the same namespace of placementrule
	err := mapper.List(context.TODO(), pbList, &client.ListOptions{Namespace: object.GetNamespace()})
	if err != nil {
		return nil
	}

	var result []reconcile.Request
	// loop through pb to find if current placement is used for policy
	for _, pb := range pbList.Items {
		// found matching placement rule in pb
		if pb.PlacementRef.APIGroup == clusterv1alpha1.SchemeGroupVersion.Group &&
			pb.PlacementRef.Kind == clusterv1alpha1.Kind && pb.PlacementRef.Name == placementName {
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
