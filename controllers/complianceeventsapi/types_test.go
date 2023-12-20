package complianceeventsapi

import (
	"strings"
	"testing"
	"time"
)

func TestClusterValidation(t *testing.T) {
	tests := map[string]struct {
		obj    Cluster
		errMsg string
	}{
		"no name":       {Cluster{ClusterID: "foo"}, "field not provided: cluster.name"},
		"no cluster id": {Cluster{Name: "foo"}, "field not provided: cluster.cluster_id"},
	}

	for input, tc := range tests {
		t.Run(input, func(t *testing.T) {
			err := tc.obj.Validate()
			if err == nil {
				t.Fatal("expected error")
			}

			if !strings.Contains(err.Error(), tc.errMsg) {
				t.Fatal("expected error to include", tc.errMsg, "in the error string; got", err.Error())
			}
		})
	}
}

func TestEventDetailsValidation(t *testing.T) {
	tests := map[string]struct {
		obj    EventDetails
		errMsg string
	}{
		"no compliance": {
			EventDetails{Message: "hello", Timestamp: time.Now()},
			"field not provided: event.compliance",
		},
		"bad compliance": {
			EventDetails{Compliance: "bad", Message: "hello", Timestamp: time.Now()},
			"should be Compliant or NonCompliant",
		},
		"no message": {
			EventDetails{Compliance: "Compliant", Timestamp: time.Now()},
			"field not provided: event.message",
		},
		"no timestamp": {
			EventDetails{Compliance: "Compliant", Message: "hello"},
			"field not provided: event.timestamp",
		},
	}

	for input, tc := range tests {
		t.Run(input, func(t *testing.T) {
			err := tc.obj.Validate()
			if err == nil {
				t.Fatal("expected error")
			}

			if !strings.Contains(err.Error(), tc.errMsg) {
				t.Fatal("expected error to include", tc.errMsg, "in the error string; got", err.Error())
			}
		})
	}
}

func TestParentPolicyValidation(t *testing.T) {
	tests := map[string]struct {
		obj    ParentPolicy
		errMsg string
	}{
		"no name": {
			ParentPolicy{Categories: []string{"hello"}},
			"field not provided: parent_policy.name",
		},
	}

	for input, tc := range tests {
		t.Run(input, func(t *testing.T) {
			err := tc.obj.Validate()
			if err == nil {
				t.Fatal("expected error")
			}

			if !strings.Contains(err.Error(), tc.errMsg) {
				t.Fatal("expected error to include", tc.errMsg, "in the error string; got", err.Error())
			}
		})
	}
}

func TestPolicyValidation(t *testing.T) {
	basespec := `{"test":"one","severity":"low"}`
	basehash := "cb84fe29e44202e3aeb46d39ba46993f60cdc6af"
	badhash := "foobarbaz"
	badspec := `{foo: bar: baz`
	noncompactspec := `{"foo" : "bar"   }`

	tests := map[string]struct {
		obj    Policy
		errMsg string
	}{
		"no name": {
			Policy{
				Kind:     "policy",
				APIGroup: "v1",
				Spec:     &basespec,
				SpecHash: &basehash,
			},
			"field not provided: policy.name",
		},
		"no API group": {
			Policy{
				Kind:     "policy",
				Name:     "foobar",
				Spec:     &basespec,
				SpecHash: &basehash,
			},
			"field not provided: policy.apiGroup",
		},
		"no kind": {
			Policy{
				APIGroup: "v1",
				Name:     "foobar",
				Spec:     &basespec,
				SpecHash: &basehash,
			},
			"field not provided: policy.kind",
		},
		"no spec or hash": {
			Policy{
				Kind:     "policy",
				APIGroup: "v1",
				Name:     "foobar",
			},
			"field not provided: policy.spec or policy.specHash",
		},
		"not valid json": {
			Policy{
				Kind:     "policy",
				APIGroup: "v1",
				Name:     "foobar",
				Spec:     &badspec,
			},
			"policy.spec is not valid JSON",
		},
		"not compact json": {
			Policy{
				Kind:     "policy",
				APIGroup: "v1",
				Name:     "foobar",
				Spec:     &noncompactspec,
			},
			"policy.spec is not compact JSON",
		},
		"not matching hash": {
			Policy{
				Kind:     "policy",
				APIGroup: "v1",
				Name:     "foobar",
				Spec:     &basespec,
				SpecHash: &badhash,
			},
			"policy.specHash does not match the compact policy.Spec",
		},
	}

	for input, tc := range tests {
		t.Run(input, func(t *testing.T) {
			err := tc.obj.Validate()
			if err == nil {
				t.Fatal("expected error")
			}

			if !strings.Contains(err.Error(), tc.errMsg) {
				t.Fatal("expected error to include", tc.errMsg, "in the error string; got", err.Error())
			}
		})
	}
}
