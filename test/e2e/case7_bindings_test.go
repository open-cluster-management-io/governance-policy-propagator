// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test policy propagation", func() {
	const (
		case7PolicyName   string = "case7-test-policy"
		case7PolicyYaml   string = "../resources/case7_placement_bindings/case7-test-policy.yaml"
		case7BindingYaml1 string = "../resources/case7_placement_bindings/case7-test-binding1.yaml"
		case7BindingYaml2 string = "../resources/case7_placement_bindings/case7-test-binding2.yaml"
		case7BindingYaml3 string = "../resources/case7_placement_bindings/case7-test-binding3.yaml"
		case7BindingYaml4 string = "../resources/case7_placement_bindings/case7-test-binding4.yaml"
	)

	Describe("Create policy/pb/plc in ns:"+testNamespace, Ordered, func() {
		BeforeAll(func() {
			By("Creating " + case7PolicyYaml)
			utils.Kubectl("apply",
				"-f", case7PolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case7PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
			By("Creating " + case7BindingYaml1)
			utils.Kubectl("apply",
				"-f", case7BindingYaml1,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			binding := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				"case7-test-policy-pb1",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			Expect(binding).NotTo(BeNil())
			By("Creating " + case7BindingYaml2)
			utils.Kubectl("apply",
				"-f", case7BindingYaml2,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			binding = utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				"case7-test-policy-pb2",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			Expect(binding).NotTo(BeNil())
			By("Creating " + case7BindingYaml3)
			utils.Kubectl("apply",
				"-f", case7BindingYaml3,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			binding = utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				"case7-test-policy-pb3",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			Expect(binding).NotTo(BeNil())
			By("Creating " + case7BindingYaml4)
			utils.Kubectl("apply",
				"-f", case7BindingYaml4,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			binding = utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				"case7-test-policy-pb4",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			Expect(binding).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementDecision,
				case7PolicyName+"-plr-1",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case7PolicyName, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case7PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("placement bindings propagate to cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of cluster managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementDecision,
				case7PolicyName+"-plr-2",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case7PolicyName, "managed2", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case7PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed2", func(ctx SpecContext) {
			By("Patching test-policy-pb with a non existing plr")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case7PolicyName+"-pb2",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["placementRef"] = &policiesv1.Subject{
				APIGroup: "cluster.open-cluster-management.io",
				Kind:     "Placement",
				Name:     case7PolicyName + "-plr-nonexists",
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				ctx, pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case7PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("mixed placement propagate to cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case7PolicyName+"-plr3", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case7PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed2", func(ctx SpecContext) {
			By("Patching test-policy-pb with a non existing plr")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case7PolicyName+"-pb1",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["placementRef"] = &policiesv1.Subject{
				APIGroup: "cluster.open-cluster-management.io",
				Kind:     "Placement",
				Name:     case7PolicyName + "-plr-nonexists",
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				ctx, pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case7PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("app placement propagate to cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case7PolicyName+"-plr4", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case7PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		AfterAll(func() {
			By("Clean up")
			utils.Kubectl("delete",
				"-f", case7PolicyYaml,
				"-n", testNamespace,
				"--ignore-not-found",
				"--kubeconfig="+kubeconfigHub)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, 10)
			utils.Kubectl("delete",
				"-f", case7BindingYaml1,
				"-n", testNamespace,
				"--ignore-not-found",
				"--kubeconfig="+kubeconfigHub)
			utils.Kubectl("delete",
				"-f", case7BindingYaml2,
				"-n", testNamespace,
				"--ignore-not-found",
				"--kubeconfig="+kubeconfigHub)
			utils.Kubectl("delete",
				"-f", case7BindingYaml3,
				"-n", testNamespace,
				"--ignore-not-found",
				"--kubeconfig="+kubeconfigHub)
			utils.Kubectl("delete",
				"-f", case7BindingYaml4,
				"-n", testNamespace,
				"--ignore-not-found",
				"--kubeconfig="+kubeconfigHub)
		})
	})
})
