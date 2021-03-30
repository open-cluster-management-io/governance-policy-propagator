// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"
	"encoding/json"

	policyv1alpha1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// CreateAnsibleJob creates ansiblejob with given PolicyAutomation
func CreateAnsibleJob(policyAutomation *policyv1alpha1.PolicyAutomation,
	dyamicClient dynamic.Interface, mode string, targetCluster []string) error {
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
	if targetCluster != nil {
		ansibleJob.Object["spec"].(map[string]interface{})["extra_vars"].(map[string]interface{})["target_clusters"] = targetCluster
	}

	ansibleJobRes := schema.GroupVersionResource{Group: "tower.ansible.com", Version: "v1alpha1",
		Resource: "ansiblejobs"}
	ansibleJob.SetGenerateName(policyAutomation.GetName() + "-" + mode + "-")
	ansibleJob.SetOwnerReferences([]metav1.OwnerReference{
		*metav1.NewControllerRef(policyAutomation, policyAutomation.GroupVersionKind()),
	})
	_, err := dyamicClient.Resource(ansibleJobRes).Namespace(policyAutomation.GetNamespace()).
		Create(context.TODO(), ansibleJob, v1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}
