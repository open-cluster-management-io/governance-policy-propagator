// Copyright Contributors to the Open Cluster Management project

package v1beta1

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func nonEmptyStrings(values ...string) []NonEmptyString {
	out := make([]NonEmptyString, len(values))
	for i, v := range values {
		out[i] = NonEmptyString(v)
	}

	return out
}

func policySetExclusion(policyName string, clusterNames ...string) PolicySetExclusion {
	return PolicySetExclusion{
		PolicyName:   NonEmptyString(policyName),
		ClusterNames: nonEmptyStrings(clusterNames...),
	}
}

func testPolicySet(policies []string, exclusions ...PolicySetExclusion) *PolicySet {
	return &PolicySet{
		ObjectMeta: metav1.ObjectMeta{Name: "test-policyset", Namespace: "policies"},
		Spec: PolicySetSpec{
			Policies:   nonEmptyStrings(policies...),
			Exclusions: exclusions,
		},
	}
}

func TestPolicySetCustomValidator_ValidateCreate(t *testing.T) {
	t.Parallel()

	validator := &PolicySetCustomValidator{}
	ctx := context.Background()

	tests := []struct {
		name       string
		policySet  *PolicySet
		wantErrs   []error
		errMessage string
	}{
		{
			name:      "no exclusions",
			policySet: testPolicySet([]string{"policy-a", "policy-b"}),
		},
		{
			name: "empty exclusions slice",
			policySet: &PolicySet{
				ObjectMeta: metav1.ObjectMeta{Name: "test-policyset", Namespace: "policies"},
				Spec: PolicySetSpec{
					Policies:   nonEmptyStrings("policy-a"),
					Exclusions: []PolicySetExclusion{},
				},
			},
		},
		{
			name: "valid single exclusion",
			policySet: testPolicySet(
				[]string{"policy-a"},
				policySetExclusion("policy-a", "managed1"),
			),
		},
		{
			name: "valid multiple exclusions",
			policySet: testPolicySet(
				[]string{"policy-a", "policy-b"},
				policySetExclusion("policy-a", "managed1"),
				policySetExclusion("policy-b", "managed2"),
			),
		},
		{
			name: "invalid exclusion policy not in set",
			policySet: testPolicySet(
				[]string{"policy-a"},
				policySetExclusion("policy-b", "managed1"),
			),
			wantErrs:   []error{errInvalidExclusionPolicy},
			errMessage: "policy-b",
		},
		{
			name: "multiple invalid exclusion policies",
			policySet: testPolicySet(
				[]string{"policy-a"},
				policySetExclusion("policy-b", "managed1"),
				policySetExclusion("policy-c", "managed2"),
			),
			wantErrs:   []error{errInvalidExclusionPolicy},
			errMessage: "policy-b, policy-c",
		},
		{
			name: "duplicate invalid policy name reported once",
			policySet: testPolicySet(
				[]string{"policy-a"},
				policySetExclusion("policy-b", "managed1"),
				policySetExclusion("policy-b", "managed2"),
			),
			wantErrs:   []error{errInvalidExclusionPolicy},
			errMessage: "policy-b",
		},
		{
			name: "duplicate exclusion entries for policy in set",
			policySet: testPolicySet(
				[]string{"policy-a"},
				policySetExclusion("policy-a", "managed1"),
				policySetExclusion("policy-a", "managed2"),
			),
			wantErrs:   []error{errDuplicateExclusionPolicy},
			errMessage: "policy-a",
		},
		{
			name: "multiple duplicate exclusion policies",
			policySet: testPolicySet(
				[]string{"policy-a", "policy-b"},
				policySetExclusion("policy-a", "managed1"),
				policySetExclusion("policy-a", "managed2"),
				policySetExclusion("policy-b", "managed1"),
				policySetExclusion("policy-b", "managed3"),
			),
			wantErrs:   []error{errDuplicateExclusionPolicy},
			errMessage: "policy-a, policy-b",
		},
		{
			name: "both invalid and duplicate exclusion errors are reported together",
			policySet: testPolicySet(
				[]string{"policy-a"},
				policySetExclusion("policy-b", "managed1"),
				policySetExclusion("policy-a", "managed1"),
				policySetExclusion("policy-a", "managed2"),
			),
			wantErrs: []error{errDuplicateExclusionPolicy, errInvalidExclusionPolicy},
		},
		{
			name: "exclusions with empty policies list",
			policySet: testPolicySet(
				nil,
				policySetExclusion("policy-a", "managed1"),
			),
			wantErrs:   []error{errInvalidExclusionPolicy},
			errMessage: "policy-a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := validator.ValidateCreate(ctx, tt.policySet)
			assertValidationError(t, err, tt.wantErrs, tt.errMessage)
		})
	}
}

func TestPolicySetCustomValidator_ValidateUpdate(t *testing.T) {
	t.Parallel()

	validator := &PolicySetCustomValidator{}
	ctx := context.Background()

	t.Run("accepts valid exclusions on update", func(t *testing.T) {
		t.Parallel()

		oldPolicySet := testPolicySet([]string{"policy-a"})
		newPolicySet := testPolicySet(
			[]string{"policy-a"},
			policySetExclusion("policy-a", "managed1"),
		)

		_, err := validator.ValidateUpdate(ctx, oldPolicySet, newPolicySet)
		assertValidationError(t, err, nil, "")
	})

	t.Run("rejects invalid exclusions on update", func(t *testing.T) {
		t.Parallel()

		oldPolicySet := testPolicySet([]string{"policy-a"})
		newPolicySet := testPolicySet(
			[]string{"policy-a"},
			policySetExclusion("policy-b", "managed1"),
		)

		_, err := validator.ValidateUpdate(ctx, oldPolicySet, newPolicySet)
		assertValidationError(t, err, []error{errInvalidExclusionPolicy}, "policy-b")
	})
}

func TestPolicySetCustomValidator_ValidateDelete(t *testing.T) {
	t.Parallel()

	validator := &PolicySetCustomValidator{}

	_, err := validator.ValidateDelete(context.Background(), testPolicySet([]string{"policy-a"}))
	if err != nil {
		t.Fatalf("expected no error on delete, got %v", err)
	}
}

func assertValidationError(t *testing.T, err error, wantErrs []error, contains string) {
	t.Helper()

	if len(wantErrs) == 0 {
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		return
	}

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	for _, want := range wantErrs {
		if !errors.Is(err, want) {
			t.Fatalf("expected errors.Is(err, %v), got %v", want, err)
		}
	}

	if contains != "" && !strings.Contains(err.Error(), contains) {
		t.Fatalf("expected error message to contain %q, got %q", contains, err.Error())
	}
}
