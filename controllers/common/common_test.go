package common

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
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
		return result[i].Namespace < result[j].Namespace
	})

	if !cmp.Equal(result, expectedResult) {
		t.Fatalf(`GetAffectedObjs test failed expected: %+v but result is %+v`, expectedResult, result)
	}
}

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

const (
	exclusionTestNamespace = "policies"
	exclusionTestPolicy    = "policy-a"
	exclusionTestPolicySet = "policyset-a"
)

func nes(ss ...string) []policiesv1beta1.NonEmptyString {
	out := make([]policiesv1beta1.NonEmptyString, len(ss))
	for i, s := range ss {
		out[i] = policiesv1beta1.NonEmptyString(s)
	}

	return out
}

func exclusion(clusters ...string) policiesv1beta1.PolicySetExclusion {
	return policiesv1beta1.PolicySetExclusion{
		PolicyName: policiesv1beta1.NonEmptyString(exclusionTestPolicy), ClusterNames: nes(clusters...),
	}
}

func policyExclusions(clusters ...string) []policiesv1.PolicyExclusion {
	out := make([]policiesv1.PolicyExclusion, len(clusters))
	for i, c := range clusters {
		out[i] = policiesv1.PolicyExclusion{ClusterName: c}
	}

	return out
}

func newExclusionTestScheme(t *testing.T) *k8sruntime.Scheme {
	t.Helper()

	scheme := k8sruntime.NewScheme()
	for _, add := range []func(*k8sruntime.Scheme) error{
		policiesv1.AddToScheme, policiesv1beta1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatalf("failed to add scheme: %v", err)
		}
	}

	if err := clusterv1beta1.Install(scheme); err != nil {
		t.Fatalf("failed to install cluster scheme: %v", err)
	}

	return scheme
}

func makeTestPlacement(name string) *clusterv1beta1.Placement {
	return &clusterv1beta1.Placement{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: exclusionTestNamespace},
	}
}

func makeTestPlacementDecision(placementName string, clusters ...string) *clusterv1beta1.PlacementDecision {
	decisions := make([]clusterv1beta1.ClusterDecision, len(clusters))
	for i, cluster := range clusters {
		decisions[i] = clusterv1beta1.ClusterDecision{ClusterName: cluster}
	}

	return &clusterv1beta1.PlacementDecision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      placementName + "-decision",
			Namespace: exclusionTestNamespace,
			Labels: map[string]string{
				clusterv1beta1.PlacementLabel: placementName,
			},
		},
		Status: clusterv1beta1.PlacementDecisionStatus{Decisions: decisions},
	}
}

func makeTestPlacementBinding(
	name, subjectKind, subjectName, placementName string, extraSubjects ...policiesv1.Subject,
) *policiesv1.PlacementBinding {
	subjects := []policiesv1.Subject{{
		APIGroup: policiesv1.SchemeGroupVersion.Group, Kind: subjectKind, Name: subjectName,
	}}
	subjects = append(subjects, extraSubjects...)

	return &policiesv1.PlacementBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: exclusionTestNamespace},
		Subjects:   subjects,
		PlacementRef: policiesv1.PlacementSubject{
			APIGroup: clusterv1beta1.GroupVersion.Group, Kind: "Placement", Name: placementName,
		},
	}
}

func makeTestSubject(kind, name string) policiesv1.Subject {
	return policiesv1.Subject{
		APIGroup: policiesv1.SchemeGroupVersion.Group, Kind: kind, Name: name,
	}
}

func makeTestDirectPlacement(binding string) *policiesv1.Placement {
	return &policiesv1.Placement{PlacementBinding: binding}
}

func makeTestPolicySetPlacement(binding, policySet string, clusters ...string) *policiesv1.Placement {
	return &policiesv1.Placement{
		PlacementBinding: binding,
		PolicySet:        policySet,
		Exclusions:       policyExclusions(clusters...),
	}
}

func makeTestPolicySet(name string, exclusions ...policiesv1beta1.PolicySetExclusion) *policiesv1beta1.PolicySet {
	if name == "" {
		name = exclusionTestPolicySet
	}

	return &policiesv1beta1.PolicySet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: exclusionTestNamespace},
		Spec: policiesv1beta1.PolicySetSpec{
			Policies: nes(exclusionTestPolicy), Exclusions: exclusions,
		},
	}
}

func makeTestRootPolicy(mutate ...func(*policiesv1.Policy)) *policiesv1.Policy {
	p := &policiesv1.Policy{
		ObjectMeta: metav1.ObjectMeta{Name: exclusionTestPolicy, Namespace: exclusionTestNamespace},
	}
	if len(mutate) > 0 && mutate[0] != nil {
		mutate[0](p)
	}

	return p
}

func newExclusionTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	return fake.NewClientBuilder().WithScheme(newExclusionTestScheme(t)).WithObjects(objs...).Build()
}

func TestBuildPolicyExclusionsStatus(t *testing.T) {
	t.Parallel()

	policySet := makeTestPolicySet("", exclusion("managed2", "managed1"))

	got := buildPolicyExclusionsStatus(policySet, exclusionTestPolicy)
	if !cmp.Equal(got, policyExclusions("managed1", "managed2")) {
		t.Fatalf("expected %+v, got %+v", policyExclusions("managed1", "managed2"), got)
	}
}

func TestComputeRemainingBindings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	policySet := makeTestPolicySet("", exclusion("managed2"))
	policySetPL := makeTestPlacement("policyset-plm")
	policySetPD := makeTestPlacementDecision("policyset-plm", "managed1", "managed2")
	directPL := makeTestPlacement("direct-plm")
	directPD := makeTestPlacementDecision("direct-plm", "managed2")
	policySetPB := makeTestPlacementBinding(
		"policyset-pb", policiesv1.PolicySetKind, exclusionTestPolicySet, "policyset-plm",
	)
	directPB := makeTestPlacementBinding("direct-pb", policiesv1.Kind, exclusionTestPolicy, "direct-plm")
	rootPolicy := makeTestRootPolicy()
	disabledPolicy := makeTestRootPolicy(func(p *policiesv1.Policy) { p.Spec.Disabled = true })

	for _, tt := range []struct {
		name       string
		objects    []client.Object
		policy     *policiesv1.Policy
		decisions  DecisionSet
		placements []*policiesv1.Placement
		want       map[string][]policiesv1.RemainingBinding
	}{
		{
			name:    "disabled policy returns nil",
			objects: []client.Object{disabledPolicy, policySet, policySetPL, policySetPD, policySetPB},
			policy:  disabledPolicy, decisions: DecisionSet{"managed1": {"policyset-pb"}},
		},
		{
			name: "empty decisions returns nil",
			objects: []client.Object{
				rootPolicy, policySet, policySetPL, policySetPD, directPL, directPD, policySetPB, directPB,
			},
			policy: rootPolicy, decisions: DecisionSet{},
		},
		{
			name: "cluster without active exclusions has no remaining bindings",
			placements: []*policiesv1.Placement{
				makeTestPolicySetPlacement("policyset-pb", exclusionTestPolicySet, "managed2"),
				makeTestDirectPlacement("direct-pb"),
			},
			policy:    rootPolicy,
			decisions: DecisionSet{"managed1": {"policyset-pb", "direct-pb"}},
		},
		{
			name: "only excluding bindings yields no remaining bindings",
			placements: []*policiesv1.Placement{
				makeTestPolicySetPlacement("policyset-pb", exclusionTestPolicySet, "managed2"),
				makeTestPolicySetPlacement("policyset-b-pb", "policyset-b", "managed2"),
			},
			policy: rootPolicy,
			decisions: DecisionSet{
				"managed2": {"policyset-pb", "policyset-b-pb"},
			},
		},
		{
			name: "misused shared binding direct path is not a remaining binding",
			placements: []*policiesv1.Placement{
				makeTestDirectPlacement("shared-pb"),
				makeTestPolicySetPlacement("shared-pb", exclusionTestPolicySet, "managed2"),
				makeTestDirectPlacement("direct-pb"),
			},
			policy: rootPolicy,
			decisions: DecisionSet{
				"managed2": {"shared-pb", "direct-pb"},
			},
			want: map[string][]policiesv1.RemainingBinding{
				"managed2": {{PlacementBinding: "direct-pb"}},
			},
		},
		{
			name: "two policysets with exclusions list only direct remaining binding",
			objects: []client.Object{
				rootPolicy, policySet, policySetPL, policySetPD, directPL, directPD, policySetPB, directPB,
				makeTestPolicySet("policyset-b", exclusion("managed2")),
				makeTestPlacement("policyset-b-plm"),
				makeTestPlacementDecision("policyset-b-plm", "managed1", "managed2"),
				makeTestPlacementBinding("policyset-b-pb", policiesv1.PolicySetKind, "policyset-b", "policyset-b-plm"),
			},
			policy: rootPolicy,
			decisions: DecisionSet{
				"managed1": {"policyset-pb", "direct-pb"},
				"managed2": {"policyset-pb", "direct-pb", "policyset-b-pb"},
			},
			want: map[string][]policiesv1.RemainingBinding{
				"managed2": {{PlacementBinding: "direct-pb"}},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			placements := tt.placements
			if placements == nil {
				c := newExclusionTestClient(t, tt.objects...)

				var err error

				placements, _, err = GetClusterDecisions(ctx, c, tt.policy)
				if err != nil {
					t.Fatalf("unexpected error getting placements: %v", err)
				}
			}

			got := computeRemainingBindings(tt.policy, placements, tt.decisions)

			if !cmp.Equal(got, tt.want) {
				t.Fatalf("expected %+v, got %+v", tt.want, got)
			}
		})
	}
}

func TestGetPolicyPlacementDecisionsDoesNotFilterDirectBinding(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	policySet := makeTestPolicySet("", exclusion("managed2"))
	rootPolicy := makeTestRootPolicy()
	pl := makeTestPlacement("shared-plm")
	pd := makeTestPlacementDecision("shared-plm", "managed1", "managed2")
	pb := makeTestPlacementBinding(
		"shared-pb", policiesv1.Kind, exclusionTestPolicy, "shared-plm",
		makeTestSubject(policiesv1.PolicySetKind, exclusionTestPolicySet),
	)
	c := newExclusionTestClient(t, rootPolicy, policySet, pl, pd, pb)

	decisions, placements, err := GetPolicyPlacementDecisions(ctx, c, rootPolicy, pb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cmp.Equal(decisions, []string{"managed1", "managed2"}) {
		t.Fatalf("expected unfiltered direct-binding decisions, got %v", decisions)
	}

	expectedPlacements := []*policiesv1.Placement{
		{PlacementBinding: "shared-pb", Placement: "shared-plm"},
		{
			PlacementBinding: "shared-pb", Placement: "shared-plm", PolicySet: exclusionTestPolicySet,
			Exclusions: policyExclusions("managed2"),
		},
	}
	if !cmp.Equal(placements, expectedPlacements) {
		t.Fatalf("expected %+v, got %+v", expectedPlacements, placements)
	}
}
