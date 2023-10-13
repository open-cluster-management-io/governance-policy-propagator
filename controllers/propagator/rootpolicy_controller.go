// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"sync"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

const ControllerName string = "policy-propagator"

var log = ctrl.Log.WithName(ControllerName)

type RootPolicyReconciler struct {
	Propagator
}

// Reconcile handles root policies, sending events to the replicated policy reconciler to ensure
// that the desired policies are on the correct clusters. It also populates the status of the root
// policy with placement information.
func (r *RootPolicyReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	log.V(3).Info("Acquiring the lock for the root policy")

	lock, _ := r.RootPolicyLocks.LoadOrStore(request.NamespacedName, &sync.Mutex{})

	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

	log.Info("Reconciling the policy")

	// Fetch the Policy instance
	instance := &policiesv1.Policy{}

	err := r.Get(ctx, request.NamespacedName, instance)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			count, err := r.updateExistingReplicas(ctx, request.Namespace+"."+request.Name)
			if err != nil {
				log.Error(err, "Failed to send update events to replicated policies, requeueing")

				return reconcile.Result{}, err
			}

			log.Info("Replicated policies sent for deletion", "count", count)

			return reconcile.Result{}, nil
		}

		log.Error(err, "Failed to get the policy")

		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	inClusterNs, err := common.IsInClusterNamespace(ctx, r.Client, instance.Namespace)
	if err != nil {
		log.Error(err, "Failed to determine if the policy is in a managed cluster namespace. Requeueing the request.")

		return reconcile.Result{}, err
	}

	if !inClusterNs {
		err := r.handleRootPolicy(ctx, instance)
		if err != nil {
			log.Error(err, "Failure during root policy handling")

			propagationFailureMetric.WithLabelValues(instance.GetName(), instance.GetNamespace()).Inc()
		}

		log.Info("Reconciliation complete")

		return reconcile.Result{}, err
	}

	log = log.WithValues("name", instance.GetName(), "namespace", instance.GetNamespace())

	log.Info("The policy was found in the cluster namespace but doesn't belong to any root policy, deleting it")

	err = r.Delete(ctx, instance)
	if err != nil && !k8serrors.IsNotFound(err) {
		log.Error(err, "Failed to delete the policy")

		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

//+kubebuilder:object:root=true

type GuttedObject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
}

// updateExistingReplicas lists all existing replicated policies for this root policy, and sends
// events for each of them to the replicated policy reconciler. This will trigger updates on those
// replicated policies, for example when the root policy spec changes, or when the replicated
// policies might need to be deleted.
func (r *RootPolicyReconciler) updateExistingReplicas(ctx context.Context, rootPolicyFullName string) (int, error) {
	// Get all the replicated policies for this root policy
	policyList := &policiesv1.PolicyList{}
	opts := &client.ListOptions{}

	matcher := client.MatchingLabels{common.RootPolicyLabel: rootPolicyFullName}
	matcher.ApplyToList(opts)

	err := r.List(ctx, policyList, opts)
	if err != nil && !k8serrors.IsNotFound(err) {
		return 0, err
	}

	for _, replicated := range policyList.Items {
		simpleObj := &GuttedObject{
			TypeMeta: metav1.TypeMeta{
				Kind:       replicated.Kind,
				APIVersion: replicated.APIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      replicated.Name,
				Namespace: replicated.Namespace,
			},
		}

		r.ReplicatedPolicyUpdates <- event.GenericEvent{Object: simpleObj}
	}

	return len(policyList.Items), nil
}
