// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"fmt"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test root policy metrics", Ordered, func() {
	const (
		policyName           = "case9-test-policy"
		policyYaml           = "../resources/case9_templates/case9-test-policy.yaml"
		replicatedPolicyYaml = "../resources/case9_templates/case9-test-replpolicy-managed1.yaml"
	)

	Describe("Create policy, placement and referenced resource in ns:"+testNamespace, func() {
		prePolicyDuration := -1

		It("should record root policy duration before the policy is created", func() {
			durationMetric := utils.GetMetrics(
				"ocm_handle_root_policy_duration_seconds_bucket_bucket", fmt.Sprintf(`le=\"%d\"`, 10))
			Expect(durationMetric).ShouldNot(BeEmpty())

			numEvals, err := strconv.Atoi(durationMetric[0])
			Expect(err).ShouldNot(HaveOccurred())

			prePolicyDuration = numEvals
			Expect(prePolicyDuration).Should(BeNumerically(">", -1))
		})

		It("should be created in user ns", func() {
			By("Creating " + policyYaml)
			utils.Kubectl("apply",
				"-f", policyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
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
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+policyName, "managed1",
				true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())

			yamlPlc := utils.ParseYaml(replicatedPolicyYaml)
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

		It("should update root policy duration after the policy is created", func() {
			By("Checking metric bucket for root policy duration (10 seconds or less)")
			Eventually(func() interface{} {
				metric := utils.GetMetrics(
					"ocm_handle_root_policy_duration_seconds_bucket_bucket", fmt.Sprintf(`le=\"%d\"`, 10))
				if len(metric) == 0 {
					return false
				}
				numEvals, err := strconv.Atoi(metric[0])
				if err != nil {
					return false
				}

				return numEvals > prePolicyDuration
			}, defaultTimeoutSeconds, 1).Should(BeTrue())
		})

		It("should correctly report root policy hub template watches when propagated", func() {
			By("Checking metric endpoint for root policy hub template watches")
			Eventually(func() interface{} {
				return utils.GetMetrics("hub_templates_active_watches", "\"[0-9]\"")
			}, defaultTimeoutSeconds, 1).Should(Equal([]string{"3"}))
		})

		cleanup := func() {
			utils.Kubectl("delete",
				"-f", policyYaml,
				"-n", testNamespace,
				"--ignore-not-found",
				"--kubeconfig="+kubeconfigHub)
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
