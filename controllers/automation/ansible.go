// Copyright Contributors to the Open Cluster Management project

package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	policyv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

const (
	PolicyAutomationLabel      string = "policy.open-cluster-management.io/policyautomation-name"
	PolicyAutomationGeneration string = "policy.open-cluster-management.io/policyautomation-generation"
	// policyautomation-ResouceVersion
	PolicyAutomationResouceV string = "policy.open-cluster-management.io/policyautomation-resource-version"
)

var ansibleJobRes = schema.GroupVersionResource{
	Group: "tower.ansible.com", Version: "v1alpha1",
	Resource: "ansiblejobs",
}

// Check any ansiblejob is made by input genteration number
// Returning "true" means the policy automation already created ansiblejob with the generation
func MatchPAGeneration(ctx context.Context, log logr.Logger, policyAutomation *policyv1beta1.PolicyAutomation,
	dynamicClient dynamic.Interface, generation int64,
) (bool, error) {
	ansiblejobList, err := dynamicClient.Resource(ansibleJobRes).Namespace(policyAutomation.GetNamespace()).List(
		ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", PolicyAutomationLabel, policyAutomation.GetName()),
		},
	)
	if err != nil {
		log.Error(err, "Failed to get ansiblejob list")

		return false, err
	}

	s := strconv.FormatInt(generation, 10)

	for _, aj := range ansiblejobList.Items {
		annotations := aj.GetAnnotations()
		if annotations[PolicyAutomationGeneration] == s {
			return true, nil
		}
	}

	return false, nil
}

// Check any ansiblejob is made by current resourceVersion number
// Returning "true" means the policy automation already created ansiblejob with this resourceVersion
func MatchPAResouceV(ctx context.Context, log logr.Logger, policyAutomation *policyv1beta1.PolicyAutomation,
	dynamicClient dynamic.Interface, resourceVersion string,
) (bool, error) {
	ansiblejobList, err := dynamicClient.Resource(ansibleJobRes).Namespace(policyAutomation.GetNamespace()).List(
		ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", PolicyAutomationLabel, policyAutomation.GetName()),
		},
	)
	if err != nil {
		log.Error(err, "Failed to get ansiblejob list")

		return false, err
	}

	for _, aj := range ansiblejobList.Items {
		annotations := aj.GetAnnotations()
		if annotations[PolicyAutomationResouceV] == resourceVersion {
			return true, nil
		}
	}

	return false, nil
}

// CreateAnsibleJob creates ansiblejob with given PolicyAutomation
func CreateAnsibleJob(ctx context.Context, log logr.Logger, policyAutomation *policyv1beta1.PolicyAutomation,
	dynamicClient dynamic.Interface, mode string, violationContext policyv1beta1.ViolationContext,
) error {
	ansibleJob := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "tower.ansible.com/v1alpha1",
			"kind":       "AnsibleJob",
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					PolicyAutomationGeneration: strconv.
						FormatInt(policyAutomation.GetGeneration(), 10),
					PolicyAutomationResouceV: policyAutomation.GetResourceVersion(),
				},
			},
			"spec": map[string]interface{}{
				"job_template_name": policyAutomation.Spec.Automation.Name,
				"tower_auth_secret": policyAutomation.Spec.Automation.TowerSecret,
				"extra_vars":        map[string]interface{}{},
				"job_ttl":           86400, // default TTL is 24 hours
			},
		},
	}

	mapExtraVars := map[string]interface{}{}
	if policyAutomation.Spec.Automation.ExtraVars != nil {
		// This is to translate the runtime.RawExtension to a map[string]interface{}
		err := json.Unmarshal(policyAutomation.Spec.Automation.ExtraVars.Raw, &mapExtraVars)
		if err != nil {
			return err
		}
	}

	values := reflect.ValueOf(violationContext)
	typesOf := values.Type()
	// add every violationContext fields into mapExtraVars as well as the empty values,
	// or when the whole violationContext is empty
	for i := range values.NumField() {
		tag := typesOf.Field(i).Tag
		value := values.Field(i).Interface()

		var fieldName string

		switch {
		case tag.Get("ansibleJob") != "":
			fieldName = tag.Get("ansibleJob")
		case tag.Get("json") != "":
			fieldName = strings.SplitN(tag.Get("json"), ",", 2)[0]
		default:
			fieldName = typesOf.Field(i).Name
		}

		mapExtraVars[fieldName] = value
	}

	label := map[string]string{
		PolicyAutomationLabel: policyAutomation.GetName(),
	}
	ansibleJob.SetLabels(label)

	ansibleJob.Object["spec"].(map[string]interface{})["extra_vars"] = mapExtraVars

	if policyAutomation.Spec.Automation.JobTTL != nil {
		ansibleJob.Object["spec"].(map[string]interface{})["job_ttl"] = *policyAutomation.Spec.Automation.JobTTL
	}

	ansibleJobRes := schema.GroupVersionResource{
		Group: "tower.ansible.com", Version: "v1alpha1",
		Resource: "ansiblejobs",
	}

	// Replace all special characters with hyphens. The ansible tower API requires this.
	nonAlphaNumerics := regexp.MustCompile("[^a-zA-Z0-9]")
	cleanName := nonAlphaNumerics.ReplaceAllString(policyAutomation.GetName(), "-")

	// Ansible tower API requires the lower case naming
	ansibleJob.SetGenerateName(strings.ToLower(cleanName + "-" + mode + "-"))
	ansibleJob.SetOwnerReferences([]metav1.OwnerReference{
		*metav1.NewControllerRef(policyAutomation, policyAutomation.GroupVersionKind()),
	})

	_, err := dynamicClient.Resource(ansibleJobRes).Namespace(policyAutomation.GetNamespace()).
		Create(ctx, ansibleJob, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}
