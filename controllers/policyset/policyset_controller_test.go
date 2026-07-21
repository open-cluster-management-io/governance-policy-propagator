// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	policyv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

func nes(ss ...string) []policyv1beta1.NonEmptyString {
	out := make([]policyv1beta1.NonEmptyString, len(ss))
	for i, s := range ss {
		out[i] = policyv1beta1.NonEmptyString(s)
	}

	return out
}

func exclusionPolicySet(clusters ...string) policyv1beta1.PolicySetExclusion {
	return policyv1beta1.PolicySetExclusion{
		PolicyName: policyv1beta1.NonEmptyString("policy-a"), ClusterNames: nes(clusters...),
	}
}

func testPolicySetForExclusions(exclusions ...policyv1beta1.PolicySetExclusion) *policyv1beta1.PolicySet {
	return &policyv1beta1.PolicySet{
		ObjectMeta: metav1.ObjectMeta{Name: "policyset-a", Namespace: "policies"},
		Spec: policyv1beta1.PolicySetSpec{
			Policies:   nes("policy-a"),
			Exclusions: exclusions,
		},
	}
}

func TestBuildPolicySetExclusionsStatus(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		exclusions []policyv1beta1.PolicySetExclusion
		want       []policyv1beta1.PolicySetStatusExclusion
	}{
		{
			name:       "with exclusions",
			exclusions: []policyv1beta1.PolicySetExclusion{exclusionPolicySet("managed2", "managed1")},
			want: []policyv1beta1.PolicySetStatusExclusion{
				{PolicyName: "policy-a", Clusters: nes("managed1", "managed2")},
			},
		},
		{
			name: "sorts exclusions by policy name",
			exclusions: []policyv1beta1.PolicySetExclusion{
				{PolicyName: "policy-b", ClusterNames: nes("managed2")},
				{PolicyName: "policy-a", ClusterNames: nes("managed1")},
			},
			want: []policyv1beta1.PolicySetStatusExclusion{
				{PolicyName: "policy-a", Clusters: nes("managed1")},
				{PolicyName: "policy-b", Clusters: nes("managed2")},
			},
		},
		{
			name: "drops exclusion with empty cluster names",
			exclusions: []policyv1beta1.PolicySetExclusion{
				{PolicyName: "policy-a", ClusterNames: nil},
				{PolicyName: "policy-b", ClusterNames: nes("managed2")},
			},
			want: []policyv1beta1.PolicySetStatusExclusion{
				{PolicyName: "policy-b", Clusters: nes("managed2")},
			},
		},
		{
			name:       "empty exclusions",
			exclusions: nil,
			want:       nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := buildPolicySetExclusionsStatus(testPolicySetForExclusions(tt.exclusions...))
			if !cmp.Equal(got, tt.want) {
				t.Fatalf("expected %+v, got %+v", tt.want, got)
			}
		})
	}
}
