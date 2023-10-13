package propagator

import (
	"context"
	"reflect"
	"strings"
	"testing"

	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

func fakeBasicPolicy(name, namespace string) *policiesv1.Policy {
	return &policiesv1.Policy{
		TypeMeta: v1.TypeMeta{
			Kind:       policiesv1.Kind,
			APIVersion: policiesv1.GroupVersion.String(),
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: policiesv1.PolicySpec{
			Dependencies: []policiesv1.PolicyDependency{},
			PolicyTemplates: []*policiesv1.PolicyTemplate{{
				ExtraDependencies: []policiesv1.PolicyDependency{},
			}},
		},
	}
}

func fakePolicyWithDeps(name, namespace string, deps, extraDeps []policiesv1.PolicyDependency) *policiesv1.Policy {
	pol := fakeBasicPolicy(name, namespace)
	pol.Spec.Dependencies = deps
	pol.Spec.PolicyTemplates[0].ExtraDependencies = extraDeps

	return pol
}

func fakePolicySet(name, namespace string, policies ...string) *policiesv1beta1.PolicySet {
	pols := []policiesv1beta1.NonEmptyString{}
	for _, p := range policies {
		pols = append(pols, policiesv1beta1.NonEmptyString(p))
	}

	return &policiesv1beta1.PolicySet{
		TypeMeta: v1.TypeMeta{
			Kind:       policiesv1.PolicySetKind,
			APIVersion: policiesv1beta1.GroupVersion.String(),
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: policiesv1beta1.PolicySetSpec{
			Policies: pols,
		},
	}
}

func fakeDependencyFromObj(obj client.Object, compliance string) policiesv1.PolicyDependency {
	return policiesv1.PolicyDependency{
		TypeMeta: v1.TypeMeta{
			Kind:       obj.GetObjectKind().GroupVersionKind().Kind,
			APIVersion: obj.GetObjectKind().GroupVersionKind().GroupVersion().String(),
		},
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
		Compliance: policiesv1.ComplianceState(compliance),
	}
}

func TestEquivalentReplicatedPolicies(t *testing.T) {
	basePolicy := fakeBasicPolicy("base-policy", "default")

	withAnnotation := basePolicy.DeepCopy()
	withAnnotation.SetAnnotations(map[string]string{
		"test-annotation.io/foo": "bar",
	})

	withLabel := basePolicy.DeepCopy()
	withLabel.SetLabels(map[string]string{
		"test-label.io/fizz": "buzz",
	})

	thwimpDep := fakeDependencyFromObj(fakeBasicPolicy("thwimp", "thwump"), "NonCompliant")
	withDependency := fakePolicyWithDeps("base-policy", "default", []policiesv1.PolicyDependency{thwimpDep}, nil)

	withStatus := basePolicy.DeepCopy()
	withStatus.Status = policiesv1.PolicyStatus{
		ComplianceState: policiesv1.Pending,
	}

	tests := map[string]struct {
		comparePlc *policiesv1.Policy
		expected   bool
	}{
		"reflexive":             {basePolicy, true},
		"different annotations": {withAnnotation, false},
		"different labels":      {withLabel, false},
		"different specs":       {withDependency, false},
		"different status":      {withStatus, true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if equivalentReplicatedPolicies(basePolicy, test.comparePlc) != test.expected {
				if test.expected {
					t.Fatalf("Expected policies to be equivalent: %+v, %+v", basePolicy, test.comparePlc)
				} else {
					t.Fatalf("Expected policies not to be equivalent: %+v, %+v", basePolicy, test.comparePlc)
				}
			}
		})
	}
}

func TestCanonicalizeDependencies(t *testing.T) {
	depPol := func(name, namespace, compliance string) policiesv1.PolicyDependency {
		return fakeDependencyFromObj(fakeBasicPolicy(name, namespace), compliance)
	}

	unusualDependency := policiesv1.PolicyDependency{
		TypeMeta: v1.TypeMeta{
			Kind:       "WeirdPolicy",
			APIVersion: "something.com/v4beta7",
		},
		Name:       "foo",
		Namespace:  "bar",
		Compliance: "Auditing",
	}

	fooSet := fakePolicySet("foo", "default", "one", "two")
	barSet := fakePolicySet("bar", "other-ns", "three", "four", "five")
	fakeScheme := k8sruntime.NewScheme()

	if err := policiesv1beta1.AddToScheme(fakeScheme); err != nil {
		t.Fatalf("Unexpected error building scheme: %v", err)
	}

	fakeClient := fakeClient.NewClientBuilder().
		WithScheme(fakeScheme).
		WithObjects(fooSet, barSet).
		Build()

	fakeReconciler := Propagator{Client: fakeClient}

	tests := map[string]struct {
		input []policiesv1.PolicyDependency
		want  []policiesv1.PolicyDependency
	}{
		"empty": {
			input: []policiesv1.PolicyDependency{},
			want:  []policiesv1.PolicyDependency{},
		},
		"single compliant set": {
			input: []policiesv1.PolicyDependency{
				fakeDependencyFromObj(fooSet, "Compliant"),
			},
			want: []policiesv1.PolicyDependency{
				depPol("default.one", "", "Compliant"),
				depPol("default.two", "", "Compliant"),
			},
		},
		"two sets, mixed compliance": {
			input: []policiesv1.PolicyDependency{
				fakeDependencyFromObj(fooSet, "Compliant"),
				fakeDependencyFromObj(barSet, "NonCompliant"),
			},
			want: []policiesv1.PolicyDependency{
				depPol("default.one", "", "Compliant"),
				depPol("default.two", "", "Compliant"),
				depPol("other-ns.three", "", "NonCompliant"),
				depPol("other-ns.four", "", "NonCompliant"),
				depPol("other-ns.five", "", "NonCompliant"),
			},
		},
		"policies with namespaces": {
			input: []policiesv1.PolicyDependency{
				depPol("red", "colors", "Compliant"),
				depPol("blue", "colors", "NonCompliant"),
			},
			want: []policiesv1.PolicyDependency{
				depPol("colors.red", "", "Compliant"),
				depPol("colors.blue", "", "NonCompliant"),
			},
		},
		"policies without namespaces": {
			input: []policiesv1.PolicyDependency{
				depPol("magikarp", "", "Compliant"),
				depPol("gyarados", "", "Compliant"),
			},
			want: []policiesv1.PolicyDependency{
				depPol("sentinel.magikarp", "", "Compliant"),
				depPol("sentinel.gyarados", "", "Compliant"),
			},
		},
		"policies already canonicalized": {
			input: []policiesv1.PolicyDependency{
				depPol("legendary.lugia", "", "Terminating"),
				depPol("legendary.ho-oh", "", "Terminating"),
			},
			want: []policiesv1.PolicyDependency{
				depPol("legendary.lugia", "", "Terminating"),
				depPol("legendary.ho-oh", "", "Terminating"),
			},
		},
		"unusual dependency should be unchanged": {
			input: []policiesv1.PolicyDependency{unusualDependency},
			want:  []policiesv1.PolicyDependency{unusualDependency},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := fakeReconciler.canonicalizeDependencies(context.TODO(), test.input, "sentinel")
			if err != nil {
				t.Fatal("Got unexpected error")
			}

			if !reflect.DeepEqual(test.want, got) {
				t.Fatalf("expected: %v, got: %v", test.want, got)
			}
		})
	}
}

func TestGetPolicySetDependencies(t *testing.T) {
	makeDepPlcSet := func(name, namespace, compliance string) policiesv1.PolicyDependency {
		return fakeDependencyFromObj(fakePolicySet(name, namespace), compliance)
	}

	makeDepPolicy := func(name, namespace, compliance string) policiesv1.PolicyDependency {
		return fakeDependencyFromObj(fakeBasicPolicy(name, namespace), compliance)
	}

	idsFromDeps := func(deps ...policiesv1.PolicyDependency) map[k8sdepwatches.ObjectIdentifier]bool {
		ids := make(map[k8sdepwatches.ObjectIdentifier]bool)

		for _, d := range deps {
			ids[k8sdepwatches.ObjectIdentifier{
				Group:     strings.Split(d.APIVersion, "/")[0],
				Version:   strings.Split(d.APIVersion, "/")[1],
				Kind:      d.Kind,
				Namespace: d.Namespace,
				Name:      d.Name,
			}] = true
		}

		return ids
	}

	fooSet := makeDepPlcSet("foo", "default", "Compliant")
	barSet := makeDepPlcSet("bar", "default", "NonCompliant")
	myPolicy := makeDepPolicy("mypolicy", "mynamespace", "Compliant")
	fooSetEmptyNamespace := makeDepPlcSet("foo", "", "Compliant")
	fooSetInferredNamespace := makeDepPlcSet("foo", "testns", "Compliant")

	tests := map[string]struct {
		inputDeps      []policiesv1.PolicyDependency
		inputExtraDeps []policiesv1.PolicyDependency
		want           map[k8sdepwatches.ObjectIdentifier]bool
	}{
		"no dependencies": {
			want: idsFromDeps(),
		},
		"one top set dependency": {
			inputDeps: []policiesv1.PolicyDependency{fooSet},
			want:      idsFromDeps(fooSet),
		},
		"one extra set dependency": {
			inputExtraDeps: []policiesv1.PolicyDependency{fooSet},
			want:           idsFromDeps(fooSet),
		},
		"set dependencies at both levels": {
			inputDeps:      []policiesv1.PolicyDependency{fooSet},
			inputExtraDeps: []policiesv1.PolicyDependency{barSet},
			want:           idsFromDeps(fooSet, barSet),
		},
		"one policy should be ignored": {
			inputDeps: []policiesv1.PolicyDependency{myPolicy},
			want:      idsFromDeps(),
		},
		"policies should be ignored when mixed with sets": {
			inputDeps: []policiesv1.PolicyDependency{fooSet, myPolicy},
			want:      idsFromDeps(fooSet),
		},
		"namespace should be inferred from policy when not provided": {
			inputDeps: []policiesv1.PolicyDependency{fooSetEmptyNamespace},
			want:      idsFromDeps(fooSetInferredNamespace),
		},
		"extraDependency namespace should be inferred from policy when not provided": {
			inputExtraDeps: []policiesv1.PolicyDependency{fooSetEmptyNamespace},
			want:           idsFromDeps(fooSetInferredNamespace),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			testpol := fakePolicyWithDeps("testpolicy", "testns", test.inputDeps, test.inputExtraDeps)

			got := getPolicySetDependencies(testpol)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("expected: %v, got: %v", test.want, got)
			}
		})
	}
}
