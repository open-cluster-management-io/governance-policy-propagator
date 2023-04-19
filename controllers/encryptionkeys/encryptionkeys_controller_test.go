// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package encryptionkeys

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/controllers/propagator"
)

const (
	keySize     = 256
	clusterName = "local-cluster"
	day         = time.Hour * 24
)

type erroringFakeClient struct {
	client.Client
	GetError    bool
	ListError   bool
	PatchError  bool
	UpdateError bool
}

func (c *erroringFakeClient) Get(
	ctx context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption,
) error {
	if c.GetError {
		return errors.New("some get error")
	}

	return c.Client.Get(ctx, key, obj)
}

func (c *erroringFakeClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if c.ListError {
		return errors.New("some list error")
	}

	return c.Client.List(ctx, list, opts...)
}

func (c *erroringFakeClient) Patch(
	ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption,
) error {
	if c.PatchError {
		return errors.New("some patch error")
	}

	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *erroringFakeClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if c.UpdateError {
		return errors.New("some update error")
	}

	return c.Client.Update(ctx, obj, opts...)
}

func generateSecret() *corev1.Secret {
	key := make([]byte, keySize/8)
	_, err := rand.Read(key)
	Expect(err).ToNot(HaveOccurred())

	encryptionSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      propagator.EncryptionKeySecret,
			Namespace: clusterName,
		},
		Data: map[string][]byte{"key": key},
	}

	prevKey := make([]byte, keySize/8)
	_, err = rand.Read(prevKey)
	Expect(err).ToNot(HaveOccurred())

	encryptionSecret.Data["previousKey"] = prevKey

	return encryptionSecret
}

func generatePolicies() []client.Object {
	return []client.Object{
		client.Object(
			&v1.Policy{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "default.policy-one",
					Namespace:   clusterName,
					Annotations: map[string]string{propagator.IVAnnotation: "7cznVUq5SXEE4RMZNkGOrQ=="},
					Labels:      map[string]string{common.RootPolicyLabel: "default.policy-one"},
				},
			},
		),
		client.Object(
			&v1.Policy{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "policy-one",
					Namespace:   "default",
					Annotations: map[string]string{},
				},
			},
		),
		client.Object(
			&v1.Policy{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "default.policy-two",
					Namespace:   clusterName,
					Annotations: map[string]string{},
					Labels:      map[string]string{common.RootPolicyLabel: "default.policy-two"},
				},
			},
		),
		client.Object(
			&v1.Policy{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "policy-two",
					Namespace:   "default",
					Annotations: map[string]string{},
				},
			},
		),
		client.Object(
			&v1.Policy{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "default.policy-three",
					Namespace:   "some-other-cluster",
					Annotations: map[string]string{propagator.IVAnnotation: "7cznVUq5SXEE4RMZNkGOrQ=="},
					Labels:      map[string]string{common.RootPolicyLabel: "default.policy-three"},
				},
			},
		),
		client.Object(
			&v1.Policy{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "policy-three",
					Namespace:   "default",
					Annotations: map[string]string{},
				},
			},
		),
		client.Object(
			&v1.Policy{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "default.policy-four",
					Namespace:   clusterName,
					Annotations: map[string]string{propagator.IVAnnotation: "7cznVUq5SXEE4RMZNkGOrQ=="},
					Labels:      map[string]string{common.RootPolicyLabel: "default.policy-four"},
				},
			},
		),
		client.Object(
			&v1.Policy{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "policy-four",
					Namespace:   "default",
					Annotations: map[string]string{},
				},
			},
		),
	}
}

func getRequeueAfterDays(result reconcile.Result) int {
	return int(result.RequeueAfter.Round(day) / (day))
}

func getReconciler(encryptionSecret *corev1.Secret) *EncryptionKeysReconciler {
	policies := generatePolicies()

	scheme := k8sruntime.NewScheme()
	err := clientgoscheme.AddToScheme(scheme)
	Expect(err).ToNot(HaveOccurred())
	err = v1.AddToScheme(scheme)
	Expect(err).ToNot(HaveOccurred())

	builder := fake.NewClientBuilder().WithObjects(policies...).WithScheme(scheme)

	if encryptionSecret != nil {
		builder = builder.WithObjects(encryptionSecret)
	}

	client := builder.Build()

	return &EncryptionKeysReconciler{
		Client:                  client,
		KeyRotationDays:         30,
		MaxConcurrentReconciles: 1,
		Scheme:                  scheme,
	}
}

func assertTriggerUpdate(r *EncryptionKeysReconciler) {
	policyList := v1.PolicyList{}
	err := r.List(context.TODO(), &policyList)
	Expect(err).ToNot(HaveOccurred())

	for _, policy := range policyList.Items {
		annotation := policy.Annotations[propagator.TriggerUpdateAnnotation]

		if policy.ObjectMeta.Name == "policy-one" || policy.ObjectMeta.Name == "policy-four" {
			expectedPrefix := fmt.Sprintf("rotate-key-%s-", clusterName)
			Expect(strings.HasPrefix(annotation, expectedPrefix)).To(BeTrue())
		} else {
			Expect(annotation).To(Equal(""))
		}
	}
}

func assertNoTriggerUpdate(r *EncryptionKeysReconciler) {
	policyList := v1.PolicyList{}
	err := r.List(context.TODO(), &policyList)
	Expect(err).ToNot(HaveOccurred())

	for _, policy := range policyList.Items {
		annotation := policy.Annotations[propagator.TriggerUpdateAnnotation]
		Expect(annotation).To(Equal(""))
	}
}

func TestReconcileRotateKey(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)

	tests := []struct{ Annotation string }{{""}, {"2020-04-15T01:02:03Z"}, {"not-a-timestamp"}}

	for _, test := range tests {
		test := test

		t.Run(
			fmt.Sprintf(`annotation="%s"`, test.Annotation),
			func(t *testing.T) {
				t.Parallel()

				encryptionSecret := generateSecret()
				if test.Annotation != "" {
					annotations := map[string]string{propagator.LastRotatedAnnotation: test.Annotation}
					encryptionSecret.SetAnnotations(annotations)
				}
				originalKey := encryptionSecret.Data["key"]

				r := getReconciler(encryptionSecret)

				secretID := types.NamespacedName{
					Namespace: clusterName, Name: propagator.EncryptionKeySecret,
				}
				request := ctrl.Request{NamespacedName: secretID}
				result, err := r.Reconcile(context.TODO(), request)

				Expect(err).ToNot(HaveOccurred())
				Expect(result.Requeue).To(BeFalse())
				Expect(getRequeueAfterDays(result)).To(Equal(30))

				err = r.Get(context.TODO(), secretID, encryptionSecret)
				Expect(err).ToNot(HaveOccurred())
				Expect(bytes.Equal(encryptionSecret.Data["key"], originalKey)).To(BeFalse())
				Expect(bytes.Equal(encryptionSecret.Data["previousKey"], originalKey)).To(BeTrue())

				assertTriggerUpdate(r)
			},
		)
	}
}

func TestReconcileNoRotation(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)

	encryptionSecret := generateSecret()
	now := time.Now().UTC().Format(time.RFC3339)

	encryptionSecret.SetAnnotations(map[string]string{propagator.LastRotatedAnnotation: now})

	originalKey := encryptionSecret.Data["key"]

	r := getReconciler(encryptionSecret)

	secretID := types.NamespacedName{
		Namespace: clusterName, Name: propagator.EncryptionKeySecret,
	}
	request := ctrl.Request{NamespacedName: secretID}
	result, err := r.Reconcile(context.TODO(), request)

	Expect(err).ToNot(HaveOccurred())
	Expect(getRequeueAfterDays(result)).To(Equal(30))

	err = r.Get(context.TODO(), secretID, encryptionSecret)
	Expect(err).ToNot(HaveOccurred())
	Expect(bytes.Equal(encryptionSecret.Data["key"], originalKey)).To(BeTrue())
	Expect(bytes.Equal(encryptionSecret.Data["previousKey"], originalKey)).To(BeFalse())

	assertNoTriggerUpdate(r)
}

func TestReconcileNotFound(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)

	r := getReconciler(nil)

	secretID := types.NamespacedName{
		Namespace: clusterName, Name: propagator.EncryptionKeySecret,
	}
	request := ctrl.Request{NamespacedName: secretID}
	result, err := r.Reconcile(context.TODO(), request)

	Expect(err).ToNot(HaveOccurred())
	Expect(result.RequeueAfter).To(Equal(time.Duration(0)))

	policyList := v1.PolicyList{}
	err = r.List(context.TODO(), &policyList)
	Expect(err).ToNot(HaveOccurred())

	for _, policy := range policyList.Items {
		annotation := policy.Annotations[propagator.TriggerUpdateAnnotation]
		Expect(annotation).To(Equal(""))
	}
}

func TestReconcileManualRotation(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)

	encryptionSecret := generateSecret()
	annotations := map[string]string{
		"policy.open-cluster-management.io/disable-rotation": "true",
		propagator.LastRotatedAnnotation:                     "2020-04-15T01:02:03Z",
	}
	encryptionSecret.SetAnnotations(annotations)
	originalKey := encryptionSecret.Data["key"]
	originalPrevKey := encryptionSecret.Data["previousKey"]

	r := getReconciler(encryptionSecret)

	secretID := types.NamespacedName{
		Namespace: clusterName, Name: propagator.EncryptionKeySecret,
	}
	request := ctrl.Request{NamespacedName: secretID}
	result, err := r.Reconcile(context.TODO(), request)

	Expect(err).ToNot(HaveOccurred())
	Expect(result.Requeue).To(BeFalse())
	Expect(result.RequeueAfter).To(Equal(time.Duration(0)))

	err = r.Get(context.TODO(), secretID, encryptionSecret)
	Expect(err).ToNot(HaveOccurred())
	Expect(bytes.Equal(encryptionSecret.Data["key"], originalKey)).To(BeTrue())
	Expect(bytes.Equal(encryptionSecret.Data["previousKey"], originalPrevKey)).To(BeTrue())

	assertTriggerUpdate(r)
}

func TestReconcileInvalidKey(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)

	encryptionSecret := generateSecret()
	encryptionSecret.Data["key"] = []byte("not-a-key")
	originalKey := encryptionSecret.Data["key"]

	now := time.Now().UTC().Format(time.RFC3339)

	encryptionSecret.SetAnnotations(map[string]string{propagator.LastRotatedAnnotation: now})

	r := getReconciler(encryptionSecret)

	secretID := types.NamespacedName{
		Namespace: clusterName, Name: propagator.EncryptionKeySecret,
	}
	request := ctrl.Request{NamespacedName: secretID}
	result, err := r.Reconcile(context.TODO(), request)

	Expect(err).ToNot(HaveOccurred())
	Expect(result.Requeue).To(BeFalse())
	Expect(getRequeueAfterDays(result)).To(Equal(30))

	err = r.Get(context.TODO(), secretID, encryptionSecret)
	Expect(err).ToNot(HaveOccurred())
	Expect(bytes.Equal(encryptionSecret.Data["key"], originalKey)).To(BeFalse())
	Expect(encryptionSecret.Data["previousKey"]).To(BeEmpty())

	assertTriggerUpdate(r)
}

func TestReconcileInvalidPreviousKey(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)

	encryptionSecret := generateSecret()
	encryptionSecret.Data["previousKey"] = []byte("not-a-key")
	originalKey := encryptionSecret.Data["key"]

	now := time.Now().UTC().Format(time.RFC3339)

	encryptionSecret.SetAnnotations(map[string]string{propagator.LastRotatedAnnotation: now})

	r := getReconciler(encryptionSecret)

	secretID := types.NamespacedName{
		Namespace: clusterName, Name: propagator.EncryptionKeySecret,
	}
	request := ctrl.Request{NamespacedName: secretID}
	result, err := r.Reconcile(context.TODO(), request)

	Expect(err).ToNot(HaveOccurred())
	Expect(result.Requeue).To(BeFalse())
	Expect(getRequeueAfterDays(result)).To(Equal(30))

	err = r.Get(context.TODO(), secretID, encryptionSecret)
	Expect(err).ToNot(HaveOccurred())
	Expect(bytes.Equal(encryptionSecret.Data["key"], originalKey)).To(BeTrue())
	Expect(encryptionSecret.Data["previousKey"]).To(BeEmpty())

	assertNoTriggerUpdate(r)
}

func TestReconcileSecretNotFiltered(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)

	randomSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "random-secret",
			Namespace: clusterName,
		},
	}

	r := getReconciler(randomSecret)

	secretID := types.NamespacedName{Namespace: clusterName, Name: "random-secret"}
	request := ctrl.Request{NamespacedName: secretID}
	result, err := r.Reconcile(context.TODO(), request)

	Expect(err).ToNot(HaveOccurred())
	Expect(result.Requeue).To(BeFalse())
	Expect(result.RequeueAfter).To(Equal(time.Duration(0)))

	assertNoTriggerUpdate(r)
}

func TestReconcileAPIFails(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)

	originalRetries := retries
	retries = 0

	t.Cleanup(func() { retries = originalRetries })

	tests := []struct {
		ExpectedRotation bool
		GetError         bool
		ListError        bool
		Name             string
		PatchError       bool
		UpdateError      bool
	}{
		{ExpectedRotation: false, GetError: true, Name: "get-secret"},
		{ExpectedRotation: false, UpdateError: true, Name: "update-secret"},
		{ExpectedRotation: true, ListError: true, Name: "list-policy"},
		{ExpectedRotation: true, PatchError: true, Name: "patch-policy"},
	}

	for _, test := range tests {
		test := test
		t.Run(
			test.Name,
			func(t *testing.T) {
				t.Parallel()
				encryptionSecret := generateSecret()
				originalKey := encryptionSecret.Data["key"]
				r := getReconciler(encryptionSecret)
				erroringClient := erroringFakeClient{
					Client:      r.Client,
					GetError:    test.GetError,
					ListError:   test.ListError,
					PatchError:  test.PatchError,
					UpdateError: test.UpdateError,
				}
				r.Client = &erroringClient

				secretID := types.NamespacedName{Namespace: clusterName, Name: propagator.EncryptionKeySecret}
				request := ctrl.Request{NamespacedName: secretID}
				result, err := r.Reconcile(context.TODO(), request)

				if !test.ExpectedRotation {
					Expect(err).Should(HaveOccurred())
					Expect(result.RequeueAfter).Should(Equal(time.Duration(0)))
				} else {
					Expect(err).ShouldNot(HaveOccurred())
					Expect(result.RequeueAfter).ShouldNot(Equal(time.Duration(0)))
				}

				Expect(result.Requeue).To(BeFalse())

				// Revert back the fake client to verify the secret and that no policy updates were triggered
				r.Client = erroringClient.Client

				err = r.Get(context.TODO(), client.ObjectKeyFromObject(encryptionSecret), encryptionSecret)
				Expect(err).ShouldNot(HaveOccurred())

				if test.ExpectedRotation {
					Expect(bytes.Equal(originalKey, encryptionSecret.Data["key"])).Should(BeFalse())
				} else {
					Expect(bytes.Equal(originalKey, encryptionSecret.Data["key"])).Should(BeTrue())
				}

				// Revert back the fake client to verify no policy updates were triggered
				r.Client = erroringClient.Client
				assertNoTriggerUpdate(r)
			},
		)
	}
}
