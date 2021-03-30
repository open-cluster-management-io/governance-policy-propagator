package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// PolicyAutomationSpec defines the desired state of PolicyAutomation
type PolicyAutomationSpec struct {
	// PolicyRef is the name of the policy automation is going to binding with.
	// +kubebuilder:validation:Required
	PolicyRef string `json:"policyRef"`
	// Mode decides how automation is going to be triggered
	// +kubebuilder:validation:Enum={once,disabled}
	// +kubebuilder:validation:Required
	Mode string `json:"mode"`
	// EventHook decides when automation is going to be triggered
	// +kubebuilder:validation:Enum={noncompliant}
	// +kubebuilder:validation:Required
	EventHook   string `json:"eventHook,omitempty"`
	RescanAfter string `json:"rescanAfter,omitempty"`
	// +kubebuilder:validation:Required
	Automation AutomationDef `json:"automationDef"`
}

// AutomationDef defines the automation to invoke
type AutomationDef struct {
	// Type of the automation to invoke
	Type string `json:"type,omitempty"`
	// Name of the Ansible Template to run in Tower as a job
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// ExtraVars is passed to the Ansible job at execution time and is a known Ansible entity.
	// +kubebuilder:pruning:PreserveUnknownFields
	ExtraVars *runtime.RawExtension `json:"extra_vars,omitempty"`
	// +kubebuilder:validation:Required
	TowerSecret string `json:"secret"`
}

// PolicyAutomationStatus defines the observed state of PolicyAutomation
type PolicyAutomationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PolicyAutomation is the Schema for the policyautomations API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=policyautomations,scope=Namespaced
// +kubebuilder:resource:path=policyautomations,shortName=plca
type PolicyAutomation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PolicyAutomationSpec   `json:"spec,omitempty"`
	Status PolicyAutomationStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PolicyAutomationList contains a list of PolicyAutomation
type PolicyAutomationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PolicyAutomation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolicyAutomation{}, &PolicyAutomationList{})
}
