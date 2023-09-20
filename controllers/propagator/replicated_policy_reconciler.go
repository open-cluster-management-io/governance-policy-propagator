package propagator

import (
	"context"
	"fmt"
	"strings"

	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
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
		CreateFunc: func(e event.CreateEvent) bool {
			replicated, err := common.IsReplicatedPolicy(r.Client, e.Object)
			if !replicated && err == nil {
				// If there was an error, better to consider it for a reconcile.
				return false
			}

			// TOBEDONE: ignore the create event from when this controller creates the resource
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			replicated, err := common.IsReplicatedPolicy(r.Client, e.Object)
			if !replicated && err == nil {
				// If there was an error, better to consider it for a reconcile.
				return false
			}

			// TOBEDONE: ignore the delete event from when this controller deletes the resource
			return true
		},
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

			// TOBEDONE: ignore updates where we've already reconciled the new ResourceVersion
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
	log.Info("Reconciling the replicated policy")

	replicatedExists := true
	replicatedPolicy := &policiesv1.Policy{}

	if err := r.Get(ctx, request.NamespacedName, replicatedPolicy); err != nil {
		if !k8serrors.IsNotFound(err) {
			log.Error(err, "Failed to get the replicated policy")

			return reconcile.Result{}, err
		}

		replicatedExists = false
	}

	rootNS, rootName, dotFound := strings.Cut(request.Name, ".")
	if !dotFound && replicatedExists {
		if err := r.cleanUpReplicated(ctx, replicatedPolicy); err != nil {
			if !k8serrors.IsNotFound(err) {
				log.Error(err, "Failed to delete the invalid replicated policy, requeueing")

				return reconcile.Result{}, err
			}
		}

		log.Info("Invalid replicated policy deleted")

		return reconcile.Result{}, nil
	}

	// Fetch the Root Policy instance
	rootPolicy := &policiesv1.Policy{}
	rootNN := types.NamespacedName{Namespace: rootNS, Name: rootName}

	if err := r.Get(ctx, rootNN, rootPolicy); err != nil {
		if k8serrors.IsNotFound(err) {
			if replicatedExists {
				if err := r.cleanUpReplicated(ctx, replicatedPolicy); err != nil {
					if !k8serrors.IsNotFound(err) {
						log.Error(err, "Failed to delete the orphaned replicated policy, requeueing")

						return reconcile.Result{}, err
					}
				}

				log.Info("Orphaned replicated policy deleted")

				return reconcile.Result{}, nil
			}

			log.Info("Root policy and replicated policy already missing")

			return reconcile.Result{}, nil
		}

		log.Error(err, "Failed to get the root policy, requeueing")

		return reconcile.Result{}, err
	}

	if rootPolicy.Spec.Disabled {
		if replicatedExists {
			if err := r.cleanUpReplicated(ctx, replicatedPolicy); err != nil {
				if !k8serrors.IsNotFound(err) {
					log.Error(err, "Failed to delete the disabled replicated policy, requeueing")

					return reconcile.Result{}, err
				}
			}

			log.Info("Disabled replicated policy deleted")

			return reconcile.Result{}, nil
		}

		log.Info("Root policy is disabled, and replicated policy correctly not found.")

		return reconcile.Result{}, nil
	}

	// calculate the decision for this specific cluster
	decision, err := r.singleClusterDecision(ctx, rootPolicy, request.Namespace)
	if err != nil {
		log.Error(err, "Failed to determine if policy should be replicated, requeueing")

		return reconcile.Result{}, err
	}

	// an empty decision means the policy should not be replicated
	if decision.Cluster.ClusterName == "" {
		if replicatedExists {
			if err := r.cleanUpReplicated(ctx, replicatedPolicy); err != nil {
				if !k8serrors.IsNotFound(err) {
					log.Error(err, "Failed to remove the replicated policy for this managed cluster, requeueing")

					return reconcile.Result{}, err
				}
			}

			log.Info("Removed replicated policy from managed cluster")

			return reconcile.Result{}, nil
		}

		log.Info("Replicated policy should not exist on this managed cluster, and does not.")

		return reconcile.Result{}, nil
	}

	objsToWatch := getPolicySetDependencies(rootPolicy)

	desiredReplicatedPolicy, err := r.buildReplicatedPolicy(rootPolicy, decision)
	if err != nil {
		log.Error(err, "Unable to build desired replicated policy, requeueing")

		return reconcile.Result{}, err
	}

	// save the watcherError for later, so that the policy can still be updated now.
	var watcherErr error

	if policyHasTemplates(rootPolicy) {
		if replicatedExists {
			// If the replicated policy has an initialization vector specified, set it for processing
			if initializationVector, ok := replicatedPolicy.Annotations[IVAnnotation]; ok {
				tempAnnotations := desiredReplicatedPolicy.GetAnnotations()
				if tempAnnotations == nil {
					tempAnnotations = make(map[string]string)
				}

				tempAnnotations[IVAnnotation] = initializationVector

				desiredReplicatedPolicy.SetAnnotations(tempAnnotations)
			}
		}

		// resolve hubTemplate before replicating
		// #nosec G104 -- any errors are logged and recorded in the processTemplates method,
		// but the ignored status will be handled appropriately by the policy controllers on
		// the managed cluster(s).
		templObjsToWatch, _ := r.processTemplates(desiredReplicatedPolicy, decision.Cluster, rootPolicy)

		for objID, val := range templObjsToWatch {
			if val {
				objsToWatch[objID] = true
			}
		}
	}

	instanceGVK := desiredReplicatedPolicy.GroupVersionKind()
	instanceObjID := k8sdepwatches.ObjectIdentifier{
		Group:     instanceGVK.Group,
		Version:   instanceGVK.Version,
		Kind:      instanceGVK.Kind,
		Namespace: request.Namespace,
		Name:      request.Name,
	}
	refObjs := make([]k8sdepwatches.ObjectIdentifier, 0, len(objsToWatch))

	for refObj := range objsToWatch {
		refObjs = append(refObjs, refObj)
	}

	if len(refObjs) != 0 {
		watcherErr = r.DynamicWatcher.AddOrUpdateWatcher(instanceObjID, refObjs...)
		if watcherErr != nil {
			log.Error(watcherErr, "Failed to update the dynamic watches for the hub policy templates")
		}
	} else {
		watcherErr = r.DynamicWatcher.RemoveWatcher(instanceObjID)
		if watcherErr != nil {
			log.Error(watcherErr, "Failed to remove the dynamic watches for the hub policy templates")
		}
	}

	if !replicatedExists {
		err = r.Create(ctx, desiredReplicatedPolicy)
		if err != nil {
			log.Error(err, "Failed to create the replicated policy, requeueing")

			return reconcile.Result{}, err
		}

		log.Info("Created replicated policy")

		return reconcile.Result{}, nil
	}

	// replicated policy already created, need to compare and possibly update
	if !equivalentReplicatedPolicies(desiredReplicatedPolicy, replicatedPolicy) {
		replicatedPolicy.SetAnnotations(desiredReplicatedPolicy.GetAnnotations())
		replicatedPolicy.SetLabels(desiredReplicatedPolicy.GetLabels())
		replicatedPolicy.Spec = desiredReplicatedPolicy.Spec

		err = r.Update(ctx, replicatedPolicy)
		if err != nil {
			log.Error(err, "Failed to update the replicated policy, requeueing")

			return reconcile.Result{}, err
		}

		log.Info("Replicated policy updated")
	} else {
		log.Info("Replicated policy matches, no update needed")
	}

	if watcherErr != nil {
		log.Info("Requeueing for the dynamic watcher error")
	}

	return reconcile.Result{}, watcherErr
}

func (r *ReplicatedPolicyReconciler) cleanUpReplicated(ctx context.Context, replicatedPolicy *policiesv1.Policy) error {
	gvk := replicatedPolicy.GroupVersionKind()

	watcherErr := r.DynamicWatcher.RemoveWatcher(k8sdepwatches.ObjectIdentifier{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Kind:      gvk.Kind,
		Namespace: replicatedPolicy.Namespace,
		Name:      replicatedPolicy.Name,
	})

	deleteErr := r.Delete(ctx, replicatedPolicy)

	if watcherErr != nil {
		return watcherErr
	}

	if deleteErr != nil {
		return deleteErr
	}

	return nil
}

func (r *ReplicatedPolicyReconciler) singleClusterDecision(
	ctx context.Context, rootPlc *policiesv1.Policy, clusterName string,
) (decision clusterDecision, err error) {
	positiveDecision := clusterDecision{
		Cluster: appsv1.PlacementDecision{
			ClusterName:      clusterName,
			ClusterNamespace: clusterName,
		},
	}

	pbList := &policiesv1.PlacementBindingList{}

	err = r.List(ctx, pbList, &client.ListOptions{Namespace: rootPlc.GetNamespace()})
	if err != nil {
		return clusterDecision{}, err
	}

	foundWithoutSubFilter := false

	// Process all placement bindings without subFilter
	for _, pb := range pbList.Items {
		if pb.SubFilter == policiesv1.Restricted {
			continue
		}

		found, err := r.isSingleClusterInDecisions(ctx, &pb, rootPlc.GetName(), clusterName)
		if err != nil {
			return clusterDecision{}, err
		}

		if !found {
			continue
		}

		if strings.EqualFold(pb.BindingOverrides.RemediationAction, string(policiesv1.Enforce)) {
			positiveDecision.PolicyOverrides = pb.BindingOverrides
			// If an override is found, then no other decisions can currently change this result.
			// NOTE: if additional overrides are added in the future, this will additional logic.
			return positiveDecision, nil
		}

		foundWithoutSubFilter = true
	}

	if !foundWithoutSubFilter {
		// No need to look through the subFilter bindings.
		return clusterDecision{}, nil
	}

	// Process all placement bindings with subFilter
	for _, pb := range pbList.Items {
		if pb.SubFilter != policiesv1.Restricted {
			continue
		}

		found, err := r.isSingleClusterInDecisions(ctx, &pb, rootPlc.GetName(), clusterName)
		if err != nil {
			return clusterDecision{}, err
		}

		if !found {
			continue
		}

		if strings.EqualFold(pb.BindingOverrides.RemediationAction, string(policiesv1.Enforce)) {
			positiveDecision.PolicyOverrides = pb.BindingOverrides
			// If an override is found, then no other decisions can currently change this result.
			// NOTE: if additional overrides are added in the future, this will additional logic.
			return positiveDecision, nil
		}
	}

	// None of the bindings had any overrides.
	return positiveDecision, nil
}

func (r *ReplicatedPolicyReconciler) isSingleClusterInDecisions(
	ctx context.Context, pb *policiesv1.PlacementBinding, policyName, clusterName string,
) (found bool, err error) {
	if !common.HasValidPlacementRef(pb) {
		return false, nil
	}

	subjectFound := false
subjectLoop:
	for _, subject := range pb.Subjects {
		if subject.APIGroup != policiesv1.SchemeGroupVersion.Group {
			continue
		}

		switch subject.Kind {
		case policiesv1.Kind:
			if subject.Name == policyName {
				subjectFound = true

				break subjectLoop
			}
		case policiesv1.PolicySetKind:
			if r.isPolicyInPolicySet(policyName, subject.Name, pb.GetNamespace()) {
				subjectFound = true

				break subjectLoop
			}
		}
	}

	if !subjectFound {
		return false, nil
	}

	refNN := types.NamespacedName{
		Namespace: pb.GetNamespace(),
		Name:      pb.PlacementRef.Name,
	}

	switch pb.PlacementRef.Kind {
	case "Placement":
		pl := &clusterv1beta1.Placement{}

		err := r.Get(ctx, refNN, pl)
		if err != nil && !k8serrors.IsNotFound(err) {
			return false, fmt.Errorf("failed to get Placement '%v': %w", pb.PlacementRef.Name, err)
		}

		if k8serrors.IsNotFound(err) {
			return false, nil
		}

		list := &clusterv1beta1.PlacementDecisionList{}
		lopts := &client.ListOptions{Namespace: pb.GetNamespace()}

		opts := client.MatchingLabels{"cluster.open-cluster-management.io/placement": pl.GetName()}
		opts.ApplyToList(lopts)

		err = r.List(ctx, list, lopts)
		if err != nil && !k8serrors.IsNotFound(err) {
			return false, fmt.Errorf("failed to list the PlacementDecisions for '%v', %w", pb.PlacementRef.Name, err)
		}

		for _, item := range list.Items {
			for _, cluster := range item.Status.Decisions {
				if cluster.ClusterName == clusterName {
					return true, nil
				}
			}
		}
	case "PlacementRule":
		plr := &appsv1.PlacementRule{}
		if err := r.Get(ctx, refNN, plr); err != nil && !k8serrors.IsNotFound(err) {
			return false, fmt.Errorf("failed to get PlacementRule '%v': %w", pb.PlacementRef.Name, err)
		}

		for _, decision := range plr.Status.Decisions {
			if decision.ClusterName == clusterName {
				return true, nil
			}
		}
	}

	return false, nil
}
