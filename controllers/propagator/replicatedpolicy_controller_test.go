package propagator

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/complianceeventsapi"
)

func getPolicyTemplateAnnotations(policy *policiesv1.Policy, templateIndex int) (map[string]string, error) {
	plcTmplUnstruct := &unstructured.Unstructured{}

	err := plcTmplUnstruct.UnmarshalJSON(policy.Spec.PolicyTemplates[templateIndex].ObjectDefinition.Raw)
	if err != nil {
		return nil, err
	}

	return plcTmplUnstruct.GetAnnotations(), nil
}

func TestSetDBAnnotationsNoDB(t *testing.T) {
	complianceAPICtx, err := complianceeventsapi.NewComplianceServerCtx("postgres://localhost?mydb")
	if err != nil {
		t.Fatalf("Failed create the compliance server context: %v", err)
	}

	// The unit tests shouldn't use the database, so that part of the code can't be covered here.
	complianceAPICtx.DB = nil

	reconciler := ReplicatedPolicyReconciler{
		ComplianceServerCtx: complianceAPICtx,
	}

	// Test no cache entry, no existing annotation on the replicated policy, and no database connection
	rootPolicy := &policiesv1.Policy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-policy",
			Namespace: "policies",
			Annotations: map[string]string{
				"policy.open-cluster-management.io/categories": "category1",
				"policy.open-cluster-management.io/controls":   "controls1, controls2",
				"policy.open-cluster-management.io/standards":  "standard1",
			},
		},
		Spec: policiesv1.PolicySpec{
			PolicyTemplates: []*policiesv1.PolicyTemplate{
				{
					ObjectDefinition: runtime.RawExtension{
						Raw: []byte(`{
							"apiVersion": "policy.open-cluster-management.io",
							"kind": "ConfigurationPolicy",
							"metadata": {
								"name": "my-config",
								"annotations": {}
							},
							"spec": {
								"severity": "critical",
								"option1": "option2"
							}
						}`),
					},
				},
			},
		},
	}

	replicatedPolicy := rootPolicy.DeepCopy()

	existingReplicatedPolicy := replicatedPolicy.DeepCopy()

	reconciler.setDBAnnotations(context.TODO(), rootPolicy, replicatedPolicy, existingReplicatedPolicy)

	annotations := rootPolicy.GetAnnotations()
	if annotations[ParentPolicyIDAnnotation] != "" {
		t.Fatalf("Expected no parent policy annotation but got: %s", annotations[ParentPolicyIDAnnotation])
	}

	templateAnnotations, err := getPolicyTemplateAnnotations(replicatedPolicy, 0)
	if err != nil {
		t.Fatalf("Expected to get the policy template annotations but got: %v", err)
	}

	if templateAnnotations[PolicyIDAnnotation] != "" {
		t.Fatalf("Expected no policy annotation but got: %s", templateAnnotations[PolicyIDAnnotation])
	}

	// Test an existing replicated policy with annotations
	rootPolicy2 := rootPolicy.DeepCopy()
	replicatedPolicy2 := rootPolicy2.DeepCopy()
	existingReplicatedPolicy2 := rootPolicy2.DeepCopy()

	existingReplicatedPolicy2.Annotations["policy.open-cluster-management.io/parent-policy-compliance-db-id"] = "23"
	existingReplicatedPolicy2.Spec.PolicyTemplates[0].ObjectDefinition.Raw = []byte(`{
		"apiVersion": "policy.open-cluster-management.io",
		"kind": "ConfigurationPolicy",
		"metadata": {
			"name": "my-config",
			"annotations": {
				"policy.open-cluster-management.io/policy-compliance-db-id": "56"
			}
		},
		"spec": {
			"severity": "critical",
			"option1": "option2"
		}
	}`)

	reconciler.setDBAnnotations(context.TODO(), rootPolicy2, replicatedPolicy2, existingReplicatedPolicy2)

	annotations = replicatedPolicy2.GetAnnotations()
	if annotations[ParentPolicyIDAnnotation] != "23" {
		t.Fatalf("Expected the parent policy ID of 23 but got: %s", annotations[ParentPolicyIDAnnotation])
	}

	templateAnnotations, err = getPolicyTemplateAnnotations(replicatedPolicy2, 0)
	if err != nil {
		t.Fatalf("Expected to get the policy template annotations but got: %v", err)
	}

	if templateAnnotations[PolicyIDAnnotation] != "56" {
		t.Fatalf("Expected the policy ID of 56 but got: %s", templateAnnotations[PolicyIDAnnotation])
	}

	// Test a cache hit from the last run using the policies from the first run
	reconciler.setDBAnnotations(context.TODO(), rootPolicy, replicatedPolicy, existingReplicatedPolicy)

	annotations = replicatedPolicy.GetAnnotations()
	if annotations[ParentPolicyIDAnnotation] != "23" {
		t.Fatalf("Expected the parent policy ID of 23 but got: %s", annotations[ParentPolicyIDAnnotation])
	}

	templateAnnotations, err = getPolicyTemplateAnnotations(replicatedPolicy, 0)
	if err != nil {
		t.Fatalf("Expected to get the policy template annotations but got: %v", err)
	}

	if templateAnnotations[PolicyIDAnnotation] != "56" {
		t.Fatalf("Expected the policy ID of 56 but got: %s", templateAnnotations[PolicyIDAnnotation])
	}
}
