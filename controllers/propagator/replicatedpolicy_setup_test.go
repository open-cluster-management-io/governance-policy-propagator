package propagator

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

func TestManagedClusterLifecycleReconcileEnqueuesReplicatedPolicies(t *testing.T) {
	fakeScheme := runtime.NewScheme()
	if err := policiesv1.AddToScheme(fakeScheme); err != nil {
		t.Fatalf("Failed to add policy scheme: %v", err)
	}

	replicatedPolicy := &policiesv1.Policy{ObjectMeta: metav1.ObjectMeta{
		Name:      "policies.example-policy",
		Namespace: "managed-cluster",
		Labels:    map[string]string{common.RootPolicyLabel: "policies.example-policy"},
	}}
	rootPolicy := &policiesv1.Policy{ObjectMeta: metav1.ObjectMeta{
		Name:      "example-policy",
		Namespace: "managed-cluster",
	}}
	otherClusterPolicy := &policiesv1.Policy{ObjectMeta: metav1.ObjectMeta{
		Name:      "policies.other-policy",
		Namespace: "another-cluster",
		Labels:    map[string]string{common.RootPolicyLabel: "policies.other-policy"},
	}}

	fake := fakeClient.NewClientBuilder().WithScheme(fakeScheme).
		WithObjects(replicatedPolicy, rootPolicy, otherClusterPolicy).Build()
	updates := make(chan event.GenericEvent, 2)
	r := &ManagedClusterPolicyReconciler{Client: fake, ReplicatedPolicyUpdates: updates}

	_, err := r.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{
		Name: "managed-cluster",
	}})
	if err != nil {
		t.Fatalf("Reconcile returned an unexpected error: %v", err)
	}

	if len(updates) != 1 {
		t.Fatalf("got %d reconcile events, want 1", len(updates))
	}

	update := <-updates

	if update.Object.GetNamespace() != "managed-cluster" || update.Object.GetName() != "policies.example-policy" {
		t.Errorf("got reconcile event for %s/%s", update.Object.GetNamespace(), update.Object.GetName())
	}
}

func TestManagedClusterLifecycleReconcileReturnsListErrorForRetry(t *testing.T) {
	listErr := errors.New("temporary list failure")
	r := &ManagedClusterPolicyReconciler{
		Client:                  failingListClient{err: listErr},
		ReplicatedPolicyUpdates: make(chan event.GenericEvent, 1),
	}

	_, err := r.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{
		Name: "managed-cluster",
	}})
	if !errors.Is(err, listErr) {
		t.Fatalf("Reconcile error = %v, want %v", err, listErr)
	}
}

type failingListClient struct {
	client.Client
	err error
}

func (c failingListClient) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return c.err
}

func TestManagedClusterLifecycleChanges(t *testing.T) {
	pred := managedClusterLifecycleChanges()

	t.Run("create", func(t *testing.T) {
		if !pred.Create(event.CreateEvent{}) {
			t.Error("create event did not trigger reconciliation")
		}
	})

	t.Run("delete", func(t *testing.T) {
		if !pred.Delete(event.DeleteEvent{}) {
			t.Error("delete event did not trigger reconciliation")
		}
	})

	t.Run("UID changed", func(t *testing.T) {
		oldCluster := &clusterv1.ManagedCluster{ObjectMeta: metav1.ObjectMeta{UID: "old"}}
		newCluster := &clusterv1.ManagedCluster{ObjectMeta: metav1.ObjectMeta{UID: "new"}}

		if !pred.Update(event.UpdateEvent{ObjectOld: oldCluster, ObjectNew: newCluster}) {
			t.Error("UID update did not trigger reconciliation")
		}
	})

	t.Run("other update", func(t *testing.T) {
		oldCluster := &clusterv1.ManagedCluster{ObjectMeta: metav1.ObjectMeta{UID: "same"}}
		newCluster := &clusterv1.ManagedCluster{ObjectMeta: metav1.ObjectMeta{UID: "same"}}

		if pred.Update(event.UpdateEvent{ObjectOld: oldCluster, ObjectNew: newCluster}) {
			t.Error("non-UID update triggered reconciliation")
		}
	})
}
