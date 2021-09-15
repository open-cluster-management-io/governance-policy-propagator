// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"fmt"
	"time"

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	appsv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/apps/v1"
	clusterv1alpha1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/cluster/v1alpha1"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
	"github.com/open-cluster-management/governance-policy-propagator/pkg/controller/common"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		&common.EnqueueRequestsFromMapFunc{ToRequests: &policyMapper{mgr.GetClient()}})
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

	// Watch for changes to placementdecision
	err = c.Watch(
		&source.Kind{Type: &clusterv1alpha1.PlacementDecision{}},
		&handler.EnqueueRequestsFromMapFunc{ToRequests: &placementDecisionMapper{mgr.GetClient()}})
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
			// Owned objects are automatically garbage collected.
			reqLogger.Info("Policy not found, may have been deleted, deleting replicated policies...")
			replicatedPlcList := &policiesv1.PolicyList{}
			err := r.client.List(context.TODO(), replicatedPlcList,
				client.MatchingLabels(common.LabelsForRootPolicy(&policiesv1.Policy{
					TypeMeta: metav1.TypeMeta{
						Kind:       policiesv1.Kind,
						APIVersion: policiesv1.SchemeGroupVersion.Group,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      request.Name,
						Namespace: request.Namespace,
					},
				})))
			if err != nil {
				// there was an error, requeue
				reqLogger.Error(err, "Failed to list replicated policy...")
				return reconcile.Result{}, err
			}
			for _, plc := range replicatedPlcList.Items {
				reqLogger.Info("Deleting replicated policies...", "Namespace", plc.GetNamespace(),
					"Name", plc.GetName())
				// #nosec G601 -- no memory addresses are stored in collections
				err := r.client.Delete(context.TODO(), &plc)
				if err != nil && !errors.IsNotFound(err) {
					reqLogger.Error(err, "Failed to delete replicated policy...", "Namespace", plc.GetNamespace(),
						"Name", plc.GetName())
					return reconcile.Result{}, err
				}
			}
			reqLogger.Info("Policy clean up complete, reconciliation completed.")
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
		// handleRootPolicy handles all retries and it will give up as appropriate. In that case
		// requeue it to be reprocessed later.
		err := r.handleRootPolicy(instance)
		if err != nil {
			r.recordWarning(
				instance,
				fmt.Sprintf("Retrying the request in %d minutes", requeueErrorDelay),
			)
			duration := time.Duration(requeueErrorDelay) * time.Minute
			// An error must not be returned for RequeueAfter to take effect. See:
			// https://github.com/kubernetes-sigs/controller-runtime/blob/5de246bfbfd1a75f966b5662edcb9c7235244160/pkg/internal/controller/controller.go#L319-L322
			return reconcile.Result{RequeueAfter: duration}, nil
		}

		return reconcile.Result{}, nil
	}

	reqLogger.Info("Policy was found in cluster namespace but doesn't belong to any root policy, deleting it...",
		instance.GetNamespace(), "Name", instance.GetName())
	err = r.client.Delete(context.TODO(), instance)
	if err != nil && !errors.IsNotFound(err) {
		reqLogger.Error(err, "Failed to delete policy...", "Namespace", instance.GetNamespace(),
			"Name", instance.GetName())
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}
