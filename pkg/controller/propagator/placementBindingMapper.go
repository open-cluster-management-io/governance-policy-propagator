package propagator

import (
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type placementBindingMapper struct {
	client.Client
}

func (mapper *placementBindingMapper) Map(obj handler.MapObject) []reconcile.Request {
	object := obj.Object.(*policiesv1.PlacementBinding)
	log.Info("Reconcile Request for PlacementBinding %s in namespace %s", object.GetName(), object.GetNamespace())

	var result []reconcile.Request
	// check if pb is for policy
	if object.Spec.Subject.APIGroup == policiesv1.SchemeGroupVersion.Group && object.Spec.Subject.Kind == policiesv1.Kind {
		request := reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      object.Spec.Subject.Name,
			Namespace: object.GetNamespace(),
		}}
		result = append(result, request)
	}
	return result
}
