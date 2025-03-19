// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"open-cluster-management.io/governance-policy-propagator/controllers/propagator"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test policy templates", func() {
	const (
		case9PolicyName                   = "case9-test-policy"
		case9PathPrefix                   = "../resources/case9_templates/"
		case9PolicyYaml                   = case9PathPrefix + "case9-test-policy.yaml"
		case9ReplicatedPolicyYamlM1       = case9PathPrefix + "case9-test-replpolicy-managed1.yaml"
		case9ReplicatedPolicyYamlM1Update = case9PathPrefix + "case9-test-replpolicy-managed1-relabelled.yaml"
		case9ReplicatedPolicyYamlM2       = case9PathPrefix + "case9-test-replpolicy-managed2.yaml"
		case9PolicyNameEncrypted          = "case9-test-policy-encrypted"
		case9PolicyYamlEncrypted          = case9PathPrefix + "case9-test-policy_encrypted.yaml"
		case9PolicyYamlEncryptedRepl      = case9PathPrefix + "case9-test-replpolicy_encrypted-"
		case9EncryptionSecret             = case9PathPrefix + "case9-test-encryption-secret.yaml"
		case9EncryptionSecretName         = "policy-encryption-key"
		case9SecretName                   = "case9-secret"
		case9PolicyNameCopy               = "case9-test-policy-copy"
		case9PolicyYamlCopy               = case9PathPrefix + "case9-test-policy_copy.yaml"
		case9PolicyYamlCopiedRepl         = case9PathPrefix + "case9-test-replpolicy_copied-"
		case9PolicyWithCSLookupName       = "case9-test-policy-cslookup"
		case9PolicyWithCSLookupYaml       = case9PathPrefix + "case9-test-policy-cslookup.yaml"
		case9SAYaml                       = case9PathPrefix + "case9-sa.yaml"
		case9SATokenYaml                  = case9PathPrefix + "case9-token-sa.yaml"
		case9SAPolicyYaml                 = case9PathPrefix + "case9-sa-policy.yaml"
		case9SAPolicyNoPermYaml           = case9PathPrefix + "case9-sa-policy-no-permission.yaml"
		case9SAPolicyMissingSAYaml        = case9PathPrefix + "case9-sa-policy-missing-sa.yaml"
	)

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
		It("should update the templated value when the managed cluster labels change", func() {
			By("Updating the label on managed1")
			utils.Kubectl("label", "managedcluster", "managed1",
				"vendor=Fake", "--overwrite", "--kubeconfig="+kubeconfigHub)

			By("Verifying the policy is updated")
			yamlPlc := utils.ParseYaml(case9ReplicatedPolicyYamlM1Update)
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
			Expect(err).ToNot(HaveOccurred())
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

	Describe("Test encrypted policy templates", Ordered, func() {
		for i := 1; i <= 2; i++ {
			managedCluster := "managed" + strconv.Itoa(i)

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

	Describe("Test encrypted policy templates with secret copy", Ordered, func() {
		for i := 1; i <= 2; i++ {
			managedCluster := "managed" + strconv.Itoa(i)

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
				Eventually(func() interface{} {
					replicatedPlc = utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+"."+case9PolicyNameCopy,
						managedCluster,
						true,
						defaultTimeoutSeconds,
					)

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
					"--ignore-not-found",
					"--kubeconfig="+kubeconfigHub)
				utils.Kubectl("delete", "-f", case9PolicyYamlCopy,
					"-n", testNamespace,
					"--ignore-not-found",
					"--kubeconfig="+kubeconfigHub)
			})
		}
	})

	Describe("Test policy templates with cluster-scoped lookup", Ordered, func() {
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

	Describe("Test a custom service account", Ordered, func() {
		AfterAll(func(_ context.Context) {
			utils.Kubectl("delete", "-f", case9SAYaml, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
			utils.Kubectl(
				"-n",
				testNamespace,
				"delete",
				"-f",
				case9SAPolicyYaml,
				"--kubeconfig="+kubeconfigHub,
				"--ignore-not-found",
			)
			utils.Kubectl(
				"-n", testNamespace, "delete", "sa", "case9-sa-does-not-exist",
				"--ignore-not-found", "--kubeconfig="+kubeconfigHub,
			)

			for i := range 3 {
				utils.Kubectl(
					"delete", "secret", "policy-encryption-key", "-n", fmt.Sprintf("managed%d", i+1),
					"--ignore-not-found", "--kubeconfig="+kubeconfigHub,
				)
			}
		})

		It("Template resolution with a custom service account", func(ctx SpecContext) {
			By("Creating the service account")
			utils.Kubectl("apply", "-f", case9SAYaml, "--kubeconfig="+kubeconfigHub)

			By("Creating the policy")
			utils.Kubectl("-n", testNamespace, "apply", "-f", case9SAPolicyYaml, "--kubeconfig="+kubeconfigHub)

			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, "case9-sa-policy", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2", "managed3")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying the replicated policy has the correct values")
			for i := range 3 {
				cluster := fmt.Sprintf("managed%d", i+1)

				By("Checking the policy for " + cluster)

				Eventually(func(g Gomega) {
					plc := utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+".case9-sa-policy",
						cluster,
						true,
						defaultTimeoutSeconds,
					)
					g.Expect(plc).NotTo(BeNil())

					tmpls, _, _ := unstructured.NestedSlice(plc.Object, "spec", "policy-templates")
					g.Expect(tmpls).To(HaveLen(1))

					tmplLabels, _, _ := unstructured.NestedStringMap(
						tmpls[0].(map[string]interface{}), "objectDefinition", "metadata", "labels",
					)
					g.Expect(tmplLabels).ToNot(BeEmpty())
					g.Expect(tmplLabels["configmap"]).To(Equal("Raleigh"))
					g.Expect(tmplLabels["namespace"]).To(Equal("default"))
				}, defaultTimeoutSeconds, 1).Should(Succeed())
			}
		})

		It("Template resolution fails when the SA doesn't have permission", func() {
			By("Updating the policy")
			utils.Kubectl("-n", testNamespace, "apply", "-f", case9SAPolicyNoPermYaml, "--kubeconfig="+kubeconfigHub)

			By("Verifying the replicated policy has an error")
			for i := range 3 {
				cluster := fmt.Sprintf("managed%d", i+1)

				By("Checking the policy for " + cluster)
				Eventually(func(g Gomega) {
					plc := utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+".case9-sa-policy",
						cluster,
						true,
						defaultTimeoutSeconds,
					)
					g.Expect(plc).NotTo(BeNil())

					By("Verifying the replicated policy was created with the correct error annotation in the template")
					tmpls, _, _ := unstructured.NestedSlice(plc.Object, "spec", "policy-templates")
					g.Expect(tmpls).To(HaveLen(1))

					tmplAnnotations, _, _ := unstructured.NestedStringMap(tmpls[0].(map[string]interface{}),
						"objectDefinition", "metadata", "annotations")
					g.Expect(tmplAnnotations).ToNot(BeEmpty())

					hubTmplErrAnnotation := tmplAnnotations["policy.open-cluster-management.io/hub-templates-error"]
					g.Expect(hubTmplErrAnnotation).To(ContainSubstring(
						`User "system:serviceaccount:policy-propagator-test:case9-sa" cannot list resource "secrets" ` +
							`in API group "" in the namespace "open-cluster-management": ensure the client has the ` +
							`list and watch permissions for the query: GroupVersion=v1, Kind=Secret, ` +
							`Namespace=open-cluster-management, Name=postgres-cert`,
					))
				}, defaultTimeoutSeconds, 1).Should(Succeed())
			}

			By("Reverting back the policy")
			utils.Kubectl("-n", testNamespace, "apply", "-f", case9SAPolicyYaml, "--kubeconfig="+kubeconfigHub)

			By("Verifying the replicated policy has no error")
			for i := range 3 {
				cluster := fmt.Sprintf("managed%d", i+1)

				By("Checking the policy for " + cluster)
				Eventually(func(g Gomega) {
					plc := utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+".case9-sa-policy",
						cluster,
						true,
						defaultTimeoutSeconds,
					)
					g.Expect(plc).NotTo(BeNil())

					tmpls, _, _ := unstructured.NestedSlice(plc.Object, "spec", "policy-templates")
					g.Expect(tmpls).To(HaveLen(1))

					tmplAnnotations, _, _ := unstructured.NestedStringMap(tmpls[0].(map[string]interface{}),
						"objectDefinition", "metadata", "annotations")
					hubTmplErrAnnotation := tmplAnnotations["policy.open-cluster-management.io/hub-templates-error"]
					g.Expect(hubTmplErrAnnotation).To(BeEmpty())
				}, defaultTimeoutSeconds, 1).Should(Succeed())
			}
		})

		It("Template resolution fails when the SA changes and does not exist", func() {
			By("Updating the policy")
			utils.Kubectl("-n", testNamespace, "apply", "-f", case9SAPolicyMissingSAYaml, "--kubeconfig="+kubeconfigHub)

			By("Verifying the replicated policy has an error")
			for i := range 3 {
				cluster := fmt.Sprintf("managed%d", i+1)

				By("Checking the policy for " + cluster)
				Eventually(func(g Gomega) {
					plc := utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+".case9-sa-policy",
						cluster,
						true,
						defaultTimeoutSeconds,
					)
					g.Expect(plc).NotTo(BeNil())

					By("Verifying the replicated policy was created with the correct error annotation in the template")
					tmpls, _, _ := unstructured.NestedSlice(plc.Object, "spec", "policy-templates")
					g.Expect(tmpls).To(HaveLen(1))

					tmplAnnotations, _, _ := unstructured.NestedStringMap(tmpls[0].(map[string]interface{}),
						"objectDefinition", "metadata", "annotations")
					g.Expect(tmplAnnotations).ToNot(BeEmpty())

					hubTmplErrAnnotation := tmplAnnotations["policy.open-cluster-management.io/hub-templates-error"]
					g.Expect(hubTmplErrAnnotation).To(ContainSubstring(
						`the service account in hubTemplateOptions.serviceAccountName ` +
							`(policy-propagator-test/case9-sa-does-not-exist) does not exist`,
					))
				}, defaultTimeoutSeconds, 1).Should(Succeed())
			}

			By("Creating the service account makes the policy reconcile and resolve the templates")
			utils.Kubectl("-n", testNamespace, "create", "sa", "case9-sa-does-not-exist", "--kubeconfig="+kubeconfigHub)

			By("Verifying the replicated policy has the correct values")
			for i := range 3 {
				cluster := fmt.Sprintf("managed%d", i+1)

				By("Checking the policy for " + cluster)

				Eventually(func(g Gomega) {
					plc := utils.GetWithTimeout(
						clientHubDynamic,
						gvrPolicy,
						testNamespace+".case9-sa-policy",
						cluster,
						true,
						defaultTimeoutSeconds,
					)
					g.Expect(plc).NotTo(BeNil())

					tmpls, _, _ := unstructured.NestedSlice(plc.Object, "spec", "policy-templates")
					g.Expect(tmpls).To(HaveLen(1))

					tmplAnnotations, _, _ := unstructured.NestedStringMap(tmpls[0].(map[string]interface{}),
						"objectDefinition", "metadata", "annotations")
					hubTmplErrAnnotation := tmplAnnotations["policy.open-cluster-management.io/hub-templates-error"]
					g.Expect(hubTmplErrAnnotation).To(BeEmpty())

					tmplLabels, _, _ := unstructured.NestedStringMap(
						tmpls[0].(map[string]interface{}), "objectDefinition", "metadata", "labels",
					)
					g.Expect(tmplLabels).ToNot(BeEmpty())
					g.Expect(tmplLabels["configmap"]).To(Equal("Raleigh"))
					g.Expect(tmplLabels["namespace"]).To(Equal("default"))
				}, defaultTimeoutSeconds, 1).Should(Succeed())
			}
		})
	})

	Describe("Test token issuance and refresh", Ordered, func() {
		BeforeAll(func() {
			utils.Kubectl("apply", "-f", case9SATokenYaml, "--kubeconfig="+kubeconfigHub)

			DeferCleanup(func() {
				utils.Kubectl("delete", "-f", case9SATokenYaml, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
			})
		})

		It("A token should be refreshed within a minute", func(ctx SpecContext) {
			tokenCtx, tokenCtxCancel := context.WithCancel(ctx)

			defer tokenCtxCancel()

			// Refresh the token between 1 minute and 30 seconds after issuance
			refreshConfig := propagator.TokenRefreshConfig{
				ExpirationSeconds: 600, // 10 minutes
				MinRefreshMins:    9,
				MaxRefreshMins:    9.5,
			}

			var tokenPath string

			Eventually(func(g Gomega) {
				var err error

				tokenPath, err = propagator.GetToken(
					tokenCtx,
					&sync.WaitGroup{},
					clientHubCtrlRuntime,
					types.NamespacedName{Namespace: "default", Name: "test-user"},
					refreshConfig,
				)
				g.Expect(err).ToNot(HaveOccurred())
			}, defaultTimeoutSeconds, 1).Should(Succeed())

			token, err := os.ReadFile(tokenPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).ToNot(BeNil())

			By("Monitoring the token file for a change: " + tokenPath)

			Eventually(func(g Gomega) {
				refreshedToken, err := os.ReadFile(tokenPath)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(refreshedToken).ToNot(BeNil())
				g.Expect(bytes.Equal(token, refreshedToken)).To(BeFalse(), "Expected the token to have been refreshed")
			}, 65, 3).Should(Succeed())
		})

		It("The template resolver should be cleaned up when the service account gets deleted", func(ctx SpecContext) {
			By("Getting a token for the service account")
			tokenCtx, tokenCtxCancel := context.WithCancel(ctx)

			defer tokenCtxCancel()

			refreshConfig := propagator.TokenRefreshConfig{
				ExpirationSeconds: 600, // 10 minutes
				MinRefreshMins:    9.8,
				MaxRefreshMins:    9.9,
				OnFailedRefresh: func(_ error) {
				},
			}

			var tokenPath string

			Eventually(func(g Gomega) {
				var err error

				tokenPath, err = propagator.GetToken(
					tokenCtx,
					&sync.WaitGroup{},
					clientHubCtrlRuntime,
					types.NamespacedName{Namespace: "default", Name: "test-user"},
					refreshConfig,
				)
				g.Expect(err).ToNot(HaveOccurred())
			}, defaultTimeoutSeconds, 1).Should(Succeed())

			By("Deleting the service account")
			utils.Kubectl("delete", "-f", case9SATokenYaml, "--kubeconfig="+kubeconfigHub)

			By("Verifying the token file was deleted")
			Eventually(func(g Gomega) {
				_, err := os.Stat(tokenPath)
				g.Expect(err).To(MatchError(os.ErrNotExist))
			}, 30, 3).Should(Succeed())
		})
	})
})
