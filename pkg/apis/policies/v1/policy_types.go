package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// Kind Policy
const Kind = "Policy"

// RemediationAction describes weather to enforce or inform
type RemediationAction string

const (
	// Enforce is an remediationAction to make changes
	Enforce RemediationAction = "Enforce"

	// Inform is an remediationAction to only inform
	Inform RemediationAction = "Inform"
)

//PolicyTemplate template for custom security policy
type PolicyTemplate struct {
	ObjectDefinition runtime.RawExtension `json:"objectDefinition,omitempty"`
	//Status shows the individual status of each template within a policy
	Status TemplateStatus `json:"status,omitempty" protobuf:"bytes,12,rep,name=status"`
}

//TemplateStatus hold the status result
type TemplateStatus struct {
	ComplianceState ComplianceState `json:"Compliant,omitempty"` // Compliant, NonCompliant
	Conditions      []Condition     `json:"conditions,omitempty"`
}

// ComplianceState shows the state of enforcement
type ComplianceState string

// Condition is the base struct for representing resource conditions
type Condition struct {
	// Type of condition, e.g Complete or Failed.
	Type string `json:"type"`
	// Status of the condition, one of True, False, Unknown.
	Status corev1.ConditionStatus `json:"status,omitempty" protobuf:"bytes,12,rep,name=status"`
	// The last time the condition transitioned from one status to another.
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty" protobuf:"bytes,3,opt,name=lastTransitionTime"`
	// The reason for the condition's last transition.
	Reason string `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`
	// A human readable message indicating details about the transition.
	Message string `json:"message,omitempty" protobuf:"bytes,5,opt,name=message"`
}

const (
	// Compliant is an ComplianceState
	Compliant ComplianceState = "Compliant"

	// NonCompliant is an ComplianceState
	NonCompliant ComplianceState = "NonCompliant"
)

// PolicySpec defines the desired state of Policy
type PolicySpec struct {
	Disabled          bool              `json:"disabled"`
	RemediationAction RemediationAction `json:"remediationAction,omitempty"` //enforce, inform
	PolicyTemplates   []*PolicyTemplate `json:"policy-templates,omitempty"`
}

// Placement defines the placement results
type Placement struct {
	PlacementBinding string `json:"placementBinding,omitempty"`
	PlacementRule    string `json:"placementRule,omitempty"`
}

// CompliancePerClusterStatus defines compliance per cluster status
type CompliancePerClusterStatus struct {
	ComplianceState  ComplianceState `json:"compliant,omitempty"`
	ClusterName      string          `json:"clustername,omitempty"`
	ClusterNamespace string          `json:"clusternamespace,omitempty"`
}

// PolicyStatus defines the observed state of Policy
type PolicyStatus struct {
	Placement []*Placement                  `json:"placement,omitempty"`
	Status    []*CompliancePerClusterStatus `json:"status,omitempty"`
	// +kubebuilder:validation:Enum=Compliant;NonCompliant
	ComplianceState ComplianceState `json:"compliant,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Policy is the Schema for the policies API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=policies,scope=Namespaced
type Policy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PolicySpec   `json:"spec,omitempty"`
	Status PolicyStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PolicyList contains a list of Policy
type PolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Policy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Policy{}, &PolicyList{})
}
