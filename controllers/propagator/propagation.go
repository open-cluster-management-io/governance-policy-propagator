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
	"time"

	retry "github.com/avast/retry-go/v3"
	"github.com/go-logr/logr"
	templates "github.com/stolostron/go-template-utils/v3/pkg/templates"
	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

const (
	attemptsDefault = 3
	attemptsEnvName = "CONTROLLER_CONFIG_RETRY_ATTEMPTS"
)

// The configuration in minutes to requeue after if something failed after several
// retries.
const (
	requeueErrorDelayEnvName = "CONTROLLER_CONFIG_REQUEUE_ERROR_DELAY"
	requeueErrorDelayDefault = 5
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
	attempts             int
	requeueErrorDelay    int
	concurrencyPerPolicy int
	kubeConfig           *rest.Config
	kubeClient           *kubernetes.Interface
)

func Initialize(kubeconfig *rest.Config, kubeclient *kubernetes.Interface) {
	kubeConfig = kubeconfig
	kubeClient = kubeclient
	attempts = getEnvVarPosInt(attemptsEnvName, attemptsDefault)
	requeueErrorDelay = getEnvVarPosInt(requeueErrorDelayEnvName, requeueErrorDelayDefault)
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

// The options to call retry.Do with
func getRetryOptions(logger logr.Logger, retryMsg string) []retry.Option {
	return common.GetRetryOptions(logger, retryMsg, uint(attempts))
}

func (r *PolicyReconciler) deletePolicy(plc *policiesv1.Policy) error {
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

// cleanUpPolicy will delete all replicated policies associated with provided policy.
func (r *PolicyReconciler) cleanUpPolicy(instance *policiesv1.Policy) error {
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

		return err
	}

	err = r.List(
		context.TODO(), replicatedPlcList, client.MatchingLabels(LabelsForRootPolicy(instance)),
	)
	if err != nil {
		log.Error(err, "Failed to list the replicated policies")

		return err
	}

	if len(replicatedPlcList.Items) == 0 {
		log.V(2).Info("No replicated policies to delete.")

		return nil
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
		return errors.New("failed to delete one or more replicated policies")
	}

	return nil
}

type decisionHandler interface {
	handleDecision(instance *policiesv1.Policy, decision appsv1.PlacementDecision) (
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
	decisions <-chan appsv1.PlacementDecision,
	results chan<- decisionResult,
) {
	for decision := range decisions {
		log := log.WithValues(
			"policyName", instance.GetName(),
			"policyNamespace", instance.GetNamespace(),
			"decision", decision,
		)
		log.Info("Handling the decision")

		templateRefObjs := map[k8sdepwatches.ObjectIdentifier]bool{}

		instanceCopy := *instance.DeepCopy()
		err := retry.Do(
			func() error {
				var err error
				templateRefObjs, err = decisionHandler.handleDecision(&instanceCopy, decision)

				return err
			},
			getRetryOptions(log.V(1), "Retrying to replicate the policy")...,
		)

		if err == nil {
			log.V(1).Info("Replicated the policy")
		} else {
			log.Info("Gave up on replicating the policy after too many retries")
		}

		results <- decisionResult{decision, templateRefObjs, err}
	}
}

type decisionSet map[appsv1.PlacementDecision]bool

// handleDecisions will get all the placement decisions based on the input policy and placement
// binding list and propagate the policy. Note that this method performs concurrent operations.
// It returns the following:
//   - placements - a slice of all the placement decisions discovered
//   - allDecisions - a set of all the placement decisions encountered
//   - failedClusters - a set of all the clusters that encountered an error during propagation
//   - allFailed - a bool that determines if all clusters encountered an error during propagation
func (r *PolicyReconciler) handleDecisions(
	instance *policiesv1.Policy, pbList *policiesv1.PlacementBindingList,
) (
	placements []*policiesv1.Placement, allDecisions decisionSet, failedClusters decisionSet, allFailed bool,
) {
	log := log.WithValues("policyName", instance.GetName(), "policyNamespace", instance.GetNamespace())
	allDecisions = map[appsv1.PlacementDecision]bool{}
	failedClusters = map[appsv1.PlacementDecision]bool{}

	allTemplateRefObjs := getPolicySetDependencies(instance)

	for _, pb := range pbList.Items {
		subjects := pb.Subjects
		for _, subject := range subjects {
			if !(subject.APIGroup == policiesv1.SchemeGroupVersion.Group &&
				subject.Kind == policiesv1.Kind &&
				subject.Name == instance.GetName()) && !r.isPolicySetSubject(instance, subject) {
				continue
			}

			var decisions []appsv1.PlacementDecision
			var p []*policiesv1.Placement

			err := retry.Do(
				func() error {
					var err error
					decisions, p, err = getPlacementDecisions(r.Client, pb, instance)

					return err
				},
				getRetryOptions(log.V(1), "Retrying to get the placement decisions")...,
			)
			if err != nil {
				log.Info("Gave up on getting the placement decisions after too many retries")

				allFailed = true

				return
			}

			placements = append(placements, p...)

			if instance.Spec.Disabled {
				// Only handle the first match in pb.spec.subjects
				break
			}

			if len(decisions) == 0 {
				log.Info("No placement decisions to process on this policy")

				break
			}

			// Setup the workers which will call r.handleDecision. The number of workers depends
			// on the number of decisions and the limit defined in concurrencyPerPolicy.
			// decisionsChan acts as the work queue of decisions to process. resultsChan contains
			// the results from the decisions being processed.
			decisionsChan := make(chan appsv1.PlacementDecision, len(decisions))
			resultsChan := make(chan decisionResult, len(decisions))
			numWorkers := common.GetNumWorkers(len(decisions), concurrencyPerPolicy)

			for i := 0; i < numWorkers; i++ {
				go handleDecisionWrapper(r, instance, decisionsChan, resultsChan)
			}

			log.Info("Handling the placement decisions", "count", len(decisions))

			for _, decision := range decisions {
				log.V(2).Info(
					"Scheduling work to handle the decision for the cluster",
					"name", decision.ClusterName,
				)
				decisionsChan <- decision
			}

			// Wait for all the decisions to be processed.
			log.V(2).Info("Waiting for the result of handling the decision(s)", "count", len(decisions))

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
				if processedResults == len(decisions) {
					close(decisionsChan)
					close(resultsChan)
					log.Info("All the placement decisions have been handled", "count", len(decisions))
				}
			}

			// Only handle the first match in pb.spec.subjects
			break
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
func (r *PolicyReconciler) cleanUpOrphanedRplPolicies(instance *policiesv1.Policy, allDecisions decisionSet) error {
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
		name := fullNameForPolicy(instance)
		log := log.WithValues("name", name, "namespace", cluster.ClusterNamespace)
		log.Info("Deleting the orphaned replicated policy")

		err := retry.Do(
			func() error {
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

				if err != nil && k8serrors.IsNotFound(err) {
					return nil
				}

				return err
			},
			getRetryOptions(log.V(1), "Retrying to delete the orphaned replicated policy")...,
		)
		if err != nil {
			successful = false

			log.Error(err, "Failed to delete the orphaned replicated policy")
		}
	}

	if !successful {
		return errors.New("one or more orphaned replicated policies failed to be deleted")
	}

	return nil
}

func (r *PolicyReconciler) recordWarning(instance *policiesv1.Policy, msgPrefix string) {
	msg := fmt.Sprintf(
		"%s for the policy %s/%s",
		msgPrefix,
		instance.GetNamespace(),
		instance.GetName(),
	)
	r.Recorder.Event(instance, "Warning", "PolicyPropagation", msg)
}

// handleRootPolicy will properly replicate or clean up when a root policy is updated.
//
// Errors are logged in this method and a summary error is returned. This is because the method
// handles retries and will only return after giving up.
//
// There are several retries within handleRootPolicy. This approach is taken over retrying the whole
// method because it makes the retries more targeted and prevents race conditions, such as a
// placement binding getting updated, from causing inconsistencies.
func (r *PolicyReconciler) handleRootPolicy(instance *policiesv1.Policy) error {
	// Generate a metric for elapsed handling time for each policy
	entryTS := time.Now()
	defer func() {
		now := time.Now()
		elapsed := now.Sub(entryTS).Seconds()
		roothandlerMeasure.Observe(elapsed)
	}()

	log := log.WithValues("policyName", instance.GetName(), "policyNamespace", instance.GetNamespace())
	originalInstance := instance.DeepCopy()

	// Clean up the replicated policies if the policy is disabled
	if instance.Spec.Disabled {
		log.Info("The policy is disabled, doing clean up")

		err := retry.Do(
			func() error { return r.cleanUpPolicy(instance) },
			getRetryOptions(log, "Retrying the policy clean up")...,
		)
		if err != nil {
			log.Info("Gave up on the policy clean up after too many retries")
			r.recordWarning(instance, "One or more replicated policies could not be deleted")

			return err
		}

		r.Recorder.Event(instance, "Normal", "PolicyPropagation",
			fmt.Sprintf("Policy %s/%s was disabled", instance.GetNamespace(), instance.GetName()))
	}

	// Get the placement binding in order to later get the placement decisions
	pbList := &policiesv1.PlacementBindingList{}

	log.V(1).Info("Getting the placement bindings", "namespace", instance.GetNamespace())

	err := retry.Do(
		func() error {
			return r.List(
				context.TODO(), pbList, &client.ListOptions{Namespace: instance.GetNamespace()},
			)
		},
		getRetryOptions(log.V(1), "Retrying to list the placement bindings")...,
	)
	if err != nil {
		log.Info("Gave up on listing the placement bindings after too many retries")
		r.recordWarning(instance, "Could not list the placement bindings")

		return err
	}

	// allDecisions and failedClusters are sets in the format of <namespace>/<name>
	placements, allDecisions, failedClusters, allFailed := r.handleDecisions(instance, pbList)
	if allFailed {
		log.Info("Failed to get any placement decisions. Giving up on the request.")

		msg := "Could not get the placement decisions"

		r.recordWarning(instance, msg)

		// Make the error start with a lower case for the linting check
		return errors.New("c" + msg[1:])
	}

	cpcs, _ := r.calculatePerClusterStatus(instance, failedClusters)

	instance.Status.Status = cpcs
	instance.Status.ComplianceState = calculateRootCompliance(cpcs)

	// looped through all pb, update status.placement
	sort.Slice(placements, func(i, j int) bool {
		return placements[i].PlacementBinding < placements[j].PlacementBinding
	})

	instance.Status.Placement = placements

	log.V(1).Info("Updating the root policy status")

	err = retry.Do(
		func() error {
			return r.Status().Patch(
				context.TODO(), instance, client.MergeFrom(originalInstance),
			)
		},
		getRetryOptions(log.V(1), "Retrying to update the root policy status")...,
	)

	if err != nil {
		log.Error(err, "Gave up on updating the root policy status after too many retries")
		r.recordWarning(instance, "Failed to update the policy status")

		return err
	}

	err = r.cleanUpOrphanedRplPolicies(instance, allDecisions)
	if err != nil {
		log.Error(err, "Gave up on deleting the orphaned replicated policies after too many retries")
		r.recordWarning(instance, "Failed to delete orphaned replicated policies")

		return err
	}

	log.Info("Reconciliation complete")

	return nil
}

// getApplicationPlacements return the placements from an application
// lifecycle placementrule
func getApplicationPlacements(
	c client.Client, pb policiesv1.PlacementBinding, instance *policiesv1.Policy,
) ([]*policiesv1.Placement, error) {
	plr := &appsv1.PlacementRule{}

	err := c.Get(context.TODO(), types.NamespacedName{
		Namespace: instance.GetNamespace(),
		Name:      pb.PlacementRef.Name,
	}, plr)
	// no error when not found
	if err != nil && !k8serrors.IsNotFound(err) {
		log.Error(
			err,
			"Failed to get the PlacementRule",
			"namespace", instance.GetNamespace(),
			"name", pb.PlacementRef.Name,
		)

		return nil, err
	}

	var placements []*policiesv1.Placement

	plcPlacementAdded := false

	for _, subject := range pb.Subjects {
		if subject.Kind == policiesv1.PolicySetKind {
			// retrieve policyset to see if policy is part of it
			plcset := &policiesv1beta1.PolicySet{}
			err := c.Get(context.TODO(), types.NamespacedName{
				Namespace: instance.GetNamespace(),
				Name:      subject.Name,
			}, plcset)
			// no error when not found
			if err != nil && !k8serrors.IsNotFound(err) {
				log.Error(
					err,
					"Failed to get the policyset",
					"namespace", instance.GetNamespace(),
					"name", subject.Name,
				)

				continue
			}

			for _, plcName := range plcset.Spec.Policies {
				if plcName == policiesv1beta1.NonEmptyString(instance.Name) {
					// found matching policy in policyset, add placement to it
					placement := &policiesv1.Placement{
						PlacementBinding: pb.GetName(),
						PlacementRule:    plr.GetName(),
						PolicySet:        subject.Name,
					}
					placements = append(placements, placement)

					break
				}
			}
		} else if subject.Kind == policiesv1.Kind && subject.Name == instance.GetName() && !plcPlacementAdded {
			placement := &policiesv1.Placement{
				PlacementBinding: pb.GetName(),
				PlacementRule:    plr.GetName(),
			}
			placements = append(placements, placement)
			// should only add policy placement once in case placement binding subjects contains duplicated policies
			plcPlacementAdded = true
		}
	}

	return placements, nil
}

// getClusterPlacements return the placement decisions from an application
// lifecycle placementrule
func getClusterPlacements(
	c client.Client, pb policiesv1.PlacementBinding, instance *policiesv1.Policy,
) ([]*policiesv1.Placement, error) {
	log := log.WithValues("name", pb.PlacementRef.Name, "namespace", instance.GetNamespace())
	pl := &clusterv1beta1.Placement{}

	err := c.Get(context.TODO(), types.NamespacedName{
		Namespace: instance.GetNamespace(),
		Name:      pb.PlacementRef.Name,
	}, pl)
	// no error when not found
	if err != nil && !k8serrors.IsNotFound(err) {
		log.Error(err, "Failed to get the Placement")

		return nil, err
	}

	var placements []*policiesv1.Placement

	plcPlacementAdded := false

	for _, subject := range pb.Subjects {
		if subject.Kind == policiesv1.PolicySetKind {
			// retrieve policyset to see if policy is part of it
			plcset := &policiesv1beta1.PolicySet{}
			err := c.Get(context.TODO(), types.NamespacedName{
				Namespace: instance.GetNamespace(),
				Name:      subject.Name,
			}, plcset)
			// no error when not found
			if err != nil && !k8serrors.IsNotFound(err) {
				log.Error(
					err,
					"Failed to get the policyset",
					"namespace", instance.GetNamespace(),
					"name", subject.Name,
				)

				continue
			}

			for _, plcName := range plcset.Spec.Policies {
				if plcName == policiesv1beta1.NonEmptyString(instance.Name) {
					// found matching policy in policyset, add placement to it
					placement := &policiesv1.Placement{
						PlacementBinding: pb.GetName(),
						Placement:        pl.GetName(),
						PolicySet:        subject.Name,
					}
					placements = append(placements, placement)

					break
				}
			}
		} else if subject.Kind == policiesv1.Kind && subject.Name == instance.GetName() && !plcPlacementAdded {
			// add current Placement to placement, if not found no decisions will be found
			placement := &policiesv1.Placement{
				PlacementBinding: pb.GetName(),
				Placement:        pl.GetName(),
			}
			placements = append(placements, placement)
			// should only add policy placement once in case placement binding subjects contains duplicated policies
			plcPlacementAdded = true
		}
	}

	return placements, nil
}

// getPlacementDecisions gets the PlacementDecisions for a PlacementBinding
func getPlacementDecisions(c client.Client, pb policiesv1.PlacementBinding,
	instance *policiesv1.Policy,
) ([]appsv1.PlacementDecision, []*policiesv1.Placement, error) {
	if pb.PlacementRef.APIGroup == appsv1.SchemeGroupVersion.Group &&
		pb.PlacementRef.Kind == "PlacementRule" {
		d, err := common.GetApplicationPlacementDecisions(c, pb, instance, log)
		if err != nil {
			return nil, nil, err
		}

		placement, err := getApplicationPlacements(c, pb, instance)
		if err != nil {
			return nil, nil, err
		}

		return d, placement, nil
	} else if pb.PlacementRef.APIGroup == clusterv1beta1.SchemeGroupVersion.Group &&
		pb.PlacementRef.Kind == "Placement" {
		d, err := common.GetClusterPlacementDecisions(c, pb, instance, log)
		if err != nil {
			return nil, nil, err
		}

		placement, err := getClusterPlacements(c, pb, instance)
		if err != nil {
			return nil, nil, err
		}

		return d, placement, nil
	}

	return nil, nil, fmt.Errorf("placement binding %s/%s reference is not valid", pb.Name, pb.Namespace)
}

func (r *PolicyReconciler) handleDecision(
	rootPlc *policiesv1.Policy, decision appsv1.PlacementDecision,
) (
	map[k8sdepwatches.ObjectIdentifier]bool, error,
) {
	log := log.WithValues(
		"policyName", rootPlc.GetName(),
		"policyNamespace", rootPlc.GetNamespace(),
		"replicatePolicyName", fullNameForPolicy(rootPlc),
		"replicatedPolicyNamespace", decision.ClusterNamespace,
	)
	// retrieve replicated policy in cluster namespace
	replicatedPlc := &policiesv1.Policy{}
	templateRefObjs := map[k8sdepwatches.ObjectIdentifier]bool{}

	err := r.Get(context.TODO(), types.NamespacedName{
		Namespace: decision.ClusterNamespace,
		Name:      fullNameForPolicy(rootPlc),
	}, replicatedPlc)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			replicatedPlc, err = r.buildReplicatedPolicy(rootPlc, decision)
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
	desiredReplicatedPolicy, err := r.buildReplicatedPolicy(rootPlc, decision)
	if err != nil {
		return templateRefObjs, err
	}

	if policyHasTemplates(desiredReplicatedPolicy) {
		// If the replicated policy has an initialization vector specified, set it for processing
		if initializationVector, ok := replicatedPlc.Annotations[IVAnnotation]; ok {
			tempAnnotations := replicatedPlc.GetAnnotations()
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

// iterates through policy definitions  and  processes hub templates
// a special  annotation policy.open-cluster-management.io/trigger-update is used to trigger reprocessing of the
// templates and ensuring that the replicated-policies in cluster is updated only if there is a change.
// this annotation is deleted from the replicated policies and not propagated to the cluster namespaces.
func (r *PolicyReconciler) processTemplates(
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
			ManagedClusterName string
		}{
			ManagedClusterName: decision.ClusterName,
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

func (r *PolicyReconciler) isPolicySetSubject(instance *policiesv1.Policy, subject policiesv1.Subject) bool {
	log := log.WithValues("policyName", instance.GetName(), "policyNamespace", instance.GetNamespace())

	if subject.APIGroup == policiesv1.SchemeGroupVersion.Group &&
		subject.Kind == policiesv1.PolicySetKind {
		policySetNamespacedName := types.NamespacedName{
			Name:      subject.Name,
			Namespace: instance.GetNamespace(),
		}

		policySet := &policiesv1beta1.PolicySet{}

		err := r.Get(context.TODO(), policySetNamespacedName, policySet)
		if err != nil {
			log.Error(err, "Failed to get the policyset", "policySetName", subject.Name, "policyName",
				instance.GetName(), "policyNamespace", instance.GetNamespace())

			return false
		}

		for _, plc := range policySet.Spec.Policies {
			if string(plc) == instance.GetName() {
				return true
			}
		}
	}

	return false
}
