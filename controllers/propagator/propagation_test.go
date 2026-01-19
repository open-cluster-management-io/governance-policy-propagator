// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"reflect"
	"sync"
	"testing"

	"github.com/stolostron/go-template-utils/v7/pkg/templates"
	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func TestGetTemplateResolverOpts_DefaultSA(t *testing.T) {
	rootPolicy := &policiesv1.Policy{
		TypeMeta: metav1.TypeMeta{
			Kind:       policiesv1.Kind,
			APIVersion: policiesv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "policy-one",
			Namespace: "default",
		},
		Spec: policiesv1.PolicySpec{},
	}

	replicatedPolicy := rootPolicy.DeepCopy()
	replicatedPolicy.Name = "default.policy-one"
	replicatedPolicy.Namespace = "managed-cluster"

	fakeScheme := runtime.NewScheme()
	if err := policiesv1.AddToScheme(fakeScheme); err != nil {
		t.Fatalf("Failed to add policy scheme: %v", err)
	}

	fakeC := fakeClient.NewClientBuilder().
		WithScheme(fakeScheme).
		Build()

	eventRecorder := record.NewFakeRecorder(10)

	r := &ReplicatedPolicyReconciler{
		Propagator: Propagator{
			Client:               fakeC,
			Scheme:               fakeScheme,
			Recorder:             eventRecorder,
			TemplateFuncDenylist: []string{},
		},
		ResourceVersions: &sync.Map{},
	}

	watcher := k8sdepwatches.ObjectIdentifier{
		Group:     policiesv1.GroupVersion.Group,
		Version:   policiesv1.GroupVersion.Version,
		Kind:      replicatedPolicy.Kind,
		Namespace: replicatedPolicy.GetNamespace(),
		Name:      replicatedPolicy.GetName(),
	}

	saNSName, resolveOptions := r.getTemplateResolverOpts(
		replicatedPolicy, rootPolicy, "managed-cluster", watcher,
	)

	if saNSName != defaultSANamespacedName {
		t.Errorf("Expected default service account, got %v", saNSName)
	}

	if resolveOptions.Watcher == nil {
		t.Error("Expected Watcher to be set in resolve options")
	}

	if resolveOptions.LookupNamespace != "default" {
		t.Errorf("Expected LookupNamespace to be 'default', got %s", resolveOptions.LookupNamespace)
	}

	if len(resolveOptions.DenylistFunctions) != 0 {
		t.Errorf("Expected no denylist functions, got %v", resolveOptions.DenylistFunctions)
	}

	expectedClusterScopedAllowList := []templates.ClusterScopedObjectIdentifier{
		{
			Group: "cluster.open-cluster-management.io",
			Kind:  "ManagedCluster",
			Name:  "managed-cluster",
		},
	}
	if !reflect.DeepEqual(resolveOptions.ClusterScopedAllowList, expectedClusterScopedAllowList) {
		t.Errorf("Expected cluster-scoped allow list %v, got %v",
			expectedClusterScopedAllowList, resolveOptions.ClusterScopedAllowList)
	}
}

func TestGetTemplateResolverOpts_DefaultSAWithDenylist(t *testing.T) {
	rootPolicy := &policiesv1.Policy{
		TypeMeta: metav1.TypeMeta{
			Kind:       policiesv1.Kind,
			APIVersion: policiesv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "policy-two",
			Namespace: "default",
		},
		Spec: policiesv1.PolicySpec{},
	}

	replicatedPolicy := rootPolicy.DeepCopy()
	replicatedPolicy.Name = "default.policy-two"
	replicatedPolicy.Namespace = "managed-cluster"
	replicatedPolicy.Spec.HubTemplateOptions = &policiesv1.HubTemplateOptions{
		ServiceAccountName: "test-sa",
	}

	fakeScheme := runtime.NewScheme()
	if err := policiesv1.AddToScheme(fakeScheme); err != nil {
		t.Fatalf("Failed to add policy scheme: %v", err)
	}

	fakeC := fakeClient.NewClientBuilder().
		WithScheme(fakeScheme).
		Build()

	eventRecorder := record.NewFakeRecorder(10)

	denylist := []string{"add"}

	r := &ReplicatedPolicyReconciler{
		Propagator: Propagator{
			Client:               fakeC,
			Scheme:               fakeScheme,
			Recorder:             eventRecorder,
			TemplateFuncDenylist: denylist,
		},
		ResourceVersions: &sync.Map{},
	}

	watcher := k8sdepwatches.ObjectIdentifier{
		Group:     policiesv1.GroupVersion.Group,
		Version:   policiesv1.GroupVersion.Version,
		Kind:      replicatedPolicy.Kind,
		Namespace: replicatedPolicy.GetNamespace(),
		Name:      replicatedPolicy.GetName(),
	}

	saNSName, resolveOptions := r.getTemplateResolverOpts(
		replicatedPolicy, rootPolicy, "managed-cluster", watcher,
	)

	expectedSA := types.NamespacedName{
		Namespace: "default",
		Name:      "test-sa",
	}
	if saNSName != expectedSA {
		t.Errorf("Expected service account %v, got %v", expectedSA, saNSName)
	}

	if !reflect.DeepEqual(resolveOptions.DenylistFunctions, denylist) {
		t.Errorf("Expected denylist %v, got %v", denylist, resolveOptions.DenylistFunctions)
	}
}

func TestGetTemplateResolverOpts_HubSAWithDenylist(t *testing.T) {
	rootPolicy := &policiesv1.Policy{
		TypeMeta: metav1.TypeMeta{
			Kind:       policiesv1.Kind,
			APIVersion: policiesv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "policy-three",
			Namespace: "default",
		},
		Spec: policiesv1.PolicySpec{},
	}

	replicatedPolicy := rootPolicy.DeepCopy()
	replicatedPolicy.Name = "default.policy-three"
	replicatedPolicy.Namespace = "managed-cluster"
	replicatedPolicy.Spec.HubTemplateOptions = &policiesv1.HubTemplateOptions{
		ServiceAccountName: "hub-sa",
	}

	fakeScheme := runtime.NewScheme()
	if err := policiesv1.AddToScheme(fakeScheme); err != nil {
		t.Fatalf("Failed to add policy scheme: %v", err)
	}

	fakeC := fakeClient.NewClientBuilder().
		WithScheme(fakeScheme).
		Build()

	eventRecorder := record.NewFakeRecorder(10)

	denylist := []string{"add", "add1"}
	r := &ReplicatedPolicyReconciler{
		Propagator: Propagator{
			Client:               fakeC,
			Scheme:               fakeScheme,
			Recorder:             eventRecorder,
			TemplateFuncDenylist: denylist,
		},
		ResourceVersions: &sync.Map{},
	}

	watcher := k8sdepwatches.ObjectIdentifier{
		Group:     policiesv1.GroupVersion.Group,
		Version:   policiesv1.GroupVersion.Version,
		Kind:      replicatedPolicy.Kind,
		Namespace: replicatedPolicy.GetNamespace(),
		Name:      replicatedPolicy.GetName(),
	}

	saNSName, resolveOptions := r.getTemplateResolverOpts(
		replicatedPolicy, rootPolicy, "managed-cluster", watcher,
	)

	expectedSA := types.NamespacedName{
		Namespace: "default",
		Name:      "hub-sa",
	}
	if saNSName != expectedSA {
		t.Errorf("Expected service account %v, got %v", expectedSA, saNSName)
	}

	if !reflect.DeepEqual(resolveOptions.DenylistFunctions, denylist) {
		t.Errorf("Expected denylist %v, got %v", denylist, resolveOptions.DenylistFunctions)
	}

	if resolveOptions.Watcher == nil {
		t.Error("Expected Watcher to be set in resolve options")
	}

	if len(resolveOptions.ClusterScopedAllowList) != 0 {
		t.Errorf("Expected empty cluster-scoped allow list for hub SA, got %d entries",
			len(resolveOptions.ClusterScopedAllowList))
	}

	if resolveOptions.LookupNamespace != "" {
		t.Errorf("Expected empty LookupNamespace for hub SA, got %s", resolveOptions.LookupNamespace)
	}
}
