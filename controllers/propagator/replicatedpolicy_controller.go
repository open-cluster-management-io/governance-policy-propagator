package propagator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/controllers/complianceeventsapi"
)

const (
	ParentPolicyIDAnnotation = "policy.open-cluster-management.io/parent-policy-compliance-db-id"
	PolicyIDAnnotation       = "policy.open-cluster-management.io/policy-compliance-db-id"
)

var _ reconcile.Reconciler = &ReplicatedPolicyReconciler{}

type ReplicatedPolicyReconciler struct {
	Propagator
	ResourceVersions    *sync.Map
	DynamicWatcher      k8sdepwatches.DynamicWatcher
	ComplianceServerCtx *complianceeventsapi.ComplianceServerCtx
	TemplateResolvers   *TemplateResolvers
}

func (r *ReplicatedPolicyReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	log.Info("Reconciling the replicated policy")

	// Set the hub template watch metric after reconcile
	defer func() {
		watchCount := r.TemplateResolvers.GetWatchCount()

		log.V(3).Info("Setting hub template watch metric", "value", watchCount)

		hubTemplateActiveWatchesMetric.Set(float64(watchCount))
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
	if err != nil {
		if !replicatedExists {
			log.Error(err, "Invalid replicated policy sent for reconcile, rejecting")

			return reconcile.Result{}, nil
		}

		cleanUpErr := r.cleanUpReplicated(ctx, replicatedPolicy)
		if cleanUpErr != nil && !k8serrors.IsNotFound(cleanUpErr) {
			log.Error(err, "Failed to delete the invalid replicated policy, requeueing")

			return reconcile.Result{}, err
		}

		log.Info("Invalid replicated policy deleted")

		return reconcile.Result{}, nil
	}

	rsrcVersKey := request.Namespace + "/" + request.Name

	// Fetch the Root Policy instance
	rootPolicy := &policiesv1.Policy{}
	rootNN := types.NamespacedName{Namespace: rootNS, Name: rootName}

	if err := r.Get(ctx, rootNN, rootPolicy); err != nil {
		if !k8serrors.IsNotFound(err) {
			log.Error(err, "Failed to get the root policy, requeueing")

			return reconcile.Result{}, err
		}

		if !replicatedExists {
			version := safeWriteLoad(r.ResourceVersions, rsrcVersKey)
			defer version.Unlock()

			// Store this to ensure the cache matches a known possible state for this situation
			version.resourceVersion = "deleted"

			log.V(1).Info("Root policy and replicated policy already missing")

			return reconcile.Result{}, nil
		}

		inClusterNS, err := common.IsInClusterNamespace(ctx, r.Client, request.Namespace)
		if err != nil {
			return reconcile.Result{}, err
		}

		if !inClusterNS {
			// "Hub of hubs" scenario: this cluster is managed by another cluster,
			// which has the root policy for the policy being reconciled.
			log.V(1).Info("Found a replicated policy in non-cluster namespace, skipping it")

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

		log.V(1).Info("Root policy is disabled, and replicated policy correctly not found.")

		return reconcile.Result{}, nil
	}

	// calculate the decision for this specific cluster
	decision, err := r.singleClusterDecision(ctx, rootPolicy, request.Namespace)
	if err != nil {
		log.Error(err, "Failed to determine if policy should be replicated, requeueing")

		return reconcile.Result{}, err
	}

	// an empty decision means the policy should not be replicated
	if decision.Cluster == "" {
		if replicatedExists {
			inClusterNS, err := common.IsInClusterNamespace(ctx, r.Client, request.Namespace)
			if err != nil {
				return reconcile.Result{}, err
			}

			if !inClusterNS {
				// "Hosted mode" scenario: this cluster is hosting another cluster, which is syncing
				// this policy to a cluster namespace that this propagator doesn't know about.
				log.V(1).Info("Found a possible replicated policy for a hosted cluster, skipping it")

				return reconcile.Result{}, nil
			}

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

		log.V(1).Info("Replicated policy should not exist on this managed cluster, and does not.")

		return reconcile.Result{}, nil
	}

	objsToWatch := getPolicySetDependencies(rootPolicy)

	desiredReplicatedPolicy, err := r.buildReplicatedPolicy(ctx, rootPolicy, decision)
	if err != nil {
		log.Error(err, "Unable to build desired replicated policy, requeueing")

		return reconcile.Result{}, err
	}

	instanceGVK := desiredReplicatedPolicy.GroupVersionKind()
	instanceObjID := k8sdepwatches.ObjectIdentifier{
		Group:     instanceGVK.Group,
		Version:   instanceGVK.Version,
		Kind:      instanceGVK.Kind,
		Namespace: request.Namespace,
		Name:      request.Name,
	}

	// save the watcherError for later, so that the policy can still be updated now.
	var watcherErr error

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

	// Any errors to expose to the user are logged and recorded in the processTemplates method. Only retry
	// the request if it's determined to be a retryable error (i.e. don't retry syntax errors).
	tmplErr := r.processTemplates(ctx, desiredReplicatedPolicy, decision.Cluster, rootPolicy)
	if errors.Is(tmplErr, ErrRetryable) {
		// Return the error if it's retryable, which will utilize controller-runtime's exponential backoff.
		return reconcile.Result{}, tmplErr
	} else if errors.Is(tmplErr, ErrSAMissing) {
		saName := desiredReplicatedPolicy.Spec.HubTemplateOptions.ServiceAccountName
		saObjID := k8sdepwatches.ObjectIdentifier{
			Version:   "v1",
			Kind:      "ServiceAccount",
			Namespace: rootNS,
			Name:      saName,
		}

		log.Info("Adding a watch for the missing hub templates service account", "serviceAccount", saObjID)

		objsToWatch[saObjID] = true
	}

	r.setDBAnnotations(ctx, rootPolicy, desiredReplicatedPolicy, replicatedPolicy)

	if len(objsToWatch) != 0 {
		refObjs := make([]k8sdepwatches.ObjectIdentifier, 0, len(objsToWatch))
		for objToWatch := range objsToWatch {
			refObjs = append(refObjs, objToWatch)
		}

		watcherErr = r.DynamicWatcher.AddOrUpdateWatcher(instanceObjID, refObjs...)
		if watcherErr != nil {
			log.Error(watcherErr, "Failed to update the dynamic watches for the policy set dependencies")
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

		r.Recorder.Event(rootPolicy, "Normal", "PolicyPropagation",
			fmt.Sprintf("Policy %s/%s was propagated to cluster %s", rootPolicy.GetNamespace(),
				rootPolicy.GetName(), decision.Cluster))

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

		r.Recorder.Event(rootPolicy, "Normal", "PolicyPropagation",
			fmt.Sprintf("Policy %s/%s was updated for cluster %s", rootPolicy.GetNamespace(),
				rootPolicy.GetName(), decision.Cluster))

		log.Info("Replicated policy updated")
	} else {
		log.Info("Replicated policy matches, no update needed")
	}

	// whether it was updated or not, this resourceVersion can be cached
	version.resourceVersion = replicatedPolicy.GetResourceVersion()

	var returnErr error

	// Retry template errors due to permission issues. This isn't ideal, but there's no good event driven way to
	// be notified when the permissions are given to the service account.
	if k8serrors.IsForbidden(tmplErr) || k8serrors.IsUnauthorized(tmplErr) {
		returnErr = tmplErr
	}

	if watcherErr != nil {
		log.Info("Requeueing for the dynamic watcher error")

		returnErr = watcherErr
	}

	return reconcile.Result{}, returnErr
}

// getParentPolicyID needs to have the caller call r.ComplianceServerCtx.Lock.RLock.
func (r *ReplicatedPolicyReconciler) getParentPolicyID(
	ctx context.Context,
	rootPolicy *policiesv1.Policy,
	existingReplicatedPolicy *policiesv1.Policy,
) (int32, error) {
	dbParentPolicy := complianceeventsapi.ParentPolicyFromPolicyObj(rootPolicy)

	// Check the cache first.
	cachedParentPolicyID, ok := r.ComplianceServerCtx.ParentPolicyToID.Load(dbParentPolicy.Key())
	if ok {
		return cachedParentPolicyID.(int32), nil
	}

	// Try the database second before checking the replicated policy to be able to recover if the compliance history
	// database is restored from backup and the IDs on the replicated policy no longer exist.
	var dbErr error

	if r.ComplianceServerCtx.DB != nil {
		err := dbParentPolicy.GetOrCreate(ctx, r.ComplianceServerCtx.DB)
		if err == nil {
			r.ComplianceServerCtx.ParentPolicyToID.Store(dbParentPolicy.Key(), dbParentPolicy.KeyID)

			return dbParentPolicy.KeyID, nil
		}

		if r.ComplianceServerCtx.DB.PingContext(ctx) != nil {
			dbErr = complianceeventsapi.ErrDBConnectionFailed
		} else {
			dbErr = fmt.Errorf("%w: failed to get the database ID of the parent policy", err)
		}
	} else {
		dbErr = complianceeventsapi.ErrDBConnectionFailed
	}

	// Check if the existing replicated policy already has the annotation set and has the same
	// categories, controls, and standards as the current root policy.
	var parentPolicyIDFromRepl string
	if existingReplicatedPolicy != nil {
		parentPolicyIDFromRepl = existingReplicatedPolicy.Annotations[ParentPolicyIDAnnotation]
	}

	if parentPolicyIDFromRepl != "" {
		dbParentPolicyFromRepl := complianceeventsapi.ParentPolicyFromPolicyObj(existingReplicatedPolicy)
		dbParentPolicyFromRepl.Name = rootPolicy.Name
		dbParentPolicyFromRepl.Namespace = rootPolicy.Namespace

		if dbParentPolicy.Key() == dbParentPolicyFromRepl.Key() {
			parentPolicyID, err := strconv.ParseInt(parentPolicyIDFromRepl, 10, 32)
			if err == nil && parentPolicyID != 0 {
				r.ComplianceServerCtx.ParentPolicyToID.Store(dbParentPolicy.Key(), int32(parentPolicyID))

				return int32(parentPolicyID), nil
			}
		}
	}

	return 0, dbErr
}

// getPolicyID needs to have the caller call r.ComplianceServerCtx.Lock.RLock.
func (r *ReplicatedPolicyReconciler) getPolicyID(
	ctx context.Context,
	replPolicy *policiesv1.Policy,
	existingReplPolicy *policiesv1.Policy,
	replTemplateIdx int,
	skipDB bool,
) (int32, *unstructured.Unstructured, error) {
	// Start by checking the cache.
	plcTemplate := replPolicy.Spec.PolicyTemplates[replTemplateIdx]
	plcTmplUnstruct := &unstructured.Unstructured{}

	err := plcTmplUnstruct.UnmarshalJSON(plcTemplate.ObjectDefinition.Raw)
	if err != nil {
		return 0, plcTmplUnstruct, err
	}

	dbPolicy := complianceeventsapi.PolicyFromUnstructured(*plcTmplUnstruct)
	if err := dbPolicy.Validate(); err != nil {
		return 0, plcTmplUnstruct, err
	}

	var policyID int32

	cachedPolicyID, ok := r.ComplianceServerCtx.PolicyToID.Load(dbPolicy.Key())
	if ok {
		policyID = cachedPolicyID.(int32)

		return policyID, plcTmplUnstruct, nil
	}

	// Try the database second before checking the replicated policy to be able to recover if the compliance history
	// database is restored from backup and the IDs on the replicated policy no longer exist.
	var dbErr error
	if skipDB || r.ComplianceServerCtx.DB == nil {
		dbErr = complianceeventsapi.ErrDBConnectionFailed
	} else {
		err = dbPolicy.GetOrCreate(ctx, r.ComplianceServerCtx.DB)
		if err == nil {
			r.ComplianceServerCtx.PolicyToID.Store(dbPolicy.Key(), dbPolicy.KeyID)

			return dbPolicy.KeyID, plcTmplUnstruct, nil
		}

		dbErr = err
	}

	// Check if the existing policy template matches the existing one
	var existingPlcTemplate *policiesv1.PolicyTemplate

	existingPlcTmplUnstruct := unstructured.Unstructured{}

	var existingAnnotation string
	var existingDBPolicy *complianceeventsapi.Policy

	// Try the existing policy template first before trying the database.
	if existingReplPolicy != nil && len(existingReplPolicy.Spec.PolicyTemplates) >= replTemplateIdx+1 {
		existingPlcTemplate = existingReplPolicy.Spec.PolicyTemplates[replTemplateIdx]

		err = existingPlcTmplUnstruct.UnmarshalJSON(existingPlcTemplate.ObjectDefinition.Raw)
		if err == nil {
			existingAnnotations := existingPlcTmplUnstruct.GetAnnotations()
			existingAnnotation = existingAnnotations[PolicyIDAnnotation]

			if existingAnnotation != "" {
				existingDBPolicy = complianceeventsapi.PolicyFromUnstructured(existingPlcTmplUnstruct)
			}
		}
	}

	// This is a continuation from the above if statement but this was broken up here to make it less indented.
	if existingAnnotation != "" {
		if err := existingDBPolicy.Validate(); err == nil {
			if dbPolicy.Key() == existingDBPolicy.Key() {
				policyID, err := strconv.ParseInt(existingAnnotation, 10, 32)
				if err == nil && policyID != 0 {
					r.ComplianceServerCtx.PolicyToID.Store(dbPolicy.Key(), int32(policyID))

					return int32(policyID), plcTmplUnstruct, nil
				}
			}
		}
	}

	return 0, plcTmplUnstruct, dbErr
}

// setDBAnnotations sets the parent policy ID on the replicated policy and the policy ID for each policy template.
// If the DB connection is unavailable, it queues up a reconcile for when the DB becomes available.
func (r *ReplicatedPolicyReconciler) setDBAnnotations(
	ctx context.Context,
	rootPolicy *policiesv1.Policy,
	replicatedPolicy *policiesv1.Policy,
	existingReplicatedPolicy *policiesv1.Policy,
) {
	r.ComplianceServerCtx.Lock.RLock()
	defer r.ComplianceServerCtx.Lock.RUnlock()

	// Assume the database is connected unless told otherwise.
	dbAvailable := true
	var requeueForDB bool

	annotations := replicatedPolicy.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	parentPolicyID, err := r.getParentPolicyID(ctx, rootPolicy, existingReplicatedPolicy)
	if err != nil {
		if errors.Is(err, complianceeventsapi.ErrDBConnectionFailed) {
			dbAvailable = false
		} else {
			log.Error(
				err, "Failed to get the parent policy ID", "name", rootPolicy.Name, "namespace", rootPolicy.Namespace,
			)
		}

		requeueForDB = true

		// Remove it if the user accidentally provided the annotation
		if annotations[ParentPolicyIDAnnotation] != "" {
			delete(annotations, PolicyIDAnnotation)
			replicatedPolicy.SetAnnotations(annotations)
		}
	} else {
		annotations[ParentPolicyIDAnnotation] = strconv.FormatInt(int64(parentPolicyID), 10)
		replicatedPolicy.SetAnnotations(annotations)
	}

	for i, plcTemplate := range replicatedPolicy.Spec.PolicyTemplates {
		if plcTemplate == nil {
			continue
		}

		policyID, plcTmplUnstruct, err := r.getPolicyID(
			ctx, replicatedPolicy, existingReplicatedPolicy, i, !dbAvailable,
		)
		if err != nil {
			if errors.Is(err, complianceeventsapi.ErrDBConnectionFailed) {
				dbAvailable = false
			} else {
				log.Error(
					err,
					"Failed to get the policy ID for the policy template",
					"name", plcTmplUnstruct.GetName(),
					"namespace", plcTmplUnstruct.GetNamespace(),
					"index", i,
				)
			}

			requeueForDB = true
			tmplAnnotations := plcTmplUnstruct.GetAnnotations()

			if tmplAnnotations[PolicyIDAnnotation] == "" {
				continue
			}

			// Remove it if the user accidentally provided the annotation
			delete(tmplAnnotations, PolicyIDAnnotation)
			plcTmplUnstruct.SetAnnotations(tmplAnnotations)
		} else {
			tmplAnnotations := plcTmplUnstruct.GetAnnotations()
			if tmplAnnotations == nil {
				tmplAnnotations = map[string]string{}
			}

			tmplAnnotations[PolicyIDAnnotation] = strconv.FormatInt(int64(policyID), 10)
			plcTmplUnstruct.SetAnnotations(tmplAnnotations)
		}

		updatedTemplate, err := plcTmplUnstruct.MarshalJSON()
		if err != nil {
			log.Error(
				err, "Failed to set the annotation on the policy template", "index", i, "anotation", PolicyIDAnnotation,
			)

			continue
		}

		replicatedPolicy.Spec.PolicyTemplates[i].ObjectDefinition.Raw = updatedTemplate
	}

	if requeueForDB {
		log.V(2).Info(
			"The compliance events database is not available. Queuing this replicated policy to be reprocessed if the "+
				"database becomes available.",
			"namespace", replicatedPolicy.Namespace,
			"name", replicatedPolicy.Name,
		)
		r.ComplianceServerCtx.Queue.Add(
			types.NamespacedName{Namespace: replicatedPolicy.Namespace, Name: replicatedPolicy.Name},
		)
	}
}

func (r *ReplicatedPolicyReconciler) cleanUpReplicated(ctx context.Context, replicatedPolicy *policiesv1.Policy) error {
	gvk := replicatedPolicy.GroupVersionKind()

	objID := k8sdepwatches.ObjectIdentifier{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Kind:      gvk.Kind,
		Namespace: replicatedPolicy.Namespace,
		Name:      replicatedPolicy.Name,
	}

	watcherErr := r.DynamicWatcher.RemoveWatcher(objID)

	uncacheErr := r.TemplateResolvers.RemoveReplicatedPolicy(objID)
	if uncacheErr != nil {
		if watcherErr == nil {
			watcherErr = uncacheErr
		} else {
			watcherErr = fmt.Errorf("%w; %w", watcherErr, uncacheErr)
		}
	}

	rsrcVersKey := replicatedPolicy.GetNamespace() + "/" + replicatedPolicy.GetName()

	version := safeWriteLoad(r.ResourceVersions, rsrcVersKey)
	defer version.Unlock()

	deleteErr := r.Delete(ctx, replicatedPolicy)

	if deleteErr != nil {
		if k8serrors.IsNotFound(deleteErr) {
			version.resourceVersion = "deleted"
		}
	} else {
		version.resourceVersion = "deleted"
	}

	return errors.Join(watcherErr, deleteErr)
}

func (r *ReplicatedPolicyReconciler) singleClusterDecision(
	ctx context.Context, rootPlc *policiesv1.Policy, clusterName string,
) (decision clusterDecision, err error) {
	positiveDecision := clusterDecision{
		Cluster: clusterName,
	}

	pbList := &policiesv1.PlacementBindingList{}

	err = r.List(ctx, pbList, &client.ListOptions{Namespace: rootPlc.GetNamespace()})
	if err != nil {
		return clusterDecision{}, err
	}

	foundWithoutSubFilter := false

	// Process all placement bindings without subFilter
	for i, pb := range pbList.Items {
		if pb.SubFilter == policiesv1.Restricted {
			continue
		}

		found, err := r.isSingleClusterInDecisions(ctx, &pbList.Items[i], rootPlc.GetName(), clusterName)
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
	for i, pb := range pbList.Items {
		if pb.SubFilter != policiesv1.Restricted {
			continue
		}

		found, err := r.isSingleClusterInDecisions(ctx, &pbList.Items[i], rootPlc.GetName(), clusterName)
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
			if common.IsPolicyInPolicySet(ctx, r.Client, policyName, subject.Name, pb.GetNamespace()) {
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
