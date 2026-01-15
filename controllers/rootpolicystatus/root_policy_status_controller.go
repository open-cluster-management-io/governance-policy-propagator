// Copyright Contributors to the Open Cluster Management project

package policystatus

import (
	"context"
	"sync"

	"github.com/go-logr/logr"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

const ControllerName string = "root-policy-status"

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/status,verbs=get;update;patch

// SetupWithManager sets up the controller with the Manager.
func (r *RootPolicyStatusReconciler) SetupWithManager(
	mgr ctrl.Manager,
	maxConcurrentReconciles uint16,
	plrsEnabled bool,
) error {
	log := ctrl.Log.WithName(ControllerName)

	ctrlBldr := ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(
			&policiesv1.Policy{},
			builder.WithPredicates(common.NeverEnqueue),
		).
		Watches(
			&policiesv1.PlacementBinding{},
			handler.EnqueueRequestsFromMapFunc(mapBindingToPolicies(log, mgr.GetClient())),
		).
		Watches(
			&clusterv1beta1.PlacementDecision{},
			handler.EnqueueRequestsFromMapFunc(mapDecisionToPolicies(log, mgr.GetClient())),
		).
		// This is a workaround - the controller-runtime requires a "For", but does not allow it to
		// modify the eventhandler. Currently we need to enqueue requests for Policies in a very
		// particular way, so we will define that in a separate "Watches"
		Watches(
			&policiesv1.Policy{},
			handler.EnqueueRequestsFromMapFunc(common.MapToRootPolicy(log, mgr.GetClient())),
			builder.WithPredicates(policyStatusPredicate()),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: int(maxConcurrentReconciles)}).
		WithLogConstructor(func(req *reconcile.Request) logr.Logger {
			log = common.LogConstructor(ControllerName, "Policy", req)

			return log
		})

	if plrsEnabled {
		ctrlBldr = ctrlBldr.Watches(&appsv1.PlacementRule{},
			handler.EnqueueRequestsFromMapFunc(mapRuleToPolicies(log, mgr.GetClient())))
	}

	return ctrlBldr.Complete(r)
}

// blank assignment to verify that RootPolicyStatusReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &RootPolicyStatusReconciler{}

// RootPolicyStatusReconciler handles replicated policy status updates and updates the root policy status.
type RootPolicyStatusReconciler struct {
	client.Client
	// Use a shared lock with the main policy controller to avoid conflicting updates.
	RootPolicyLocks *sync.Map
	Scheme          *runtime.Scheme
}

// Reconcile will update the root policy status based on the current state whenever a root or replicated policy status
// is updated. The reconcile request is always on the root policy. This approach is taken rather than just handling a
// single replicated policy status per reconcile to be able to "batch" status update requests when there are bursts of
// replicated policy status updates. This lowers resource utilization on the controller and the Kubernetes API server.
func (r *RootPolicyStatusReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("Reconciling the root policy status")

	log.V(3).Info("Acquiring the lock for the root policy")

	lock, _ := r.RootPolicyLocks.LoadOrStore(request.NamespacedName, &sync.Mutex{})

	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

	log.V(2).Info("Retrieving root policy")

	rootPolicy := &policiesv1.Policy{}

	err := r.Get(ctx, request.NamespacedName, rootPolicy)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.V(2).Info("The root policy has been deleted. Doing nothing.")

			return reconcile.Result{}, nil
		}

		log.Error(err, "Failed to get the root policy")

		return reconcile.Result{}, err
	}

	log.V(2).Info("Successfully retrieved root policy",
		"resourceVersion", rootPolicy.GetResourceVersion())

	// Replicated policies don't need to update status here
	if _, ok := rootPolicy.Labels["policy.open-cluster-management.io/root-policy"]; ok {
		log.V(2).Info("Policy is a replicated policy, skipping status update")

		return reconcile.Result{}, nil
	}

	log.V(1).Info("Processing root policy status update")

	_, err = common.RootStatusUpdate(ctx, r.Client, rootPolicy)
	if err != nil {
		log.Error(err, "Failed to update root policy status")

		return reconcile.Result{}, err
	}

	log.V(1).Info("Successfully updated root policy status")

	return reconcile.Result{}, nil
}
