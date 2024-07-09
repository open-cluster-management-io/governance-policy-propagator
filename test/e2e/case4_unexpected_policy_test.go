// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test unexpect policy handling", Label("non-placement-rule"), func() {
	const (
		case4PolicyName string = "case4-test-policy"
		case4PolicyYaml string = "../resources/case4_unexpected_policy/case4-test-policy.yaml"
	)

	It("Unexpected root policy in cluster namespace should be deleted", func() {
		By("Creating " + case4PolicyYaml + "in cluster namespace: managed1")
		out, err := utils.KubectlWithOutput("apply",
			"-f", case4PolicyYaml,
			"-n", "managed1",
			"--kubeconfig="+kubeconfigHub)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(out).Should(ContainSubstring(case4PolicyName + " created"))
		Eventually(func() interface{} {
			return utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case4PolicyName, "managed1", false, defaultTimeoutSeconds,
			)
		}, defaultTimeoutSeconds, 1).Should(BeNil())
	})
	It("Unexpected replicated policy in cluster namespace should be deleted", func() {
		const plcYaml string = "../resources/case4_unexpected_policy/case4-test-replicated-policy.yaml"
		By("Creating " + plcYaml + " in cluster namespace: managed1")
		out, err := utils.KubectlWithOutput("apply",
			"-f", plcYaml,
			"-n", "managed1",
			"--kubeconfig="+kubeconfigHub)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(out).Should(ContainSubstring("policy-propagator-test.case4-test-policy created"))
		Eventually(func() interface{} {
			return utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				"policy-propagator-test.case4-test-policy",
				"managed1",
				false,
				defaultTimeoutSeconds,
			)
		}, defaultTimeoutSeconds, 1).Should(BeNil())
	})
	It("Unexpected replicated policy in non-cluster namespace should be skipped", func() {
		const plcYaml string = "../resources/case4_unexpected_policy/case4-test-replicated-policy-out-of-cluster.yaml"
		By("Creating " + plcYaml + " in non-cluster namespace: leaf-hub1")
		out, err := utils.KubectlWithOutput("apply",
			"-f", plcYaml,
			"-n", "leaf-hub1",
			"--kubeconfig="+kubeconfigHub)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(out).Should(ContainSubstring("policy-propagator-test.case4-test-policy created"))
		utils.Pause(2)
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, "policy-propagator-test.case4-test-policy",
			"leaf-hub1", true, defaultTimeoutSeconds,
		)
		Expect(plc).NotTo(BeNil())
	})
	It("should clean up the non-cluster policy", func() {
		utils.Kubectl("delete",
			"-f", "../resources/case4_unexpected_policy/case4-test-replicated-policy-out-of-cluster.yaml",
			"-n", "leaf-hub1", "--kubeconfig="+kubeconfigHub)
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, metav1.ListOptions{}, 0, false, 10)
	})
})
