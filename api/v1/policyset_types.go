// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A custom type is required since there is no way to have a kubebuilder marker
// apply to the items of a slice.

// +kubebuilder:validation:MinLength=1
type NonEmptyString string

// PolicySetSpec defines the desired state of PolicySet
type PolicySetSpec struct {
	Description string `json:"description,omitempty"`
	// +kubebuilder:validation:Required
	Policies []NonEmptyString `json:"policies"`
}

// PolicySetStatus defines the observed state of PolicySet
type PolicySetStatus struct {
	Placement []PolicySetStatusPlacement `json:"placement,omitempty"`
	Compliant string                     `json:"compliant,omitempty"`
	Results   []PolicySetStatusResult    `json:"results,omitempty"`
}

// PolicySetStatusPlacement defines a placement object for the status
type PolicySetStatusPlacement struct {
	PlacementBinding   string   `json:"placementBinding,omitempty"`
	Placement          string   `json:"placement,omitempty"`
	PlacementDecisions []string `json:"placementDecisions,omitempty"`
}

// PolicySetResultCluster shows the compliance status of a policy for a specific cluster
type PolicySetResultCluster struct {
	ClusterName      string `json:"clusterName,omitempty"`
	ClusterNamespace string `json:"clusterNamespace,omitempty"`
	Compliant        string `json:"compliant,omitempty"`
}

// PolicySetStatusResult shows the compliance status of a policy in the set
type PolicySetStatusResult struct {
	Policy    string                   `json:"policy,omitempty"`
	Compliant string                   `json:"compliant,omitempty"`
	Clusters  []PolicySetResultCluster `json:"clusters,omitempty"`
	Message   string                   `json:"message,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
// +kubebuilder:resource:path=policysets,scope=Namespaced
// PolicySet is the Schema for the policysets API
type PolicySet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Required
	Spec   PolicySetSpec   `json:"spec"`
	Status PolicySetStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PolicySetList contains a list of PolicySet
type PolicySetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PolicySet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolicySet{}, &PolicySetList{})
}
