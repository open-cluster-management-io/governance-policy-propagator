// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	policyv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

// CreateAnsibleJob creates ansiblejob with given PolicyAutomation
func CreateAnsibleJob(policyAutomation *policyv1beta1.PolicyAutomation,
	dynamicClient dynamic.Interface, mode string, violationContext policyv1beta1.ViolationContext,
) error {
	ansibleJob := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "tower.ansible.com/v1alpha1",
			"kind":       "AnsibleJob",
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
	for i := 0; i < values.NumField(); i++ {
		tag := typesOf.Field(i).Tag
		value := values.Field(i).Interface()

		var fieldName string
		if tag.Get("ansibleJob") != "" {
			fieldName = tag.Get("ansibleJob")
		} else if tag.Get("json") != "" {
			fieldName = strings.SplitN(tag.Get("json"), ",", 2)[0]
		} else {
			fieldName = typesOf.Field(i).Name
		}

		mapExtraVars[fieldName] = value
	}

	ansibleJob.Object["spec"].(map[string]interface{})["extra_vars"] = mapExtraVars

	if policyAutomation.Spec.Automation.JobTTL != nil {
		ansibleJob.Object["spec"].(map[string]interface{})["job_ttl"] = *policyAutomation.Spec.Automation.JobTTL
	}

	ansibleJobRes := schema.GroupVersionResource{
		Group: "tower.ansible.com", Version: "v1alpha1",
		Resource: "ansiblejobs",
	}

	// Ansible tower API requires the lower case naming
	ansibleJob.SetGenerateName(strings.ToLower(policyAutomation.GetName() + "-" + mode + "-"))
	ansibleJob.SetOwnerReferences([]metav1.OwnerReference{
		*metav1.NewControllerRef(policyAutomation, policyAutomation.GroupVersionKind()),
	})

	_, err := dynamicClient.Resource(ansibleJobRes).Namespace(policyAutomation.GetNamespace()).
		Create(context.TODO(), ansibleJob, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}
