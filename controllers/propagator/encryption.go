// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/stolostron/go-template-utils/v3/pkg/templates"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	// #nosec G101
	EncryptionKeySecret   = "policy-encryption-key"
	IVAnnotation          = "policy.open-cluster-management.io/encryption-iv"
	LastRotatedAnnotation = "policy.open-cluster-management.io/last-rotated"
)

// getEncryptionKey will get the encryption key for a managed cluster used for policy template encryption. If it doesn't
// already exist as a secret on the Hub cluster, it will be generated.
func (r *PolicyReconciler) getEncryptionKey(clusterName string) ([]byte, error) {
	ctx := context.TODO()
	objectKey := types.NamespacedName{
		Name:      EncryptionKeySecret,
		Namespace: clusterName,
	}
	encryptionSecret := &corev1.Secret{}

	// Since there is a controller that is watching the policy-encryption-key secrets, this secret
	// will always be cached by controller-runtime.
	err := r.Get(ctx, objectKey, encryptionSecret)
	if k8serrors.IsNotFound(err) {
		log.V(1).Info(
			"Generating an encryption key for policy templates that will be stored in a secret",
			"cluster", clusterName,
			"name", EncryptionKeySecret,
			"namespace", clusterName,
		)

		key, err := GenerateEncryptionKey()
		if err != nil {
			return nil, err
		}

		encryptionSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      EncryptionKeySecret,
				Namespace: clusterName,
				// This is required for disaster recovery.
				Labels: map[string]string{"cluster.open-cluster-management.io/backup": "policy"},
				Annotations: map[string]string{
					LastRotatedAnnotation: time.Now().Format(time.RFC3339),
				},
			},
			Data: map[string][]byte{
				"key": key,
			},
		}

		err = r.Create(ctx, encryptionSecret)
		if err != nil {
			return nil, fmt.Errorf("failed to create the Secret %s/%s: %w", clusterName, EncryptionKeySecret, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("failed to get the Secret %s/%s: %w", clusterName, EncryptionKeySecret, err)
	}

	return encryptionSecret.Data["key"], nil
}

func GenerateEncryptionKey() ([]byte, error) {
	const keySize = 256
	key := make([]byte, keySize/8)

	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate an AES-256 key: %w", err)
	}

	return key, nil
}

// getInitializationVector retrieves the initialization vector from the annotation
// "policy.open-cluster-management.io/encryption-iv" if the annotation exists or generates a new
// initialization vector and adds it to the annotations object if it's missing.
func (r *PolicyReconciler) getInitializationVector(
	policyName string, clusterName string, annotations map[string]string,
) ([]byte, error) {
	log := log.WithValues("policy", policyName, "cluster", clusterName)

	if initializationVector, ok := annotations[IVAnnotation]; ok {
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

	annotations[IVAnnotation] = base64.StdEncoding.EncodeToString(initializationVector)

	return initializationVector, nil
}
