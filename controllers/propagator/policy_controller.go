// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"fmt"
	"time"

	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

const ControllerName string = "policy-propagator"

var log = ctrl.Log.WithName(ControllerName)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/finalizers,verbs=update
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=placementbindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policysets,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters;placementdecisions;placements,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=*,resources=*,verbs=get;list;watch

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyReconciler) SetupWithManager(mgr ctrl.Manager, additionalSources ...source.Source) error {
	builder := ctrl.NewControllerManagedBy(mgr).
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
			&source.Kind{Type: &policiesv1beta1.PolicySet{}},
			handler.EnqueueRequestsFromMapFunc(policySetMapper(mgr.GetClient())),
			builder.WithPredicates(policySetPredicateFuncs)).
		Watches(
			&source.Kind{Type: &policiesv1.PlacementBinding{}},
			handler.EnqueueRequestsFromMapFunc(placementBindingMapper(mgr.GetClient())),
			builder.WithPredicates(pbPredicateFuncs)).
		Watches(
			&source.Kind{Type: &appsv1.PlacementRule{}},
			handler.EnqueueRequestsFromMapFunc(placementRuleMapper(mgr.GetClient()))).
		Watches(
			&source.Kind{Type: &clusterv1beta1.PlacementDecision{}},
			handler.EnqueueRequestsFromMapFunc(placementDecisionMapper(mgr.GetClient())),
		)

	for _, source := range additionalSources {
		builder.Watches(source, &handler.EnqueueRequestForObject{})
	}

	return builder.Complete(r)
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &PolicyReconciler{}

// PolicyReconciler reconciles a Policy object
type PolicyReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Recorder       record.EventRecorder
	DynamicWatcher k8sdepwatches.DynamicWatcher
}

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *PolicyReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	// Set the hub template watch metric after reconcile
	defer func() {
		hubTempWatches := r.DynamicWatcher.GetWatchCount()
		log.V(3).Info("Setting hub template watch metric", "value", hubTempWatches)

		hubTemplateActiveWatchesMetric.Set(float64(hubTempWatches))
	}()

	log.Info("Reconciling the policy")

	// Fetch the Policy instance
	instance := &policiesv1.Policy{}

	err := r.Get(ctx, request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected.
			log.Info("Policy not found, so it may have been deleted. Deleting the replicated policies.")

			err := r.cleanUpPolicy(&policiesv1.Policy{
				TypeMeta: metav1.TypeMeta{
					Kind:       policiesv1.Kind,
					APIVersion: policiesv1.GroupVersion.Group + "/" + policiesv1.GroupVersion.Version,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      request.Name,
					Namespace: request.Namespace,
				},
			})
			if err != nil {
				return reconcile.Result{}, err
			}

			return reconcile.Result{}, nil
		}

		log.Error(err, "Failed to get the policy")

		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	clusterList := &clusterv1.ManagedClusterList{}

	err = r.List(ctx, clusterList, &client.ListOptions{})
	if err != nil {
		log.Error(err, "Failed to list ManagedCluster objects. Requeueing the request.")

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
			// nolint: nilerr
			return reconcile.Result{RequeueAfter: duration}, nil
		}

		return reconcile.Result{}, nil
	}

	log = log.WithValues("name", instance.GetName(), "namespace", instance.GetNamespace())

	log.Info("The policy was found in the cluster namespace but doesn't belong to any root policy, deleting it")

	err = r.Delete(ctx, instance)
	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Failed to delete the policy")

		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}
