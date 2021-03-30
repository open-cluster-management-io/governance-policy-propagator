// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
	"github.com/open-cluster-management/governance-policy-propagator/pkg/controller/common"
	"github.com/open-cluster-management/governance-policy-propagator/test/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const case5PolicyName string = "case5-test-policy"
const case5PolicyYaml string = "../resources/case5_policy_automation/case5-test-policy.yaml"

var _ = Describe("Test policy automation", func() {
	Describe("Create policy/pb/plc in ns:"+testNamespace+" and then update pb/plc", func() {
		It("should be created in user ns", func() {
			By("Creating " + case5PolicyName)
			utils.KubectlWithOutput("apply",
				"-f", case5PolicyYaml,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case5PolicyName, testNamespace, true, defaultTimeoutSeconds)
			Expect(plc).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementRule, case5PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
	})
	Describe("Test PolicyAutomation spec.mode", func() {
		It("Test mode = disable", func() {
			By("Creating an policyAutomation with mode=disable")
			utils.KubectlWithOutput("apply",
				"-f", "../resources/case5_policy_automation/case5-policy-automation.yaml",
				"-n", testNamespace)
			By("Should not create any ansiblejob when mode = disable")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(context.TODO(), metav1.ListOptions{})
				Expect(err).To(BeNil())
				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
		})
		It("Test mode = once", func() {
			By("Patching policyAutomation with mode=once")
			policyAutomation, err := clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Get(context.TODO(), "create-service-now-ticket", metav1.GetOptions{})
			Expect(err).To(BeNil())
			policyAutomation.Object["spec"].(map[string]interface{})["mode"] = "once"
			_, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Update(context.TODO(), policyAutomation, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			By("Should still not create any ansiblejob when mode = once and policy is pending")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(context.TODO(), metav1.ListOptions{})
				Expect(err).To(BeNil())
				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
			By("Should still not create any ansiblejob when mode = once and policy is Compliant")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(context.TODO(), metav1.ListOptions{})
				Expect(err).To(BeNil())
				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
			By("Patching policy to make both cluster NonCompliant")
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName}
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.NonCompliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(context.TODO(), &replicatedPlc, metav1.UpdateOptions{})
				Expect(err).To(BeNil())
			}
			By("Should only create one ansiblejob when mode = once and policy is NonCompliant")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(context.TODO(), metav1.ListOptions{})
				Expect(err).To(BeNil())
				utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(context.TODO(), metav1.ListOptions{})
				Expect(err).To(BeNil())
				utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))
			By("Mode should be set to disabled after ansiblejob is created")
			policyAutomation, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Get(context.TODO(), "create-service-now-ticket", metav1.GetOptions{})
			Expect(err).To(BeNil())
			Expect(policyAutomation.Object["spec"].(map[string]interface{})["mode"]).To(Equal("disabled"))
		})
		It("Test manual run", func() {
			By("Applying manual run annotation")
			utils.KubectlWithOutput("annotate", "policyautomation", "-n", testNamespace, "create-service-now-ticket",
				"--overwrite", "policy.open-cluster-management.io/rerun=true")
			By("Should only create one more ansiblejob because policy is NonCompliant")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(context.TODO(), metav1.ListOptions{})
				Expect(err).To(BeNil())
				utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(2))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(context.TODO(), metav1.ListOptions{})
				Expect(err).To(BeNil())
				utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(2))
			By("Patching policy to make both cluster Compliant")
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName}
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.Compliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(context.TODO(), &replicatedPlc, metav1.UpdateOptions{})
				Expect(err).To(BeNil())
			}
			By("Applying manual run annotation again")
			utils.KubectlWithOutput("annotate", "policyautomation", "-n", testNamespace, "create-service-now-ticket",
				"--overwrite", "policy.open-cluster-management.io/rerun=true")
			By("Should still create one more ansiblejob when policy is Compliant")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(context.TODO(), metav1.ListOptions{})
				Expect(err).To(BeNil())
				utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(3))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(context.TODO(), metav1.ListOptions{})
				Expect(err).To(BeNil())
				utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(3))
		})
	})
	Describe("Clean up", func() {
		It("Test AnsibleJob clean up", func() {
			By("Removing config map")
			utils.KubectlWithOutput("delete", "policyautomation", "-n", testNamespace, "create-service-now-ticket")
			By("Ansiblejob should also be removed")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(context.TODO(), metav1.ListOptions{})
				Expect(err).To(BeNil())
				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
			By("Removing policy")
			utils.KubectlWithOutput("delete", "policy", "-n", testNamespace, case5PolicyName)
		})
	})
})
