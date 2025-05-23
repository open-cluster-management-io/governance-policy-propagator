// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	// log is for logging in this package.
	policylog = logf.Log.WithName("policy-validating-webhook")
	errName   = errors.New("the combined length of the policy namespace and name " +
		"cannot exceed 62 characters")
	errRemediation = errors.New("RemediationAction field of the policy and " +
		"policy template cannot both be unset")
)

func (r *Policy) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(&PolicyCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-policy-open-cluster-management-io-v1-policy,mutating=false,failurePolicy=Ignore,sideEffects=None,groups=policy.open-cluster-management.io,resources=policies,verbs=create,versions=v1,name=policy.open-cluster-management.io.webhook,admissionReviewVersions=v1
// +kubebuilder:object:generate=false
type PolicyCustomValidator struct{}

var _ webhook.CustomValidator = &PolicyCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type
func (r *PolicyCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	policy, ok := obj.(*Policy)
	if !ok {
		return nil, fmt.Errorf("expected a Policy object but got %T", obj)
	}

	log := policylog.WithValues("policyName", policy.Name, "policyNamespace", policy.Namespace)
	log.V(1).Info("Validate policy creation request")

	err := policy.validateName()
	if err != nil {
		return nil, err
	}

	err = policy.validateRemediationAction()
	if err != nil {
		return nil, err
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type
func (r *PolicyCustomValidator) ValidateUpdate(
	_ context.Context, _, newObj runtime.Object,
) (admission.Warnings, error) {
	policy, ok := newObj.(*Policy)
	if !ok {
		return nil, fmt.Errorf("expected a Policy object but got %T", newObj)
	}

	log := policylog.WithValues("policyName", policy.Name, "policyNamespace", policy.Namespace)
	log.V(1).Info("Validate policy update request")

	err := policy.validateRemediationAction()
	if err != nil {
		return nil, err
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type
func (r *PolicyCustomValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validate the policy name and namespace length
func (r *Policy) validateName() error {
	log := policylog.WithValues("policyName", r.Name, "policyNamespace", r.Namespace)
	log.V(1).Info("Validating the policy name through a validating webhook")

	// replicated policies don't need pass this validation
	if _, ok := r.GetLabels()["policy.open-cluster-management.io/root-policy"]; ok {
		return nil
	}

	// 1 character for "."
	if (utf8.RuneCountInString(r.Name) + utf8.RuneCountInString(r.Namespace)) > 62 {
		log.Info("Invalid policy name/namespace: " + errName.Error())

		return errName
	}

	return nil
}

// validate the remediationAction field of the root policy and its policy templates
func (r *Policy) validateRemediationAction() error {
	log := policylog.WithValues("policyName", r.Name, "policyNamespace", r.Namespace)
	log.V(1).Info("Validating the Policy and ConfigurationPolicy remediationAction through a validating webhook")

	if r.Spec.RemediationAction != "" {
		return nil
	}

	plcTemplates := r.Spec.PolicyTemplates

	for _, obj := range plcTemplates {
		objUnstruct := &unstructured.Unstructured{}
		_ = json.Unmarshal(obj.ObjectDefinition.Raw, objUnstruct)

		if objUnstruct.GroupVersionKind().Kind == "ConfigurationPolicy" {
			_, found, _ := unstructured.NestedString(objUnstruct.Object, "spec", "remediationAction")
			if !found {
				log.Info("Invalid remediationAction configuration: " + errRemediation.Error())

				return errRemediation
			}
		}
	}

	return nil
}
