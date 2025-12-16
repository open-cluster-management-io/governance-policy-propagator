// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test policy status aggregation", func() {
	const (
		case2PolicyName string = "case2-test-policy"
		case2PolicyYaml string = "../resources/case2_aggregation/case2-test-policy.yaml"
		faultyPBName    string = "case2-faulty-placementbinding"
		faultyPBYaml    string = "../resources/case2_aggregation/faulty-placementbinding.yaml"
	)

	Describe("Root status from different placements", Ordered, func() {
		AfterAll(func() {
			utils.Kubectl("delete",
				"-f", faultyPBYaml,
				"-n", testNamespace,
				"--ignore-not-found",
				"--kubeconfig="+kubeconfigHub)
			utils.Kubectl("delete",
				"-f", case2PolicyYaml,
				"-n", testNamespace,
				"--ignore-not-found",
				"--kubeconfig="+kubeconfigHub)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, 10)
		})

		It("should create the faulty PlacementBinding in user ns", func() {
			By("Creating " + faultyPBName)
			utils.Kubectl("apply",
				"-f", faultyPBYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			pb := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementBinding, faultyPBName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(pb).NotTo(BeNil())
		})
		It("should be created in user ns", func() {
			By("Creating " + case2PolicyYaml)
			utils.Kubectl("apply",
				"-f", case2PolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})

		It("should contain status.placement with managed1", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case2PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case2PolicyName, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case2PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
			By("Checking the status.placement of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed1-status.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should contain status.placement with both managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of cluster managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case2PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case2PolicyName, "managed2", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case2PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			By("Checking the status.placement of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-status.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should contain status.placement with managed2", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of cluster managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case2PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case2PolicyName, "managed2", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case2PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
			By("Checking the status.placement of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed2-status.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should contain status.placement with two pb/plr", func() {
			By("Creating pb-plr-2 to binding second set of placement")
			utils.Kubectl("apply",
				"-f", "../resources/case2_aggregation/pb-plr-2.yaml",
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-placement-single-status.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should contain status.placement with two pb/plr and both status", func(ctx SpecContext) {
			By("Creating pb-plr-2 to binding second set of placement")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case2PolicyName+"-plr2", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-placement-status.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should still contain status.placement with two pb/plr and both status", func(ctx SpecContext) {
			By("Patch" + case2PolicyName + "-plr2 with both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case2PolicyName+"-plr2", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-placement-status.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should still contain status.placement with two pb, one plr and both status", func() {
			By("Remove" + case2PolicyName + "-plr")
			utils.Kubectl("delete",
				"placementrule", case2PolicyName+"-plr",
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-placement-status-missing-plr.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should clear out status.status", func() {
			By("Remove" + case2PolicyName + "-plr2")
			utils.Kubectl("delete",
				"placementrule", case2PolicyName+"-plr2",
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-placementbinding.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should clear out status", func() {
			By("Remove" + case2PolicyName + "-pb and " + case2PolicyName + "-pb2")
			utils.Kubectl("delete",
				"placementbinding", case2PolicyName+"-pb",
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			utils.Kubectl("delete",
				"placementbinding", case2PolicyName+"-pb2",
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			By("Checking the status of root policy")
			emptyStatus := map[string]interface{}{}
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(emptyStatus))
		})
	})
	Describe("Root compliance from managed statuses", Ordered, func() {
		// To get around `testNamespace` not being initialized during Ginkgo's Tree Construction phase
		listOpts := func() metav1.ListOptions {
			return metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case2PolicyName,
			}
		}

		BeforeAll(func(ctx SpecContext) {
			By("Creating " + case2PolicyYaml)
			utils.Kubectl("apply",
				"-f", case2PolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			By("Patching test-policy-plr with decision of cluster managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case2PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case2PolicyName, "managed2", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, listOpts(), 2, true, defaultTimeoutSeconds)
		})

		AfterAll(func() {
			By("Cleaning up")
			utils.Kubectl(
				"delete",
				"-f",
				case2PolicyYaml,
				"-n",
				testNamespace,
				"--ignore-not-found",
				"--kubeconfig="+kubeconfigHub,
			)
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, metav1.ListOptions{}, 0, false, 10)
		})

		It("should be compliant when both managed clusters are compliant", func(ctx SpecContext) {
			By("Patching both replicated policy status to compliant")
			replicatedPlcList := utils.ListWithTimeout(
				clientHubDynamic, gvrPolicy, listOpts(), 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.Compliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					ctx, &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
			}

			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-status-compliant.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})

		It("should be noncompliant when one managed cluster is noncompliant", func(ctx SpecContext) {
			By("Patching one replicated policy status to noncompliant")
			replicatedPlcList := utils.ListWithTimeout(
				clientHubDynamic, gvrPolicy, listOpts(), 2, true, defaultTimeoutSeconds)
			replicatedPlc := replicatedPlcList.Items[0]
			if replicatedPlc.GetNamespace() == "managed2" {
				replicatedPlc = replicatedPlcList.Items[1]
			}

			replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
				ComplianceState: policiesv1.NonCompliant,
			}
			_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
				ctx, &replicatedPlc, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-one-status-noncompliant.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})

		It("should be noncompliant when one is pending, and one is noncompliant", func(ctx SpecContext) {
			By("Patching one replicated policy to pending")
			replicatedPlcList := utils.ListWithTimeout(
				clientHubDynamic, gvrPolicy, listOpts(), 2, true, defaultTimeoutSeconds)
			replicatedPlc := replicatedPlcList.Items[0]
			if replicatedPlc.GetNamespace() == "managed1" {
				replicatedPlc = replicatedPlcList.Items[1]
			}

			replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
				ComplianceState: policiesv1.Pending,
			}
			_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
				ctx, &replicatedPlc, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-mixed-pending-noncompliant.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})

		It("should be pending when one is pending, and one is compliant", func(ctx SpecContext) {
			By("Patching one replicated policy to compliant")
			replicatedPlcList := utils.ListWithTimeout(
				clientHubDynamic, gvrPolicy, listOpts(), 2, true, defaultTimeoutSeconds)
			replicatedPlc := replicatedPlcList.Items[0]
			if replicatedPlc.GetNamespace() == "managed2" {
				replicatedPlc = replicatedPlcList.Items[1]
			}

			replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
				ComplianceState: policiesv1.Compliant,
			}
			_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
				ctx, &replicatedPlc, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-mixed-pending-compliant.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
	})
})
