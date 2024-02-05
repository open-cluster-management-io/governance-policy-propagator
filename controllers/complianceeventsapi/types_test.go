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
			"should be Compliant, NonCompliant, Disabled, or Pending got bad",
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
			ParentPolicy{Namespace: "policies", Categories: []string{"hello"}},
			"field not provided: parent_policy.name",
		},
		"no namespace": {
			ParentPolicy{Name: "my-policy", Categories: []string{"hello"}},
			"field not provided: parent_policy.namespace",
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
	var basespec JSONMap = map[string]interface{}{"test": "one", "severity": "low"}

	tests := map[string]struct {
		obj    Policy
		errMsg string
	}{
		"no name": {
			Policy{
				Kind:     "policy",
				APIGroup: "v1",
				Spec:     basespec,
			},
			"field not provided: policy.name",
		},
		"no API group": {
			Policy{
				Kind: "policy",
				Name: "foobar",
				Spec: basespec,
			},
			"field not provided: policy.apiGroup",
		},
		"no kind": {
			Policy{
				APIGroup: "v1",
				Name:     "foobar",
				Spec:     basespec,
			},
			"field not provided: policy.kind",
		},
		"no spec or hash": {
			Policy{
				Kind:     "policy",
				APIGroup: "v1",
				Name:     "foobar",
			},
			"field not provided: policy.spec",
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
