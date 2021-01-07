// Copyright (c) 2021 Red Hat, Inc.

package propagator

import (
	"strings"

	"github.com/open-cluster-management/governance-policy-propagator/pkg/controller/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type policyMapper struct {
	client.Client
}

func (mapper *policyMapper) Map(obj handler.MapObject) []reconcile.Request {
	return getOwnerReconcileRequest(obj.Meta)
}

// getOwnerReconcileRequest looks at object and returns a slice of reconcile.Request to reconcile
// owners of object from label: policy.open-cluster-management.io/root-policy
func getOwnerReconcileRequest(object metav1.Object) []reconcile.Request {
	var result []reconcile.Request
	rootPlcName := object.GetLabels()[common.RootPolicyLabel]
	var name string
	var namespace string
	if rootPlcName != "" {
		// policy.open-cluster-management.io/root-policy exists, should be a replicated policy
		log.Info("Found reconciliation request from replicated policy...", "Namespace", object.GetNamespace(),
			"Name", object.GetName())
		name = strings.Split(rootPlcName, ".")[1]
		namespace = strings.Split(rootPlcName, ".")[0]
	} else {
		// policy.open-cluster-management.io/root-policy doesn't exist, should be a root policy
		log.Info("Found reconciliation request from root policy...", "Namespace", object.GetNamespace(),
			"Name", object.GetName())
		name = object.GetName()
		namespace = object.GetNamespace()
	}
	request := reconcile.Request{NamespacedName: types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}}
	result = append(result, request)
	return result
}
