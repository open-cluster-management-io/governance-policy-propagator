// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case17PolicyLongYaml       string = "../resources/case17_policy_webhook/case17_policy_long.yaml"
	case17PolicyReplicatedYaml string = "../resources/case17_policy_webhook/" +
		"case17_policy_replicate.yaml"
	longNamesapce              string = "long-long-long-long-long-long-long"
	case17PolicyReplicatedName string = "case17-test-policy-replicated-longlong"
	errMsg                     string = `admission webhook "policy.open-cluster-management.io.webhook" denied the ` +
		`request: the combined length of the policy namespace and name <namespace>.<name> ` +
		`cannot exceed 63 characters`
)

var _ = Describe("Test policy webhook", Label("webhook"), Ordered, func() {
	BeforeAll(func() {
		_, err := utils.KubectlWithOutput("create",
			"ns", longNamesapce,
		)
		Expect(err).ShouldNot(HaveOccurred())
	})
	AfterAll(func() {
		// cleanup
		_, err := utils.KubectlWithOutput("delete",
			"ns", longNamesapce,
			"--ignore-not-found",
		)
		Expect(err).ShouldNot(HaveOccurred())

		_, err = utils.KubectlWithOutput("delete",
			"policy", case17PolicyReplicatedName,
			"-n", testNamespace,
			"--ignore-not-found",
		)
		Expect(err).ShouldNot(HaveOccurred())
	})
	Describe("Test name + namespace over 63", func() {
		It("Should the error message is presented", func() {
			output, err := utils.KubectlWithOutput("apply",
				"-f", case17PolicyLongYaml,
				"-n", longNamesapce)
			Expect(err).Should(HaveOccurred())
			Expect(output).Should(ContainSubstring(errMsg))
		})
		It("Should replicated policy should not be validated", func() {
			_, err := utils.KubectlWithOutput("apply",
				"-f", case17PolicyReplicatedYaml,
				"-n", testNamespace)
			Expect(err).ShouldNot(HaveOccurred())
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case17PolicyReplicatedName+"-plr",
				testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Replicated policy name and cluster namespace should be over 63 character")
			Expect(utf8.RuneCountInString("managed1." + testNamespace +
				"." + case17PolicyReplicatedName)).Should(BeNumerically(">", 63))

			By("Replicated policy should be created")
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case17PolicyReplicatedName,
				"managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
		})
	})
})
