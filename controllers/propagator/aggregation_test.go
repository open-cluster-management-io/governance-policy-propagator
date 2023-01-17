package propagator

import (
	"reflect"
	"testing"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func fakeCPCS(name, compliance string) *policiesv1.CompliancePerClusterStatus {
	return &policiesv1.CompliancePerClusterStatus{
		ComplianceState:  policiesv1.ComplianceState(compliance),
		ClusterName:      name,
		ClusterNamespace: name,
	}
}

func TestCalculateRootCompliance(t *testing.T) {
	allCompliantList := []*policiesv1.CompliancePerClusterStatus{
		fakeCPCS("articuno", "Compliant"),
		fakeCPCS("zapdos", "Compliant"),
		fakeCPCS("moltres", "Compliant"),
	}

	tests := map[string]struct {
		input []*policiesv1.CompliancePerClusterStatus
		want  policiesv1.ComplianceState
	}{
		"all compliant": {
			input: allCompliantList,
			want:  policiesv1.Compliant,
		},
		"one noncompliant": {
			input: append(allCompliantList, fakeCPCS("foo", "NonCompliant")),
			want:  policiesv1.NonCompliant,
		},
		"one pending": {
			input: append(allCompliantList, fakeCPCS("bar", "Pending")),
			want:  policiesv1.Pending,
		},
		"one empty": {
			input: append(allCompliantList, fakeCPCS("thud", "")),
			want:  policiesv1.ComplianceState(""),
		},
		"one odd value": {
			input: append(allCompliantList, fakeCPCS("wibble", "Discombobulated")),
			want:  policiesv1.ComplianceState(""),
		},
		"noncompliant and pending": {
			input: append(allCompliantList,
				fakeCPCS("foo", "NonCompliant"),
				fakeCPCS("bar", "Pending")),
			want: policiesv1.NonCompliant,
		},
		"pending and unknown": {
			input: append(allCompliantList,
				fakeCPCS("bar", "Pending"),
				fakeCPCS("thud", "")),
			want: policiesv1.Pending,
		},
		"all states": {
			input: append(allCompliantList,
				fakeCPCS("foo", "NonCompliant"),
				fakeCPCS("bar", "Pending"),
				fakeCPCS("thud", ""),
				fakeCPCS("wibble", "Discombobulated")),
			want: policiesv1.NonCompliant,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got := CalculateRootCompliance(test.input)
			if !reflect.DeepEqual(test.want, got) {
				t.Fatalf("expected: %v, got: %v", test.want, got)
			}
		})
	}
}
