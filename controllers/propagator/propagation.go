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

	templates "github.com/stolostron/go-template-utils/v4/pkg/templates"
	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/tools/record"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
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

var ErrRetryable = errors.New("")

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
	Cluster         appsv1.PlacementDecision
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
		key := appsv1.PlacementDecision{
			ClusterName:      cluster.ClusterNamespace,
			ClusterNamespace: cluster.ClusterNamespace,
		}
		if allDecisions[key] {
			continue
		}

		// not found in allDecisions, orphan, send an event for it to delete itself
		simpleObj := &GuttedObject{
			TypeMeta: metav1.TypeMeta{
				Kind:       policiesv1.Kind,
				APIVersion: policiesv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      common.FullNameForPolicy(instance),
				Namespace: cluster.ClusterNamespace,
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
		simpleObj := &GuttedObject{
			TypeMeta: metav1.TypeMeta{
				Kind:       policiesv1.Kind,
				APIVersion: policiesv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      common.FullNameForPolicy(instance),
				Namespace: decision.ClusterNamespace,
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

// a helper to quickly check if there are any templates in any of the policy templates
func policyHasTemplates(instance *policiesv1.Policy) bool {
	for _, policyT := range instance.Spec.PolicyTemplates {
		if templates.HasTemplate(policyT.ObjectDefinition.Raw, TemplateStartDelim, false) {
			return true
		}
	}

	return false
}

type templateCtx struct {
	ManagedClusterName   string
	ManagedClusterLabels map[string]string
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
func (r *ReplicatedPolicyReconciler) processTemplates(
	ctx context.Context,
	replicatedPlc *policiesv1.Policy, decision appsv1.PlacementDecision, rootPlc *policiesv1.Policy,
) error {
	log := log.WithValues(
		"policyName", rootPlc.GetName(),
		"policyNamespace", rootPlc.GetNamespace(),
		"cluster", decision.ClusterName,
	)
	log.V(1).Info("Processing templates")

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

	plcGVK := replicatedPlc.GroupVersionKind()

	templateResolverOptions := templates.ResolveOptions{
		ClusterScopedAllowList: []templates.ClusterScopedObjectIdentifier{
			{
				Group: "cluster.open-cluster-management.io",
				Kind:  "ManagedCluster",
				Name:  decision.ClusterName,
			},
		},
		DisableAutoCacheCleanUp: true,
		LookupNamespace:         rootPlc.GetNamespace(),
		Watcher: &k8sdepwatches.ObjectIdentifier{
			Group:     plcGVK.Group,
			Version:   plcGVK.Version,
			Kind:      plcGVK.Kind,
			Namespace: replicatedPlc.GetNamespace(),
			Name:      replicatedPlc.GetName(),
		},
	}

	var templateResult templates.TemplateResult

	// A policy can have multiple policy templates within it, iterate and process each
	for _, policyT := range replicatedPlc.Spec.PolicyTemplates {
		if !templates.HasTemplate(policyT.ObjectDefinition.Raw, TemplateStartDelim, false) {
			continue
		}

		if !isConfigurationPolicy(policyT) {
			// has Templates but not a configuration policy
			err := k8serrors.NewBadRequest("Templates are restricted to only Configuration Policies")
			log.Error(err, "Not a Configuration Policy")

			r.Recorder.Event(rootPlc, "Warning", "PolicyPropagation",
				fmt.Sprintf(
					"Policy %s/%s has templates but it is not a ConfigurationPolicy.",
					rootPlc.GetName(),
					rootPlc.GetNamespace(),
				),
			)

			return err
		}

		log.V(1).Info("Found an object definition with templates")

		templateContext := templateCtx{ManagedClusterName: decision.ClusterName}

		if strings.Contains(string(policyT.ObjectDefinition.Raw), "ManagedClusterLabels") {
			templateResolverOptions.ContextTransformers = append(
				templateResolverOptions.ContextTransformers, addManagedClusterLabels(decision.ClusterName),
			)
		}

		// Handle value encryption initialization
		usesEncryption := templates.UsesEncryption(policyT.ObjectDefinition.Raw, TemplateStartDelim, TemplateStopDelim)
		// Initialize AES Key and initialization vector
		if usesEncryption && !templateResolverOptions.EncryptionEnabled {
			log.V(1).Info("Found an object definition requiring encryption. Handling encryption keys.")
			// Get/generate the encryption key
			encryptionKey, err := r.getEncryptionKey(ctx, decision.ClusterName)
			if err != nil {
				log.Error(err, "Failed to get/generate the policy encryption key")

				return fmt.Errorf("%w%w", ErrRetryable, err)
			}

			// Get/generate the initialization vector
			initializationVector, err := r.getInitializationVector(
				rootPlc.GetName(), decision.ClusterName, annotations,
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

		var tplErr error

		templateResult, tplErr = r.TemplateResolver.ResolveTemplate(
			policyT.ObjectDefinition.Raw, templateContext, &templateResolverOptions,
		)

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

			// If the failure was due to a Kubernetes API error that could be recoverable, let's retry it.
			// Missing objects are handled by the templating library sending reconcile requests when they get created.
			if errors.Is(tplErr, templates.ErrMissingAPIResource) ||
				k8serrors.IsInternalError(tplErr) ||
				k8serrors.IsServiceUnavailable(tplErr) ||
				k8serrors.IsTimeout(tplErr) ||
				k8serrors.IsTooManyRequests(tplErr) {
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

	if templateResult.CacheCleanUp != nil {
		err := templateResult.CacheCleanUp()
		if err != nil {
			return fmt.Errorf("%w%w", ErrRetryable, err)
		}
	}

	log.V(1).Info("Successfully processed templates")

	return nil
}

func isConfigurationPolicy(policyT *policiesv1.PolicyTemplate) bool {
	// check if it is a configuration policy first
	var jsonDef map[string]interface{}
	_ = json.Unmarshal(policyT.ObjectDefinition.Raw, &jsonDef)

	return jsonDef != nil && jsonDef["kind"] == "ConfigurationPolicy"
}
