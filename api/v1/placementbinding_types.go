// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Subject defines the resource to bind to the placement resource.
type Subject struct {
	// APIGroup is the API group to which the kind belongs. Must be set to
	// "policy.open-cluster-management.io".
	//
	// +kubebuilder:validation:Enum=policy.open-cluster-management.io
	// +kubebuilder:validation:MinLength=1
	APIGroup string `json:"apiGroup"`
	// Kind is the kind of the object to bind to the placement. Must be set to either "Policy" or
	// "PolicySet".
	//
	// +kubebuilder:validation:Enum=Policy;PolicySet
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`
	// Name is the name of the policy or policy set to bind to the placement resource.
	//
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// PlacementSubject defines the placement resource that is being bound to the subjects defined in
// the binding.
type PlacementSubject struct {
	// APIGroup is the API group to which the kind belongs. Must be set to
	// "cluster.open-cluster-management.io" for Placement or (deprecated)
	// "apps.open-cluster-management.io" for PlacementRule.
	//
	// +kubebuilder:validation:Enum=apps.open-cluster-management.io;cluster.open-cluster-management.io
	// +kubebuilder:validation:MinLength=1
	APIGroup string `json:"apiGroup"`
	// Kind is the kind of the placement resource. Must be set to either "Placement" or (deprecated)
	// "PlacementRule".
	//
	// +kubebuilder:validation:Enum=PlacementRule;Placement
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`
	// Name is the name of the placement resource being bound.
	//
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// BindingOverrides defines the overrides for the Subjects.
type BindingOverrides struct {
	// RemediationAction overrides the policy remediationAction on target clusters. Optional, but if
	// set it must be set to "enforce".
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Enforce;enforce
	RemediationAction string `json:"remediationAction,omitempty"`
}

// SubFilter defines the selection rule for bound clusters.
type SubFilter string

const (
	Restricted SubFilter = "restricted"
)

// PlacementBindingStatus defines the observed state of the placement binding.
type PlacementBindingStatus struct{}

// PlacementBinding is the Schema for the placementbindings API. A placement binding binds a managed
// cluster placement resource to a policy or policy set, along with configurable overrides.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=placementbindings,scope=Namespaced
// +kubebuilder:resource:path=placementbindings,shortName=pb
type PlacementBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Optional
	BindingOverrides BindingOverrides `json:"bindingOverrides,omitempty"`
	// SubFilter provides the ability to select a subset of bound clusters. Optional, but if set it
	// must be set to "restricted".
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=restricted
	SubFilter    SubFilter        `json:"subFilter,omitempty"`
	PlacementRef PlacementSubject `json:"placementRef"`
	// +kubebuilder:validation:MinItems=1
	Subjects []Subject              `json:"subjects"`
	Status   PlacementBindingStatus `json:"status,omitempty"`
}

// PlacementBindingList contains a list of placement bindings.
//
// +kubebuilder:object:root=true
type PlacementBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PlacementBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PlacementBinding{}, &PlacementBindingList{})
}
