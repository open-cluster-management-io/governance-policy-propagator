// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policyv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case5PolicyName string = "case5-test-policy"
	case5PolicyYaml string = "../resources/case5_policy_automation/case5-test-policy.yaml"
)

var _ = Describe("Test policy automation", func() {
	Describe("Create policy/pb/plc in ns:"+testNamespace+" and then update pb/plc", func() {
		It("should be created in user ns", func() {
			By("Creating " + case5PolicyName)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case5PolicyYaml,
				"-n", testNamespace)
			Expect(err).Should(BeNil())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case5PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case5PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
	})
	Describe("Test PolicyAutomation spec.mode", func() {
		It("Test mode = disable", func() {
			By("Creating an policyAutomation with mode=disable")
			_, err := utils.KubectlWithOutput("apply",
				"-f", "../resources/case5_policy_automation/case5-policy-automation.yaml",
				"-n", testNamespace)
			Expect(err).Should(BeNil())
			By("Should not create any ansiblejob when mode = disable")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
		})
		It("Test mode = once", func() {
			By("Patching policyAutomation with mode=once")
			policyAutomation, err := clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Get(
				context.TODO(), "create-service-now-ticket", metav1.GetOptions{},
			)
			Expect(err).To(BeNil())
			policyAutomation.Object["spec"].(map[string]interface{})["mode"] = string(policyv1beta1.Once)
			_, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Update(
				context.TODO(), policyAutomation, metav1.UpdateOptions{},
			)
			Expect(err).To(BeNil())
			By("Should still not create any ansiblejob when mode = once and policy is pending")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
			By("Should still not create any ansiblejob when mode = once and policy is Compliant")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
			By("Patching policy to make both clusters NonCompliant")
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.NonCompliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).To(BeNil())
			}
			By("Should only create one ansiblejob when mode = once and policy is NonCompliant")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))
			By("Mode should be set to disabled after ansiblejob is created")
			policyAutomation, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Get(
				context.TODO(), "create-service-now-ticket", metav1.GetOptions{},
			)
			Expect(err).To(BeNil())
			Expect(
				policyAutomation.Object["spec"].(map[string]interface{})["mode"],
			).To(Equal(string(policyv1beta1.Disabled)))
		})
		It("Test mode = everyEvent", func() {
			By("Patching policy to make both clusters back to Compliant")
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.Compliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).To(BeNil())
			}

			By("Waiting until the status on each cluster is updated to Compliant")
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				Eventually(func() interface{} {
					status := replicatedPlc.Object["status"]

					return status.(map[string]interface{})["compliant"]
				}, 30, 1).Should(Equal(string(policiesv1.Compliant)))
			}

			By("Patching policyAutomation with mode=everyEvent")
			policyAutomation, err := clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Get(
				context.TODO(), "create-service-now-ticket", metav1.GetOptions{},
			)
			Expect(err).To(BeNil())
			policyAutomation.Object["spec"].(map[string]interface{})["mode"] = string(policyv1beta1.EveryEvent)
			_, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Update(
				context.TODO(), policyAutomation, metav1.UpdateOptions{},
			)
			Expect(err).To(BeNil())

			By("Should not create any new ansiblejob")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())

				return len(ansiblejobList.Items)
			}, 15, 1).Should(Equal(1))

			By("Patching policy to make both clusters NonCompliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.NonCompliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).To(BeNil())
			}

			By("Waiting until the status on each cluster is updated to NonCompliant")
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				Eventually(func() interface{} {
					status := replicatedPlc.Object["status"]

					return status.(map[string]interface{})["compliant"]
				}, 30, 1).Should(Equal(string(policiesv1.NonCompliant)))
			}

			By("Should only create the second new ansiblejob as once-mode test created the first ansiblejob")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(2))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(2))

			By("Patching policy to make both clusters back to Compliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.Compliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).To(BeNil())
			}

			By("Waiting until the status on each cluster is updated to Compliant")
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				Eventually(func() interface{} {
					status := replicatedPlc.Object["status"]

					return status.(map[string]interface{})["compliant"]
				}, 30, 1).Should(Equal(string(policiesv1.Compliant)))
			}

			By("Should not create any new ansiblejob")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())

				return len(ansiblejobList.Items)
			}, 15, 1).Should(Equal(2))

			By("Patching policy to make both clusters back to NonCompliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.NonCompliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).To(BeNil())
			}

			By("Waiting until the status on each cluster is updated to NonCompliant")
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				Eventually(func() interface{} {
					status := replicatedPlc.Object["status"]

					return status.(map[string]interface{})["compliant"]
				}, 30, 1).Should(Equal(string(policiesv1.NonCompliant)))
			}

			By("Should only create the third new ansiblejob")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(3))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(3))

			By("Patching policy to make both clusters back to Compliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.Compliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).To(BeNil())
			}

			By("Waiting until the status on each cluster is updated to Compliant")
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				Eventually(func() interface{} {
					status := replicatedPlc.Object["status"]

					return status.(map[string]interface{})["compliant"]
				}, 30, 1).Should(Equal(string(policiesv1.Compliant)))
			}

			By("Should not create any new ansiblejob")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())

				return len(ansiblejobList.Items)
			}, 15, 1).Should(Equal(3))

			By("Patching policyAutomation with mode=everyEvent and delayAfterRunSeconds = 240")
			policyAutomation, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Get(
				context.TODO(), "create-service-now-ticket", metav1.GetOptions{},
			)
			Expect(err).To(BeNil())
			policyAutomation.Object["spec"].(map[string]interface{})["delayAfterRunSeconds"] = 240
			_, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Update(
				context.TODO(), policyAutomation, metav1.UpdateOptions{},
			)
			Expect(err).To(BeNil())

			By("Should not create any new ansiblejob")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())

				return len(ansiblejobList.Items)
			}, 15, 1).Should(Equal(3))

			By("Patching policy to make both clusters NonCompliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.NonCompliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).To(BeNil())
			}

			By("Waiting until the status on each cluster is updated to NonCompliant")
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				Eventually(func() interface{} {
					status := replicatedPlc.Object["status"]

					return status.(map[string]interface{})["compliant"]
				}, 30, 1).Should(Equal(string(policiesv1.NonCompliant)))
			}

			By("Should only create the fourth new ansiblejob")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(4))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(4))

			By("Patching policy to make both clusters back to Compliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.Compliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).To(BeNil())
			}

			By("Waiting until the status on each cluster is updated to Compliant")
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				Eventually(func() interface{} {
					status := replicatedPlc.Object["status"]

					return status.(map[string]interface{})["compliant"]
				}, 30, 1).Should(Equal(string(policiesv1.Compliant)))
			}

			By("Should not create any new ansiblejob")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())

				return len(ansiblejobList.Items)
			}, 15, 1).Should(Equal(4))

			By("Patching policy to make both clusters NonCompliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.NonCompliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).To(BeNil())
			}
			By("Waiting until the status on each cluster is updated to NonCompliant")
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				Eventually(func() interface{} {
					status := replicatedPlc.Object["status"]

					return status.(map[string]interface{})["compliant"]
				}, 30, 1).Should(Equal(string(policiesv1.NonCompliant)))
			}

			By("Should not create any new ansiblejob within delayAfterRunSeconds period")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(4))

			By("Should only create the fifth new ansiblejob after delayAfterRunSeconds period passed")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 240, 1).Should(Equal(5))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(5))
		})
		It("Test manual run", func() {
			By("Applying manual run annotation")
			_, err := utils.KubectlWithOutput(
				"annotate",
				"policyautomation",
				"-n",
				testNamespace,
				"create-service-now-ticket",
				"--overwrite",
				"policy.open-cluster-management.io/rerun=true",
			)
			Expect(err).Should(BeNil())
			By("Should only create one more ansiblejob because policy is NonCompliant")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(6))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(6))
			By("Patching policy to make both clusters Compliant")
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.Compliant,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).To(BeNil())
			}
			By("Applying manual run annotation again")
			_, err = utils.KubectlWithOutput(
				"annotate",
				"policyautomation",
				"-n",
				testNamespace,
				"create-service-now-ticket",
				"--overwrite",
				"policy.open-cluster-management.io/rerun=true",
			)
			Expect(err).Should(BeNil())
			By("Should still create one more ansiblejob when policy is Compliant")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(7))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).Should(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(7))
		})
	})
	Describe("Clean up", func() {
		It("Test AnsibleJob clean up", func() {
			By("Removing config map")
			_, err := utils.KubectlWithOutput(
				"delete", "policyautomation", "-n", testNamespace, "create-service-now-ticket",
			)
			Expect(err).Should(BeNil())
			By("Ansiblejob should also be removed")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).To(BeNil())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
			By("Removing policy")
			_, err = utils.KubectlWithOutput("delete", "policy", "-n", testNamespace, case5PolicyName)
			Expect(err).Should(BeNil())
		})
	})
})
