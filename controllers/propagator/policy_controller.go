// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	clusterv1alpha1 "github.com/open-cluster-management/api/cluster/v1alpha1"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/api/v1"
	"github.com/open-cluster-management/governance-policy-propagator/controllers/common"
	appsv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
)

const ControllerName string = "policy-propagator"

var log = logf.Log.WithName(ControllerName)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/finalizers,verbs=update
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=placementbindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters;placementdecisions;placements,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(
			&policiesv1.Policy{},
			builder.WithPredicates(common.NeverEnqueue)).
		// This is a workaround - the controller-runtime requires a "For", but does not allow it to
		// modify the eventhandler. Currently we need to enqueue requests for Policies in a very
		// particular way, so we will define that in a separate "Watches"
		Watches(
			&source.Kind{Type: &policiesv1.Policy{}},
			&common.EnqueueRequestsFromMapFunc{ToRequests: policyMapper(mgr.GetClient())}).
		Watches(
			&source.Kind{Type: &policiesv1.PlacementBinding{}},
			handler.EnqueueRequestsFromMapFunc(placementBindingMapper(mgr.GetClient())),
			builder.WithPredicates(pbPredicateFuncs)).
		Watches(
			&source.Kind{Type: &appsv1.PlacementRule{}},
			handler.EnqueueRequestsFromMapFunc(placementRuleMapper(mgr.GetClient()))).
		Watches(
			&source.Kind{Type: &clusterv1alpha1.PlacementDecision{}},
			handler.EnqueueRequestsFromMapFunc(placementDecisionMapper(mgr.GetClient()))).
		Complete(r)
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &PolicyReconciler{}

// PolicyReconciler reconciles a Policy object
type PolicyReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *PolicyReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	reqLogger.Info("Reconciling Policy...")

	// Fetch the Policy instance
	instance := &policiesv1.Policy{}
	err := r.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected.
			reqLogger.Info("Policy not found, may have been deleted, deleting replicated policies...")
			replicatedPlcList := &policiesv1.PolicyList{}
			err := r.List(context.TODO(), replicatedPlcList,
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
				err := r.Delete(context.TODO(), &plc)
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
	err = r.List(context.TODO(), clusterList, &client.ListOptions{})
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
		"Namespace", instance.GetNamespace(), "Name", instance.GetName())
	err = r.Delete(context.TODO(), instance)
	if err != nil && !errors.IsNotFound(err) {
		reqLogger.Error(err, "Failed to delete policy...", "Namespace", instance.GetNamespace(),
			"Name", instance.GetName())
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}
