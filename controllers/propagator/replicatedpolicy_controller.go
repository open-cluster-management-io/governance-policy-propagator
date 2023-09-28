package propagator

import (
	"context"
	"fmt"
	"strings"
	"sync"

	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

var _ reconcile.Reconciler = &ReplicatedPolicyReconciler{}

type ReplicatedPolicyReconciler struct {
	Propagator
	ResourceVersions *sync.Map
}

func (r *ReplicatedPolicyReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	log.Info("Reconciling the replicated policy")

	// Set the hub template watch metric after reconcile
	defer func() {
		hubTempWatches := r.DynamicWatcher.GetWatchCount()
		log.V(3).Info("Setting hub template watch metric", "value", hubTempWatches)

		hubTemplateActiveWatchesMetric.Set(float64(hubTempWatches))
	}()

	replicatedExists := true
	replicatedPolicy := &policiesv1.Policy{}

	if err := r.Get(ctx, request.NamespacedName, replicatedPolicy); err != nil {
		if !k8serrors.IsNotFound(err) {
			log.Error(err, "Failed to get the replicated policy")

			return reconcile.Result{}, err
		}

		replicatedExists = false
	}

	rootName, rootNS, err := common.ParseRootPolicyLabel(request.Name)
	if err != nil && replicatedExists {
		if err := r.cleanUpReplicated(ctx, replicatedPolicy); err != nil {
			if !k8serrors.IsNotFound(err) {
				log.Error(err, "Failed to delete the invalid replicated policy, requeueing")

				return reconcile.Result{}, err
			}
		}

		log.Info("Invalid replicated policy deleted")

		return reconcile.Result{}, nil
	}

	rsrcVersKey := request.Namespace + "/" + request.Name

	// Fetch the Root Policy instance
	rootPolicy := &policiesv1.Policy{}
	rootNN := types.NamespacedName{Namespace: rootNS, Name: rootName}

	if err := r.Get(ctx, rootNN, rootPolicy); err != nil {
		if k8serrors.IsNotFound(err) {
			if replicatedExists {
				// do not handle a replicated policy which does not belong to the current cluster
				inClusterNS, err := common.IsInClusterNamespace(r.Client, request.Namespace)
				if err != nil {
					return reconcile.Result{}, err
				}

				if !inClusterNS {
					log.Info("Found a replicated policy in non-cluster namespace, skipping it")

					return reconcile.Result{}, nil
				}

				// otherwise, we need to clean it up
				if err := r.cleanUpReplicated(ctx, replicatedPolicy); err != nil {
					if !k8serrors.IsNotFound(err) {
						log.Error(err, "Failed to delete the orphaned replicated policy, requeueing")

						return reconcile.Result{}, err
					}
				}

				log.Info("Orphaned replicated policy deleted")

				return reconcile.Result{}, nil
			}

			version := safeWriteLoad(r.ResourceVersions, rsrcVersKey)
			defer version.Unlock()

			// Store this to ensure the cache matches a known possible state for this situation
			version.resourceVersion = "deleted"

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

		version := safeWriteLoad(r.ResourceVersions, rsrcVersKey)
		defer version.Unlock()

		// Store this to ensure the cache matches a known possible state for this situation
		version.resourceVersion = "deleted"

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

		version := safeWriteLoad(r.ResourceVersions, rsrcVersKey)
		defer version.Unlock()

		// Store this to ensure the cache matches a known possible state for this situation
		version.resourceVersion = "deleted"

		log.Info("Replicated policy should not exist on this managed cluster, and does not.")

		return reconcile.Result{}, nil
	}

	objsToWatch := getPolicySetDependencies(rootPolicy)

	desiredReplicatedPolicy, err := r.buildReplicatedPolicy(rootPolicy, decision)
	if err != nil {
		log.Error(err, "Unable to build desired replicated policy, requeueing")

		return reconcile.Result{}, err
	}

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

	// save the watcherError for later, so that the policy can still be updated now.
	var watcherErr error

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
		version := safeWriteLoad(r.ResourceVersions, rsrcVersKey)
		defer version.Unlock()

		err = r.Create(ctx, desiredReplicatedPolicy)
		if err != nil {
			log.Error(err, "Failed to create the replicated policy, requeueing")

			return reconcile.Result{}, err
		}

		version.resourceVersion = desiredReplicatedPolicy.GetResourceVersion()

		log.Info("Created replicated policy")

		return reconcile.Result{}, watcherErr
	}

	version := safeWriteLoad(r.ResourceVersions, rsrcVersKey)
	defer version.Unlock()

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

	// whether it was updated or not, this resourceVersion can be cached
	version.resourceVersion = replicatedPolicy.GetResourceVersion()

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

	rsrcVersKey := replicatedPolicy.GetNamespace() + "/" + replicatedPolicy.GetName()

	version := safeWriteLoad(r.ResourceVersions, rsrcVersKey)
	defer version.Unlock()

	deleteErr := r.Delete(ctx, replicatedPolicy)

	version.resourceVersion = "deleted"

	if watcherErr != nil {
		return watcherErr
	}

	return deleteErr
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

	for _, subject := range pb.Subjects {
		if subject.APIGroup != policiesv1.SchemeGroupVersion.Group {
			continue
		}

		switch subject.Kind {
		case policiesv1.Kind:
			if subject.Name == policyName {
				subjectFound = true
			}
		case policiesv1.PolicySetKind:
			if r.isPolicyInPolicySet(policyName, subject.Name, pb.GetNamespace()) {
				subjectFound = true
			}
		}

		if subjectFound {
			break
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
	case "Placement":
		pl := &clusterv1beta1.Placement{}
		if err := r.Get(ctx, refNN, pl); err != nil && !k8serrors.IsNotFound(err) {
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
	}

	return false, nil
}
