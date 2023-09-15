package propagator

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=placementbindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules,verbs=get;list;watch

// SetupWithManager sets up the controller with the Manager.
func (r *ReplicatedPolicyReconciler) SetupWithManager(mgr ctrl.Manager, additionalSources ...source.Source) error {
	// only consider updates to *replicated* policies, which are not pure status updates.
	policyPredicates := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			replicated, err := common.IsReplicatedPolicy(r.Client, e.ObjectNew)
			if !replicated && err == nil {
				// If there was an error, better to consider it for a reconcile.
				return false
			}

			//nolint:forcetypeassert
			oldPolicy := e.ObjectOld.(*policiesv1.Policy)
			//nolint:forcetypeassert
			updatedPolicy := e.ObjectNew.(*policiesv1.Policy)

			// TODO: ignore updates where we've already reconciled the new ResourceVersion
			// Ignore pure status updates since those are handled by a separate controller
			return oldPolicy.Generation != updatedPolicy.Generation ||
				!equality.Semantic.DeepEqual(oldPolicy.ObjectMeta.Labels, updatedPolicy.ObjectMeta.Labels) ||
				!equality.Semantic.DeepEqual(oldPolicy.ObjectMeta.Annotations, updatedPolicy.ObjectMeta.Annotations)
		},
	}

	// placementRuleMapper maps from the PlacementRule to all possible replicated policies for its decisions
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

		var rootPolicies []reconcile.Request
		// loop through pbs find the matching ones
		for _, pb := range pbList.Items {
			match := pb.PlacementRef.APIGroup == appsv1.SchemeGroupVersion.Group &&
				pb.PlacementRef.Kind == "PlacementRule" &&
				pb.PlacementRef.Name == object.GetName()

			if match {
				rootPolicies = append(rootPolicies, common.GetPoliciesInPlacementBinding(ctx, r.Client, &pb)...)
			}
		}

		//nolint:forcetypeassert
		plr := object.(*appsv1.PlacementRule)

		var result []reconcile.Request

		// Note: during Updates, this mapper is run on both the old object and the new object. This ensures
		// that if a decision is removed, that replicated policy *will* be seen here. But it means that
		// it is more difficult to possibly ignore unchanged decisions (because we don't have access to
		// both lists at the same time here)
		for _, dec := range plr.Status.Decisions {
			for _, plc := range rootPolicies {
				result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{
					Namespace: dec.ClusterNamespace,
					Name:      plc.Namespace + "." + plc.Name,
				}})
			}
		}

		return result
	}

	// placementDecisionMapper maps from a PlacementDecision to all possible replicated policies for it
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

		var rootPolicies []reconcile.Request
		// loop through pbs find the matching ones
		for _, pb := range pbList.Items {
			match := pb.PlacementRef.APIGroup == clusterv1beta1.SchemeGroupVersion.Group &&
				pb.PlacementRef.Kind == "Placement" &&
				pb.PlacementRef.Name == placementName

			if match {
				rootPolicies = append(rootPolicies, common.GetPoliciesInPlacementBinding(ctx, r.Client, &pb)...)
			}
		}

		//nolint:forcetypeassert
		pldec := object.(*clusterv1beta1.PlacementDecision)

		var result []reconcile.Request

		// Note: during Updates, this mapper is run on both the old object and the new object. This ensures
		// that if a decision is removed, that replicated policy *will* be seen here. But it means that
		// it is more difficult to possibly ignore unchanged decisions (because we don't have access to
		// both lists at the same time here)
		for _, dec := range pldec.Status.Decisions {
			for _, plc := range rootPolicies {
				result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{
					Namespace: dec.ClusterName,
					Name:      plc.Namespace + "." + plc.Name,
				}})
			}
		}

		return result
	}

	builder := ctrl.NewControllerManagedBy(mgr).
		Named("replicated-policy-reconciler").
		For(
			&policiesv1.Policy{},
			builder.WithPredicates(policyPredicates)).
		Watches(
			&appsv1.PlacementRule{},
			handler.EnqueueRequestsFromMapFunc(placementRuleMapper)).
		Watches(
			&clusterv1beta1.PlacementDecision{},
			handler.EnqueueRequestsFromMapFunc(placementDecisionMapper))

	for _, source := range additionalSources {
		builder.WatchesRawSource(source, &handler.EnqueueRequestForObject{})
	}

	return builder.Complete(r)
}

var _ reconcile.Reconciler = &ReplicatedPolicyReconciler{}

type ReplicatedPolicyReconciler struct {
	Propagator
}

func (r *ReplicatedPolicyReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	lock, _ := r.RootPolicyLocks.LoadOrStore(request.NamespacedName, &sync.Mutex{})

	if lock.(*sync.Mutex).TryLock() {
		log.V(3).Info("Acquired the lock for the root policy")

		defer lock.(*sync.Mutex).Unlock()
	} else {
		log.V(3).Info("Could not acquire lock for the root policy, requeueing")

		return reconcile.Result{Requeue: true}, nil
	}

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

	if err := r.handleRootPolicy(instance, false); err != nil {
		log.Error(err, "Failure during root policy handling")

		propagationFailureMetric.WithLabelValues(instance.GetName(), instance.GetNamespace()).Inc()

		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}
