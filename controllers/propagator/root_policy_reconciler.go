// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/finalizers,verbs=update
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policysets,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters;placementdecisions;placements,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=*,resources=*,verbs=get;list;watch

// SetupWithManager sets up the controller with the Manager.
func (r *RootPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// only consider updates to *root* policies, which are not pure status updates.
	policyPredicates := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			replicated, err := common.IsReplicatedPolicy(r.Client, e.Object)
			if replicated && err == nil {
				// If there was an error, better to consider it for a reconcile.
				return false
			}

			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			replicated, err := common.IsReplicatedPolicy(r.Client, e.Object)
			if replicated && err == nil {
				// If there was an error, better to consider it for a reconcile.
				return false
			}

			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			replicated, err := common.IsReplicatedPolicy(r.Client, e.ObjectNew)
			if replicated && err == nil {
				// If there was an error, better consider it for a reconcile.
				return false
			}

			//nolint:forcetypeassert
			oldPolicy := e.ObjectOld.(*policiesv1.Policy)
			//nolint:forcetypeassert
			updatedPolicy := e.ObjectNew.(*policiesv1.Policy)

			// Ignore pure status updates since those are handled by a separate controller
			return oldPolicy.Generation != updatedPolicy.Generation ||
				!equality.Semantic.DeepEqual(oldPolicy.ObjectMeta.Labels, updatedPolicy.ObjectMeta.Labels) ||
				!equality.Semantic.DeepEqual(oldPolicy.ObjectMeta.Annotations, updatedPolicy.ObjectMeta.Annotations)
		},
	}

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

	policySetMapper := func(ctx context.Context, object client.Object) []reconcile.Request {
		log := log.WithValues("policySetName", object.GetName(), "namespace", object.GetNamespace())
		log.V(2).Info("Reconcile Request for PolicySet")

		var result []reconcile.Request

		for _, plc := range object.(*policiesv1beta1.PolicySet).Spec.Policies {
			log.V(2).Info("Found reconciliation request from a policyset", "policyName", string(plc))

			request := reconcile.Request{NamespacedName: types.NamespacedName{
				Name:      string(plc),
				Namespace: object.GetNamespace(),
			}}
			result = append(result, request)
		}

		return result
	}

	// we only want to watch for policyset objects with Spec.Policies field change
	policySetPredicateFuncs := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			//nolint:forcetypeassert
			policySetObjNew := e.ObjectNew.(*policiesv1beta1.PolicySet)
			//nolint:forcetypeassert
			policySetObjOld := e.ObjectOld.(*policiesv1beta1.PolicySet)

			return !equality.Semantic.DeepEqual(policySetObjNew.Spec.Policies, policySetObjOld.Spec.Policies)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("root-policy-reconciler").
		For(
			&policiesv1.Policy{},
			builder.WithPredicates(policyPredicates)).
		Watches(
			&policiesv1.PlacementBinding{},
			handler.EnqueueRequestsFromMapFunc(placementBindingMapper),
			builder.WithPredicates(pbPredicateFuncs)).
		Watches(
			&policiesv1beta1.PolicySet{},
			handler.EnqueueRequestsFromMapFunc(policySetMapper),
			builder.WithPredicates(policySetPredicateFuncs)).
		Complete(r)
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &RootPolicyReconciler{}

type RootPolicyReconciler struct {
	Propagator
}

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *RootPolicyReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	log.V(3).Info("Acquiring the lock for the root policy")

	lock, _ := r.RootPolicyLocks.LoadOrStore(request.NamespacedName, &sync.Mutex{})

	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

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

	inClusterNs, err := common.IsInClusterNamespace(r.Client, instance.Namespace)
	if err != nil {
		log.Error(err, "Failed to determine if the policy is in a managed cluster namespace. Requeueing the request.")

		return reconcile.Result{}, err
	}

	if !inClusterNs {
		err := r.handleRootPolicy(instance, true)
		if err != nil {
			log.Error(err, "Failure during root policy handling")

			propagationFailureMetric.WithLabelValues(instance.GetName(), instance.GetNamespace()).Inc()
		}

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
