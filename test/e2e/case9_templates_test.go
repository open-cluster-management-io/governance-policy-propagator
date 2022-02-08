// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case9PolicyName              = "case9-test-policy"
	case9PolicyYaml              = "../resources/case9_templates/case9-test-policy.yaml"
	case9ReplicatedPolicyYamlM1  = "../resources/case9_templates/case9-test-replpolicy-managed1.yaml"
	case9ReplicatedPolicyYamlM2  = "../resources/case9_templates/case9-test-replpolicy-managed2.yaml"
	case9PolicyNameEncrypted     = "case9-test-policy-encrypted"
	case9PolicyYamlEncrypted     = "../resources/case9_templates/case9-test-policy_encrypted.yaml"
	case9PolicyYamlEncryptedRepl = "../resources/case9_templates/case9-test-replpolicy_encrypted-"
	case9EncryptionSecret        = "../resources/case9_templates/case9-test-encryption-secret.yaml"
	case9EncryptionSecretName    = "policy-encryption-key"
	IVAnnotation                 = "policy.open-cluster-management.io/encryption-iv"
)

var _ = Describe("Test policy templates", func() {
	Describe("Create policy, placement and referenced resource in ns:"+testNamespace, func() {
		It("should be created in user ns", func() {
			By("Creating " + case9PolicyYaml)
			utils.Kubectl("apply",
				"-f", case9PolicyYaml,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case9PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		It("should resolve templates and propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case9PolicyName+"-plr", testNamespace,
				true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case9PolicyName, "managed1",
				true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())

			yamlPlc := utils.ParseYaml(case9ReplicatedPolicyYamlM1)
			Eventually(func() interface{} {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic,
					gvrPolicy,
					testNamespace+"."+case9PolicyName,
					"managed1",
					true,
					defaultTimeoutSeconds,
				)

				return replicatedPlc.Object["spec"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
		})
		It("should resolve templates and propagate to cluster ns managed2", func() {
			By("Patching test-policy-plr with decision of cluster managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case9PolicyName+"-plr", testNamespace,
				true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case9PolicyName, "managed2",
				true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())

			yamlPlc := utils.ParseYaml(case9ReplicatedPolicyYamlM2)
			Eventually(func() interface{} {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic,
					gvrPolicy,
					testNamespace+"."+case9PolicyName,
					"managed2",
					true,
					defaultTimeoutSeconds,
				)

				return replicatedPlc.Object["spec"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
		})
		It("should clean up", func() {
			utils.Kubectl("delete",
				"-f", case9PolicyYaml,
				"-n", testNamespace)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, defaultTimeoutSeconds)
		})
	})
})

var _ = Describe("Test encrypted policy templates", func() {
	Describe("Create policy, placement and referenced resource in ns: "+testNamespace, func() {
		It("should be created in user ns", func() {
			By("Creating " + case9PolicyYamlEncrypted)
			utils.Kubectl("apply",
				"-f", case9PolicyYamlEncrypted,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case9PolicyNameEncrypted, testNamespace,
				true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		for i := 1; i <= 2; i++ {
			managedCluster := "managed" + fmt.Sprint(i)
			It("should resolve templates and propagate to cluster ns "+managedCluster, func() {
				By("Initializing AES Encryption Secret")
				_, err := utils.KubectlWithOutput("apply",
					"-f", case9EncryptionSecret,
					"-n", managedCluster)
				Expect(err).To(BeNil())

				By("Patching test-policy-plr with decision of cluster " + managedCluster)
				plr := utils.GetWithTimeout(
					clientHubDynamic, gvrPlacementRule, case9PolicyNameEncrypted+"-plr", testNamespace,
					true, defaultTimeoutSeconds,
				)
				plr.Object["status"] = utils.GeneratePlrStatus(managedCluster)
				_, err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
					context.TODO(), plr, metav1.UpdateOptions{},
				)
				Expect(err).To(BeNil())

				var replicatedPlc *unstructured.Unstructured
				By("Waiting for encrypted values")
				Eventually(func() interface{} {
					replicatedPlc = utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+"."+case9PolicyNameEncrypted,
						managedCluster,
						true,
						defaultTimeoutSeconds,
					)

					return fmt.Sprint(replicatedPlc.Object["spec"])
				}, defaultTimeoutSeconds, 1).Should(ContainSubstring("$ocm_encrypted:"))

				By("Patching the initialization vector with a static value")
				// Setting Initialization Vector so that the test results will be deterministic
				initializationVector := "7cznVUq5SXEE4RMZNkGOrQ=="
				annotations := replicatedPlc.GetAnnotations()
				annotations[IVAnnotation] = initializationVector
				replicatedPlc.SetAnnotations(annotations)
				_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(managedCluster).Update(
					context.TODO(), replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).To(BeNil())

				By("Verifying the replicated policy against a snapshot")
				yamlPlc := utils.ParseYaml(case9PolicyYamlEncryptedRepl + managedCluster + ".yaml")
				Eventually(func() interface{} {
					replicatedPlc = utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+"."+case9PolicyNameEncrypted,
						managedCluster,
						true,
						defaultTimeoutSeconds,
					)

					return replicatedPlc.Object["spec"]
				}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
			})

			It("should clean up the encryption key", func() {
				utils.Kubectl("delete", "secret",
					case9EncryptionSecretName,
					"-n", managedCluster)
				utils.GetWithTimeout(
					clientHubDynamic, gvrSecret, case9EncryptionSecretName, managedCluster,
					false, defaultTimeoutSeconds,
				)
			})
		}
		It("should clean up", func() {
			utils.Kubectl("delete",
				"-f", case9PolicyYamlEncrypted,
				"-n", testNamespace)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, defaultTimeoutSeconds)
		})
	})
})
