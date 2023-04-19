// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

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
	const automationName string = "create-service.now-ticket"

	Describe("Create policy/pb/plc in ns:"+testNamespace+" and then update pb/plc", func() {
		It("should be created in user ns", func() {
			By("Creating " + case5PolicyName)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case5PolicyYaml,
				"-n", testNamespace)
			Expect(err).ShouldNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case5PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policyset-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementRule,
				case5PolicyName+"set-plr",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
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
				"-f", "../resources/case5_policy_automation/case5-policy-automation-disable.yaml",
				"-n", testNamespace)
			Expect(err).ShouldNot(HaveOccurred())
			By("Should not create any ansiblejob when mode = disable")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
		})

		It("Test mode = once", func() {
			By("Patching policyAutomation with mode=once")
			policyAutomation, err := clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Get(
				context.TODO(), automationName, metav1.GetOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			policyAutomation.Object["spec"].(map[string]interface{})["mode"] = string(policyv1beta1.Once)
			policyAutomation, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Update(
				context.TODO(), policyAutomation, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying the added owner reference")
			Expect(policyAutomation.GetOwnerReferences()).To(HaveLen(1))
			Expect(policyAutomation.GetOwnerReferences()[0].Name).To(Equal(case5PolicyName))

			By("Should still not create any ansiblejob when mode = once and policy is pending")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
			By("Should still not create any ansiblejob when mode = once and policy is Compliant")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
			By("Patching policy to make both clusters NonCompliant")
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				// mock replicated policy PolicyStatus.Details for violationContext testing
				mockDetails := []*policiesv1.DetailsPerTemplate{
					{
						ComplianceState: policiesv1.NonCompliant,
						History: []policiesv1.ComplianceHistory{
							{
								Message:       "testing-ViolationMessage",
								LastTimestamp: metav1.NewTime(time.Now()),
								EventName:     "default.test-policy.164415c7210a573c",
							},
						},
					},
				}

				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.NonCompliant,
					Details:         mockDetails,
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
			}
			By("Should only create one ansiblejob when mode = once and policy is NonCompliant")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())
				spec := ansiblejobList.Items[0].Object["spec"]
				extraVars := spec.(map[string]interface{})["extra_vars"].(map[string]interface{})

				return len(extraVars) > 0
			}, 30, 1).Should(BeTrue())

			By("Check each violation context field in extra_vars")
			ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
				context.TODO(), metav1.ListOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			spec := ansiblejobList.Items[0].Object["spec"]
			extraVars := spec.(map[string]interface{})["extra_vars"].(map[string]interface{})
			Expect(extraVars["policy_name"]).To(Equal("case5-test-policy"))
			Expect(extraVars["policy_namespace"]).To(Equal(testNamespace))
			Expect(extraVars["hub_cluster"]).To(Equal("millienium-falcon.tatooine.local"))
			Expect(extraVars["target_clusters"].([]interface{})).To(HaveLen(1))
			Expect(extraVars["target_clusters"].([]interface{})[0]).To(Equal("managed1"))
			Expect(extraVars["policy_sets"].([]interface{})).To(HaveLen(1))
			Expect(extraVars["policy_sets"].([]interface{})[0]).To(Equal("case5-test-policyset"))
			managed1 := extraVars["policy_violations"].(map[string]interface{})["managed1"]
			compliant := managed1.(map[string]interface{})["compliant"]
			Expect(compliant).To(Equal(string(policiesv1.NonCompliant)))
			violationMessage := managed1.(map[string]interface{})["violation_message"]
			Expect(violationMessage).To(Equal("testing-ViolationMessage"))
			detail := managed1.(map[string]interface{})["details"].([]interface{})[0]
			Expect(detail.(map[string]interface{})["compliant"]).To(Equal(string(policiesv1.NonCompliant)))
			Expect(detail.(map[string]interface{})["history"].([]interface{})).To(HaveLen(1))

			By("Job TTL should match default (1 day)")
			Eventually(func(g Gomega) interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())

				return ansiblejobList.Items[0].Object["spec"].(map[string]interface{})["job_ttl"]
			}, 10, 1).Should(Equal(int64(86400)))

			By("Mode should be set to disabled after ansiblejob is created")
			policyAutomation, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Get(
				context.TODO(), automationName, metav1.GetOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(
				policyAutomation.Object["spec"].(map[string]interface{})["mode"],
			).To(Equal(string(policyv1beta1.Disabled)))

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
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(func() interface{} {
				replicatedPlcList = utils.ListWithTimeout(
					clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
				allUpdated := true
				for _, replicatedPlc := range replicatedPlcList.Items {
					compliantStatusStr := replicatedPlc.Object["status"].(map[string]interface{})["compliant"]
					if compliantStatusStr != string(policiesv1.Compliant) {
						allUpdated = false

						break
					}
				}

				return allUpdated
			}, 30, 1).Should(BeTrue())

			By("Removing config map")
			_, err = utils.KubectlWithOutput(
				"delete", "policyautomation", "-n", testNamespace, automationName,
			)
			Expect(err).ShouldNot(HaveOccurred())
			By("Ansiblejob should also be removed")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
		})

		// Create two events then two ansiblejobs for each event
		It("Test mode = everyEvent without delayAfterRunSeconds", func() {
			By("Creating an policyAutomation with mode=everyEvent")
			_, err := utils.KubectlWithOutput("apply",
				"-f", "../resources/case5_policy_automation/case5-policy-automation-everyEvent.yaml",
				"-n", testNamespace)
			Expect(err).ShouldNot(HaveOccurred())

			By("Should not create any new ansiblejob when Compliant")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 15, 1).Should(Equal(0))

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
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(func() interface{} {
				replicatedPlcList = utils.ListWithTimeout(
					clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
				allUpdated := true
				for _, replicatedPlc := range replicatedPlcList.Items {
					compliantStatusStr := replicatedPlc.Object["status"].(map[string]interface{})["compliant"]
					if compliantStatusStr != string(policiesv1.NonCompliant) {
						allUpdated = false

						break
					}
				}

				return allUpdated
			}, 30, 1).Should(BeTrue())

			By("Should only create one ansiblejob")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))

			By("Job TTL should match patch (1 hour)")
			Eventually(func(g Gomega) interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())

				return ansiblejobList.Items[0].Object["spec"].(map[string]interface{})["job_ttl"]
			}, 10, 1).Should(Equal(int64(3600)))

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
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(func() interface{} {
				replicatedPlcList = utils.ListWithTimeout(
					clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
				allUpdated := true
				for _, replicatedPlc := range replicatedPlcList.Items {
					compliantStatusStr := replicatedPlc.Object["status"].(map[string]interface{})["compliant"]
					if compliantStatusStr != string(policiesv1.Compliant) {
						allUpdated = false

						break
					}
				}

				return allUpdated
			}, 30, 1).Should(BeTrue())

			By("Should not create any new ansiblejob when Compliant, still one ansiblejobs")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 15, 1).Should(Equal(1))

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
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(func() interface{} {
				replicatedPlcList = utils.ListWithTimeout(
					clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
				allUpdated := true
				for _, replicatedPlc := range replicatedPlcList.Items {
					compliantStatusStr := replicatedPlc.Object["status"].(map[string]interface{})["compliant"]
					if compliantStatusStr != string(policiesv1.NonCompliant) {
						allUpdated = false

						break
					}
				}

				return allUpdated
			}, 30, 1).Should(BeTrue())

			By("Should only create the second ansiblejob")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(2))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(2))

			By("Removing config map")
			_, err = utils.KubectlWithOutput(
				"delete", "policyautomation", "-n", testNamespace, automationName,
			)
			Expect(err).ShouldNot(HaveOccurred())
			By("Ansiblejob should also be removed")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))

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
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(func() interface{} {
				replicatedPlcList = utils.ListWithTimeout(
					clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
				allUpdated := true
				for _, replicatedPlc := range replicatedPlcList.Items {
					compliantStatusStr := replicatedPlc.Object["status"].(map[string]interface{})["compliant"]
					if compliantStatusStr != string(policiesv1.Compliant) {
						allUpdated = false

						break
					}
				}

				return allUpdated
			}, 30, 1).Should(BeTrue())
		})

		// Create three events during delayAfterRunSeconds period
		// Got the first ansiblejobs within delayAfterRunSeconds period for the first event
		// Only got the second ansiblejobs after delayAfterRunSeconds period for the last two events
		It("Test mode = everyEvent with delayAfterRunSeconds", func() {
			By("Creating an policyAutomation with mode=everyEvent")
			_, err := utils.KubectlWithOutput("apply",
				"-f", "../resources/case5_policy_automation/case5-policy-automation-everyEvent.yaml",
				"-n", testNamespace)
			Expect(err).ShouldNot(HaveOccurred())

			By("Patching everyEvent mode policyAutomation with delayAfterRunSeconds = 240")
			var policyAutomation *unstructured.Unstructured
			// Use Eventually since there can be a race condition for when the owner reference is added by the
			// controller.
			Eventually(func(g Gomega) {
				policyAutomation, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Get(
					context.TODO(), automationName, metav1.GetOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())

				policyAutomation.Object["spec"].(map[string]interface{})["delayAfterRunSeconds"] = 240
				_, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Update(
					context.TODO(), policyAutomation, metav1.UpdateOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())
			}).Should(Succeed())

			By("Should not create any new ansiblejob when Compliant")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 15, 1).Should(Equal(0))

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
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(func() interface{} {
				replicatedPlcList = utils.ListWithTimeout(
					clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
				allUpdated := true
				for _, replicatedPlc := range replicatedPlcList.Items {
					compliantStatusStr := replicatedPlc.Object["status"].(map[string]interface{})["compliant"]
					if compliantStatusStr != string(policiesv1.NonCompliant) {
						allUpdated = false

						break
					}
				}

				return allUpdated
			}, 30, 1).Should(BeTrue())

			By("Should only create one ansiblejob for the first event during delayAfterRunSeconds period")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))

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
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(func() interface{} {
				replicatedPlcList = utils.ListWithTimeout(
					clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
				allUpdated := true
				for _, replicatedPlc := range replicatedPlcList.Items {
					compliantStatusStr := replicatedPlc.Object["status"].(map[string]interface{})["compliant"]
					if compliantStatusStr != string(policiesv1.Compliant) {
						allUpdated = false

						break
					}
				}

				return allUpdated
			}, 30, 1).Should(BeTrue())
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 15, 1).Should(Equal(1))

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
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(func() interface{} {
				replicatedPlcList = utils.ListWithTimeout(
					clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
				allUpdated := true
				for _, replicatedPlc := range replicatedPlcList.Items {
					compliantStatusStr := replicatedPlc.Object["status"].(map[string]interface{})["compliant"]
					if compliantStatusStr != string(policiesv1.NonCompliant) {
						allUpdated = false

						break
					}
				}

				return allUpdated
			}, 30, 1).Should(BeTrue())

			By("Should not create any new ansiblejob for the second Non-Compliant event" +
				" within delayAfterRunSeconds period")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))

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
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(func() interface{} {
				replicatedPlcList = utils.ListWithTimeout(
					clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
				allUpdated := true
				for _, replicatedPlc := range replicatedPlcList.Items {
					compliantStatusStr := replicatedPlc.Object["status"].(map[string]interface{})["compliant"]
					if compliantStatusStr != string(policiesv1.Compliant) {
						allUpdated = false

						break
					}
				}

				return allUpdated
			}, 30, 1).Should(BeTrue())
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 15, 1).Should(Equal(1))

			By("Patching automationStartTime to an earlier time and let delayAfterRunSeconds expire immediately")
			policyAutomation, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Get(
				context.TODO(), automationName, metav1.GetOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			status := policyAutomation.Object["status"].(map[string]interface{})
			clustersWithEvent := status["clustersWithEvent"].(map[string]interface{})
			for _, ClusterEvent := range clustersWithEvent {
				updateStartTime := time.Now().UTC().Add(-241 * time.Second).Format(time.RFC3339)
				ClusterEvent.(map[string]interface{})["automationStartTime"] = updateStartTime
			}
			_, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).UpdateStatus(
				context.TODO(), policyAutomation, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

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
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(func() interface{} {
				replicatedPlcList = utils.ListWithTimeout(
					clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
				allUpdated := true
				for _, replicatedPlc := range replicatedPlcList.Items {
					compliantStatusStr := replicatedPlc.Object["status"].(map[string]interface{})["compliant"]
					if compliantStatusStr != string(policiesv1.NonCompliant) {
						allUpdated = false

						break
					}
				}

				return allUpdated
			}, 30, 1).Should(BeTrue())

			By("After delayAfterRunSeconds is expired, should only create the second ansiblejob")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(2))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(2))

			By("Removing config map")
			_, err = utils.KubectlWithOutput(
				"delete", "policyautomation", "-n", testNamespace, automationName,
			)
			Expect(err).ShouldNot(HaveOccurred())
			By("Ansiblejob should also be removed")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
		})
		It("Test manual run", func() {
			By("Creating an policyAutomation with mode=disable")
			_, err := utils.KubectlWithOutput("apply",
				"-f", "../resources/case5_policy_automation/case5-policy-automation-disable.yaml",
				"-n", testNamespace)
			Expect(err).ShouldNot(HaveOccurred())
			By("Applying manual run annotation")
			_, err = utils.KubectlWithOutput(
				"annotate",
				"policyautomation",
				"-n",
				testNamespace,
				automationName,
				"--overwrite",
				"policy.open-cluster-management.io/rerun=true",
			)
			Expect(err).ShouldNot(HaveOccurred())
			By("Should only create one more ansiblejob because policy is NonCompliant")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))

			By("Check each violation context field is not empty in extra_vars for the violated manual run case")
			ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
				context.TODO(), metav1.ListOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			metadata := ansiblejobList.Items[0].Object["metadata"]
			violatedAnsJobName := metadata.(map[string]interface{})["name"]
			spec := ansiblejobList.Items[0].Object["spec"]
			extraVars := spec.(map[string]interface{})["extra_vars"].(map[string]interface{})
			Expect(extraVars["policy_name"]).To(Equal("case5-test-policy"))
			Expect(extraVars["policy_namespace"]).To(Equal(testNamespace))
			Expect(extraVars["hub_cluster"]).To(Equal("millienium-falcon.tatooine.local"))
			Expect(extraVars["target_clusters"].([]interface{})).To(HaveLen(2))
			Expect(extraVars["policy_sets"].([]interface{})).To(HaveLen(1))
			Expect(extraVars["policy_sets"].([]interface{})[0]).To(Equal("case5-test-policyset"))
			managed1 := extraVars["policy_violations"].(map[string]interface{})["managed1"]
			compliant := managed1.(map[string]interface{})["compliant"]
			Expect(compliant).To(Equal(string(policiesv1.NonCompliant)))
			managed2 := extraVars["policy_violations"].(map[string]interface{})["managed2"]
			compliant = managed2.(map[string]interface{})["compliant"]
			Expect(compliant).To(Equal(string(policiesv1.NonCompliant)))

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
				Expect(err).ToNot(HaveOccurred())
			}
			By("Applying manual run annotation again")
			_, err = utils.KubectlWithOutput(
				"annotate",
				"policyautomation",
				"-n",
				testNamespace,
				automationName,
				"--overwrite",
				"policy.open-cluster-management.io/rerun=true",
			)
			Expect(err).ShouldNot(HaveOccurred())
			By("Should still create one more ansiblejob when policy is Compliant")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(2))
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(2))

			By("Check policy_violations is mostly empty for the compliant manual run case")
			ansiblejobList, err = clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
				context.TODO(), metav1.ListOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			metadata = ansiblejobList.Items[1].Object["metadata"]
			ansJobName := metadata.(map[string]interface{})["name"]
			spec = ansiblejobList.Items[1].Object["spec"]
			if ansJobName == violatedAnsJobName {
				spec = ansiblejobList.Items[0].Object["spec"]
			}

			extraVars = spec.(map[string]interface{})["extra_vars"].(map[string]interface{})
			Expect(extraVars["policy_name"]).To(Equal("case5-test-policy"))
			Expect(extraVars["policy_namespace"]).To(Equal(testNamespace))
			Expect(extraVars["hub_cluster"]).To(Equal("millienium-falcon.tatooine.local"))
			Expect(extraVars["target_clusters"].([]interface{})).To(BeEmpty())
			Expect(extraVars["policy_sets"].([]interface{})).To(HaveLen(1))
			Expect(extraVars["policy_sets"].([]interface{})[0]).To(Equal("case5-test-policyset"))
			Expect(extraVars["policy_violations"]).To(BeNil())
		})
	})
	Describe("Clean up", func() {
		It("Test AnsibleJob clean up", func() {
			By("Removing policy")
			_, err := utils.KubectlWithOutput("delete", "policy", "-n", testNamespace, case5PolicyName)
			Expect(err).ShouldNot(HaveOccurred())

			By("PolicyAutomation should also be removed")
			Eventually(func() *unstructured.Unstructured {
				policyAutomation, err := clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Get(
					context.TODO(), automationName, metav1.GetOptions{},
				)
				if !k8serrors.IsNotFound(err) {
					Expect(err).ToNot(HaveOccurred())
				}

				return policyAutomation
			}).Should(BeNil())

			By("Ansiblejob should also be removed")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(0))
		})
	})
})
