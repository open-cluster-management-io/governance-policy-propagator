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
func (r *RootPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("root-policy-spec").
		For(
			&policiesv1.Policy{},
			builder.WithPredicates(rootPolicyNonStatusUpdates())).
		Watches(
			&policiesv1beta1.PolicySet{},
			handler.EnqueueRequestsFromMapFunc(mapPolicySetToPolicies),
			builder.WithPredicates(policySetPolicyListChanged())).
		Watches(
			&policiesv1.PlacementBinding{},
			handler.EnqueueRequestsFromMapFunc(mapBindingToPolicies(mgr.GetClient())),
			builder.WithPredicates(bindingForPolicy())).
		Watches(
			&appsv1.PlacementRule{},
			handler.EnqueueRequestsFromMapFunc(mapPlacementRuleToPolicies(mgr.GetClient()))).
		Watches(
			&clusterv1beta1.PlacementDecision{},
			handler.EnqueueRequestsFromMapFunc(mapPlacementDecisionToPolicies(mgr.GetClient()))).
		Complete(r)
}

// policyNonStatusUpdates triggers reconciliation if the Policy has had a change that is not just
// a status update.
func rootPolicyNonStatusUpdates() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			_, isReplicated := e.Object.GetLabels()[common.RootPolicyLabel]

			return !isReplicated
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, isReplicated := e.Object.GetLabels()[common.RootPolicyLabel]

			return !isReplicated
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			_, newIsReplicated := e.ObjectNew.GetLabels()[common.RootPolicyLabel]
			_, oldIsReplicated := e.ObjectOld.GetLabels()[common.RootPolicyLabel]

			// if either has the label, it is a replicated policy
			if oldIsReplicated || newIsReplicated {
				return false
			}

			// Ignore pure status updates since those are handled by a separate controller
			return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() ||
				!equality.Semantic.DeepEqual(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels()) ||
				!equality.Semantic.DeepEqual(e.ObjectOld.GetAnnotations(), e.ObjectNew.GetAnnotations())
		},
	}
}

// mapPolicySetToPolicies maps a PolicySet to all the Policies in its policies list.
func mapPolicySetToPolicies(_ context.Context, object client.Object) []reconcile.Request {
	log := log.WithValues("policySetName", object.GetName(), "namespace", object.GetNamespace())
	log.V(2).Info("Reconcile Request for PolicySet")

	var result []reconcile.Request

	//nolint:forcetypeassert
	policySet := object.(*policiesv1beta1.PolicySet)

	for _, plc := range policySet.Spec.Policies {
		log.V(2).Info("Found reconciliation request from a policyset", "policyName", string(plc))

		request := reconcile.Request{NamespacedName: types.NamespacedName{
			Name:      string(plc),
			Namespace: object.GetNamespace(),
		}}
		result = append(result, request)
	}

	return result
}

// policySetPolicyListChanged triggers reconciliation if the list of policies in the PolicySet has
// changed, or if the PolicySet was just created or deleted.
func policySetPolicyListChanged() predicate.Funcs {
	return predicate.Funcs{
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
}

// mapBindingToPolicies maps a PlacementBinding to the Policies that are either directly in its
// subjects list, or are in a PolicySet which is a subject of this PlacementBinding.
func mapBindingToPolicies(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		//nolint:forcetypeassert
		pb := obj.(*policiesv1.PlacementBinding)

		log := log.WithValues("placementBindingName", pb.GetName(), "namespace", pb.GetNamespace())
		log.V(2).Info("Reconcile request for a PlacementBinding")

		return common.GetPoliciesInPlacementBinding(ctx, c, pb)
	}
}

// bindingForPolicy triggers reconciliation if the binding has any Policies or PolicySets in its
// subjects list.
func bindingForPolicy() predicate.Funcs {
	return predicate.Funcs{
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
}

// mapPlacementRuleToPolicies maps a PlacementRule to all Policies which are either direct subjects
// of PlacementBindings for the PlacementRule, or are in PolicySets bound to the PlacementRule.
func mapPlacementRuleToPolicies(c client.Client) handler.MapFunc {
	return func(ctx context.Context, object client.Object) []reconcile.Request {
		log := log.WithValues("placementRuleName", object.GetName(), "namespace", object.GetNamespace())

		log.V(2).Info("Reconcile Request for PlacementRule")

		// list pb
		pbList := &policiesv1.PlacementBindingList{}
		lopts := &client.ListOptions{Namespace: object.GetNamespace()}
		opts := client.MatchingFields{"placementRef.name": object.GetName()}
		opts.ApplyToList(lopts)

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

// mapPlacementDecisionToPolicies maps a PlacementDecision to all Policies which are either direct
// subjects of PlacementBindings on the decision's Placement, or are in PolicySets which are bound
// to that Placement.
func mapPlacementDecisionToPolicies(c client.Client) handler.MapFunc {
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
