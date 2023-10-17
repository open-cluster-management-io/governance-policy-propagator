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
	"github.com/stolostron/go-template-utils/v4/pkg/templates"
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

func TestGetEncryptionKeyNoSecret(_ *testing.T) {
	RegisterFailHandler(Fail)

	client := fake.NewClientBuilder().Build()
	r := Propagator{Client: client}
	key, err := r.getEncryptionKey(clusterName)

	Expect(err).ToNot(HaveOccurred())
	// Verify that the generated key is 256 bits.
	Expect(key).To(HaveLen(keySize / 8))

	ctx := context.TODO()
	objectKey := types.NamespacedName{
		Name:      EncryptionKeySecret,
		Namespace: clusterName,
	}
	encryptionSecret := &corev1.Secret{}
	err = client.Get(ctx, objectKey, encryptionSecret)

	Expect(err).ToNot(HaveOccurred())
	// Verify that the generated key stored in the secret is 256 bits.
	Expect(encryptionSecret.Data["key"]).To(HaveLen(keySize / 8))
}

func TestGetEncryptionKeySecretExists(_ *testing.T) {
	RegisterFailHandler(Fail)

	// Generate an AES-256 key and stored it as a secret.
	key := make([]byte, keySize/8)
	_, err := rand.Read(key)
	Expect(err).ToNot(HaveOccurred())

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

	r := Propagator{Client: client}
	key, err = r.getEncryptionKey(clusterName)

	Expect(err).ToNot(HaveOccurred())
	// Verify that the returned key is 256 bits.
	Expect(key).To(HaveLen(keySize / 8))
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

	r := Propagator{}

	for _, test := range tests {
		subTest := test
		t.Run(
			test.description,
			func(t *testing.T) {
				t.Parallel()
				initializationVector, err := r.getInitializationVector(policyName, clusterName, subTest.annotations)

				Expect(err).ToNot(HaveOccurred())
				// Verify that the returned initialization vector is 128 bits
				Expect(initializationVector).To(HaveLen(templates.IVSize))
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
