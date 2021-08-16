package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PlacementDecisionSpec defines the desired state of PlacementDecision
type PlacementDecisionSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
}

// Decision defines the decision made by a placement
type Decision struct {
	ClusterName string `json:"clusterName,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// PlacementDecisionStatus defines the observed state of PlacementDecision
type PlacementDecisionStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	Decisions []Decision `json:"decisions,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PlacementDecision is the Schema for the placementdecisions API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=placementdecisions,scope=Namespaced
type PlacementDecision struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PlacementDecisionSpec   `json:"spec,omitempty"`
	Status PlacementDecisionStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PlacementDecisionList contains a list of PlacementDecision
type PlacementDecisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PlacementDecision `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PlacementDecision{}, &PlacementDecisionList{})
}
