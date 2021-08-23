// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/open-cluster-management/governance-policy-propagator/test/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const case9PolicyName string = "case9-test-policy"
const case9PolicyYaml string = "../resources/case9_templates/case9-test-policy.yaml"
const case9ReplicatedPolicyYamlM1 string = "../resources/case9_templates/case9-test-replpolicy-managed1.yaml"
const case9ReplicatedPolicyYamlM2 string = "../resources/case9_templates/case9-test-replpolicy-managed2.yaml"

var _ = Describe("Test policy templates", func() {
	Describe("Create policy, placement and referenced resource in ns:"+testNamespace, func() {
		It("should be created in user ns", func() {
			By("Creating " + case9PolicyYaml)
			utils.Kubectl("apply",
				"-f", case9PolicyYaml,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case9PolicyName, testNamespace, true, defaultTimeoutSeconds)
			Expect(plc).NotTo(BeNil())
		})
		It("should resolve templates and propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementRule, case9PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			plr, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case9PolicyName, "managed1", true, defaultTimeoutSeconds)
			Expect(plc).ToNot(BeNil())

			yamlPlc := utils.ParseYaml(case9ReplicatedPolicyYamlM1)
			Eventually(func() interface{} {
				replicatedPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case9PolicyName, "managed1", true, defaultTimeoutSeconds)
				return replicatedPlc.Object["spec"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
		})
		It("should resolve templates and propagate to cluster ns managed2", func() {
			By("Patching test-policy-plr with decision of cluster managed2")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementRule, case9PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePlrStatus("managed2")
			plr, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case9PolicyName, "managed2", true, defaultTimeoutSeconds)
			Expect(plc).ToNot(BeNil())

			yamlPlc := utils.ParseYaml(case9ReplicatedPolicyYamlM2)
			Eventually(func() interface{} {
				replicatedPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case9PolicyName, "managed2", true, defaultTimeoutSeconds)
				return replicatedPlc.Object["spec"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
		})
		It("should clean up", func() {
			utils.Kubectl("delete",
				"-f", "../resources/case9_templates/case9-test-policy.yaml",
				"-n", testNamespace)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, defaultTimeoutSeconds)
		})

	})
})
