// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	EncryptionKeySecret     = "policy-encryption-key"
	LastRotatedAnnotation   = "policy.open-cluster-management.io/last-rotated"
	RootPolicyLabel         = "policy.open-cluster-management.io/root-policy"
	TriggerUpdateAnnotation = "policy.open-cluster-management.io/trigger-update"
)

var _ = Describe("Test policy encryption key rotation", func() {
	key := bytes.Repeat([]byte{byte('A')}, 256/8)
	keyB64 := base64.StdEncoding.EncodeToString(key)
	previousKey := bytes.Repeat([]byte{byte('B')}, 256/8)

	It("should create a some sample policies", func() {
		policyConfigs := []struct {
			Name        string
			IVAnnoation string
			RootPolicy  bool
		}{
			// This policy should get the triggered update annotation
			{"policy-one", "", true},
			{testNamespace + ".policy-one", "7cznVUq5SXEE4RMZNkGOrQ==", false},
			{"policy-two", "", true},
			{testNamespace + ".policy-two", "", false},
		}

		for _, policyConf := range policyConfigs {
			annotations := map[string]interface{}{}
			if policyConf.IVAnnoation != "" {
				annotations[IVAnnotation] = policyConf.IVAnnoation
			}

			labels := map[string]interface{}{}
			if !policyConf.RootPolicy {
				labels[RootPolicyLabel] = policyConf.Name
			}

			policy := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "policy.open-cluster-management.io/v1",
					"kind":       "Policy",
					"metadata": map[string]interface{}{
						"annotations": annotations,
						"labels":      labels,
						"name":        policyConf.Name,
						"namespace":   testNamespace,
					},
					"spec": map[string]interface{}{
						"disabled": false,
						"policy-templates": []map[string]interface{}{
							{"objectDefinition": map[string]interface{}{}},
						},
					},
				},
			}
			_, err := clientHubDynamic.Resource(gvrPolicy).
				Namespace(testNamespace).
				Create(context.TODO(), policy, metav1.CreateOptions{})
			Expect(err).ShouldNot(HaveOccurred())
		}
	})

	It("should create a "+EncryptionKeySecret+" secret that needs a rotation", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        EncryptionKeySecret,
				Namespace:   testNamespace,
				Annotations: map[string]string{LastRotatedAnnotation: "2020-04-15T01:02:03Z"},
			},
			Data: map[string][]byte{"key": key, "previousKey": previousKey},
		}
		_, err := clientHub.CoreV1().Secrets(testNamespace).Create(context.TODO(), secret, metav1.CreateOptions{})
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("should have rotated the key in the "+EncryptionKeySecret+" secret", func() {
		var secret *unstructured.Unstructured
		Eventually(func() interface{} {
			secret = utils.GetWithTimeout(
				clientHubDynamic,
				gvrSecret,
				EncryptionKeySecret,
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)

			data, ok := secret.Object["data"].(map[string]interface{})
			if !ok {
				return ""
			}

			currentKey, ok := data["key"].(string)
			if !ok {
				return ""
			}

			return currentKey
		}, defaultTimeoutSeconds, 1).ShouldNot(Equal(keyB64))

		currentPrevKey, ok := secret.Object["data"].(map[string]interface{})["previousKey"].(string)
		Expect(ok).Should(BeTrue())
		Expect(currentPrevKey).Should(Equal(keyB64))
	})

	It("should have triggered policies to be reprocessed", func() {
		Eventually(func() interface{} {
			policy := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				"policy-one",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)

			return strings.HasPrefix(policy.GetAnnotations()[TriggerUpdateAnnotation], "rotate-key-")
		}, defaultTimeoutSeconds, 1).Should(BeTrue())

		policy, err := clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Get(
			context.TODO(), "policy-two", metav1.GetOptions{},
		)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(policy.GetAnnotations()[TriggerUpdateAnnotation]).Should(Equal(""))
	})

	It("clean up", func() {
		err := clientHub.CoreV1().Secrets(testNamespace).Delete(
			context.TODO(), EncryptionKeySecret, metav1.DeleteOptions{},
		)
		Expect(err).ShouldNot(HaveOccurred())

		for _, policyName := range []string{"policy-one", "policy-two"} {
			err = clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Delete(
				context.TODO(), policyName, metav1.DeleteOptions{},
			)
			Expect(err).ShouldNot(HaveOccurred())
		}
	})
})
