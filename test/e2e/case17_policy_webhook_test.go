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
	case17Prefix                     string = "../resources/case17_policy_webhook/"
	case17PolicyLongYaml             string = case17Prefix + "case17_policy_long.yaml"
	case17PolicyReplicatedYaml       string = case17Prefix + "case17_policy_replicate.yaml"
	case17PolicyRemediationYaml      string = case17Prefix + "case17_invalid_remediation_policy.yaml"
	case17PolicyRootRemediationYaml  string = case17Prefix + "case17_valid_remediation_policy_root.yaml"
	case17PolicyCfplcRemediationYaml string = case17Prefix + "case17_valid_remediation_policy_cfplc.yaml"
	longNamespace                    string = "long-long-long-long-long-long-long"
	case17PolicyReplicatedName       string = "case17-test-policy-replicated-longlong"
	case17PolicyReplicatedPlr        string = "case17-test-policy-replicated-longlong-plr"
	case17PolicyReplicatedPb         string = "case17-test-policy-replicated-longlong-pb"
	case17PolicyRemediationName      string = "case17-test-policy-no-remediation"
	case17PolicyRootRemediationName  string = "case17-test-policy-root-remediation"
	case17PolicyCfplcRemediationName string = "case17-test-policy-cfplc-remediation"
	errPrefix                        string = `admission webhook "policy.open-cluster-management.io.webhook" ` +
		`denied the request: `
	combinedLengthErr string = errPrefix + "the combined length of the policy namespace and name " +
		"cannot exceed 62 characters"
	remediationErr string = errPrefix + "RemediationAction field of the policy and policy template " +
		"cannot both be unset"
)

var _ = Describe("Test policy webhook", Label("webhook"), Ordered, func() {
	Describe("Test name + namespace over 63", func() {
		BeforeAll(func() {
			_, err := utils.KubectlWithOutput("create",
				"ns", longNamespace, "--kubeconfig="+kubeconfigHub,
			)
			Expect(err).ShouldNot(HaveOccurred())
		})
		AfterAll(func() {
			// cleanup
			_, err := utils.KubectlWithOutput("delete",
				"ns", longNamespace,
				"--kubeconfig="+kubeconfigHub,
				"--ignore-not-found",
			)
			Expect(err).ShouldNot(HaveOccurred())

			_, err = utils.KubectlWithOutput("delete",
				"policy", case17PolicyReplicatedName,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub,
				"--ignore-not-found",
			)
			Expect(err).ShouldNot(HaveOccurred())

			_, err = utils.KubectlWithOutput("delete",
				"placementrule", case17PolicyReplicatedPlr,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub,
				"--ignore-not-found",
			)
			Expect(err).ShouldNot(HaveOccurred())

			_, err = utils.KubectlWithOutput("delete",
				"placementbinding", case17PolicyReplicatedPb,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub,
				"--ignore-not-found",
			)
			Expect(err).ShouldNot(HaveOccurred())
		})
		It("Should the error message is presented", func() {
			output, err := utils.KubectlWithOutput("apply",
				"-f", case17PolicyLongYaml,
				"-n", longNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).Should(HaveOccurred())
			Expect(output).Should(ContainSubstring(combinedLengthErr))
		})
		It("Should replicated policy should not be validated", func() {
			_, err := utils.KubectlWithOutput("apply",
				"-f", case17PolicyReplicatedYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
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

	Describe("The remediationAction field should not be unset in both root policy and policy templates", func() {
		AfterAll(func() {
			_, err := utils.KubectlWithOutput("delete",
				"policy", case17PolicyRemediationName,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub,
				"--ignore-not-found",
			)
			Expect(err).ShouldNot(HaveOccurred())

			_, err = utils.KubectlWithOutput("delete",
				"policy", case17PolicyRootRemediationName,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub,
				"--ignore-not-found",
			)
			Expect(err).ShouldNot(HaveOccurred())

			_, err = utils.KubectlWithOutput("delete",
				"policy", case17PolicyCfplcRemediationName,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub,
				"--ignore-not-found",
			)
			Expect(err).ShouldNot(HaveOccurred())
		})

		It("Should return a validation error when creating policies", func() {
			By("Applying a policy where both the remediationAction of the root policy " +
				"and the configuration policy in the policy templates are unset")
			output, err := utils.KubectlWithOutput("apply",
				"-f", case17PolicyRemediationYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).Should(HaveOccurred())
			Expect(output).Should(ContainSubstring(remediationErr))
		})

		It("Should not return a validation error when creating policies", func() {
			By("Applying a policy where only the remediationAction of the " +
				"root policy is set")
			_, err := utils.KubectlWithOutput("apply",
				"-f", case17PolicyRootRemediationYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ShouldNot(HaveOccurred())

			By("Applying a policy where only the remediationAction of the " +
				"configuration policy in the policy templates is set")
			_, err = utils.KubectlWithOutput("apply",
				"-f", case17PolicyCfplcRemediationYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ShouldNot(HaveOccurred())
		})

		It("Should return a validation error when updating policies", func() {
			By("Patching a policy so that the remediationAction field of both " +
				"the root policy and configuration policy is unset")

			output, err := utils.KubectlWithOutput("patch", "policy",
				case17PolicyRootRemediationName, "-n", testNamespace,
				"--kubeconfig="+kubeconfigHub,
				"--type=json", "-p", "[{'op': 'remove', 'path': '/spec/remediationAction'}]",
			)

			Expect(err).Should(HaveOccurred())
			Expect(output).Should(ContainSubstring(remediationErr))
		})

		It("Should not return a validation error when updating policies", func() {
			By("Patching a policy so that only the remediationAction field of the root policy is unset")

			_, err := utils.KubectlWithOutput("patch", "policy",
				case17PolicyRootRemediationName, "-n", testNamespace,
				"--kubeconfig="+kubeconfigHub, "--type=json", "-p",
				"[{'op': 'add', 'path': '/spec/remediationAction', 'value': 'inform'}]",
			)

			Expect(err).ShouldNot(HaveOccurred())
		})
	})
})
