// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test policyset propagation", func() {
	const (
		path                               string = "../resources/case10_policyset_propagation/"
		case10PolicyName                   string = "case10-test-policy"
		case10PolicySetName                string = "case10-test-policyset"
		case10PolicySetYaml                string = path + "case10-test-policyset.yaml"
		case10PolicySetPlacementYaml       string = path + "case10-test-policyset-placement.yaml"
		case10PolicySetPolicyYaml          string = path + "case10-test-policyset-policy.yaml"
		case10PolicySetPolicyPlacementYaml string = path + "case10-test-policyset-policy-placement.yaml"
	)

	Describe("Test policy propagation through policyset placementbinding with placementrule", func() {
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicySetName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed2", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of cluster managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicySetName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicySetName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicySetName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				testNamespace+"."+case10PolicyName,
				"managed1",
				true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			plc = utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				testNamespace+"."+case10PolicyName,
				"managed2",
				false,
				defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicySetName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Deleting policyset")
			_, err := utils.KubectlWithOutput("delete", "policyset",
				case10PolicySetName, "-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicySetName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Deleting placementbinding")
			_, err := utils.KubectlWithOutput("delete", "PlacementBinding", case10PolicySetName+"-pb", "-n",
				testNamespace, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicySetName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Deleting placementrule")
			_, err := utils.KubectlWithOutput("delete", "PlacementRule", case10PolicySetName+"-plr", "-n",
				testNamespace, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicySetName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should clean up", func() {
			By("Deleting " + case10PolicySetYaml)
			_, err := utils.KubectlWithOutput("delete",
				"-f", case10PolicySetYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
	})

	Describe("Test policy propagation through both policy and policyset placementbinding with placementrule", func() {
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetPolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + "-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicySetName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching " + case10PolicyName + "-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicyName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching " + case10PolicyName + "-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicyName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + "-plr with decision of cluster managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicySetName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should clean up", func() {
			By("Deleting " + case10PolicySetYaml)
			_, err := utils.KubectlWithOutput("delete",
				"-f", case10PolicySetPolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
	})

	Describe("Test policy propagation through policyset placementbinding with placement", func() {
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetPlacementYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetPlacementYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching test-policy-plm with decision of cluster managed1")
			plm := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case10PolicySetName+"-plm-decision", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plm.Object["status"] = utils.GeneratePldStatus(plm.GetName(), plm.GetNamespace(), "managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plm, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed2", func(ctx SpecContext) {
			By("Patching test-policy-plm with decision of cluster managed2")
			plm := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case10PolicySetName+"-plm-decision", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plm.Object["status"] = utils.GeneratePldStatus(plm.GetName(), plm.GetNamespace(), "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plm, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to both cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policy-plm with decision of cluster managed2")
			plm := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case10PolicySetName+"-plm-decision", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plm.Object["status"] = utils.GeneratePldStatus(plm.GetName(), plm.GetNamespace(), "managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plm, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Deleting policyset")
			_, err := utils.KubectlWithOutput("delete", "policyset", case10PolicySetName,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetPlacementYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetPlacementYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
		})
		It("should propagate to both cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policy-plm with decision of cluster managed2")
			plm := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case10PolicySetName+"-plm-decision", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plm.Object["status"] = utils.GeneratePldStatus(plm.GetName(), plm.GetNamespace(), "managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plm, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Deleting placementbinding")
			_, err := utils.KubectlWithOutput("delete", "PlacementBinding", case10PolicySetName+"-pb", "-n",
				testNamespace, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetPlacementYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetPlacementYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
		})
		It("should propagate to both cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policy-plm with decision of cluster managed2")
			plm := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case10PolicySetName+"-plm-decision", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plm.Object["status"] = utils.GeneratePldStatus(plm.GetName(), plm.GetNamespace(), "managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plm, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Deleting placementDecision")
			_, err := utils.KubectlWithOutput("delete", "PlacementDecision", case10PolicySetName+"-plm-decision", "-n",
				testNamespace, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetPlacementYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetPlacementYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
		})
		It("should cleanup", func() {
			By("Deleting " + case10PolicySetPlacementYaml)
			_, err := utils.KubectlWithOutput("delete",
				"-f", case10PolicySetPlacementYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
	})

	Describe("Test policy propagation through both policy and policyset placementbinding with placement", func() {
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetPolicyPlacementYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetPolicyPlacementYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching test-policy-plm with decision of cluster managed1")
			plm := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case10PolicyName+"-plm-decision", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plm.Object["status"] = utils.GeneratePldStatus(plm.GetName(), plm.GetNamespace(), "managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plm, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to both cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching test-policyset-plm with decision of cluster managed2")
			plm := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case10PolicySetName+"-plm-decision", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plm.Object["status"] = utils.GeneratePldStatus(plm.GetName(), plm.GetNamespace(), "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plm, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should cleanup", func() {
			By("Deleting " + case10PolicySetPolicyPlacementYaml)
			_, err := utils.KubectlWithOutput("delete",
				"-f", case10PolicySetPolicyPlacementYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
	})

	Describe("Test policy propagation with policyset modification", func() {
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetPolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + "-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case10PolicySetName+"-plr", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				ctx, plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should remove from managed1", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + " with empty policies fields")
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
			plcSet.Object["spec"].(map[string]interface{})["policies"] = []string{}
			_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
				ctx, plcSet, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", false,
				defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + " with " + case10PolicyName + " policies fields")
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
			plcSet.Object["spec"].(map[string]interface{})["policies"] = []string{case10PolicyName}
			_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
				ctx, plcSet, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should still propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + " with " + case10PolicyName + " and non-exist policies fields")
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
			plcSet.Object["spec"].(map[string]interface{})["policies"] = []string{case10PolicyName, "policy-not-exists"}
			_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
				ctx, plcSet, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should remove from managed1", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + " with empty policies fields")
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
			plcSet.Object["spec"].(map[string]interface{})["policies"] = []string{}
			_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
				ctx, plcSet, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", false,
				defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should clean up", func() {
			By("Deleting " + case10PolicySetYaml)
			_, err := utils.KubectlWithOutput("delete",
				"-f", case10PolicySetPolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName, testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
	})

	Describe("Test policy propagation with multiple policysets", func() {
		const case10PolicySetMultipleYaml string = path + "case10-test-multiple-policysets.yaml"
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetMultipleYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetMultipleYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet1 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName+"1", testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet1).NotTo(BeNil())
			plcSet2 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName+"2", testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet2).NotTo(BeNil())
		})

		It("should propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + "1-plm with decision of cluster managed1")
			plm := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case10PolicySetName+"1-plm-decision", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plm.Object["status"] = utils.GeneratePldStatus(plm.GetName(), plm.GetNamespace(), "managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plm, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to both cluster ns managed1 and managed2", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + "2-plm with decision of cluster managed2")
			plm := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case10PolicySetName+"2-plm-decision", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plm.Object["status"] = utils.GeneratePldStatus(plm.GetName(), plm.GetNamespace(), "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plm, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName, "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should cleanup", func() {
			By("Deleting " + case10PolicySetMultipleYaml)
			_, err := utils.KubectlWithOutput("delete",
				"-f", case10PolicySetMultipleYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet1 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName+"1", testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet1).To(BeNil())
			plcSet2 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName+"2", testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet2).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
	})

	Describe("Test policy propagation with multiple policysets with single placementbinding", func() {
		const case10PolicySetMultipleSinglePBYaml string = path + "case10-test-multiple-policysets-single-pb.yaml"
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetMultipleSinglePBYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetMultipleSinglePBYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet1 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName+"1", testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet1).NotTo(BeNil())
			plcSet2 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName+"2", testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet2).NotTo(BeNil())
		})
		It(case10PolicyName+"1 should have "+case10PolicySetName+"1 placement", func() {
			plc1 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case10PolicyName+"1", testNamespace, true, defaultTimeoutSeconds,
			)
			placement, found, err := unstructured.NestedSlice(plc1.Object, "status", "placement")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(found).Should(BeTrue())
			Expect(placement).To(HaveLen(1))
			Expect(placement[0].(map[string]interface{})["policySet"]).Should(Equal(case10PolicySetName + "1"))
		})
		It(case10PolicyName+"2 should have "+case10PolicySetName+"2 placement", func() {
			plc1 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case10PolicyName+"2", testNamespace, true, defaultTimeoutSeconds,
			)
			placement, found, err := unstructured.NestedSlice(plc1.Object, "status", "placement")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(found).Should(BeTrue())
			Expect(placement).To(HaveLen(1))
			Expect(placement[0].(map[string]interface{})["policySet"]).Should(Equal(case10PolicySetName + "2"))
		})
		It("should propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + "-plm with decision of cluster managed1")
			plm := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case10PolicySetName+"-plm-decision", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plm.Object["status"] = utils.GeneratePldStatus(plm.GetName(), plm.GetNamespace(), "managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plm, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName+"1", "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName + "1",
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName+"2", "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName + "2",
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed2", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + "-plm with decision of cluster managed2")
			plm := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case10PolicySetName+"-plm-decision", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plm.Object["status"] = utils.GeneratePldStatus(plm.GetName(), plm.GetNamespace(), "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plm, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName+"1", "managed1", false,
				defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName+"1", "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName+"2", "managed1", false,
				defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName+"2", "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		It("should still work when pb contains non-existing policyset", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + "-pb with non-existing policyset")
			unstructuredPb := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementBinding, case10PolicySetName+"-pb", testNamespace, true,
				defaultTimeoutSeconds,
			)
			var pb policiesv1.PlacementBinding
			err := runtime.DefaultUnstructuredConverter.
				FromUnstructured(unstructuredPb.UnstructuredContent(), &pb)
			Expect(err).ToNot(HaveOccurred())
			nonExistingSubject := []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "PolicySet",
					Name:     case10PolicySetName,
				},
			}
			unstructuredPb.Object["subjects"] = append(nonExistingSubject, pb.Subjects[0], pb.Subjects[1])
			_, err = clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				ctx, unstructuredPb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
		})
		It("should still propagate to cluster ns managed1", func(ctx SpecContext) {
			By("Patching " + case10PolicySetName + "-plm with decision of cluster managed1")
			plm := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementDecision, case10PolicySetName+"-plm-decision", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plm.Object["status"] = utils.GeneratePldStatus(plm.GetName(), plm.GetNamespace(), "managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				ctx, plm, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName+"1", "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName + "1",
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case10PolicyName+"2", "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt = metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName + "2",
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should cleanup", func() {
			By("Deleting " + case10PolicySetMultipleSinglePBYaml)
			_, err := utils.KubectlWithOutput("delete",
				"-f", case10PolicySetMultipleSinglePBYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet1 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName+"1", testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet1).To(BeNil())
			plcSet2 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName+"2", testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet2).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
	})

	Describe("Test policy placement with multiple policies and policysets with single placementbinding", func() {
		case10PolicySetMultipleSinglePBYaml := path + "case10-test-multiple-policies-policysets-single-pb.yaml"
		It("should be created in user ns", func() {
			By("Creating " + case10PolicySetMultipleSinglePBYaml)
			_, err := utils.KubectlWithOutput("apply",
				"-f", case10PolicySetMultipleSinglePBYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet1 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName+"1", testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet1).NotTo(BeNil())
			plcSet2 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName+"2", testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet2).NotTo(BeNil())
		})
		It(case10PolicyName+"1 should have 2 placement", func() {
			plc1 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case10PolicyName+"1", testNamespace, true, defaultTimeoutSeconds,
			)
			placement, found, err := unstructured.NestedSlice(plc1.Object, "status", "placement")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(found).Should(BeTrue())
			Expect(placement).To(HaveLen(2))
			Expect(placement[0].(map[string]interface{})["policySet"]).Should(BeNil())
			Expect(placement[1].(map[string]interface{})["policySet"]).Should(Equal(case10PolicySetName + "1"))
		})
		It(case10PolicyName+"2 should have 1 placement", func() {
			plc1 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case10PolicyName+"2", testNamespace, true, defaultTimeoutSeconds,
			)
			placement, found, err := unstructured.NestedSlice(plc1.Object, "status", "placement")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(found).Should(BeTrue())
			Expect(placement).To(HaveLen(1))
			Expect(placement[0].(map[string]interface{})["policySet"]).Should(Equal(case10PolicySetName + "2"))
		})
		It("should cleanup", func() {
			By("Deleting " + case10PolicySetMultipleSinglePBYaml)
			_, err := utils.KubectlWithOutput("delete",
				"-f", case10PolicySetMultipleSinglePBYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())
			plcSet1 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName+"1", testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet1).To(BeNil())
			plcSet2 := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case10PolicySetName+"2", testNamespace, false, defaultTimeoutSeconds,
			)
			Expect(plcSet2).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case10PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
	})
})
