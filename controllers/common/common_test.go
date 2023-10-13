package common

import (
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func TestParseRootPolicyLabel(t *testing.T) {
	tests := map[string]struct {
		name      string
		namespace string
		shouldErr bool
	}{
		"foobar":   {"", "", true},
		"foo.bar":  {"bar", "foo", false},
		"fo.ob.ar": {"ob.ar", "fo", false},
	}

	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			name, namespace, err := ParseRootPolicyLabel(input)
			if (err != nil) != expected.shouldErr {
				t.Fatal("expected error, got nil")
			}
			if name != expected.name {
				t.Fatalf("expected name '%v', got '%v'", expected.name, name)
			}
			if namespace != expected.namespace {
				t.Fatalf("expected namespace '%v', got '%v'", expected.namespace, namespace)
			}
		})
	}
}

func TestGetAffectedObjsWithDecision(t *testing.T) {
	newOjbs := []clusterv1beta1.ClusterDecision{
		{ClusterName: "managed1", Reason: "test11"},
		{ClusterName: "managed2", Reason: "test11"},
	}
	oldObjs := []clusterv1beta1.ClusterDecision{
		{ClusterName: "managed1", Reason: "test11"},
		{ClusterName: "managed3", Reason: "test11"},
	}
	expectedResult := []clusterv1beta1.ClusterDecision{
		{ClusterName: "managed2", Reason: "test11"},
		{ClusterName: "managed3", Reason: "test11"},
	}

	result := GetAffectedObjs(newOjbs, oldObjs)
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].ClusterName < result[j].ClusterName
	})

	if !cmp.Equal(result, expectedResult) {
		t.Fatalf(`GetAffectedObjs test failed expected: %+v but result is %+v`, expectedResult, result)
	}
}

func TestGetAffectedObjsWithRequest(t *testing.T) {
	newOjbs := []reconcile.Request{
		{NamespacedName: types.NamespacedName{Namespace: "test1", Name: "test1"}},
		{NamespacedName: types.NamespacedName{Namespace: "test2", Name: "test2"}},
	}
	oldOjbs := []reconcile.Request{
		{NamespacedName: types.NamespacedName{Namespace: "test1", Name: "test1"}},
		{NamespacedName: types.NamespacedName{Namespace: "test3", Name: "test3"}},
	}
	expectedResult := []reconcile.Request{
		{NamespacedName: types.NamespacedName{Namespace: "test2", Name: "test2"}},
		{NamespacedName: types.NamespacedName{Namespace: "test3", Name: "test3"}},
	}

	result := GetAffectedObjs(newOjbs, oldOjbs)
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].NamespacedName.Namespace < result[j].NamespacedName.Namespace
	})

	if !cmp.Equal(result, expectedResult) {
		t.Fatalf(`GetAffectedObjs test failed expected: %+v but result is %+v`, expectedResult, result)
	}
}
