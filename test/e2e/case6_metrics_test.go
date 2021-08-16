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

const case6PolicyName string = "case6-test-policy"
const case6PolicyYaml string = "../resources/case6_metrics/case6-test-policy.yaml"

var _ = Describe("Test metrics appear locally", func() {
	It("should report 0 for compliant root policy and replicated policies", func() {
		By("Creating " + case6PolicyYaml)
		utils.Kubectl("apply",
			"-f", case6PolicyYaml,
			"-n", testNamespace)
		plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
		Expect(plc).NotTo(BeNil())
		By("Patching test-policy-plr with decision of cluster managed1 and managed2")
		plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementRule, case6PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds)
		plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
		_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
		Expect(err).To(BeNil())
		plc = utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed2", true, defaultTimeoutSeconds)
		Expect(plc).ToNot(BeNil())
		opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
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
		yamlPlc := utils.ParseYaml("../resources/case6_metrics/managed-both-status-compliant.yaml")
		Eventually(func() interface{} {
			rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return rootPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		By("Checking metric endpoint for root policy status")
		Eventually(func() interface{} {
			return utils.GetMetrics("policy_governance_info", `policy=\"case6-test-policy\"`, `type=\"root\"`)
		}, defaultTimeoutSeconds, 1).Should(Equal([]string{"0"}))
		By("Checking metric endpoint for managed1 replicated policy status")
		Eventually(func() interface{} {
			return utils.GetMetrics("policy_governance_info", `policy=\"case6-test-policy\"`, `cluster_namespace=\"managed1\",`)
		}, defaultTimeoutSeconds, 1).Should(Equal([]string{"0"}))
		By("Checking metric endpoint for managed2 replicated policy status")
		Eventually(func() interface{} {
			return utils.GetMetrics("policy_governance_info", `policy=\"case6-test-policy\"`, `cluster_namespace=\"managed2\",`)
		}, defaultTimeoutSeconds, 1).Should(Equal([]string{"0"}))
	})
	It("should report 1 for noncompliant root policy and replicated policies", func() {
		By("Patching both replicated policy status to noncompliant")
		opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
		replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		for _, replicatedPlc := range replicatedPlcList.Items {
			replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
				ComplianceState: policiesv1.NonCompliant,
			}
			_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(context.TODO(), &replicatedPlc, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
		}
		By("Checking the status of root policy")
		yamlPlc := utils.ParseYaml("../resources/case6_metrics/managed-both-status-noncompliant.yaml")
		Eventually(func() interface{} {
			rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			return rootPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		By("Checking metric endpoint for root policy status")
		Eventually(func() interface{} {
			return utils.GetMetrics("policy_governance_info", `policy=\"case6-test-policy\"`, `type=\"root\"`)
		}, defaultTimeoutSeconds, 1).Should(Equal([]string{"1"}))
		By("Checking metric endpoint for managed1 replicated policy status")
		Eventually(func() interface{} {
			return utils.GetMetrics("policy_governance_info", `policy=\"case6-test-policy\"`, `cluster_namespace=\"managed1\",`)
		}, defaultTimeoutSeconds, 1).Should(Equal([]string{"1"}))
		By("Checking metric endpoint for managed2 replicated policy status")
		Eventually(func() interface{} {
			return utils.GetMetrics("policy_governance_info", `policy=\"case6-test-policy\"`, `cluster_namespace=\"managed2\",`)
		}, defaultTimeoutSeconds, 1).Should(Equal([]string{"1"}))
	})
	It("should not report metrics for policies after they are deleted", func() {
		By("Deleting the policy")
		utils.Kubectl("delete",
			"-f", case6PolicyYaml,
			"-n", testNamespace)
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, 10)
		By("Checking metric endpoint for root policy status")
		Eventually(func() interface{} {
			return utils.GetMetrics("policy_governance_info", `policy=\"case6-test-policy\"`, `type=\"root\"`)
		}, defaultTimeoutSeconds, 1).Should(Equal([]string{}))
		By("Checking metric endpoint for managed1 replicated policy status")
		Eventually(func() interface{} {
			return utils.GetMetrics("policy_governance_info", `policy=\"case6-test-policy\"`, `cluster_namespace=\"managed1\",`)
		}, defaultTimeoutSeconds, 1).Should(Equal([]string{}))
		By("Checking metric endpoint for managed2 replicated policy status")
		Eventually(func() interface{} {
			return utils.GetMetrics("policy_governance_info", `policy=\"case6-test-policy\"`, `cluster_namespace=\"managed2\",`)
		}, defaultTimeoutSeconds, 1).Should(Equal([]string{}))
	})
})
