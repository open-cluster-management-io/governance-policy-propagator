// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"crypto/rand"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	clusterName = "local-cluster"
	keySize     = 256
	secretName  = "policy-encryption-key"
)

func TestEncryptionKeyCache(t *testing.T) {
	RegisterFailHandler(Fail)

	cache := EncryptionKeyCache{}

	cache.Set(clusterName, []byte{byte('A')})
	value := cache.Get(clusterName)
	Expect(value).To(Equal([]byte{byte('A')}))
}

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
		Name:      secretName,
		Namespace: clusterName,
	}
	encryptionSecret := &corev1.Secret{}
	err = client.Get(ctx, objectKey, encryptionSecret)

	Expect(err).To(BeNil())
	// Verify that the generated key stored in the secret is 256 bits.
	Expect(len(encryptionSecret.Data["key"])).To(Equal(keySize / 8))

	// Check that the value is cached.
	Expect(len(r.encryptionKeyCache.cache[clusterName])).To(Equal(keySize / 8))
}

func TestGetEncryptionKeySecretExists(t *testing.T) {
	RegisterFailHandler(Fail)

	// Generate an AES-256 key and stored it as a secret.
	key := make([]byte, keySize/8)
	_, err := rand.Read(key)
	Expect(err).To(BeNil())

	encryptionSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
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

	// Check that the value is cached.
	Expect(len(r.encryptionKeyCache.cache[clusterName])).To(Equal(keySize / 8))
}

func TestGetEncryptionKeyCached(t *testing.T) {
	RegisterFailHandler(Fail)

	client := fake.NewClientBuilder().Build()

	r := PolicyReconciler{Client: client}
	r.encryptionKeyCache.cache = map[string][]byte{clusterName: {byte('A')}}

	key, err := r.getEncryptionKey(clusterName)

	Expect(err).To(BeNil())
	Expect(string(key[0])).To(Equal("A"))
}
