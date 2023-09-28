// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	templates "github.com/stolostron/go-template-utils/v3/pkg/templates"
	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

// The configuration of the maximum number of Go routines to spawn when handling placement decisions
// per policy.
const (
	concurrencyPerPolicyEnvName = "CONTROLLER_CONFIG_CONCURRENCY_PER_POLICY"
	concurrencyPerPolicyDefault = 5
)

const (
	startDelim              = "{{hub"
	stopDelim               = "hub}}"
	TriggerUpdateAnnotation = "policy.open-cluster-management.io/trigger-update"
)

var (
	concurrencyPerPolicy int
	kubeConfig           *rest.Config
	kubeClient           *kubernetes.Interface
)

type Propagator struct {
	client.Client
	Scheme          *runtime.Scheme
	Recorder        record.EventRecorder
	DynamicWatcher  k8sdepwatches.DynamicWatcher
	RootPolicyLocks *sync.Map
}

func Initialize(kubeconfig *rest.Config, kubeclient *kubernetes.Interface) {
	kubeConfig = kubeconfig
	kubeClient = kubeclient
	concurrencyPerPolicy = getEnvVarPosInt(concurrencyPerPolicyEnvName, concurrencyPerPolicyDefault)
}

// getTemplateCfg returns the default policy template configuration.
func getTemplateCfg() templates.Config {
	// (Encryption settings are set during the processTemplates method)
	// Adding eight spaces to the indentation makes the usage of `indent N` be from the logical
	// starting point of the resource object wrapped in the ConfigurationPolicy.
	return templates.Config{
		AdditionalIndentation: 8,
		DisabledFunctions:     []string{},
		StartDelim:            startDelim,
		StopDelim:             stopDelim,
	}
}

func getEnvVarPosInt(name string, defaultValue int) int {
	envValue := os.Getenv(name)
	if envValue == "" {
		return defaultValue
	}

	envInt, err := strconv.Atoi(envValue)
	if err == nil && envInt > 0 {
		return envInt
	}

	log.Info("The environment variable is invalid. Using default.", "name", name)

	return defaultValue
}

func (r *Propagator) deletePolicy(plc *policiesv1.Policy) error {
	// #nosec G601 -- no memory addresses are stored in collections
	err := r.Delete(context.TODO(), plc)
	if err != nil && !k8serrors.IsNotFound(err) {
		log.Error(
			err,
			"Failed to delete the replicated policy",
			"name", plc.GetName(),
			"namespace", plc.GetNamespace(),
		)

		return err
	}

	return nil
}

type policyDeleter interface {
	deletePolicy(instance *policiesv1.Policy) error
}

type deletionResult struct {
	Identifier string
	Err        error
}

func plcDeletionWrapper(
	deletionHandler policyDeleter,
	policies <-chan policiesv1.Policy,
	results chan<- deletionResult,
) {
	for policy := range policies {
		identifier := fmt.Sprintf("%s/%s", policy.GetNamespace(), policy.GetName())
		err := deletionHandler.deletePolicy(&policy)
		results <- deletionResult{identifier, err}
	}
}

// cleanUpPolicy will delete all replicated policies associated with provided policy and return a boolean
// indicating all of the replicated policies were deleted, if any
func (r *Propagator) cleanUpPolicy(instance *policiesv1.Policy) (bool, error) {
	log := log.WithValues("policyName", instance.GetName(), "policyNamespace", instance.GetNamespace())
	replicatedPlcList := &policiesv1.PolicyList{}

	instanceGVK := instance.GroupVersionKind()

	err := r.DynamicWatcher.RemoveWatcher(k8sdepwatches.ObjectIdentifier{
		Group:     instanceGVK.Group,
		Version:   instanceGVK.Version,
		Kind:      instanceGVK.Kind,
		Namespace: instance.Namespace,
		Name:      instance.Name,
	})
	if err != nil {
		log.Error(
			err,
			fmt.Sprintf(
				"Failed to remove watches for the policy %s/%s from the dynamic watcher",
				instance.Namespace,
				instance.Name,
			),
		)

		return false, err
	}

	err = r.List(
		context.TODO(), replicatedPlcList, client.MatchingLabels(common.LabelsForRootPolicy(instance)),
	)
	if err != nil {
		log.Error(err, "Failed to list the replicated policies")

		return false, err
	}

	if len(replicatedPlcList.Items) == 0 {
		log.V(2).Info("No replicated policies to delete.")

		return false, nil
	}

	log.V(2).Info(
		"Deleting replicated policies because root policy was deleted", "count", len(replicatedPlcList.Items))

	policiesChan := make(chan policiesv1.Policy, len(replicatedPlcList.Items))
	deletionResultsChan := make(chan deletionResult, len(replicatedPlcList.Items))

	numWorkers := common.GetNumWorkers(len(replicatedPlcList.Items), concurrencyPerPolicy)

	for i := 0; i < numWorkers; i++ {
		go plcDeletionWrapper(r, policiesChan, deletionResultsChan)
	}

	log.V(2).Info("Scheduling work to handle deleting replicated policies")

	for _, plc := range replicatedPlcList.Items {
		policiesChan <- plc
	}

	// Wait for all the deletions to be processed.
	log.V(1).Info("Waiting for the result of deleting the replicated policies", "count", len(policiesChan))

	processedResults := 0
	failures := 0

	for result := range deletionResultsChan {
		if result.Err != nil {
			log.V(2).Info("Failed to delete replicated policy " + result.Identifier)
			failures++
		}

		processedResults++

		// Once all the deletions have been processed, it's safe to close
		// the channels and stop blocking in this goroutine.
		if processedResults == len(replicatedPlcList.Items) {
			close(policiesChan)
			close(deletionResultsChan)
			log.V(2).Info("All replicated policy deletions have been handled", "count", len(replicatedPlcList.Items))
		}
	}

	if failures > 0 {
		return false, errors.New("failed to delete one or more replicated policies")
	}

	propagationFailureMetric.DeleteLabelValues(instance.GetName(), instance.GetNamespace())

	return true, nil
}

// clusterDecision contains a single decision where the replicated policy
// should be processed and any overrides to the root policy
type clusterDecision struct {
	Cluster         appsv1.PlacementDecision
	PolicyOverrides policiesv1.BindingOverrides
}

type decisionHandler interface {
	handleDecision(instance *policiesv1.Policy, decision clusterDecision) (
		templateRefObjs map[k8sdepwatches.ObjectIdentifier]bool, err error,
	)
}

// decisionResult contains the result of handling a placement decision of a policy. It is intended
// to be sent in a channel by handleDecisionWrapper for the calling Go routine to determine if the
// processing was successful. Identifier is the PlacementDecision, with the ClusterNamespace and
// the ClusterName. TemplateRefObjs is a set of identifiers of objects accessed by hub policy
// templates. Err is the error associated with handling the decision. This can be nil to denote success.
type decisionResult struct {
	Identifier      appsv1.PlacementDecision
	TemplateRefObjs map[k8sdepwatches.ObjectIdentifier]bool
	Err             error
}

// handleDecisionWrapper wraps the handleDecision method for concurrency. decisionHandler is an
// object with the handleDecision method. This is used instead of making this a method on the
// PolicyReconciler struct in order for easier unit testing. instance is the policy the placement
// decision is about. decisions is the channel with the placement decisions for the input policy to
// process. When this channel closes, it means that all decisions have been processed. results is a
// channel this method will send the outcome of handling each placement decision. The calling Go
// routine can use this to determine success.
func handleDecisionWrapper(
	decisionHandler decisionHandler,
	instance *policiesv1.Policy,
	decisions <-chan clusterDecision,
	results chan<- decisionResult,
) {
	for decision := range decisions {
		log := log.WithValues(
			"policyName", instance.GetName(),
			"policyNamespace", instance.GetNamespace(),
			"decision", decision.Cluster,
			"policyOverrides", decision.PolicyOverrides,
		)
		log.V(1).Info("Handling the decision")

		templateRefObjs, err := decisionHandler.handleDecision(instance, decision)
		if err == nil {
			log.V(1).Info("Replicated the policy")
		}

		results <- decisionResult{decision.Cluster, templateRefObjs, err}
	}
}

type decisionSet map[appsv1.PlacementDecision]bool

func (set decisionSet) namespaces() []string {
	namespaces := make([]string, 0)

	for decision, isTrue := range set {
		if isTrue {
			namespaces = append(namespaces, decision.ClusterNamespace)
		}
	}

	return namespaces
}

// getPolicyPlacementDecisions retrieves the placement decisions for a input
// placement binding when the policy is bound within it.
func (r *Propagator) getPolicyPlacementDecisions(
	instance *policiesv1.Policy, pb *policiesv1.PlacementBinding,
) (decisions []appsv1.PlacementDecision, placements []*policiesv1.Placement, err error) {
	if !common.HasValidPlacementRef(pb) {
		return nil, nil, fmt.Errorf("placement binding %s/%s reference is not valid", pb.Name, pb.Namespace)
	}

	policySubjectFound := false
	policySetSubjects := make(map[string]struct{}) // a set, to prevent duplicates

	for _, subject := range pb.Subjects {
		if subject.APIGroup != policiesv1.SchemeGroupVersion.Group {
			continue
		}

		switch subject.Kind {
		case policiesv1.Kind:
			if !policySubjectFound && subject.Name == instance.GetName() {
				policySubjectFound = true

				placements = append(placements, &policiesv1.Placement{
					PlacementBinding: pb.GetName(),
				})
			}
		case policiesv1.PolicySetKind:
			if _, exists := policySetSubjects[subject.Name]; !exists {
				policySetSubjects[subject.Name] = struct{}{}

				if r.isPolicyInPolicySet(instance.GetName(), subject.Name, pb.GetNamespace()) {
					placements = append(placements, &policiesv1.Placement{
						PlacementBinding: pb.GetName(),
						PolicySet:        subject.Name,
					})
				}
			}
		}
	}

	if len(placements) == 0 {
		// None of the subjects in the PlacementBinding were relevant to this Policy.
		return nil, nil, nil
	}

	// If the placementRef exists, then it needs to be added to the placement item
	refNN := types.NamespacedName{
		Namespace: pb.GetNamespace(),
		Name:      pb.PlacementRef.Name,
	}

	switch pb.PlacementRef.Kind {
	case "PlacementRule":
		plr := &appsv1.PlacementRule{}
		if err := r.Get(context.TODO(), refNN, plr); err != nil && !k8serrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("failed to check for PlacementRule '%v': %w", pb.PlacementRef.Name, err)
		}

		for i := range placements {
			placements[i].PlacementRule = plr.Name // will be empty if the PlacementRule was not found
		}
	case "Placement":
		pl := &clusterv1beta1.Placement{}
		if err := r.Get(context.TODO(), refNN, pl); err != nil && !k8serrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("failed to check for Placement '%v': %w", pb.PlacementRef.Name, err)
		}

		for i := range placements {
			placements[i].Placement = pl.Name // will be empty if the Placement was not found
		}
	}

	// If there are no placements, then the PlacementBinding is not for this Policy.
	if len(placements) == 0 {
		return nil, nil, nil
	}

	// If the policy is disabled, don't return any decisions, so that the policy isn't put on any clusters
	if instance.Spec.Disabled {
		return nil, placements, nil
	}

	decisions, err = common.GetDecisions(r.Client, pb)

	return decisions, placements, err
}

// getAllClusterDecisions retrieves all cluster decisions for the input policy, taking into
// account subFilter and bindingOverrides specified in the placement binding from the input
// placement binding list.
// It first processes all placement bindings with disabled subFilter to obtain a list of bound
// clusters along with their policy overrides if any, then processes all placement bindings
// with subFilter:restricted to override the policy for the subset of bound clusters as needed.
// It returns:
//   - allClusterDecisions: a slice of all the cluster decisions should be handled
//   - placements: a slice of all the placement decisions discovered
//   - err: error
//
// The rules for policy overrides are as follows:
//
//   - remediationAction: If any placement binding that the cluster is bound to has
//     bindingOverrides.remediationAction set to "enforce", the remediationAction
//     for the replicated policy will be set to "enforce".
func (r *Propagator) getAllClusterDecisions(
	instance *policiesv1.Policy, pbList *policiesv1.PlacementBindingList,
) (
	allClusterDecisions []clusterDecision, placements []*policiesv1.Placement, err error,
) {
	allClusterDecisionsMap := map[appsv1.PlacementDecision]policiesv1.BindingOverrides{}

	// Process all placement bindings without subFilter
	for _, pb := range pbList.Items {
		if pb.SubFilter == policiesv1.Restricted {
			continue
		}

		plcDecisions, plcPlacements, err := r.getPolicyPlacementDecisions(instance, &pb)
		if err != nil {
			return nil, nil, err
		}

		if len(plcDecisions) == 0 {
			log.Info("No placement decisions to process for this policy from this binding",
				"policyName", instance.GetName(), "bindingName", pb.GetName())
		}

		for _, decision := range plcDecisions {
			if overrides, ok := allClusterDecisionsMap[decision]; ok {
				// Found cluster in the decision map
				if strings.EqualFold(pb.BindingOverrides.RemediationAction, string(policiesv1.Enforce)) {
					overrides.RemediationAction = strings.ToLower(string(policiesv1.Enforce))
					allClusterDecisionsMap[decision] = overrides
				}
			} else {
				// No found cluster in the decision map, add it to the map
				allClusterDecisionsMap[decision] = policiesv1.BindingOverrides{
					// empty string if pb.BindingOverrides.RemediationAction is not defined
					RemediationAction: strings.ToLower(pb.BindingOverrides.RemediationAction),
				}
			}
		}

		placements = append(placements, plcPlacements...)
	}

	// Process all placement bindings with subFilter:restricted
	for _, pb := range pbList.Items {
		if pb.SubFilter != policiesv1.Restricted {
			continue
		}

		foundInDecisions := false

		plcDecisions, plcPlacements, err := r.getPolicyPlacementDecisions(instance, &pb)
		if err != nil {
			return nil, nil, err
		}

		if len(plcDecisions) == 0 {
			log.Info("No placement decisions to process for this policy from this binding",
				"policyName", instance.GetName(), "bindingName", pb.GetName())
		}

		for _, decision := range plcDecisions {
			if overrides, ok := allClusterDecisionsMap[decision]; ok {
				// Found cluster in the decision map
				foundInDecisions = true

				if strings.EqualFold(pb.BindingOverrides.RemediationAction, string(policiesv1.Enforce)) {
					overrides.RemediationAction = strings.ToLower(string(policiesv1.Enforce))
					allClusterDecisionsMap[decision] = overrides
				}
			}
		}

		if foundInDecisions {
			placements = append(placements, plcPlacements...)
		}
	}

	// Covert the decision map to a slice of clusterDecision
	for cluster, overrides := range allClusterDecisionsMap {
		decision := clusterDecision{
			Cluster:         cluster,
			PolicyOverrides: overrides,
		}
		allClusterDecisions = append(allClusterDecisions, decision)
	}

	return allClusterDecisions, placements, nil
}

// handleDecisions will get all the placement decisions based on the input policy and placement
// binding list and propagate the policy. Note that this method performs concurrent operations.
// It returns the following:
//   - placements - a slice of all the placement decisions discovered
//   - allDecisions - a set of all the placement decisions encountered
//   - failedClusters - a set of all the clusters that encountered an error during propagation
//   - allFailed - a bool that determines if all clusters encountered an error during propagation
func (r *Propagator) handleDecisions(
	instance *policiesv1.Policy, pbList *policiesv1.PlacementBindingList,
) (
	placements []*policiesv1.Placement, allDecisions decisionSet, failedClusters decisionSet, allFailed bool,
) {
	log := log.WithValues("policyName", instance.GetName(), "policyNamespace", instance.GetNamespace())
	allDecisions = map[appsv1.PlacementDecision]bool{}
	failedClusters = map[appsv1.PlacementDecision]bool{}

	allTemplateRefObjs := getPolicySetDependencies(instance)

	allClusterDecisions, placements, err := r.getAllClusterDecisions(instance, pbList)
	if err != nil {
		allFailed = true

		return
	}

	if len(allClusterDecisions) != 0 {
		// Setup the workers which will call r.handleDecision. The number of workers depends
		// on the number of decisions and the limit defined in concurrencyPerPolicy.
		// decisionsChan acts as the work queue of decisions to process. resultsChan contains
		// the results from the decisions being processed.
		decisionsChan := make(chan clusterDecision, len(allClusterDecisions))
		resultsChan := make(chan decisionResult, len(allClusterDecisions))
		numWorkers := common.GetNumWorkers(len(allClusterDecisions), concurrencyPerPolicy)

		for i := 0; i < numWorkers; i++ {
			go handleDecisionWrapper(r, instance, decisionsChan, resultsChan)
		}

		log.Info("Handling the placement decisions", "count", len(allClusterDecisions))

		for _, decision := range allClusterDecisions {
			log.V(2).Info(
				"Scheduling work to handle the decision for the cluster",
				"name", decision.Cluster.ClusterName,
			)
			decisionsChan <- decision
		}

		// Wait for all the decisions to be processed.
		log.V(2).Info("Waiting for the result of handling the decision(s)", "count", len(allClusterDecisions))

		processedResults := 0

		for result := range resultsChan {
			allDecisions[result.Identifier] = true

			if result.Err != nil {
				failedClusters[result.Identifier] = true
			}

			processedResults++

			for refObject := range result.TemplateRefObjs {
				allTemplateRefObjs[refObject] = true
			}

			// Once all the decisions have been processed, it's safe to close
			// the channels and stop blocking in this goroutine.
			if processedResults == len(allClusterDecisions) {
				close(decisionsChan)
				close(resultsChan)
				log.Info("All the placement decisions have been handled", "count", len(allClusterDecisions))
			}
		}
	}

	instanceGVK := instance.GroupVersionKind()
	instanceObjID := k8sdepwatches.ObjectIdentifier{
		Group:     instanceGVK.Group,
		Version:   instanceGVK.Version,
		Kind:      instanceGVK.Kind,
		Namespace: instance.Namespace,
		Name:      instance.Name,
	}
	refObjs := make([]k8sdepwatches.ObjectIdentifier, 0, len(allTemplateRefObjs))

	for refObj := range allTemplateRefObjs {
		refObjs = append(refObjs, refObj)
	}

	if len(refObjs) != 0 {
		err := r.DynamicWatcher.AddOrUpdateWatcher(instanceObjID, refObjs...)
		if err != nil {
			log.Error(
				err,
				fmt.Sprintf(
					"Failed to update the dynamic watches for the policy %s/%s on objects referenced by hub policy "+
						"templates",
					instance.Namespace,
					instance.Name,
				),
			)

			allFailed = true
		}
	} else {
		err := r.DynamicWatcher.RemoveWatcher(instanceObjID)
		if err != nil {
			log.Error(
				err,
				fmt.Sprintf(
					"Failed to remove the dynamic watches for the policy %s/%s on objects referenced by hub policy "+
						"templates",
					instance.Namespace,
					instance.Name,
				),
			)

			allFailed = true
		}
	}

	return
}

// cleanUpOrphanedRplPolicies compares the status of the input policy against the input placement
// decisions. If the cluster exists in the status but doesn't exist in the input placement
// decisions, then it's considered stale and will be removed.
func (r *Propagator) cleanUpOrphanedRplPolicies(
	instance *policiesv1.Policy, allDecisions decisionSet,
) error {
	log := log.WithValues("policyName", instance.GetName(), "policyNamespace", instance.GetNamespace())
	successful := true

	for _, cluster := range instance.Status.Status {
		key := appsv1.PlacementDecision{
			ClusterName:      cluster.ClusterNamespace,
			ClusterNamespace: cluster.ClusterNamespace,
		}
		if allDecisions[key] {
			continue
		}
		// not found in allDecisions, orphan, delete it
		name := common.FullNameForPolicy(instance)
		log := log.WithValues("name", name, "namespace", cluster.ClusterNamespace)
		log.Info("Deleting the orphaned replicated policy")

		err := r.Delete(context.TODO(), &policiesv1.Policy{
			TypeMeta: metav1.TypeMeta{
				Kind:       policiesv1.Kind,
				APIVersion: policiesv1.SchemeGroupVersion.Group,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: cluster.ClusterNamespace,
			},
		})
		if err != nil && !k8serrors.IsNotFound(err) {
			successful = false

			log.Error(err, "Failed to delete the orphaned replicated policy")
		}
	}

	if !successful {
		return errors.New("one or more orphaned replicated policies failed to be deleted")
	}

	return nil
}

// handleRootPolicy will properly replicate or clean up when a root policy is updated.
func (r *Propagator) handleRootPolicy(instance *policiesv1.Policy) error {
	// Generate a metric for elapsed handling time for each policy
	entryTS := time.Now()
	defer func() {
		now := time.Now()
		elapsed := now.Sub(entryTS).Seconds()
		roothandlerMeasure.Observe(elapsed)
	}()

	log := log.WithValues("policyName", instance.GetName(), "policyNamespace", instance.GetNamespace())

	// Clean up the replicated policies if the policy is disabled
	if instance.Spec.Disabled {
		log.Info("The policy is disabled, doing clean up")

		allReplicasDeleted, err := r.cleanUpPolicy(instance)
		if err != nil {
			log.Info("One or more replicated policies could not be deleted")

			return err
		}

		// Checks if replicated policies exist in the event that
		// a double reconcile to prevent emitting the same event twice
		if allReplicasDeleted {
			r.Recorder.Event(instance, "Normal", "PolicyPropagation",
				fmt.Sprintf("Policy %s/%s was disabled", instance.GetNamespace(), instance.GetName()))
		}
	}

	// Get the placement binding in order to later get the placement decisions
	pbList := &policiesv1.PlacementBindingList{}

	log.V(1).Info("Getting the placement bindings", "namespace", instance.GetNamespace())

	err := r.List(context.TODO(), pbList, &client.ListOptions{Namespace: instance.GetNamespace()})
	if err != nil {
		log.Error(err, "Could not list the placement bindings")

		return err
	}

	placements, allDecisions, failedClusters, allFailed := r.handleDecisions(instance, pbList)
	if allFailed {
		log.Info("Failed to get any placement decisions. Giving up on the request.")

		return errors.New("could not get the placement decisions")
	}

	// Clean up before the status update in case the status update fails
	err = r.cleanUpOrphanedRplPolicies(instance, allDecisions)
	if err != nil {
		log.Error(err, "Failed to delete orphaned replicated policies")

		return err
	}

	log.V(1).Info("Updating the root policy status")

	cpcs, _ := r.calculatePerClusterStatus(instance, allDecisions, failedClusters)

	// loop through all pb, update status.placement
	sort.Slice(placements, func(i, j int) bool {
		return placements[i].PlacementBinding < placements[j].PlacementBinding
	})

	err = r.Get(context.TODO(), types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}, instance)
	if err != nil {
		log.Error(err, "Failed to refresh the cached policy. Will use existing policy.")
	}

	instance.Status.Status = cpcs
	instance.Status.ComplianceState = CalculateRootCompliance(cpcs)
	instance.Status.Placement = placements

	err = r.Status().Update(context.TODO(), instance)
	if err != nil {
		return err
	}

	if len(failedClusters) != 0 {
		return errors.New("failed to handle cluster namespaces:" + strings.Join(failedClusters.namespaces(), ","))
	}

	log.Info("Reconciliation complete")

	return nil
}

// handleDecision puts the policy on the cluster, creating it or updating it as required,
// including resolving hub templates. It will return an error if an API call fails; no
// internal states will result in errors (eg invalid templates don't cause errors here)
func (r *Propagator) handleDecision(
	rootPlc *policiesv1.Policy, clusterDec clusterDecision,
) (
	map[k8sdepwatches.ObjectIdentifier]bool, error,
) {
	decision := clusterDec.Cluster

	log := log.WithValues(
		"policyName", rootPlc.GetName(),
		"policyNamespace", rootPlc.GetNamespace(),
		"replicatedPolicyNamespace", decision.ClusterNamespace,
	)
	// retrieve replicated policy in cluster namespace
	replicatedPlc := &policiesv1.Policy{}
	templateRefObjs := map[k8sdepwatches.ObjectIdentifier]bool{}

	err := r.Get(context.TODO(), types.NamespacedName{
		Namespace: decision.ClusterNamespace,
		Name:      common.FullNameForPolicy(rootPlc),
	}, replicatedPlc)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			replicatedPlc, err = r.buildReplicatedPolicy(rootPlc, clusterDec)
			if err != nil {
				return templateRefObjs, err
			}

			// do a quick check for any template delims in the policy before putting it through
			// template processor
			if policyHasTemplates(rootPlc) {
				// resolve hubTemplate before replicating
				// #nosec G104 -- any errors are logged and recorded in the processTemplates method,
				// but the ignored status will be handled appropriately by the policy controllers on
				// the managed cluster(s).
				templateRefObjs, _ = r.processTemplates(replicatedPlc, decision, rootPlc)
			}

			log.Info("Creating the replicated policy")

			err = r.Create(context.TODO(), replicatedPlc)
			if err != nil {
				log.Error(err, "Failed to create the replicated policy")

				return templateRefObjs, err
			}

			r.Recorder.Event(rootPlc, "Normal", "PolicyPropagation",
				fmt.Sprintf("Policy %s/%s was propagated to cluster %s/%s", rootPlc.GetNamespace(),
					rootPlc.GetName(), decision.ClusterNamespace, decision.ClusterName))

			// exit after handling the create path, shouldnt be going to through the update path
			return templateRefObjs, nil
		}

		// failed to get replicated object, requeue
		log.Error(err, "Failed to get the replicated policy")

		return templateRefObjs, err
	}

	// replicated policy already created, need to compare and patch
	desiredReplicatedPolicy, err := r.buildReplicatedPolicy(rootPlc, clusterDec)
	if err != nil {
		return templateRefObjs, err
	}

	if policyHasTemplates(desiredReplicatedPolicy) {
		// If the replicated policy has an initialization vector specified, set it for processing
		if initializationVector, ok := replicatedPlc.Annotations[IVAnnotation]; ok {
			tempAnnotations := desiredReplicatedPolicy.GetAnnotations()
			if tempAnnotations == nil {
				tempAnnotations = make(map[string]string)
			}

			tempAnnotations[IVAnnotation] = initializationVector

			desiredReplicatedPolicy.SetAnnotations(tempAnnotations)
		}
		// resolve hubTemplate before replicating
		// #nosec G104 -- any errors are logged and recorded in the processTemplates method,
		// but the ignored status will be handled appropriately by the policy controllers on
		// the managed cluster(s).
		templateRefObjs, _ = r.processTemplates(desiredReplicatedPolicy, decision, rootPlc)
	}

	if !equivalentReplicatedPolicies(desiredReplicatedPolicy, replicatedPlc) {
		// update needed
		log.Info("Root policy and replicated policy mismatch, updating replicated policy")
		replicatedPlc.SetAnnotations(desiredReplicatedPolicy.GetAnnotations())
		replicatedPlc.SetLabels(desiredReplicatedPolicy.GetLabels())
		replicatedPlc.Spec = desiredReplicatedPolicy.Spec

		err = r.Update(context.TODO(), replicatedPlc)
		if err != nil {
			log.Error(err, "Failed to update the replicated policy")

			return templateRefObjs, err
		}

		r.Recorder.Event(rootPlc, "Normal", "PolicyPropagation",
			fmt.Sprintf("Policy %s/%s was updated for cluster %s/%s", rootPlc.GetNamespace(),
				rootPlc.GetName(), decision.ClusterNamespace, decision.ClusterName))
	}

	return templateRefObjs, nil
}

// a helper to quickly check if there are any templates in any of the policy templates
func policyHasTemplates(instance *policiesv1.Policy) bool {
	for _, policyT := range instance.Spec.PolicyTemplates {
		if templates.HasTemplate(policyT.ObjectDefinition.Raw, startDelim, false) {
			return true
		}
	}

	return false
}

// Iterates through policy definitions and processes hub templates. A special annotation
// policy.open-cluster-management.io/trigger-update is used to trigger reprocessing of the templates
// and ensure that replicated-policies in the cluster are updated only if there is a change. This
// annotation is deleted from the replicated policies and not propagated to the cluster namespaces.
func (r *Propagator) processTemplates(
	replicatedPlc *policiesv1.Policy, decision appsv1.PlacementDecision, rootPlc *policiesv1.Policy,
) (
	map[k8sdepwatches.ObjectIdentifier]bool, error,
) {
	log := log.WithValues(
		"policyName", rootPlc.GetName(),
		"policyNamespace", rootPlc.GetNamespace(),
		"cluster", decision.ClusterName,
	)
	log.V(1).Info("Processing templates")

	annotations := replicatedPlc.GetAnnotations()
	templateRefObjs := map[k8sdepwatches.ObjectIdentifier]bool{}

	// handle possible nil map
	if len(annotations) == 0 {
		annotations = make(map[string]string)
	}

	// if disable-templates annotations exists and is true, then exit without processing templates
	if disable, ok := annotations["policy.open-cluster-management.io/disable-templates"]; ok {
		if boolDisable, err := strconv.ParseBool(disable); err == nil && boolDisable {
			log.Info("Detected the disable-templates annotation. Will not process templates.")

			return templateRefObjs, nil
		}
	}

	// clear the trigger-update annotation, it's only for the root policy shouldn't be in replicated
	// policies as it will cause an unnecessary update to the managed clusters
	if _, ok := annotations[TriggerUpdateAnnotation]; ok {
		delete(annotations, TriggerUpdateAnnotation)
		replicatedPlc.SetAnnotations(annotations)
	}

	templateCfg := getTemplateCfg()
	templateCfg.LookupNamespace = rootPlc.GetNamespace()
	templateCfg.ClusterScopedAllowList = []templates.ClusterScopedObjectIdentifier{{
		Group: "cluster.open-cluster-management.io",
		Kind:  "ManagedCluster",
		Name:  decision.ClusterName,
	}}

	tmplResolver, err := templates.NewResolver(kubeClient, kubeConfig, templateCfg)
	if err != nil {
		log.Error(err, "Error instantiating template resolver")
		panic(err)
	}

	// A policy can have multiple policy templates within it, iterate and process each
	for _, policyT := range replicatedPlc.Spec.PolicyTemplates {
		if !templates.HasTemplate(policyT.ObjectDefinition.Raw, templateCfg.StartDelim, false) {
			continue
		}

		if !isConfigurationPolicy(policyT) {
			// has Templates but not a configuration policy
			err = k8serrors.NewBadRequest("Templates are restricted to only Configuration Policies")
			log.Error(err, "Not a Configuration Policy")

			r.Recorder.Event(rootPlc, "Warning", "PolicyPropagation",
				fmt.Sprintf(
					"Policy %s/%s has templates but it is not a ConfigurationPolicy.",
					rootPlc.GetName(),
					rootPlc.GetNamespace(),
				),
			)

			return templateRefObjs, err
		}

		log.V(1).Info("Found an object definition with templates")

		templateContext := struct {
			ManagedClusterName   string
			ManagedClusterLabels map[string]string
		}{
			ManagedClusterName: decision.ClusterName,
		}

		if strings.Contains(string(policyT.ObjectDefinition.Raw), "ManagedClusterLabels") {
			templateRefObjs[k8sdepwatches.ObjectIdentifier{
				Group:     "cluster.open-cluster-management.io",
				Version:   "v1",
				Kind:      "ManagedCluster",
				Namespace: "",
				Name:      decision.ClusterName,
			}] = true

			managedCluster := &clusterv1.ManagedCluster{}

			err := r.Get(context.TODO(), types.NamespacedName{Name: decision.ClusterName}, managedCluster)
			if err != nil {
				log.Error(err, "Failed to get the ManagedCluster in order to use its labels in a hub template")
			}

			// if an error occurred, the ManagedClusterLabels will just be left empty
			templateContext.ManagedClusterLabels = managedCluster.Labels
		}

		// Handle value encryption initialization
		usesEncryption := templates.UsesEncryption(
			policyT.ObjectDefinition.Raw, templateCfg.StartDelim, templateCfg.StopDelim,
		)
		// Initialize AES Key and initialization vector
		if usesEncryption && !templateCfg.EncryptionEnabled {
			log.V(1).Info("Found an object definition requiring encryption. Handling encryption keys.")
			// Get/generate the encryption key
			encryptionKey, err := r.getEncryptionKey(decision.ClusterName)
			if err != nil {
				log.Error(err, "Failed to get/generate the policy encryption key")

				return templateRefObjs, err
			}

			// Get/generate the initialization vector
			initializationVector, err := r.getInitializationVector(
				rootPlc.GetName(), decision.ClusterName, annotations,
			)
			if err != nil {
				log.Error(err, "Failed to get initialization vector")

				return templateRefObjs, err
			}

			// Set the initialization vector in the annotations
			replicatedPlc.SetAnnotations(annotations)

			// Set the EncryptionConfig with the retrieved key
			templateCfg.EncryptionConfig = templates.EncryptionConfig{
				EncryptionEnabled:    true,
				AESKey:               encryptionKey,
				InitializationVector: initializationVector,
			}

			err = tmplResolver.SetEncryptionConfig(templateCfg.EncryptionConfig)
			if err != nil {
				log.Error(err, "Error setting encryption configuration")

				return templateRefObjs, err
			}
		}

		templateResult, tplErr := tmplResolver.ResolveTemplate(policyT.ObjectDefinition.Raw, templateContext)

		// Record the referenced objects in the template even if there is an error. This is because a change in the
		// object could fix the error.
		for _, refObj := range templateResult.ReferencedObjects {
			templateRefObjs[refObj] = true
		}

		if tplErr != nil {
			log.Error(tplErr, "Failed to resolve templates")

			r.Recorder.Event(
				rootPlc,
				"Warning",
				"PolicyPropagation",
				fmt.Sprintf(
					"Failed to resolve templates for cluster %s/%s: %s",
					decision.ClusterNamespace,
					decision.ClusterName,
					tplErr.Error(),
				),
			)
			// Set an annotation on the policyTemplate(e.g. ConfigurationPolicy) to the template processing error msg
			// managed clusters will use this when creating a violation
			policyTObjectUnstructured := &unstructured.Unstructured{}

			jsonErr := json.Unmarshal(policyT.ObjectDefinition.Raw, policyTObjectUnstructured)
			if jsonErr != nil {
				// it shouldn't get here but if it did just log a msg
				// it's all right, a generic msg will be used on the managedcluster
				log.Error(jsonErr, "Error unmarshalling the object definition to JSON")
			} else {
				policyTAnnotations := policyTObjectUnstructured.GetAnnotations()
				if policyTAnnotations == nil {
					policyTAnnotations = make(map[string]string)
				}
				policyTAnnotations["policy.open-cluster-management.io/hub-templates-error"] = tplErr.Error()
				policyTObjectUnstructured.SetAnnotations(policyTAnnotations)

				updatedPolicyT, jsonErr := json.Marshal(policyTObjectUnstructured)
				if jsonErr != nil {
					log.Error(jsonErr, "Failed to marshall the policy template to JSON")
				} else {
					policyT.ObjectDefinition.Raw = updatedPolicyT
				}
			}

			return templateRefObjs, tplErr
		}

		policyT.ObjectDefinition.Raw = templateResult.ResolvedJSON

		// Set initialization vector annotation on the ObjectDefinition for the controller's use
		if usesEncryption {
			policyTObjectUnstructured := &unstructured.Unstructured{}

			jsonErr := json.Unmarshal(templateResult.ResolvedJSON, policyTObjectUnstructured)
			if jsonErr != nil {
				return templateRefObjs, fmt.Errorf("failed to unmarshal the object definition to JSON: %w", jsonErr)
			}

			policyTAnnotations := policyTObjectUnstructured.GetAnnotations()
			if policyTAnnotations == nil {
				policyTAnnotations = make(map[string]string)
			}

			policyIV := annotations[IVAnnotation]
			foundIV := policyTAnnotations[IVAnnotation]

			if policyIV != foundIV {
				policyTAnnotations[IVAnnotation] = policyIV
				policyTObjectUnstructured.SetAnnotations(policyTAnnotations)

				updatedPolicyT, jsonErr := json.Marshal(policyTObjectUnstructured)
				if jsonErr != nil {
					return templateRefObjs, fmt.Errorf("failed to marshal the policy template to JSON: %w", jsonErr)
				}

				policyT.ObjectDefinition.Raw = updatedPolicyT
			}
		}
	}

	log.V(1).Info("Successfully processed templates")

	return templateRefObjs, nil
}

func isConfigurationPolicy(policyT *policiesv1.PolicyTemplate) bool {
	// check if it is a configuration policy first
	var jsonDef map[string]interface{}
	_ = json.Unmarshal(policyT.ObjectDefinition.Raw, &jsonDef)

	return jsonDef != nil && jsonDef["kind"] == "ConfigurationPolicy"
}

func (r *Propagator) isPolicyInPolicySet(policyName, policySetName, namespace string) bool {
	log := log.WithValues("policyName", policyName, "policySetName", policySetName, "policyNamespace", namespace)

	policySet := policiesv1beta1.PolicySet{}
	setNN := types.NamespacedName{
		Name:      policySetName,
		Namespace: namespace,
	}

	if err := r.Get(context.TODO(), setNN, &policySet); err != nil {
		log.Error(err, "Failed to get the policyset")

		return false
	}

	for _, plc := range policySet.Spec.Policies {
		if string(plc) == policyName {
			return true
		}
	}

	return false
}
