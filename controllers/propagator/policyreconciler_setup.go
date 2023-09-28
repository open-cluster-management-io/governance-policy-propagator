// Copyright (c) 2023 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
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
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/finalizers,verbs=update
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=placementbindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policysets,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters;placementdecisions;placements,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps.open-cluster-management.io,resources=placementrules,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch;delete
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
			&policiesv1.Policy{},
			handler.EnqueueRequestsFromMapFunc(common.PolicyMapper(mgr.GetClient())),
			builder.WithPredicates(policyPredicates())).
		Watches(
			&policiesv1beta1.PolicySet{},
			handler.EnqueueRequestsFromMapFunc(policySetMapper(mgr.GetClient())),
			builder.WithPredicates(policySetPredicateFuncs)).
		Watches(
			&policiesv1.PlacementBinding{},
			handler.EnqueueRequestsFromMapFunc(placementBindingMapper(mgr.GetClient())),
			builder.WithPredicates(pbPredicateFuncs)).
		Watches(
			&appsv1.PlacementRule{},
			handler.EnqueueRequestsFromMapFunc(placementRuleMapper(mgr.GetClient()))).
		Watches(
			&clusterv1beta1.PlacementDecision{},
			handler.EnqueueRequestsFromMapFunc(placementDecisionMapper(mgr.GetClient())),
		)

	for _, source := range additionalSources {
		builder.WatchesRawSource(source, &handler.EnqueueRequestForObject{})
	}

	return builder.Complete(r)
}

// policyPredicates filters out updates to policies that are pure status updates.
func policyPredicates() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
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
}

func policySetMapper(_ client.Client) handler.MapFunc {
	return func(ctx context.Context, object client.Object) []reconcile.Request {
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
}

// we only want to watch for policyset objects with Spec.Policies field change
var policySetPredicateFuncs = predicate.Funcs{
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

func placementBindingMapper(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		//nolint:forcetypeassert
		pb := obj.(*policiesv1.PlacementBinding)

		log := log.WithValues("placementBindingName", pb.GetName(), "namespace", pb.GetNamespace())
		log.V(2).Info("Reconcile request for a PlacementBinding")

		return common.GetPoliciesInPlacementBinding(ctx, c, pb)
	}
}

// we only want to watch for pb contains policy and policyset as subjects
var pbPredicateFuncs = predicate.Funcs{
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

func placementRuleMapper(c client.Client) handler.MapFunc {
	return func(ctx context.Context, object client.Object) []reconcile.Request {
		log := log.WithValues("placementRuleName", object.GetName(), "namespace", object.GetNamespace())

		log.V(2).Info("Reconcile Request for PlacementRule")

		// list pb
		pbList := &policiesv1.PlacementBindingList{}

		// find pb in the same namespace of placementrule
		err := c.List(ctx, pbList, &client.ListOptions{Namespace: object.GetNamespace()})
		if err != nil {
			return nil
		}

		var result []reconcile.Request
		// loop through pbs and collect policies from each matching one.
		for _, pb := range pbList.Items {
			if pb.PlacementRef.APIGroup != appsv1.SchemeGroupVersion.Group ||
				pb.PlacementRef.Kind != "PlacementRule" || pb.PlacementRef.Name != object.GetName() {
				continue
			}

			result = append(result, common.GetPoliciesInPlacementBinding(ctx, c, &pb)...)
		}

		return result
	}
}

func placementDecisionMapper(c client.Client) handler.MapFunc {
	return func(ctx context.Context, object client.Object) []reconcile.Request {
		log := log.WithValues("placementDecisionName", object.GetName(), "namespace", object.GetNamespace())

		log.V(2).Info("Reconcile request for a placement decision")

		// get the placement name from the placementdecision
		placementName := object.GetLabels()["cluster.open-cluster-management.io/placement"]
		if placementName == "" {
			return nil
		}

		pbList := &policiesv1.PlacementBindingList{}
		// find pb in the same namespace of placementrule
		lopts := &client.ListOptions{Namespace: object.GetNamespace()}
		opts := client.MatchingFields{"placementRef.name": placementName}
		opts.ApplyToList(lopts)

		err := c.List(ctx, pbList, lopts)
		if err != nil {
			return nil
		}

		var result []reconcile.Request
		// loop through pbs and collect policies from each matching one.
		for _, pb := range pbList.Items {
			if pb.PlacementRef.APIGroup != clusterv1beta1.SchemeGroupVersion.Group ||
				pb.PlacementRef.Kind != "Placement" || pb.PlacementRef.Name != placementName {
				continue
			}

			result = append(result, common.GetPoliciesInPlacementBinding(ctx, c, &pb)...)
		}

		return result
	}
}
