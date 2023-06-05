// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"sort"
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

const automationName string = "create-service.now-ticket"

var _ = Describe("Test policy automation", Ordered, func() {
	ansiblelistlen := 0
	// Use this only when target_clusters managed1 managed2 managed3
	getLastAnsiblejob := func() *unstructured.Unstructured {
		ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
			context.TODO(), metav1.ListOptions{},
		)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		for _, ansiblejob := range ansiblejobList.Items {
			targetClusters, _, err := unstructured.NestedSlice(ansiblejob.Object,
				"spec", "extra_vars", "target_clusters")
			if err != nil {
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
			}
			for _, clusterName := range targetClusters {
				if clusterName == "managed3" {
					return &ansiblejob
				}
			}
		}

		return nil
	}
	getLastAnsiblejobByTime := func() *unstructured.Unstructured {
		ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
			context.TODO(), metav1.ListOptions{},
		)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		sort.Slice(ansiblejobList.Items, func(i, j int) bool {
			p1 := ansiblejobList.Items[i].GetCreationTimestamp()
			p2 := ansiblejobList.Items[j].GetCreationTimestamp()

			return !p1.Before(&p2)
		})

		return &ansiblejobList.Items[0]
	}
	getTargetListlen := func(ansiblejobList *unstructured.UnstructuredList) int {
		var index int
		if len(ansiblejobList.Items) > 0 {
			index = len(ansiblejobList.Items) - 1
		} else {
			return 0
		}
		spec := ansiblejobList.Items[index].Object["spec"]
		extraVars := spec.(map[string]interface{})["extra_vars"].(map[string]interface{})

		return len(extraVars["target_clusters"].([]interface{}))
	}
	// Use this only when target_clusters managed1 managed2 managed3
	getLastJobCompliant := func() string {
		ansiblejob := getLastAnsiblejob()
		lastCompliant, _, err := unstructured.NestedString(ansiblejob.Object,
			"spec", "extra_vars", "policy_violations", "managed3", "compliant")

		Expect(err).ToNot(HaveOccurred())

		return lastCompliant
	}

	BeforeAll(func() {
		ansiblelistlen = 0
		By("Create policy/pb/plc in ns:" + testNamespace + " and then update pb/plc")
		By("Creating " + case5PolicyName + " in user ns")
		_, err := utils.KubectlWithOutput("apply",
			"-f", case5PolicyYaml,
			"-n", testNamespace)
		Expect(err).ShouldNot(HaveOccurred())
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case5PolicyName, testNamespace, true, defaultTimeoutSeconds,
		)
		Expect(plc).NotTo(BeNil())
		plr := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPlacementRule,
			case5PolicyName+"set-plr",
			testNamespace,
			true,
			defaultTimeoutSeconds,
		)
		plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2", "managed3")
		_, err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
			context.TODO(), plr, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())
		opt := metav1.ListOptions{
			LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
		}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
	})

	cleanupPolicyAutomation := func() {
		By("Removing config map")
		_, err := utils.KubectlWithOutput(
			"delete", "policyautomation", "-n", testNamespace, automationName,
			"--ignore-not-found",
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

		By("Patching policy to make all clusters back to Compliant")
		opt := metav1.ListOptions{
			LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
		}
		replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
				clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
	}

	Describe("Test PolicyAutomation spec.mode", Ordered, func() {
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
			By("Patching policy to make all clusters NonCompliant")
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
			var ansiblejobList *unstructured.UnstructuredList
			Eventually(func() interface{} {
				ansiblejobList, err = clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(1))
			Consistently(func() interface{} {
				ansiblejobList, err = clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())
				index := len(ansiblejobList.Items) - 1
				spec := ansiblejobList.Items[index].Object["spec"]
				extraVars := spec.(map[string]interface{})["extra_vars"].(map[string]interface{})

				return len(extraVars) > 0
			}, 30, 1).Should(BeTrue())

			By("Check each violation context field in extra_vars")
			Expect(err).ToNot(HaveOccurred())
			lastAnsiblejob := ansiblejobList.Items[0]
			spec := lastAnsiblejob.Object["spec"]
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

			By("Change mode to once again, should create one more ansiblejob")
			Eventually(func(g Gomega) {
				policyAutomation, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Get(
					context.TODO(), automationName, metav1.GetOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())

				policyAutomation.Object["spec"].(map[string]interface{})["mode"] = string(policyv1beta1.Once)
				_, err = clientHubDynamic.Resource(gvrPolicyAutomation).Namespace(testNamespace).Update(
					context.TODO(), policyAutomation, metav1.UpdateOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())
			}).Should(Succeed())

			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(Equal(2))

			By("Patching policy to make all clusters back to Compliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
					clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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

			By("Patching policy to make all clusters NonCompliant")
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
					clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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

			By("Should create ansiblejobs more than 1")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(BeNumerically(">", 0))

			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(BeNumerically(">", 0))

			By("Should the last ansiblejob include managed3")
			Eventually(func() interface{} {
				return getLastJobCompliant()
			}, 30, 1).Should(Equal("NonCompliant"))

			By("Job TTL should match patch (1 hour)")
			Eventually(func(g Gomega) interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				g.Expect(err).ToNot(HaveOccurred())

				// Save ansiblelistlen for next test
				ansiblelistlen = len(ansiblejobList.Items)
				index := len(ansiblejobList.Items) - 1

				return ansiblejobList.Items[index].Object["spec"].(map[string]interface{})["job_ttl"]
			}, 10, 1).Should(Equal(int64(3600)))

			By("Patching policy to make all clusters back to Compliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
					clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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

			By("Should not create any new ansiblejob when policies become Compliant")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 15, 1).Should(Equal(ansiblelistlen))

			By("Patching policy to make all clusters back to NonCompliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
					clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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

			By("Should the last ansiblejob include managed3 that is noncompliant")
			Eventually(func() interface{} {
				return getLastJobCompliant()
			}, 30, 1).Should(Equal("NonCompliant"))

			By("Should more ansiblejobs after change Compliant to Noncompliant")
			Eventually(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				// This ansiblelistlen is the length created before changed to noncompliant
				return len(ansiblejobList.Items) > ansiblelistlen
			}, 30, 1).Should(BeTrue())
			cleanupPolicyAutomation()
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

			By("Patching policy to make all clusters NonCompliant")
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
					clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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

			By("checking the last AnsibleJob has managed3 in target_clsuter for" +
				"the first event during delayAfterRunSeconds period")
			Eventually(func() interface{} {
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return getLastJobCompliant()
			}, 30, 1).Should(Equal("NonCompliant"))

			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ShouldNot(HaveOccurred())

				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())
				// Save ansiblelistlen for next test
				ansiblelistlen = len(ansiblejobList.Items)

				return getLastJobCompliant()
			}, 30, 1).Should(Equal("NonCompliant"))

			By("Patching policy to make all clusters back to Compliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
					clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
			By("Should not create a new ansiblejobs when policies become Compliant")
			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 15, 1).Should(Equal(ansiblelistlen))

			// Save ansiblelistlen that indicate before compliant change
			ansiblejobList, _ := clientHubDynamic.Resource(gvrAnsibleJob).List(
				context.TODO(), metav1.ListOptions{},
			)
			ansiblelistlen = len(ansiblejobList.Items)

			By("Patching policy to make all clusters back to NonCompliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
					clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
			}, 30, 1).Should(Equal(ansiblelistlen))

			By("Patching policy to make all clusters back to Compliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
					clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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

			// Save ansiblelist length for next test
			ansiblejobList, err = clientHubDynamic.Resource(gvrAnsibleJob).List(
				context.TODO(), metav1.ListOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			ansiblelistlen = len(ansiblejobList.Items)

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

			By("Patching policy to make all clusters back to NonCompliant")
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
			}
			replicatedPlcList = utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
					clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
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
			}, 30, 1).Should(BeNumerically(">", ansiblelistlen))

			Consistently(func() interface{} {
				ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
					context.TODO(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())

				return len(ansiblejobList.Items)
			}, 30, 1).Should(BeNumerically(">", ansiblelistlen))

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

			cleanupPolicyAutomation()
		})
	})

	Describe("Test PolicyAutomation Manual run", func() {
		Describe("Test manual run", func() {
			It("should no issue when init policyAutomation with disable and manual ", func() {
				By("Patching policy to make all clusters NonCompliant")
				opt := metav1.ListOptions{
					LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
				}
				replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy,
					opt, 3, true, defaultTimeoutSeconds)
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
			})
			It("Should only create one ansiblejob which include 3 noncompliant target_clusters", func() {
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
					Expect(err).ShouldNot(HaveOccurred())
					_, err = utils.KubectlWithOutput("get", "ansiblejobs", "-n", testNamespace)
					Expect(err).ShouldNot(HaveOccurred())

					return getTargetListlen(ansiblejobList)
				}, 30, 1).Should(Equal(3))
			})

			It("Check each violation context field is not empty in "+
				"extra_vars for the violated manual run case", func() {
				lastAnsiblejob := getLastAnsiblejob()
				spec := lastAnsiblejob.Object["spec"]
				extraVars := spec.(map[string]interface{})["extra_vars"].(map[string]interface{})
				Expect(extraVars["policy_name"]).To(Equal("case5-test-policy"))
				Expect(extraVars["policy_namespace"]).To(Equal(testNamespace))
				Expect(extraVars["hub_cluster"]).To(Equal("millienium-falcon.tatooine.local"))
				Expect(extraVars["target_clusters"].([]interface{})).To(HaveLen(3))
				Expect(extraVars["policy_sets"].([]interface{})).To(HaveLen(1))
				Expect(extraVars["policy_sets"].([]interface{})[0]).To(Equal("case5-test-policyset"))
				managed1 := extraVars["policy_violations"].(map[string]interface{})["managed1"]
				compliant := managed1.(map[string]interface{})["compliant"]
				Expect(compliant).To(Equal(string(policiesv1.NonCompliant)))
				managed2 := extraVars["policy_violations"].(map[string]interface{})["managed2"]
				compliant = managed2.(map[string]interface{})["compliant"]
				Expect(compliant).To(Equal(string(policiesv1.NonCompliant)))
			})

			It("Patching policy to make all clusters back to Compliant", func() {
				opt := metav1.ListOptions{
					LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case5PolicyName,
				}
				replicatedPlcList := utils.ListWithTimeout(clientHubDynamic,
					gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
				for _, replicatedPlc := range replicatedPlcList.Items {
					replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
						ComplianceState: policiesv1.Compliant,
					}
					_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
						context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
					)
					Expect(err).ToNot(HaveOccurred())
				}
			})

			It("Should create one ansible job when the policy is compliant", func() {
				By("Applying manual run annotation again")
				_, err := utils.KubectlWithOutput(
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
			})
			It("Check policy_violations is mostly empty for the compliant manual run case", func() {
				lastAnsibleJob := getLastAnsiblejobByTime()
				spec := lastAnsibleJob.Object["spec"]

				extraVars := spec.(map[string]interface{})["extra_vars"].(map[string]interface{})
				Expect(extraVars["policy_name"]).To(Equal("case5-test-policy"))
				Expect(extraVars["policy_namespace"]).To(Equal(testNamespace))
				Expect(extraVars["hub_cluster"]).To(Equal("millienium-falcon.tatooine.local"))
				Expect(extraVars["target_clusters"].([]interface{})).To(BeEmpty())
				Expect(extraVars["policy_sets"].([]interface{})).To(HaveLen(1))
				Expect(extraVars["policy_sets"].([]interface{})[0]).To(Equal("case5-test-policyset"))
				Expect(extraVars["policy_violations"]).To(BeNil())
				cleanupPolicyAutomation()
			})
		})
		Describe("Test manual run and diable", func() {
			It("Change policy to disabled and create policyAutomation with disabled", func() {
				By("Change policy disabled to true")
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case5PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)
				Expect(rootPlc).NotTo(BeNil())
				rootPlc.Object["spec"].(map[string]interface{})["disabled"] = true
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Update(
					context.TODO(), rootPlc, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				By("Creating an policyAutomation with mode=disable")
				_, err = utils.KubectlWithOutput("apply",
					"-f", "../resources/case5_policy_automation/case5-policy-automation-disable.yaml",
					"-n", testNamespace)
				Expect(err).ShouldNot(HaveOccurred())
			})
			It("Should no issue when policy set to disabled = true ", func() {
				By("Applying manual run annotation")
				_, err := utils.KubectlWithOutput(
					"annotate",
					"policyautomation",
					"-n",
					testNamespace,
					automationName,
					"--overwrite",
					"policy.open-cluster-management.io/rerun=true",
				)
				Expect(err).ShouldNot(HaveOccurred())

				By("Change policy disabled to false")
				rootPlc := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicy, case5PolicyName, testNamespace, true, defaultTimeoutSeconds,
				)
				Expect(rootPlc).NotTo(BeNil())
				Expect(rootPlc.Object["spec"].(map[string]interface{})["disabled"]).To(BeTrue())
				rootPlc.Object["spec"].(map[string]interface{})["disabled"] = false
				_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Update(
					context.TODO(), rootPlc, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				By("The ansiblejob should be only one")
				Eventually(func() interface{} {
					ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
						context.TODO(), metav1.ListOptions{},
					)
					Expect(err).ToNot(HaveOccurred())

					return len(ansiblejobList.Items)
				}, 30, 1).Should(Equal(1))
			})
			It("Should create one more ansiblejob when the manual run is set again ", func() {
				By("Applying manual run annotation")
				_, err := utils.KubectlWithOutput(
					"annotate",
					"policyautomation",
					"-n",
					testNamespace,
					automationName,
					"--overwrite",
					"policy.open-cluster-management.io/rerun=true",
				)
				Expect(err).ShouldNot(HaveOccurred())

				By("The ansiblejob should be two")
				Eventually(func() interface{} {
					ansiblejobList, err := clientHubDynamic.Resource(gvrAnsibleJob).Namespace(testNamespace).List(
						context.TODO(), metav1.ListOptions{},
					)
					Expect(err).ToNot(HaveOccurred())

					return len(ansiblejobList.Items)
				}, 30, 1).Should(Equal(2))

				cleanupPolicyAutomation()
			})
		})
	})

	AfterAll(func() {
		By("Removing policy")
		_, err := utils.KubectlWithOutput("delete", "policy", "-n", testNamespace, case5PolicyName)
		Expect(err).ToNot(HaveOccurred())
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
