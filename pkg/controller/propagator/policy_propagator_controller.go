// Copyright (c) 2020 Red Hat, Inc.
package propagator

import (
	"context"

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	appsv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/apps/v1"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	"github.com/open-cluster-management/governance-policy-propagator/pkg/controller/common"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName string = "policy-propagator"

var log = logf.Log.WithName(controllerName)

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new Policy Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcilePolicy{client: mgr.GetClient(), scheme: mgr.GetScheme(),
		recorder: mgr.GetEventRecorderFor(controllerName)}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New(controllerName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Policy
	err = c.Watch(
		&source.Kind{Type: &policiesv1.Policy{}},
		&handler.EnqueueRequestsFromMapFunc{ToRequests: &policyMapper{mgr.GetClient()}})
	if err != nil {
		return err
	}

	// Watch for changes to placementbinding
	err = c.Watch(
		&source.Kind{Type: &policiesv1.PlacementBinding{}},
		&handler.EnqueueRequestsFromMapFunc{ToRequests: &placementBindingMapper{mgr.GetClient()}}, pbPredicateFuncs)
	if err != nil {
		return err
	}

	// Watch for changes to placementrule
	err = c.Watch(
		&source.Kind{Type: &appsv1.PlacementRule{}},
		&handler.EnqueueRequestsFromMapFunc{ToRequests: &placementRuleMapper{mgr.GetClient()}})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcilePolicy{}

// ReconcilePolicy reconciles a Policy object
type ReconcilePolicy struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client   client.Client
	scheme   *runtime.Scheme
	recorder record.EventRecorder
}

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePolicy) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Policy...")

	// Fetch the Policy instance
	instance := &policiesv1.Policy{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			reqLogger.Info("Policy not found, may have been deleted, reconciliation completed.")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	clusterList := &clusterv1.ManagedClusterList{}
	err = r.client.List(context.TODO(), clusterList, &client.ListOptions{})
	if err != nil {
		reqLogger.Error(err, "Failed to list cluster, going to retry...")
		return reconcile.Result{}, err
	}

	if !common.IsInClusterNamespace(request.Namespace, clusterList.Items) {
		return reconcile.Result{}, r.handleRootPolicy(instance)
	}

	reqLogger.Info("Policy was in cluster namespace but has no ownerReferences, ignoring it...")
	return reconcile.Result{}, nil
}
