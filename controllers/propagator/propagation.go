// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"errors"
	"fmt"
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
	"sigs.k8s.io/controller-runtime/pkg/event"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

const (
	startDelim              = "{{hub"
	stopDelim               = "hub}}"
	TriggerUpdateAnnotation = "policy.open-cluster-management.io/trigger-update"
)

var (
	kubeConfig *rest.Config
	kubeClient *kubernetes.Interface
)

type Propagator struct {
	client.Client
	Scheme                  *runtime.Scheme
	Recorder                record.EventRecorder
	RootPolicyLocks         *sync.Map
	ReplicatedPolicyUpdates chan event.GenericEvent
}

func Initialize(kubeconfig *rest.Config, kubeclient *kubernetes.Interface) {
	kubeConfig = kubeconfig
	kubeClient = kubeclient
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

// clusterDecision contains a single decision where the replicated policy
// should be processed and any overrides to the root policy
type clusterDecision struct {
	Cluster         appsv1.PlacementDecision
	PolicyOverrides policiesv1.BindingOverrides
}

type decisionSet map[appsv1.PlacementDecision]bool

// getPolicyPlacementDecisions retrieves the placement decisions for a input PlacementBinding when
// the policy is bound within it. It can return an error if the PlacementBinding is invalid, or if
// a required lookup fails.
func (r *RootPolicyReconciler) getPolicyPlacementDecisions(
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

// getAllClusterDecisions calculates which managed clusters should have a replicated policy, and
// whether there are any BindingOverrides for that cluster. The placements array it returns is
// sorted by PlacementBinding name. It can return an error if the PlacementBinding is invalid, or if
// a required lookup fails.
func (r *RootPolicyReconciler) getAllClusterDecisions(
	instance *policiesv1.Policy, pbList *policiesv1.PlacementBindingList,
) (
	decisions map[appsv1.PlacementDecision]policiesv1.BindingOverrides, placements []*policiesv1.Placement, err error,
) {
	decisions = make(map[appsv1.PlacementDecision]policiesv1.BindingOverrides)

	// Process all placement bindings without subFilter
	for i, pb := range pbList.Items {
		if pb.SubFilter == policiesv1.Restricted {
			continue
		}

		plcDecisions, plcPlacements, err := r.getPolicyPlacementDecisions(instance, &pbList.Items[i])
		if err != nil {
			return nil, nil, err
		}

		if len(plcDecisions) == 0 {
			log.Info("No placement decisions to process for this policy from this binding",
				"policyName", instance.GetName(), "bindingName", pb.GetName())
		}

		for _, decision := range plcDecisions {
			if overrides, ok := decisions[decision]; ok {
				// Found cluster in the decision map
				if strings.EqualFold(pb.BindingOverrides.RemediationAction, string(policiesv1.Enforce)) {
					overrides.RemediationAction = strings.ToLower(string(policiesv1.Enforce))
					decisions[decision] = overrides
				}
			} else {
				// No found cluster in the decision map, add it to the map
				decisions[decision] = policiesv1.BindingOverrides{
					// empty string if pb.BindingOverrides.RemediationAction is not defined
					RemediationAction: strings.ToLower(pb.BindingOverrides.RemediationAction),
				}
			}
		}

		placements = append(placements, plcPlacements...)
	}

	if len(decisions) == 0 {
		sort.Slice(placements, func(i, j int) bool {
			return placements[i].PlacementBinding < placements[j].PlacementBinding
		})

		// No decisions, and subfilters can't add decisions, so we can stop early.
		return nil, placements, nil
	}

	// Process all placement bindings with subFilter:restricted
	for i, pb := range pbList.Items {
		if pb.SubFilter != policiesv1.Restricted {
			continue
		}

		foundInDecisions := false

		plcDecisions, plcPlacements, err := r.getPolicyPlacementDecisions(instance, &pbList.Items[i])
		if err != nil {
			return nil, nil, err
		}

		if len(plcDecisions) == 0 {
			log.Info("No placement decisions to process for this policy from this binding",
				"policyName", instance.GetName(), "bindingName", pb.GetName())
		}

		for _, decision := range plcDecisions {
			if overrides, ok := decisions[decision]; ok {
				// Found cluster in the decision map
				foundInDecisions = true

				if strings.EqualFold(pb.BindingOverrides.RemediationAction, string(policiesv1.Enforce)) {
					overrides.RemediationAction = strings.ToLower(string(policiesv1.Enforce))
					decisions[decision] = overrides
				}
			}
		}

		if foundInDecisions {
			placements = append(placements, plcPlacements...)
		}
	}

	sort.Slice(placements, func(i, j int) bool {
		return placements[i].PlacementBinding < placements[j].PlacementBinding
	})

	return decisions, placements, nil
}

// getDecisions identifies all managed clusters which should have a replicated policy
func (r *RootPolicyReconciler) getDecisions(
	instance *policiesv1.Policy,
) (
	[]*policiesv1.Placement, decisionSet, error,
) {
	log := log.WithValues("policyName", instance.GetName(), "policyNamespace", instance.GetNamespace())
	decisions := make(map[appsv1.PlacementDecision]bool)

	pbList := &policiesv1.PlacementBindingList{}

	err := r.List(context.TODO(), pbList, &client.ListOptions{Namespace: instance.GetNamespace()})
	if err != nil {
		log.Error(err, "Could not list the placement bindings")

		return nil, decisions, err
	}

	allClusterDecisions, placements, err := r.getAllClusterDecisions(instance, pbList)
	if err != nil {
		return placements, decisions, err
	}

	if allClusterDecisions == nil {
		allClusterDecisions = make(map[appsv1.PlacementDecision]policiesv1.BindingOverrides)
	}

	for dec := range allClusterDecisions {
		decisions[dec] = true
	}

	return placements, decisions, nil
}

// cleanUpOrphanedRplPolicies compares the status of the input policy against the input placement
// decisions. If the cluster exists in the status but doesn't exist in the input placement
// decisions, then it's considered stale and an event is sent to the replicated policy reconciler
// so the policy will be removed.
func (r *RootPolicyReconciler) cleanUpOrphanedRplPolicies(
	instance *policiesv1.Policy, originalCPCS []*policiesv1.CompliancePerClusterStatus, allDecisions decisionSet,
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
func (r *RootPolicyReconciler) handleRootPolicy(instance *policiesv1.Policy) error {
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

		updateCount, err := r.updateExistingReplicas(context.TODO(), instance.Namespace+"."+instance.Name)
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

	placements, decisions, err := r.getDecisions(instance)
	if err != nil {
		log.Info("Failed to get any placement decisions. Giving up on the request.")

		return errors.New("could not get the placement decisions")
	}

	log.V(1).Info("Updating the root policy status")

	cpcs, cpcsErr := r.calculatePerClusterStatus(instance, decisions)
	if cpcsErr != nil {
		// If there is a new replicated policy, then its lookup is expected to fail - it hasn't been created yet.
		log.Error(cpcsErr, "Failed to get at least one replicated policy, but that may be expected. Ignoring.")
	}

	err = r.Get(context.TODO(), types.NamespacedName{Namespace: instance.Namespace, Name: instance.Name}, instance)
	if err != nil {
		log.Error(err, "Failed to refresh the cached policy. Will use existing policy.")
	}

	// make a copy of the original status
	originalCPCS := make([]*policiesv1.CompliancePerClusterStatus, len(instance.Status.Status))
	copy(originalCPCS, instance.Status.Status)

	instance.Status.Status = cpcs
	instance.Status.ComplianceState = CalculateRootCompliance(cpcs)
	instance.Status.Placement = placements

	err = r.Status().Update(context.TODO(), instance)
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
func (r *ReplicatedPolicyReconciler) processTemplates(
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
