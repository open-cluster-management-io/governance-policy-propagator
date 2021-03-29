// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/open-cluster-management/governance-policy-propagator/test/utils"
)

const case4PolicyName string = "case4-test-policy"
const case4PolicyYaml string = "../resources/case4_unexpected_policy/case4-test-policy.yaml"

var _ = Describe("Test unexpect policy handling", func() {
	It("Unexpected root policy in cluster namespace should be deleted", func() {
		By("Creating " + case4PolicyYaml + "in cluster namespace: managed1")
		out, _ := utils.KubectlWithOutput("apply",
			"-f", case4PolicyYaml,
			"-n", "managed1")
		Expect(out).Should(ContainSubstring(case4PolicyName + " created"))
		Eventually(func() interface{} {
			return utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case4PolicyName, "managed1", false, defaultTimeoutSeconds)
		}, defaultTimeoutSeconds, 1).Should(BeNil())
	})
	It("Unexpected replicated policy in cluster namespace should be deleted", func() {
		const plcYaml string = "../resources/case4_unexpected_policy/case4-test-replicated-policy.yaml"
		By("Creating " + plcYaml + " in cluster namespace: managed1")
		out, _ := utils.KubectlWithOutput("apply",
			"-f", plcYaml,
			"-n", "managed1")
		Expect(out).Should(ContainSubstring("policy-propagator-test.case1-test-policy created"))
		Eventually(func() interface{} {
			return utils.GetWithTimeout(clientHubDynamic, gvrPolicy, "policy-propagator-test.case1-test-policy", "managed1", false, defaultTimeoutSeconds)
		}, defaultTimeoutSeconds, 1).Should(BeNil())
	})
})
