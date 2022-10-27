// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test policy template metrics", Ordered, func() {
	const (
		policyName = "case9-test-policy"
		policyYaml = "../resources/case9_templates/case9-test-policy.yaml"
	)

	Describe("Create policy, placement and referenced resource in ns:"+testNamespace, func() {
		It("should be created in user ns", func() {
			By("Creating " + policyYaml)
			utils.Kubectl("apply",
				"-f", policyYaml,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, policyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})

		It("should resolve templates and propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, policyName+"-plr", testNamespace,
				true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+policyName, "managed1",
				true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())

			yamlPlc := utils.ParseYaml(case9ReplicatedPolicyYamlM1)
			Eventually(func() interface{} {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic,
					gvrPolicy,
					testNamespace+"."+policyName,
					"managed1",
					true,
					defaultTimeoutSeconds,
				)

				return replicatedPlc.Object["spec"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
		})

		It("should correctly report root policy hub template watches when propagated", func() {
			By("Checking metric endpoint for root policy hub template watches")
			Eventually(func() interface{} {
				return utils.GetMetrics("hub_templates_active_watches", "\"[0-9]\"")
			}, defaultTimeoutSeconds, 1).Should(Equal([]string{"2"}))
		})

		cleanup := func() {
			utils.Kubectl("delete",
				"-f", policyYaml,
				"-n", testNamespace)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, defaultTimeoutSeconds)
		}

		It("should clean up", cleanup)

		It("should report root policy 0 hub template watches after clean up", func() {
			By("Checking metric endpoint for root policy hub template watches")
			Eventually(func() interface{} {
				return utils.GetMetrics("hub_templates_active_watches", "\"[0-9]\"")
			}, defaultTimeoutSeconds, 1).Should(Equal([]string{"0"}))
		})

		AfterAll(cleanup)
	})
})
