// Copyright (c) 2020 Red Hat, Inc.

package e2e

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const case3PolicyName string = "case3-test-policy"
const case3PolicyYaml string = "../resources/case3_mutation_recovery/case3-test-policy.yaml"

var _ = Describe("Test unexpected policy mutation", func() {
	BeforeEach(func() {
		By("Creating " + case3PolicyYaml)
		Kubectl("apply",
			"-f", case3PolicyYaml,
			"-n", testNamespace)
		plc := GetWithTimeout(clientHubDynamic, gvrPolicy, case3PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
		By("Patch test-policy-plr with decision of cluster managed1 and managed2")
		plr := GetWithTimeout(clientHubDynamic, gvrPlacementRule, case3PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds)
		plr.Object["status"] = GeneratePlrStatus("managed1", "managed2")
		plr, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(plr, metav1.UpdateOptions{})
		Expect(err).To(BeNil())
		opt := metav1.ListOptions{LabelSelector: "root-policy=" + testNamespace + "." + case3PolicyName}
		By("Patch both replicated policy status to compliant")
		replicatedPlcList := ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		for _, replicatedPlc := range replicatedPlcList.Items {
			replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
				ComplianceState: policiesv1.Compliant,
			}
			_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(&replicatedPlc, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
		}
		By("Checking the status of root policy")
		yamlPlc := ParseYaml("../resources/case3_mutation_recovery/managed-both-status-compliant.yaml")
		Eventually(func() interface{} {
			rootPlc := GetWithTimeout(clientHubDynamic, gvrPolicy, case3PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return rootPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(SemanticEqual(yamlPlc.Object["status"]))
	})
	AfterEach(func() {
		Kubectl("delete",
			"-f", case3PolicyYaml,
			"-n", testNamespace)
		opt := metav1.ListOptions{}
		ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
	It("Should recreate replicated policy when deleted", func() {
		By("Deleting policy in cluster ns")
		Kubectl("delete", "policy", "-n", "managed1", "--all")
		Kubectl("delete", "policy", "-n", "managed2", "--all")
		By("Checking number of policy left in all ns")
		opt := metav1.ListOptions{}
		ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
	})
	It("Should recover replicated policy when modified field disabled", func() {
		By("Modifiying policy in cluster ns managed2")
		plc := GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds)
		Expect(plc).ToNot(BeNil())
		plc.Object["spec"].(map[string]interface{})["disabled"] = true
		plc, err := clientHubDynamic.Resource(gvrPolicy).Namespace("managed2").Update(plc, metav1.UpdateOptions{})
		Expect(err).To(BeNil())
		Expect(plc.Object["spec"].(map[string]interface{})["disabled"]).To(Equal(true))
		By("Get policy in cluster ns managed2 again")
		Eventually(func() interface{} {
			plc = GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds)
			return plc.Object["spec"].(map[string]interface{})["disabled"]
		}, defaultTimeoutSeconds, 1).Should(SemanticEqual(false))
	})
	It("Should recover replicated policy when modified field remediationAction", func() {
		By("Modifiying policy in cluster ns managed2")
		plc := GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds)
		Expect(plc).ToNot(BeNil())
		plc.Object["spec"].(map[string]interface{})["remediationAction"] = "enforce"
		plc, err := clientHubDynamic.Resource(gvrPolicy).Namespace("managed2").Update(plc, metav1.UpdateOptions{})
		Expect(err).To(BeNil())
		Expect(plc.Object["spec"].(map[string]interface{})["remediationAction"]).To(Equal("enforce"))
		By("Getting policy in cluster ns managed2 again")
		Eventually(func() interface{} {
			plc = GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds)
			return plc.Object["spec"].(map[string]interface{})["remediationAction"]
		}, defaultTimeoutSeconds, 1).Should(SemanticEqual("enforce"))
	})
	It("Should recover replicated policy when modified field policy-templates", func() {
		By("Modifiying policy in cluster ns managed2")
		plc := GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds)
		Expect(plc).ToNot(BeNil())
		plc.Object["spec"].(map[string]interface{})["policy-templates"] = []*policiesv1.PolicyTemplate{}
		plc, err := clientHubDynamic.Resource(gvrPolicy).Namespace("managed2").Update(plc, metav1.UpdateOptions{})
		Expect(err).To(BeNil())
		By("Getting policy in cluster ns managed2 again")
		rootPlc := GetWithTimeout(clientHubDynamic, gvrPolicy, case3PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Eventually(func() interface{} {
			plc = GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds)
			return plc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(SemanticEqual(rootPlc.Object["spec"]))
	})
	It("Should recover root policy status if modified", func() {
		By("Modifiying policy in cluster ns managed2")
		rootPlc := GetWithTimeout(clientHubDynamic, gvrPolicy, case3PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(rootPlc).ToNot(BeNil())
		rootPlc.Object["status"] = policiesv1.PolicyStatus{}
		rootPlc, err := clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).UpdateStatus(rootPlc, metav1.UpdateOptions{})
		Expect(err).To(BeNil())
		By("Getting root policy again")
		yamlPlc := ParseYaml("../resources/case3_mutation_recovery/managed-both-status-compliant.yaml")
		Eventually(func() interface{} {
			rootPlc = GetWithTimeout(clientHubDynamic, gvrPolicy, case3PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return rootPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(SemanticEqual(yamlPlc.Object["status"]))
	})
	It("Should ignore root policy created in cluster ns", func() {
		By("Creating a policy in cluster ns managed1")
		Kubectl("apply", "-f", case3PolicyYaml, "-n", "managed1")
		Pause(2)
		opt := metav1.ListOptions{}
		ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 4, true, defaultTimeoutSeconds)
		Kubectl("delete", "-f", case3PolicyYaml, "-n", "managed1")
	})
})
