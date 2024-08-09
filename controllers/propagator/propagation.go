// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	templates "github.com/stolostron/go-template-utils/v6/pkg/templates"
	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

const (
	TemplateStartDelim      = "{{hub"
	TemplateStopDelim       = "hub}}"
	TriggerUpdateAnnotation = "policy.open-cluster-management.io/trigger-update"
)

var (
	ErrRetryable = errors.New("")
	ErrSAMissing = errors.New("the hubTemplatesOptions.serviceAccountName does not exist")
)

type Propagator struct {
	client.Client
	Scheme                  *runtime.Scheme
	Recorder                record.EventRecorder
	RootPolicyLocks         *sync.Map
	ReplicatedPolicyUpdates chan event.GenericEvent
}

// clusterDecision contains a single decision where the replicated policy
// should be processed and any overrides to the root policy
type clusterDecision struct {
	Cluster         string
	PolicyOverrides policiesv1.BindingOverrides
}

// cleanUpOrphanedRplPolicies compares the status of the input policy against the input placement
// decisions. If the cluster exists in the status but doesn't exist in the input placement
// decisions, then it's considered stale and an event is sent to the replicated policy reconciler
// so the policy will be removed.
func (r *RootPolicyReconciler) cleanUpOrphanedRplPolicies(
	instance *policiesv1.Policy, originalCPCS []*policiesv1.CompliancePerClusterStatus, allDecisions common.DecisionSet,
) error {
	log := log.WithValues("policyName", instance.GetName(), "policyNamespace", instance.GetNamespace())

	for _, cluster := range originalCPCS {
		if allDecisions[cluster.ClusterName] {
			continue
		}

		// not found in allDecisions, orphan, send an event for it to delete itself
		simpleObj := &common.GuttedObject{
			TypeMeta: metav1.TypeMeta{
				Kind:       policiesv1.Kind,
				APIVersion: policiesv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      common.FullNameForPolicy(instance),
				Namespace: cluster.ClusterName,
			},
		}

		log.V(2).Info("Sending reconcile for replicated policy", "replicatedPolicyName", simpleObj.GetName())

		r.ReplicatedPolicyUpdates <- event.GenericEvent{Object: simpleObj}
	}

	return nil
}

// handleRootPolicy will properly replicate or clean up when a root policy is updated.
func (r *RootPolicyReconciler) handleRootPolicy(ctx context.Context, instance *policiesv1.Policy) error {
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

		updateCount, err := r.updateExistingReplicas(ctx, instance.Namespace+"."+instance.Name)
		if err != nil {
			return err
		}

		// Checks if replicated policies exist in the event that
		// a double reconcile to prevent emitting the same event twice
		if updateCount > 0 {
			r.Recorder.Event(instance, "Normal", "PolicyPropagation",
				fmt.Sprintf("Policy %s/%s was disabled", instance.GetNamespace(), instance.GetName()))
		}
	}

	// make a copy of the original status
	originalCPCS := make([]*policiesv1.CompliancePerClusterStatus, len(instance.Status.Status))
	copy(originalCPCS, instance.Status.Status)

	decisions, err := common.RootStatusUpdate(ctx, r.Client, instance)
	if err != nil {
		return err
	}

	log.Info("Sending reconcile events to replicated policies", "decisionsCount", len(decisions))

	for decision := range decisions {
		simpleObj := &common.GuttedObject{
			TypeMeta: metav1.TypeMeta{
				Kind:       policiesv1.Kind,
				APIVersion: policiesv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      common.FullNameForPolicy(instance),
				Namespace: decision,
			},
		}

		log.V(2).Info("Sending reconcile for replicated policy", "replicatedPolicyName", simpleObj.GetName())

		r.ReplicatedPolicyUpdates <- event.GenericEvent{Object: simpleObj}
	}

	err = r.cleanUpOrphanedRplPolicies(instance, originalCPCS, decisions)
	if err != nil {
		log.Error(err, "Failed to delete orphaned replicated policies")

		return err
	}

	return nil
}

type templateCtx struct {
	ManagedClusterName   string
	ManagedClusterLabels map[string]string
	PolicyMetadata       map[string]interface{}
}

func addManagedClusterLabels(clusterName string) func(templates.CachingQueryAPI, interface{}) (interface{}, error) {
	return func(api templates.CachingQueryAPI, ctx interface{}) (interface{}, error) {
		typedCtx, ok := ctx.(templateCtx)
		if !ok {
			return ctx, nil
		}

		managedClusterGVK := schema.GroupVersionKind{
			Group:   "cluster.open-cluster-management.io",
			Version: "v1",
			Kind:    "ManagedCluster",
		}

		managedCluster, err := api.Get(managedClusterGVK, "", clusterName)
		if err != nil {
			return ctx, err
		}

		typedCtx.ManagedClusterLabels = managedCluster.GetLabels()

		return typedCtx, nil
	}
}

// Iterates through policy definitions and processes hub templates. A special annotation
// policy.open-cluster-management.io/trigger-update is used to trigger reprocessing of the templates
// and ensure that replicated-policies in the cluster are updated only if there is a change. This
// annotation is deleted from the replicated policies and not propagated to the cluster namespaces.
// If hubTemplateOptions.serviceAccountName specifies a service account which does not exist, an ErrSAMissing
// error is returned for the caller to add a watch on the missing service account.
func (r *ReplicatedPolicyReconciler) processTemplates(
	ctx context.Context,
	replicatedPlc *policiesv1.Policy, clusterName string, rootPlc *policiesv1.Policy,
) error {
	log := log.WithValues(
		"policyName", rootPlc.GetName(),
		"policyNamespace", rootPlc.GetNamespace(),
		"cluster", clusterName,
	)

	log.V(1).Info("Processing templates")

	watcher := k8sdepwatches.ObjectIdentifier{
		Group:     policiesv1.GroupVersion.Group,
		Version:   policiesv1.GroupVersion.Version,
		Kind:      replicatedPlc.Kind,
		Namespace: replicatedPlc.GetNamespace(),
		Name:      replicatedPlc.GetName(),
	}

	var saNSName types.NamespacedName
	var templateResolverOptions templates.ResolveOptions

	if replicatedPlc.Spec.HubTemplateOptions != nil && replicatedPlc.Spec.HubTemplateOptions.ServiceAccountName != "" {
		saNSName = types.NamespacedName{
			Namespace: rootPlc.Namespace,
			Name:      replicatedPlc.Spec.HubTemplateOptions.ServiceAccountName,
		}

		templateResolverOptions = templates.ResolveOptions{
			Watcher: &watcher,
		}
	} else {
		saNSName = defaultSANamespacedName
		templateResolverOptions = templates.ResolveOptions{
			ClusterScopedAllowList: []templates.ClusterScopedObjectIdentifier{
				{
					Group: "cluster.open-cluster-management.io",
					Kind:  "ManagedCluster",
					Name:  clusterName,
				},
			},
			LookupNamespace: rootPlc.GetNamespace(),
			Watcher:         &watcher,
		}
	}

	templateResolver, err := r.TemplateResolvers.GetResolver(watcher, saNSName)
	if err != nil {
		log.Error(err, "Failed to get the template resolver", "serviceAccount", saNSName)

		for i, policyT := range replicatedPlc.Spec.PolicyTemplates {
			if !templates.HasTemplate(policyT.ObjectDefinition.Raw, TemplateStartDelim, false) {
				continue
			}

			var setErr error

			if errors.Is(err, ErrSAMissing) {
				setErr = setTemplateError(
					policyT,
					fmt.Errorf(
						"the service account in hubTemplateOptions.serviceAccountName (%s) does not exist", saNSName,
					),
				)
			} else {
				setErr = setTemplateError(
					policyT,
					fmt.Errorf(
						"failed to set up the template resolver for the service account in hubTemplateOptions."+
							"serviceAccountName (%s): %v",
						saNSName, err,
					),
				)
			}

			if setErr != nil {
				log.Error(setErr, "Failed to set the hub template error", "policyTemplateIndex", i)
			}
		}

		return err
	}

	err = templateResolver.StartQueryBatch(watcher)
	if err != nil {
		log.Error(err, "Failed to start a query batch for the templating")

		return err
	}

	defer func() {
		if err := templateResolver.EndQueryBatch(watcher); err != nil {
			log.Error(err, "Failed to end the query batch for the templating")
		}
	}()

	annotations := replicatedPlc.GetAnnotations()

	// handle possible nil map
	if len(annotations) == 0 {
		annotations = make(map[string]string)
	}

	// if disable-templates annotations exists and is true, then exit without processing templates
	if disable, ok := annotations["policy.open-cluster-management.io/disable-templates"]; ok {
		if boolDisable, err := strconv.ParseBool(disable); err == nil && boolDisable {
			log.Info("Detected the disable-templates annotation. Will not process templates.")

			return nil
		}
	}

	// clear the trigger-update annotation, it's only for the root policy shouldn't be in replicated
	// policies as it will cause an unnecessary update to the managed clusters
	if _, ok := annotations[TriggerUpdateAnnotation]; ok {
		delete(annotations, TriggerUpdateAnnotation)
		replicatedPlc.SetAnnotations(annotations)
	}

	// A policy can have multiple policy templates within it, iterate and process each
	for i, policyT := range replicatedPlc.Spec.PolicyTemplates {
		if !templates.HasTemplate(policyT.ObjectDefinition.Raw, TemplateStartDelim, false) {
			continue
		}

		log := log.WithValues("policyTemplateIndex", i)

		log.V(1).Info("Resolving templates on the policy template")

		templateContext := templateCtx{
			ManagedClusterName: clusterName,
			PolicyMetadata: map[string]interface{}{
				"annotations": rootPlc.Annotations,
				"labels":      rootPlc.Labels,
				"name":        rootPlc.Name,
				"namespace":   rootPlc.Namespace,
			},
		}

		if strings.Contains(string(policyT.ObjectDefinition.Raw), "ManagedClusterLabels") {
			templateResolverOptions.ContextTransformers = append(
				templateResolverOptions.ContextTransformers, addManagedClusterLabels(clusterName),
			)
		}

		// Handle value encryption initialization
		usesEncryption := templates.UsesEncryption(policyT.ObjectDefinition.Raw, TemplateStartDelim, TemplateStopDelim)
		// Initialize AES Key and initialization vector
		if usesEncryption && !templateResolverOptions.EncryptionEnabled {
			log.V(1).Info("Found an object definition requiring encryption. Handling encryption keys.")
			// Get/generate the encryption key
			encryptionKey, err := r.getEncryptionKey(ctx, clusterName)
			if err != nil {
				log.Error(err, "Failed to get/generate the policy encryption key")

				return fmt.Errorf("%w%w", ErrRetryable, err)
			}

			// Get/generate the initialization vector
			initializationVector, err := r.getInitializationVector(
				rootPlc.GetName(), clusterName, annotations,
			)
			if err != nil {
				log.Error(err, "Failed to get initialization vector")

				return err
			}

			// Set the initialization vector in the annotations
			replicatedPlc.SetAnnotations(annotations)

			// Set the EncryptionConfig with the retrieved key
			templateResolverOptions.EncryptionConfig = templates.EncryptionConfig{
				EncryptionEnabled:    true,
				AESKey:               encryptionKey,
				InitializationVector: initializationVector,
			}
		}

		templateResult, tplErr := templateResolver.ResolveTemplate(
			policyT.ObjectDefinition.Raw, templateContext, &templateResolverOptions,
		)
		if tplErr != nil {
			log.Error(tplErr, "Failed to resolve templates")

			r.Recorder.Event(
				rootPlc,
				"Warning",
				"PolicyPropagation",
				fmt.Sprintf(
					"Failed to resolve templates for cluster %s: %s",
					clusterName,
					tplErr.Error(),
				),
			)

			err := setTemplateError(policyT, tplErr)
			if err != nil {
				log.Error(err, "Failed to set the hub template error on the replicated policy")
			}

			// If the failure was due to a Kubernetes API error that could be recoverable, let's retry it.
			// Missing objects are handled by the templating library sending reconcile requests when they get created.
			if errors.Is(tplErr, templates.ErrMissingAPIResource) ||
				k8serrors.IsInternalError(tplErr) ||
				k8serrors.IsServiceUnavailable(tplErr) ||
				k8serrors.IsTimeout(tplErr) ||
				k8serrors.IsTooManyRequests(tplErr) ||
				errors.Is(tplErr, k8sdepwatches.ErrWatchStopping) {
				tplErr = fmt.Errorf("%w%w", ErrRetryable, tplErr)
			}

			return tplErr
		}

		policyT.ObjectDefinition.Raw = templateResult.ResolvedJSON

		// Set initialization vector annotation on the ObjectDefinition for the controller's use
		if usesEncryption {
			policyTObjectUnstructured := &unstructured.Unstructured{}

			jsonErr := json.Unmarshal(templateResult.ResolvedJSON, policyTObjectUnstructured)
			if jsonErr != nil {
				return fmt.Errorf("failed to unmarshal the object definition to JSON: %w", jsonErr)
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
					return fmt.Errorf("failed to marshal the policy template to JSON: %w", jsonErr)
				}

				policyT.ObjectDefinition.Raw = updatedPolicyT
			}
		}
	}

	return nil
}
