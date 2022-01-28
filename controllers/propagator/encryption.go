// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"

	"github.com/stolostron/go-template-utils/v2/pkg/templates"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const ivAnnotation = "policy.open-cluster-management.io/encryption-iv"

// EncryptionKeyCache acts as a cache for encryption keys for each managed cluster for policy template encryption.
// This abstracts locking and unlocking operations to account for concurrency.
type EncryptionKeyCache struct {
	cache map[string][]byte
	mutex sync.RWMutex
}

// Get will return the key for the managed cluster in the cache. If it's not set, a nil value is returned.
func (c *EncryptionKeyCache) Get(clusterName string) []byte {
	c.mutex.RLock()

	key := c.cache[clusterName]

	c.mutex.RUnlock()

	return key
}

// Set will store the key for the managed cluster in the cache.
func (c *EncryptionKeyCache) Set(clusterName string, key []byte) {
	c.mutex.Lock()

	// Initialize the map if it's nil.
	if c.cache == nil {
		c.cache = map[string][]byte{}
	}

	c.cache[clusterName] = key

	c.mutex.Unlock()
}

// getEncryptionKey will get the encryption key for a managed cluster used for policy template encryption. If it doesn't
// already exist as a secret on the Hub cluster, it will be generated. All retrieved keys are cached.
func (r *PolicyReconciler) getEncryptionKey(clusterName string) ([]byte, error) {
	// #nosec G101
	const secretName = "policy-encryption-key"

	key := r.encryptionKeyCache.Get(clusterName)
	if key != nil {
		log.V(2).Info("Using the cached encryption key", "cluster", clusterName)

		return key, nil
	}

	ctx := context.TODO()
	objectKey := types.NamespacedName{
		Name:      secretName,
		Namespace: clusterName,
	}
	encryptionSecret := &corev1.Secret{}

	err := r.Get(ctx, objectKey, encryptionSecret)
	if k8serrors.IsNotFound(err) {
		const keySize = 256

		log.V(1).Info(
			"Generating an encryption key for policy templates that will be stored in a secret",
			"cluster", clusterName,
			"name", secretName,
			"namespace", clusterName,
		)

		key := make([]byte, keySize/8)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("failed to generate an AES-256 key: %w", err)
		}

		encryptionSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: clusterName,
				// This is required for disaster recovery.
				Labels: map[string]string{"cluster.open-cluster-management.io/backup": "policy"},
			},
			Data: map[string][]byte{
				"key": key,
			},
		}

		err = r.Create(ctx, encryptionSecret)
		if err != nil {
			return nil, fmt.Errorf("failed to create the Secret %s/%s: %w", clusterName, secretName, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("failed to get the Secret %s/%s: %w", clusterName, secretName, err)
	}

	key = encryptionSecret.Data["key"]
	r.encryptionKeyCache.Set(clusterName, key)

	return key, nil
}

// getInitializationVector retrieves the initialization vector from the annotation
// "policy.open-cluster-management.io/encryption-iv" if the annotation exists or generates a new
// initialization vector and adds it to the annotations object if it's missing.
func (r *PolicyReconciler) getInitializationVector(
	policyName string, clusterName string, annotations map[string]string,
) ([]byte, error) {
	log := log.WithValues("policy", policyName, "cluster", clusterName)

	if initializationVector, ok := annotations[ivAnnotation]; ok {
		log.V(2).Info("Found initialization vector annotation")

		decodedVector, err := base64.StdEncoding.DecodeString(initializationVector)
		if err == nil {
			if len(decodedVector) == templates.IVSize {
				return decodedVector, nil
			}
		}

		log.V(2).Info("The initialization vector failed validation")
	}

	log.V(2).Info("Generating initialization vector annotation")

	initializationVector := make([]byte, templates.IVSize)

	_, err := rand.Read(initializationVector)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to generate the initialization vector for cluster %s for policy %s: %w",
			clusterName, policyName, err,
		)
	}

	annotations[ivAnnotation] = base64.StdEncoding.EncodeToString(initializationVector)

	return initializationVector, nil
}
