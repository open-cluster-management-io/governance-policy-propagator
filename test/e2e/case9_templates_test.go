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
	"k8s.io/apimachinery/pkg/types"

	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case9PolicyName                   = "case9-test-policy"
	case9PolicyYaml                   = "../resources/case9_templates/case9-test-policy.yaml"
	case9ReplicatedPolicyYamlM1       = "../resources/case9_templates/case9-test-replpolicy-managed1.yaml"
	case9ReplicatedPolicyYamlM1Update = "../resources/case9_templates/case9-test-replpolicy-managed1-relabelled.yaml"
	case9ReplicatedPolicyYamlM2       = "../resources/case9_templates/case9-test-replpolicy-managed2.yaml"
	case9PolicyNameEncrypted          = "case9-test-policy-encrypted"
	case9PolicyYamlEncrypted          = "../resources/case9_templates/case9-test-policy_encrypted.yaml"
	case9PolicyYamlEncryptedRepl      = "../resources/case9_templates/case9-test-replpolicy_encrypted-"
	case9EncryptionSecret             = "../resources/case9_templates/case9-test-encryption-secret.yaml"
	case9EncryptionSecretName         = "policy-encryption-key"
	case9SecretName                   = "case9-secret"
	IVAnnotation                      = "policy.open-cluster-management.io/encryption-iv"
	case9PolicyNameCopy               = "case9-test-policy-copy"
	case9PolicyYamlCopy               = "../resources/case9_templates/case9-test-policy_copy.yaml"
	case9PolicyYamlCopiedRepl         = "../resources/case9_templates/case9-test-replpolicy_copied-"
	case9PolicyWithCSLookupName       = "case9-test-policy-cslookup"
	case9PolicyWithCSLookupYaml       = "../resources/case9_templates/case9-test-policy-cslookup.yaml"
)

var _ = Describe("Test policy templates", func() {
	Describe("Create policy, placement and referenced resource in ns:"+testNamespace, Ordered, func() {
		It("should be created in user ns", func() {
			By("Creating " + case9PolicyYaml)
			utils.Kubectl("apply",
				"-f", case9PolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
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
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case9PolicyName, "managed1",
				true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())

			yamlPlc := utils.ParseYaml(case9ReplicatedPolicyYamlM1)
			Eventually(func(g Gomega) interface{} {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic,
					gvrPolicy,
					testNamespace+"."+case9PolicyName,
					"managed1",
					true,
					defaultTimeoutSeconds,
				)

				err := utils.RemovePolicyTemplateDBAnnotations(replicatedPlc)
				g.Expect(err).ToNot(HaveOccurred())

				return replicatedPlc.Object["spec"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
		})
		It("should update the templated value when the managed cluster labels change", func() {
			By("Updating the label on managed1")
			utils.Kubectl("label", "managedcluster", "managed1",
				"vendor=Fake", "--overwrite", "--kubeconfig="+kubeconfigHub)

			By("Verifying the policy is updated")
			yamlPlc := utils.ParseYaml(case9ReplicatedPolicyYamlM1Update)
			Eventually(func(g Gomega) interface{} {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic,
					gvrPolicy,
					testNamespace+"."+case9PolicyName,
					"managed1",
					true,
					defaultTimeoutSeconds,
				)

				err := utils.RemovePolicyTemplateDBAnnotations(replicatedPlc)
				g.Expect(err).ToNot(HaveOccurred())

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
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case9PolicyName, "managed2",
				true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())

			yamlPlc := utils.ParseYaml(case9ReplicatedPolicyYamlM2)
			Eventually(func(g Gomega) interface{} {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic,
					gvrPolicy,
					testNamespace+"."+case9PolicyName,
					"managed2",
					true,
					defaultTimeoutSeconds,
				)

				err := utils.RemovePolicyTemplateDBAnnotations(replicatedPlc)
				g.Expect(err).ToNot(HaveOccurred())

				return replicatedPlc.Object["spec"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
		})
		AfterAll(func() {
			utils.Kubectl("delete",
				"-f", case9PolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub,
				"--ignore-not-found")
			utils.Kubectl("label", "managedcluster", "managed1",
				"vendor=auto-detect", "--overwrite", "--kubeconfig="+kubeconfigHub)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, defaultTimeoutSeconds)
		})
	})
})

var _ = Describe("Test encrypted policy templates", func() {
	Describe("Create policy, placement and referenced resource in ns: "+testNamespace, Ordered, func() {
		for i := 1; i <= 2; i++ {
			managedCluster := "managed" + fmt.Sprint(i)

			It("should be created in user ns", func() {
				By("Creating " + case9PolicyYamlEncrypted)
				utils.Kubectl("apply",
					"-f", case9PolicyYamlEncrypted,
					"-n", testNamespace,
					"--kubeconfig="+kubeconfigHub)
				plc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case9PolicyNameEncrypted, testNamespace,
					true, defaultTimeoutSeconds,
				)
				Expect(plc).NotTo(BeNil())
			})

			It("should resolve templates and propagate to cluster ns "+managedCluster, func() {
				By("Initializing AES Encryption Secret")
				_, err := utils.KubectlWithOutput("apply",
					"-f", case9EncryptionSecret,
					"-n", managedCluster,
					"--kubeconfig="+kubeconfigHub)
				Expect(err).ToNot(HaveOccurred())

				By("Patching test-policy-plr with decision of cluster " + managedCluster)
				plr := utils.GetWithTimeout(
					clientHubDynamic, gvrPlacementRule, case9PolicyNameEncrypted+"-plr", testNamespace,
					true, defaultTimeoutSeconds,
				)
				plr.Object["status"] = utils.GeneratePlrStatus(managedCluster)
				_, err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
					context.TODO(), plr, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

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
				Expect(err).ToNot(HaveOccurred())

				By("Verifying the replicated policy against a snapshot")
				yamlPlc := utils.ParseYaml(case9PolicyYamlEncryptedRepl + managedCluster + ".yaml")
				Eventually(func(g Gomega) interface{} {
					replicatedPlc = utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+"."+case9PolicyNameEncrypted,
						managedCluster,
						true,
						defaultTimeoutSeconds,
					)

					err := utils.RemovePolicyTemplateDBAnnotations(replicatedPlc)
					g.Expect(err).ToNot(HaveOccurred())

					return replicatedPlc.Object["spec"]
				}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
			})

			It("should reconcile when the secret referenced in the template is updated", func() {
				By("Updating the secret " + case9SecretName)
				newToken := "THVrZS4gSSBhbSB5b3VyIGZhdGhlci4="
				patch := []byte(`{"data": {"token": "` + newToken + `"}}`)
				_, err := clientHub.CoreV1().Secrets(testNamespace).Patch(
					context.TODO(), case9SecretName, types.StrategicMergePatchType, patch, metav1.PatchOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				By("Verifying the replicated policy was updated")
				expected := "$ocm_encrypted:dbHPzG98PxV7RXcAx25mMGPBAUbfjJTEMyFc7kE2W7U3FW5+X31LkidHu/25ic4m"
				Eventually(func() string {
					replicatedPlc := utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+"."+case9PolicyNameEncrypted,
						managedCluster,
						true,
						defaultTimeoutSeconds,
					)

					templates, _, _ := unstructured.NestedSlice(replicatedPlc.Object, "spec", "policy-templates")
					if len(templates) < 1 {
						return ""
					}

					template, ok := templates[0].(map[string]interface{})
					if !ok {
						return ""
					}

					objectTemplates, _, _ := unstructured.NestedSlice(
						template, "objectDefinition", "spec", "object-templates",
					)
					if len(objectTemplates) < 1 {
						return ""
					}

					objectTemplate, ok := objectTemplates[0].(map[string]interface{})
					if !ok {
						return ""
					}

					secretValue, _, _ := unstructured.NestedString(
						objectTemplate, "objectDefinition", "data", "someTopSecretThing",
					)

					return secretValue
				}, defaultTimeoutSeconds, 1).Should(Equal(expected))
			})

			It("should clean up the encryption key", func() {
				utils.Kubectl("delete", "secret",
					case9EncryptionSecretName,
					"-n", managedCluster,
					"--kubeconfig="+kubeconfigHub)
				utils.GetWithTimeout(
					clientHubDynamic, gvrSecret, case9EncryptionSecretName, managedCluster,
					false, defaultTimeoutSeconds,
				)
			})

			It("should clean up", func() {
				utils.Kubectl("delete", "-f", case9PolicyYamlEncrypted,
					"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
				opt := metav1.ListOptions{}
				utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, defaultTimeoutSeconds)
			})
		}
	})
})

var _ = Describe("Test encrypted policy templates with secret copy", func() {
	Describe("Create policy, placement and referenced resource in ns: "+testNamespace, Ordered, func() {
		for i := 1; i <= 2; i++ {
			managedCluster := "managed" + fmt.Sprint(i)

			It("should be created in user ns", func() {
				By("Creating " + case9PolicyYamlCopy)
				utils.Kubectl("apply",
					"-f", case9PolicyYamlCopy,
					"-n", testNamespace,
					"--kubeconfig="+kubeconfigHub)
				plc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case9PolicyNameCopy, testNamespace,
					true, defaultTimeoutSeconds,
				)
				Expect(plc).NotTo(BeNil())
			})

			It("should resolve templates and propagate to cluster ns "+managedCluster, func() {
				By("Initializing AES Encryption Secret")
				_, err := utils.KubectlWithOutput("apply",
					"-f", case9EncryptionSecret,
					"-n", managedCluster,
					"--kubeconfig="+kubeconfigHub)
				Expect(err).ToNot(HaveOccurred())

				By("Patching test-policy-plr with decision of cluster " + managedCluster)
				plr := utils.GetWithTimeout(
					clientHubDynamic, gvrPlacementRule, case9PolicyNameCopy+"-plr", testNamespace,
					true, defaultTimeoutSeconds,
				)
				plr.Object["status"] = utils.GeneratePlrStatus(managedCluster)
				_, err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
					context.TODO(), plr, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				var replicatedPlc *unstructured.Unstructured
				By("Waiting for encrypted values")
				Eventually(func() interface{} {
					replicatedPlc = utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+"."+case9PolicyNameCopy,
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
				Expect(err).ToNot(HaveOccurred())

				By("Verifying the replicated policy against a snapshot")
				yamlPlc := utils.ParseYaml(case9PolicyYamlCopiedRepl + managedCluster + ".yaml")
				Eventually(func(g Gomega) interface{} {
					replicatedPlc = utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+"."+case9PolicyNameCopy,
						managedCluster,
						true,
						defaultTimeoutSeconds,
					)

					err := utils.RemovePolicyTemplateDBAnnotations(replicatedPlc)
					g.Expect(err).ToNot(HaveOccurred())

					return replicatedPlc.Object["spec"]
				}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
			})

			It("should reconcile when the secret referenced in the template is updated", func() {
				By("Updating the secret " + case9SecretName)
				newToken := "THVrZS4gSSBhbSB5b3VyIGZhdGhlci4="
				patch := []byte(`{"data": {"token": "` + newToken + `"}}`)
				_, err := clientHub.CoreV1().Secrets(testNamespace).Patch(
					context.TODO(), case9SecretName, types.StrategicMergePatchType, patch, metav1.PatchOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				By("Verifying the replicated policy was updated")
				expected := "$ocm_encrypted:dbHPzG98PxV7RXcAx25mMGPBAUbfjJTEMyFc7kE2W7U3FW5+X31LkidHu/25ic4m"
				Eventually(func() string {
					replicatedPlc := utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+"."+case9PolicyNameCopy,
						managedCluster,
						true,
						defaultTimeoutSeconds,
					)

					templates, _, _ := unstructured.NestedSlice(replicatedPlc.Object, "spec", "policy-templates")
					if len(templates) < 1 {
						return ""
					}

					template, ok := templates[0].(map[string]interface{})
					if !ok {
						return ""
					}

					objectTemplates, _, _ := unstructured.NestedSlice(
						template, "objectDefinition", "spec", "object-templates",
					)
					if len(objectTemplates) < 1 {
						return ""
					}

					objectTemplate, ok := objectTemplates[0].(map[string]interface{})
					if !ok {
						return ""
					}

					secretValue, _, _ := unstructured.NestedString(
						objectTemplate, "objectDefinition", "data", "token",
					)

					return secretValue
				}, defaultTimeoutSeconds, 1).Should(Equal(expected))
			})

			It("should clean up the encryption key", func() {
				utils.Kubectl("delete", "secret",
					case9EncryptionSecretName,
					"-n", managedCluster,
					"--kubeconfig="+kubeconfigHub)
				utils.GetWithTimeout(
					clientHubDynamic, gvrSecret, case9EncryptionSecretName, managedCluster,
					false, defaultTimeoutSeconds,
				)
			})

			It("should clean up", func() {
				utils.Kubectl("delete", "-f", case9PolicyYamlCopy,
					"-n", testNamespace,
					"--kubeconfig="+kubeconfigHub)
				opt := metav1.ListOptions{}
				utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, defaultTimeoutSeconds)
			})
			AfterAll(func() {
				utils.Kubectl("delete", "secret",
					case9EncryptionSecretName,
					"-n", managedCluster,
					"--kubeconfig="+kubeconfigHub)
				utils.Kubectl("delete", "-f", case9PolicyYamlCopy,
					"-n", testNamespace,
					"--kubeconfig="+kubeconfigHub)
			})
		}
	})
})

var _ = Describe("Test policy templates with cluster-scoped lookup", func() {
	Describe("Create policy, placement and referenced resource in ns:"+testNamespace, Ordered, func() {
		It("should be created in user ns", func() {
			By("Creating " + case9PolicyWithCSLookupName)
			utils.Kubectl("apply",
				"-f", case9PolicyWithCSLookupYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case9PolicyWithCSLookupName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		It("should resolve templates and propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case9PolicyWithCSLookupName+"-plr", testNamespace,
				true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case9PolicyWithCSLookupName, "managed1",
				true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			By("Verifying the replicated policy was created with the correct error annotation in the template")
			tmpls, _, _ := unstructured.NestedSlice(plc.Object, "spec", "policy-templates")
			Expect(tmpls).To(HaveLen(1))

			tmplAnnotations, _, _ := unstructured.NestedStringMap(tmpls[0].(map[string]interface{}),
				"objectDefinition", "metadata", "annotations")
			Expect(tmplAnnotations).ToNot(BeEmpty())

			hubTmplErrAnnotation := tmplAnnotations["policy.open-cluster-management.io/hub-templates-error"]
			Expect(hubTmplErrAnnotation).To(ContainSubstring("error calling lookup"))
		})
		AfterAll(func() {
			utils.Kubectl("delete",
				"-f", case9PolicyWithCSLookupYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub,
				"--ignore-not-found")
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, defaultTimeoutSeconds)
		})
	})
})
