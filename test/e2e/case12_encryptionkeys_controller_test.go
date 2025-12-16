// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"bytes"
	"encoding/base64"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test policy encryption key rotation", func() {
	key := bytes.Repeat([]byte{byte('A')}, 256/8)
	keyB64 := base64.StdEncoding.EncodeToString(key)
	previousKey := bytes.Repeat([]byte{byte('B')}, 256/8)

	rsrcPath := "../resources/case12_encryptionkeys_controller/"
	policyOneYaml := rsrcPath + "policy-one.yaml"
	policyTwoYaml := rsrcPath + "policy-two.yaml"
	policyOneName := "policy-one"
	policyTwoName := "policy-two"
	replicatedPolicyOneYaml := rsrcPath + "replicated-policy-one.yaml"
	replicatedPolicyOneName := "policy-propagator-test.policy-one"

	It("should create some sample policies", func(ctx SpecContext) {
		By("Creating the root policies with placement rules and bindings")
		utils.Kubectl("apply", "-f", policyOneYaml,
			"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
		rootOne := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, policyOneName, testNamespace, true, defaultTimeoutSeconds,
		)
		Expect(rootOne).NotTo(BeNil())

		utils.Kubectl("apply", "-f", policyTwoYaml,
			"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
		rootTwo := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, policyTwoName, testNamespace, true, defaultTimeoutSeconds,
		)
		Expect(rootTwo).NotTo(BeNil())

		By("Patching in the decision for policy-one")
		plrOne := utils.GetWithTimeout(
			clientHubDynamic, gvrPlacementRule, policyOneName+"-plr", testNamespace, true, defaultTimeoutSeconds,
		)
		plrOne.Object["status"] = utils.GeneratePlrStatus("managed1")
		_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
			ctx, plrOne, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())
		replicatedOne := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+policyOneName, "managed1", true, defaultTimeoutSeconds,
		)
		Expect(replicatedOne).ToNot(BeNil())
		opt := metav1.ListOptions{
			LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + policyOneName,
		}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)

		By("Patching in the decision for policy-two")
		plrTwo := utils.GetWithTimeout(
			clientHubDynamic, gvrPlacementRule, policyTwoName+"-plr", testNamespace, true, defaultTimeoutSeconds,
		)
		plrTwo.Object["status"] = utils.GeneratePlrStatus("managed1")
		_, err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
			ctx, plrTwo, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())
		replicatedTwo := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+policyTwoName, "managed1", true, defaultTimeoutSeconds,
		)
		Expect(replicatedTwo).ToNot(BeNil())
		opt = metav1.ListOptions{
			LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + policyTwoName,
		}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)

		By("Adding the IV Annotation to the replicated policy-one")
		utils.Kubectl("apply", "-n", "managed1",
			"-f", replicatedPolicyOneYaml, "--kubeconfig="+kubeconfigHub)

		Eventually(func() interface{} {
			replicatedPolicy := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, replicatedPolicyOneName, "managed1", true, defaultTimeoutSeconds,
			)

			return replicatedPolicy.GetAnnotations()
		}, defaultTimeoutSeconds, 1).Should(HaveKey(IVAnnotation))
	})

	It("should create a "+EncryptionKeySecret+" secret that needs a rotation", func(ctx SpecContext) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        EncryptionKeySecret,
				Namespace:   "managed1",
				Annotations: map[string]string{LastRotatedAnnotation: "2020-04-15T01:02:03Z"},
			},
			Data: map[string][]byte{"key": key, "previousKey": previousKey},
		}
		_, err := clientHub.CoreV1().Secrets("managed1").Create(ctx, secret, metav1.CreateOptions{})
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("should have rotated the key in the "+EncryptionKeySecret+" secret", func() {
		var secret *unstructured.Unstructured
		Eventually(func() interface{} {
			secret = utils.GetWithTimeout(
				clientHubDynamic,
				gvrSecret,
				EncryptionKeySecret,
				"managed1",
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

	It("should have triggered policies to be reprocessed", func(ctx SpecContext) {
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
			ctx, "policy-two", metav1.GetOptions{},
		)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(policy.GetAnnotations()[TriggerUpdateAnnotation]).Should(Equal(""))
	})

	It("clean up", func(ctx SpecContext) {
		err := clientHub.CoreV1().Secrets("managed1").Delete(
			ctx, EncryptionKeySecret, metav1.DeleteOptions{},
		)
		Expect(err).ShouldNot(HaveOccurred())

		for _, policyName := range []string{"policy-one", "policy-two"} {
			err = clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Delete(
				ctx, policyName, metav1.DeleteOptions{},
			)
			Expect(err).ShouldNot(HaveOccurred())
		}
	})
})
