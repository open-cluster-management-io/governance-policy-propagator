// Copyright (c) 2023 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test selective policy enforcement", Ordered, func() {
	const (
		case16PolicyName  string = "case16-test-policy"
		case16PolicyYaml  string = "../resources/case16_selective_policy_enforcement/case16-test-policy.yaml"
		case16BindingYaml string = "../resources/case16_selective_policy_enforcement/case16-enforce-binding.yaml"
	)

	BeforeAll(func(ctx SpecContext) {
		By("Creating the test policy, the initial placement binding, and placement rule")
		_, err := utils.KubectlWithOutput("apply", "-f", case16PolicyYaml,
			"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
		Expect(err).ToNot(HaveOccurred())
		rootplc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case16PolicyName, testNamespace, true, defaultTimeoutSeconds,
		)
		Expect(rootplc).NotTo(BeNil())

		By("Patching the placement rule with decisions managed1, managed2")
		plr := utils.GetWithTimeout(
			clientHubDynamic, gvrPlacementRule, case16PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
		)
		plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
		_, err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
			ctx, plr, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())

		By("Verifying the replicated policy was created in cluster ns managed1, managed2")
		opt := metav1.ListOptions{
			LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case16PolicyName,
		}
		plcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		for _, plc := range plcList.Items {
			Expect(plc.GetName()).To(Equal(testNamespace + "." + case16PolicyName))
			Expect(plc.GetNamespace()).Should(BeElementOf("managed1", "managed2"))
		}
	})

	AfterAll(func() {
		By("Cleaning up resources")
		_, err := utils.KubectlWithOutput("delete", "-f", case16PolicyYaml,
			"-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
		Expect(err).ToNot(HaveOccurred())
		_, err = utils.KubectlWithOutput("delete", "-f", case16BindingYaml,
			"-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Testing enforcing with subFilter", Ordered, func() {
		It("should update the policy's remediationAction to enforce on cluster ns managed1, managed2",
			func(ctx SpecContext) {
				By("Creating another placement rule and binding to selectively enforce policy")
				_, err := utils.KubectlWithOutput("apply", "-f", case16BindingYaml,
					"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
				Expect(err).ToNot(HaveOccurred())

				By("Patching the case16-test-policy-plr-enforce with decisions managed1, managed2, managed3")
				plr := utils.GetWithTimeout(
					clientHubDynamic, gvrPlacementRule, case16PolicyName+"-plr-enforce",
					testNamespace, true, defaultTimeoutSeconds,
				)
				plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2", "managed3")
				_, err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
					ctx, plr, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				By("Verifying no replicated policy created on cluster ns managed3 because of the subfilter")
				Eventually(func() interface{} {
					replicatedPlc := utils.GetWithTimeout(
						clientHubDynamic, gvrPolicy, testNamespace+"."+case16PolicyName,
						"managed3", false, defaultTimeoutSeconds,
					)

					return replicatedPlc
				}).Should(BeNil())

				By("Verifying the RemediationAction of the replicated policies on cluster ns managed1, managed2")
				Eventually(func() interface{} {
					replicatedPlc := utils.GetWithTimeout(
						clientHubDynamic, gvrPolicy, testNamespace+"."+case16PolicyName,
						"managed1", true, defaultTimeoutSeconds,
					)

					return replicatedPlc.Object["spec"].(map[string]interface{})["remediationAction"]
				}).Should(Equal("enforce"))

				Eventually(func() interface{} {
					replicatedPlc := utils.GetWithTimeout(
						clientHubDynamic, gvrPolicy, testNamespace+"."+case16PolicyName,
						"managed2", true, defaultTimeoutSeconds,
					)

					return replicatedPlc.Object["spec"].(map[string]interface{})["remediationAction"]
				}).Should(Equal("enforce"))

				By("Verifying the root policy has correct placement status")
				expectedPlacementStatus := []interface{}{
					map[string]interface{}{
						"placementBinding": case16PolicyName + "-pb",
						"placementRule":    case16PolicyName + "-plr",
					},
					map[string]interface{}{
						"placementBinding": case16PolicyName + "-pb-enforce",
						"placementRule":    case16PolicyName + "-plr-enforce",
					},
				}
				Eventually(func() interface{} {
					rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy,
						case16PolicyName, testNamespace, true, defaultTimeoutSeconds)

					return rootPlc.Object["status"].(map[string]interface{})["placement"]
				}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(expectedPlacementStatus))
			})

		It("should update the replicated policy's remediationAction back to inform on cluster ns managed1",
			func(ctx SpecContext) {
				By("Removing managed1 from the case16-test-policy-plr-enforce decisions")
				plr := utils.GetWithTimeout(
					clientHubDynamic, gvrPlacementRule, case16PolicyName+"-plr-enforce",
					testNamespace, true, defaultTimeoutSeconds,
				)
				plr.Object["status"] = utils.GeneratePlrStatus("managed2")
				_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
					ctx, plr, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				By("Verifying the RemediationAction of the replicated policies on cluster ns managed1, managed2")
				Eventually(func() interface{} {
					replicatedPlc := utils.GetWithTimeout(
						clientHubDynamic, gvrPolicy, testNamespace+"."+case16PolicyName,
						"managed1", true, defaultTimeoutSeconds,
					)

					return replicatedPlc.Object["spec"].(map[string]interface{})["remediationAction"]
				}).Should(Equal("inform"))

				Eventually(func() interface{} {
					replicatedPlc := utils.GetWithTimeout(
						clientHubDynamic, gvrPolicy, testNamespace+"."+case16PolicyName,
						"managed2", true, defaultTimeoutSeconds,
					)

					return replicatedPlc.Object["spec"].(map[string]interface{})["remediationAction"]
				}).Should(Equal("enforce"))
			})

		It("should delete the replicated policy on cluster ns managed2", func(ctx SpecContext) {
			By("Removing managed2 from the case16-test-policy-plr decisions")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case16PolicyName+"-plr",
				testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying the replicated policy on cluster ns managed2 was deleted")
			Eventually(func() interface{} {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, testNamespace+"."+case16PolicyName,
					"managed2", false, defaultTimeoutSeconds,
				)

				return replicatedPlc
			}).Should(BeNil())

			By("Verifying the root policy has correct placement status")
			expectedPlacementStatus := []interface{}{
				map[string]interface{}{
					"placementBinding": case16PolicyName + "-pb",
					"placementRule":    case16PolicyName + "-plr",
				},
			}
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy,
					case16PolicyName, testNamespace, true, defaultTimeoutSeconds)

				return rootPlc.Object["status"].(map[string]interface{})["placement"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(expectedPlacementStatus))
		})

		It("should propagate to all clusters when the subfilter is removed", func(ctx SpecContext) {
			By("Putting all clusters in the case16-test-policy-plr-enforce decisions")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case16PolicyName+"-plr-enforce",
				testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2", "managed3")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying that the policy on managed3 is still not created")
			Consistently(func() interface{} {
				return utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, testNamespace+"."+case16PolicyName,
					"managed3", false, defaultTimeoutSeconds,
				)
			}, "5s", 1).Should(BeNil())

			By("Removing the subfilter from the binding")
			utils.Kubectl("patch", "placementbinding", "case16-test-policy-pb-enforce", "-n", testNamespace,
				"--kubeconfig="+kubeconfigHub, "--type=json", `-p=[{"op":"remove","path":"/subFilter"}]`)

			By("Verifying the policies exist and are enforcing on all 3 clusters")
			for _, clustername := range []string{"managed1", "managed2", "managed3"} {
				Eventually(func() interface{} {
					replicatedPlc := utils.GetWithTimeout(
						clientHubDynamic, gvrPolicy, testNamespace+"."+case16PolicyName,
						clustername, true, defaultTimeoutSeconds,
					)

					return replicatedPlc.Object["spec"].(map[string]interface{})["remediationAction"]
				}, defaultTimeoutSeconds, 1).Should(Equal("enforce"))
			}
		})

		It("should change remediationAction when the bindingOverrides are removed", func() {
			By("Removing the overrides from the binding")
			utils.Kubectl("patch", "placementbinding", "case16-test-policy-pb-enforce", "-n", testNamespace,
				"--kubeconfig="+kubeconfigHub, "--type=json", `-p=[{"op":"remove","path":"/bindingOverrides"}]`)

			By("Verifying the policies are informing on all 3 clusters")
			for _, clustername := range []string{"managed1", "managed2", "managed3"} {
				Eventually(func() interface{} {
					replicatedPlc := utils.GetWithTimeout(
						clientHubDynamic, gvrPolicy, testNamespace+"."+case16PolicyName,
						clustername, true, defaultTimeoutSeconds,
					)

					return replicatedPlc.Object["spec"].(map[string]interface{})["remediationAction"]
				}, defaultTimeoutSeconds, 1).Should(Equal("inform"))
			}
		})
	})
})
