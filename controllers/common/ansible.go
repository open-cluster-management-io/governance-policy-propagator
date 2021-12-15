// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	policyv1beta1 "github.com/open-cluster-management/governance-policy-propagator/api/v1beta1"
)

// CreateAnsibleJob creates ansiblejob with given PolicyAutomation
func CreateAnsibleJob(policyAutomation *policyv1beta1.PolicyAutomation,
	dynamicClient dynamic.Interface, mode string, targetClusters []string) error {
	ansibleJob := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "tower.ansible.com/v1alpha1",
			"kind":       "AnsibleJob",
			"spec": map[string]interface{}{
				"job_template_name": policyAutomation.Spec.Automation.Name,
				"tower_auth_secret": policyAutomation.Spec.Automation.TowerSecret,
				"extra_vars":        map[string]interface{}{},
			},
		},
	}

	if policyAutomation.Spec.Automation.ExtraVars != nil {
		// This is to translate the runtime.RawExtension to a map[string]interface{}
		mapExtraVars := map[string]interface{}{}

		err := json.Unmarshal(policyAutomation.Spec.Automation.ExtraVars.Raw, &mapExtraVars)
		if err != nil {
			return err
		}

		ansibleJob.Object["spec"].(map[string]interface{})["extra_vars"] = mapExtraVars
	}

	if targetClusters != nil {
		// nolint: forcetypeassert
		extravars := ansibleJob.Object["spec"].(map[string]interface{})["extra_vars"].(map[string]interface{})
		extravars["target_clusters"] = targetClusters
	}

	ansibleJobRes := schema.GroupVersionResource{
		Group: "tower.ansible.com", Version: "v1alpha1",
		Resource: "ansiblejobs",
	}

	ansibleJob.SetGenerateName(policyAutomation.GetName() + "-" + mode + "-")
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
