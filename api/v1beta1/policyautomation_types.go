// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package v1beta1

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
	Mode PolicyAutomationMode `json:"mode"`
	// EventHook decides when automation is going to be triggered
	// +kubebuilder:validation:Enum={noncompliant}
	// +kubebuilder:validation:Required
	EventHook   string `json:"eventHook,omitempty"`
	RescanAfter string `json:"rescanAfter,omitempty"`
	// +kubebuilder:validation:Minimum=0
	DelayAfterRunSeconds uint `json:"delayAfterRunSeconds,omitempty"`
	// +kubebuilder:validation:Required
	Automation AutomationDef `json:"automationDef"`
}

// +kubebuilder:validation:Enum={once,everyEvent,disabled}
// +kubebuilder:validation:Required
type PolicyAutomationMode string

const (
	Once       PolicyAutomationMode = "once"
	EveryEvent PolicyAutomationMode = "everyEvent"
	Disabled   PolicyAutomationMode = "disabled"
)

// AutomationDef defines the automation to invoke
type AutomationDef struct {
	// Type of the automation to invoke
	Type string `json:"type,omitempty"`
	// Name of the Ansible Template to run in Tower as a job
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// ExtraVars is passed to the Ansible job at execution time and is a known Ansible entity.
	// +kubebuilder:pruning:PreserveUnknownFields
	ExtraVars *runtime.RawExtension `json:"extra_vars,omitempty"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	TowerSecret string `json:"secret"`
	// JobTTL sets the time to live for the Kubernetes AnsibleJob object after the Ansible job run has finished.
	JobTTL *int `json:"jobTtl,omitempty"`
}

// PolicyAutomationStatus defines the observed state of PolicyAutomation
type PolicyAutomationStatus struct {
	// Cluster name as the key of ClustersWithEvent
	ClustersWithEvent map[string]ClusterEvent `json:"clustersWithEvent,omitempty"`
}

//+kubebuilder:object:root=true

// PolicyAutomation is the Schema for the policyautomations API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=policyautomations,scope=Namespaced
// +kubebuilder:resource:path=policyautomations,shortName=plca
type PolicyAutomation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Required
	Spec   PolicyAutomationSpec   `json:"spec"`
	Status PolicyAutomationStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PolicyAutomationList contains a list of PolicyAutomation
type PolicyAutomationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PolicyAutomation `json:"items"`
}

// PolicyAutomation events on each target cluster
type ClusterEvent struct {
	// Policy automation start time for everyEvent mode
	AutomationStartTime string `json:"automationStartTime"`
	// The last policy compliance transition event time
	EventTime string `json:"eventTime"`
}

func init() {
	SchemeBuilder.Register(&PolicyAutomation{}, &PolicyAutomationList{})
}
