// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stolostron/go-template-utils/v2/pkg/templates"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	policyName  = "test-policy"
	clusterName = "local-cluster"
	keySize     = 256
)

func TestGetEncryptionKeyNoSecret(t *testing.T) {
	RegisterFailHandler(Fail)

	client := fake.NewClientBuilder().Build()
	r := PolicyReconciler{Client: client}
	key, err := r.getEncryptionKey(clusterName)

	Expect(err).To(BeNil())
	// Verify that the generated key is 256 bits.
	Expect(len(key)).To(Equal(keySize / 8))

	ctx := context.TODO()
	objectKey := types.NamespacedName{
		Name:      EncryptionKeySecret,
		Namespace: clusterName,
	}
	encryptionSecret := &corev1.Secret{}
	err = client.Get(ctx, objectKey, encryptionSecret)

	Expect(err).To(BeNil())
	// Verify that the generated key stored in the secret is 256 bits.
	Expect(len(encryptionSecret.Data["key"])).To(Equal(keySize / 8))
}

func TestGetEncryptionKeySecretExists(t *testing.T) {
	RegisterFailHandler(Fail)

	// Generate an AES-256 key and stored it as a secret.
	key := make([]byte, keySize/8)
	_, err := rand.Read(key)
	Expect(err).To(BeNil())

	encryptionSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      EncryptionKeySecret,
			Namespace: clusterName,
		},
		Data: map[string][]byte{
			"key": key,
		},
	}

	client := fake.NewClientBuilder().WithObjects(encryptionSecret).Build()

	r := PolicyReconciler{Client: client}
	key, err = r.getEncryptionKey(clusterName)

	Expect(err).To(BeNil())
	// Verify that the returned key is 256 bits.
	Expect(len(key)).To(Equal(keySize / 8))
}

func TestGetInitializationVector(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)

	// Test when the initialization vector is generated
	tests := []struct {
		description string
		annotations map[string]string
	}{
		{
			"No IV",
			map[string]string{},
		},
		{
			"Valid IV",
			map[string]string{
				IVAnnotation: "7cznVUq5SXEE4RMZNkGOrQ==",
			},
		},
		{
			"Invalid IV",
			map[string]string{
				IVAnnotation: "this-is-invalid",
			},
		},
	}

	r := PolicyReconciler{}

	for _, test := range tests {
		subTest := test
		t.Run(
			test.description,
			func(t *testing.T) {
				t.Parallel()
				initializationVector, err := r.getInitializationVector(policyName, clusterName, subTest.annotations)

				Expect(err).To(BeNil())
				// Verify that the returned initialization vector is 128 bits
				Expect(len(initializationVector)).To(Equal(templates.IVSize))
				// Verify that the annotation object was updated
				Expect(
					subTest.annotations[IVAnnotation],
				).To(Equal(
					base64.StdEncoding.EncodeToString(initializationVector),
				))
			},
		)
	}
}
