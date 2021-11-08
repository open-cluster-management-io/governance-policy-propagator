// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/api/v1"
	"github.com/open-cluster-management/governance-policy-propagator/controllers/common"
	"github.com/open-cluster-management/governance-policy-propagator/test/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const case2PolicyName string = "case2-test-policy"
const case2PolicyYaml string = "../resources/case2_aggregation/case2-test-policy.yaml"

var _ = Describe("Test policy status aggregation", func() {
	Describe("Create policy/pb/plc in ns:"+testNamespace, func() {
		It("should be created in user ns", func() {
			By("Creating " + case2PolicyYaml)
			utils.Kubectl("apply",
				"-f", case2PolicyYaml,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
			Expect(plc).NotTo(BeNil())
		})

		It("should contain status.placement with managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementRule, case2PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case2PolicyName, "managed1", true, defaultTimeoutSeconds)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case2PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
			By("Checking the status.placement of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed1-status.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should contain status.placement with both managed1 and managed2", func() {
			By("Patching test-policy-plr with decision of cluster managed1 and managed2")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementRule, case2PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case2PolicyName, "managed2", true, defaultTimeoutSeconds)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case2PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			By("Checking the status.placement of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-status.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should contain status.placement with managed2", func() {
			By("Patching test-policy-plr with decision of cluster managed2")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementRule, case2PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePlrStatus("managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case2PolicyName, "managed2", true, defaultTimeoutSeconds)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case2PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
			By("Checking the status.placement of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed2-status.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should contain status.placement with two pb/plr", func() {
			By("Creating pb-plr-2 to binding second set of placement")
			utils.Kubectl("apply",
				"-f", "../resources/case2_aggregation/pb-plr-2.yaml",
				"-n", testNamespace)
			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-placement-single-status.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should contain status.placement with two pb/plr and both status", func() {
			By("Creating pb-plr-2 to binding second set of placement")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementRule, case2PolicyName+"-plr2", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-placement-status.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should still contain status.placement with two pb/plr and both status", func() {
			By("Patch" + case2PolicyName + "-plr2 with both managed1 and managed2")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementRule, case2PolicyName+"-plr2", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-placement-status.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should still contain status.placement with two pb, one plr and both status", func() {
			By("Remove" + case2PolicyName + "-plr")
			utils.Kubectl("delete",
				"placementrule", case2PolicyName+"-plr",
				"-n", testNamespace)
			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-placement-status-missing-plr.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should clear out status.status", func() {
			By("Remove" + case2PolicyName + "-plr2")
			utils.Kubectl("delete",
				"placementrule", case2PolicyName+"-plr2",
				"-n", testNamespace)
			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-placementbinding.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should clear out status", func() {
			By("Remove" + case2PolicyName + "-pb and " + case2PolicyName + "-pb2")
			utils.Kubectl("delete",
				"placementbinding", case2PolicyName+"-pb",
				"-n", testNamespace)
			utils.Kubectl("delete",
				"placementbinding", case2PolicyName+"-pb2",
				"-n", testNamespace)
			By("Checking the status of root policy")
			emptyStatus := map[string]interface{}{}
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(emptyStatus))
		})
		It("should clean up", func() {
			utils.Kubectl("delete",
				"-f", case2PolicyYaml,
				"-n", testNamespace)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, 10)
		})
	})
	Describe("Create policy/pb/plc in ns:"+testNamespace, func() {
		It("should be created in user ns", func() {
			By("Creating " + case2PolicyYaml)
			utils.Kubectl("apply",
				"-f", case2PolicyYaml,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
			Expect(plc).NotTo(BeNil())
		})
		It("should contain status.placement with violation status from both managed1 and managed2", func() {
			By("Patching test-policy-plr with decision of cluster managed1 and managed2")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementRule, case2PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case2PolicyName, "managed2", true, defaultTimeoutSeconds)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case2PolicyName}
			By("Patching both replicated policy status to compliant")
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.Compliant,
				}
				_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(context.TODO(), &replicatedPlc, metav1.UpdateOptions{})
				Expect(err).To(BeNil())
			}
			By("Checking the status of root policy")
			yamlPlc := utils.ParseYaml("../resources/case2_aggregation/managed-both-status-compliant.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
			By("Patching both replicated policy status to noncompliant")
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.NonCompliant,
				}
				_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(context.TODO(), &replicatedPlc, metav1.UpdateOptions{})
				Expect(err).To(BeNil())
			}
			By("Checking the status of root policy")
			yamlPlc = utils.ParseYaml("../resources/case2_aggregation/managed-both-status-noncompliant.yaml")
			Eventually(func() interface{} {
				rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case2PolicyName, testNamespace, true, defaultTimeoutSeconds)
				return rootPlc.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should clean up", func() {
			utils.Kubectl("delete",
				"-f", case2PolicyYaml,
				"-n", testNamespace)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, 10)
		})
	})
})
