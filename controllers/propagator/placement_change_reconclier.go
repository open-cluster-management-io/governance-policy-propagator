package propagator

import (
	"context"
	"sync"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=placementbindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules,verbs=get;list;watch

// SetupWithManager sets up the controller with the Manager.
func (r *PlacementChangeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	placementBindingMapper := func(ctx context.Context, obj client.Object) []reconcile.Request {
		//nolint:forcetypeassert
		pb := obj.(*policiesv1.PlacementBinding)

		log := log.WithValues("placementBindingName", pb.GetName(), "namespace", pb.GetNamespace())
		log.V(2).Info("Reconcile request for a PlacementBinding")

		return common.GetPoliciesInPlacementBinding(ctx, r.Client, pb)
	}

	// only reconcile when the pb contains a policy or a policyset as a subject
	pbPredicateFuncs := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			//nolint:forcetypeassert
			pbObjNew := e.ObjectNew.(*policiesv1.PlacementBinding)
			//nolint:forcetypeassert
			pbObjOld := e.ObjectOld.(*policiesv1.PlacementBinding)

			return common.IsForPolicyOrPolicySet(pbObjNew) || common.IsForPolicyOrPolicySet(pbObjOld)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			//nolint:forcetypeassert
			pbObj := e.Object.(*policiesv1.PlacementBinding)

			return common.IsForPolicyOrPolicySet(pbObj)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			//nolint:forcetypeassert
			pbObj := e.Object.(*policiesv1.PlacementBinding)

			return common.IsForPolicyOrPolicySet(pbObj)
		},
	}

	// placementRuleMapper maps from a PlacementRule to the PlacementBindings associated with it
	placementRuleMapper := func(ctx context.Context, object client.Object) []reconcile.Request {
		log := log.WithValues("placementRuleName", object.GetName(), "namespace", object.GetNamespace())
		log.V(2).Info("Reconcile Request for PlacementRule")

		pbList := &policiesv1.PlacementBindingList{}
		lopts := &client.ListOptions{Namespace: object.GetNamespace()}

		opts := client.MatchingFields{"placementRef.name": object.GetName()}
		opts.ApplyToList(lopts)

		if err := r.List(ctx, pbList, lopts); err != nil {
			return nil
		}

		var result []reconcile.Request
		// loop through pbs find the matching ones
		for _, pb := range pbList.Items {
			match := pb.PlacementRef.APIGroup == appsv1.SchemeGroupVersion.Group &&
				pb.PlacementRef.Kind == "PlacementRule" &&
				pb.PlacementRef.Name == object.GetName()

			if match {
				result = append(result, common.GetPoliciesInPlacementBinding(ctx, r.Client, &pb)...)
			}
		}

		return result
	}

	// placementDecisionMapper maps from a PlacementDecision to the PlacementBindings associated with its Placement
	placementDecisionMapper := func(ctx context.Context, object client.Object) []reconcile.Request {
		log := log.WithValues("placementDecisionName", object.GetName(), "namespace", object.GetNamespace())
		log.V(2).Info("Reconcile request for a placement decision")

		// get the Placement name from the PlacementDecision
		placementName := object.GetLabels()["cluster.open-cluster-management.io/placement"]
		if placementName == "" {
			return nil
		}

		pbList := &policiesv1.PlacementBindingList{}
		lopts := &client.ListOptions{Namespace: object.GetNamespace()}

		opts := client.MatchingFields{"placementRef.name": placementName}
		opts.ApplyToList(lopts)

		if err := r.List(ctx, pbList, lopts); err != nil {
			return nil
		}

		var result []reconcile.Request
		// loop through pbs find the matching ones
		for _, pb := range pbList.Items {
			match := pb.PlacementRef.APIGroup == clusterv1beta1.SchemeGroupVersion.Group &&
				pb.PlacementRef.Kind == "Placement" &&
				pb.PlacementRef.Name == placementName

			if match {
				result = append(result, common.GetPoliciesInPlacementBinding(ctx, r.Client, &pb)...)
			}
		}

		return result
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("placement-change-reconciler").
		For(
			&policiesv1.Policy{},
			builder.WithPredicates(common.NeverEnqueue)).
		// This is a workaround - the controller-runtime requires a "For", but does not allow it to
		// modify the eventhandler. Currently we need to enqueue requests for Policies in a very
		// particular way, so we will define that in a separate "Watches"
		Watches(
			&policiesv1.PlacementBinding{},
			handler.EnqueueRequestsFromMapFunc(placementBindingMapper),
			builder.WithPredicates(pbPredicateFuncs)).
		Watches(
			&appsv1.PlacementRule{},
			handler.EnqueueRequestsFromMapFunc(placementRuleMapper)).
		Watches(
			&clusterv1beta1.PlacementDecision{},
			handler.EnqueueRequestsFromMapFunc(placementDecisionMapper)).
		Complete(r)
}

var _ reconcile.Reconciler = &PlacementChangeReconciler{}

type PlacementChangeReconciler struct {
	Propagator
}

func (r *PlacementChangeReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	log.V(3).Info("Acquiring the lock for the root policy")

	lock, _ := r.RootPolicyLocks.LoadOrStore(request.NamespacedName, &sync.Mutex{})

	lock.(*sync.Mutex).Lock() // TODO: consider TryLock, to not let too many reconcilers be stuck on the same policy
	defer lock.(*sync.Mutex).Unlock()

	log.Info("Reconciling the policy")

	// Fetch the Policy instance
	instance := &policiesv1.Policy{}

	if err := r.Get(ctx, request.NamespacedName, instance); err != nil {
		if k8serrors.IsNotFound(err) {
			// This will be handled by the root-policy-controller
			return reconcile.Result{}, nil
		}

		log.Error(err, "Failed to get the policy")

		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	if err := r.handleRootPolicy(instance); err != nil { // TODO: this handling can be slimmer than "usual"
		log.Error(err, "Failure during root policy handling")

		propagationFailureMetric.WithLabelValues(instance.GetName(), instance.GetNamespace()).Inc()

		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}
