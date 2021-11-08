// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/api/v1"
	"github.com/open-cluster-management/governance-policy-propagator/controllers/common"
	"github.com/open-cluster-management/governance-policy-propagator/test/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const case6PolicyName string = "case6-test-policy"
const case6PolicyYaml string = "../resources/case6_placement_propagation/case6-test-policy.yaml"

var _ = Describe("Test policy propagation", func() {
	Describe("Create policy/pb/plc in ns:"+testNamespace+" and then update pb/plc", func() {
		It("should be created in user ns", func() {
			By("Creating " + case6PolicyYaml)
			utils.Kubectl("apply",
				"-f", case6PolicyYaml,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			Expect(plc).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementDecision, case6PolicyName+"-plr-1", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1")
			plr, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(context.TODO(),
				plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed1", true, defaultTimeoutSeconds)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed2", func() {
			By("Patching test-policy-plr with decision of cluster managed2")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementDecision, case6PolicyName+"-plr-1", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed2")
			plr, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed2", true, defaultTimeoutSeconds)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementDecision, case6PolicyName+"-plr-1", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1", "managed2")
			plr, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementDecision, case6PolicyName+"-plr-1", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1")
			plr, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(context.TODO(),
				plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed1", true, defaultTimeoutSeconds)
			Expect(plc).ToNot(BeNil())
			plc = utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed2", false, defaultTimeoutSeconds)
			Expect(plc).To(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementDecision, case6PolicyName+"-plr-1", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1", "managed2")
			plr, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Patching test-policy-pb with a non existing plr")
			pb := utils.GetWithTimeout(clientHubDynamic, gvrPlacementBinding, case6PolicyName+"-pb", testNamespace, true, defaultTimeoutSeconds)
			pb.Object["placementRef"] = &policiesv1.Subject{
				APIGroup: "cluster.open-cluster-management.io",
				Kind:     "Placement",
				Name:     case6PolicyName + "-plr-nonexists",
			}
			pb, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(context.TODO(), pb, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-pb with correct plr")
			pb := utils.GetWithTimeout(clientHubDynamic, gvrPlacementBinding, case6PolicyName+"-pb", testNamespace, true, defaultTimeoutSeconds)
			pb.Object["placementRef"] = &policiesv1.Subject{
				APIGroup: "cluster.open-cluster-management.io",
				Kind:     "Placement",
				Name:     case6PolicyName + "-plr",
			}
			pb, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(context.TODO(), pb, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Patching test-policy-pb with a plc with wrong apigroup")
			pb := utils.GetWithTimeout(clientHubDynamic, gvrPlacementBinding, case6PolicyName+"-pb", testNamespace, true, defaultTimeoutSeconds)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy1.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case6PolicyName,
				},
			}
			pb, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(context.TODO(), pb, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-pb with correct plc")
			pb := utils.GetWithTimeout(clientHubDynamic, gvrPlacementBinding, case6PolicyName+"-pb", testNamespace, true, defaultTimeoutSeconds)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case6PolicyName,
				},
			}
			pb, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(context.TODO(), pb, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Patching test-policy-pb with a plc with wrong kind")
			pb := utils.GetWithTimeout(clientHubDynamic, gvrPlacementBinding, case6PolicyName+"-pb", testNamespace, true, defaultTimeoutSeconds)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy1",
					Name:     case6PolicyName,
				},
			}
			pb, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(context.TODO(), pb, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-pb with correct plc")
			pb := utils.GetWithTimeout(clientHubDynamic, gvrPlacementBinding, case6PolicyName+"-pb", testNamespace, true, defaultTimeoutSeconds)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case6PolicyName,
				},
			}
			pb, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(context.TODO(), pb, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Patching test-policy-pb with a plc with wrong name")
			pb := utils.GetWithTimeout(clientHubDynamic, gvrPlacementBinding, case6PolicyName+"-pb", testNamespace, true, defaultTimeoutSeconds)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case6PolicyName + "1",
				},
			}
			pb, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(context.TODO(), pb, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-pb with correct plc")
			pb := utils.GetWithTimeout(clientHubDynamic, gvrPlacementBinding, case6PolicyName+"-pb", testNamespace, true, defaultTimeoutSeconds)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case6PolicyName,
				},
			}
			pb, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(context.TODO(), pb, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Deleting decision for test-policy-plr")
			utils.GetWithTimeout(clientHubDynamic, gvrPlacementDecision, case6PolicyName+"-plr-1", testNamespace, true, defaultTimeoutSeconds)
			// remove the placementdecision will remove the policy from all managed clusters
			utils.Kubectl("delete",
				"placementdecision", case6PolicyName+"-plr-1",
				"-n", testNamespace)
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should clean up", func() {
			utils.Kubectl("delete",
				"-f", case6PolicyYaml,
				"-n", testNamespace)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, 10)
		})
	})

	Describe("Create policy/pb/plc in ns:"+testNamespace+" and then update policy", func() {
		It("should be created in user ns", func() {
			By("Creating " + case6PolicyYaml)
			utils.Kubectl("apply",
				"-f", case6PolicyYaml,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			Expect(plc).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementDecision, case6PolicyName+"-plr-1", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1")
			plr, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed1", true, defaultTimeoutSeconds)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should update replicated policy in ns managed1", func() {
			By("Patching test-policy with spec.remediationAction = enforce")
			rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			Expect(rootPlc).NotTo(BeNil())
			Expect(rootPlc.Object["spec"].(map[string]interface{})["remediationAction"]).To(Equal("inform"))
			rootPlc.Object["spec"].(map[string]interface{})["remediationAction"] = "enforce"
			rootPlc, err := clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Update(context.TODO(), rootPlc, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			Eventually(func() interface{} {
				replicatedPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed1", true, defaultTimeoutSeconds)
				return replicatedPlc.Object["spec"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(rootPlc.Object["spec"]))
		})
		It("should remove replicated policy in ns managed1", func() {
			By("Patching test-policy with spec.disabled = true")
			rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			Expect(rootPlc).NotTo(BeNil())
			Expect(rootPlc.Object["spec"].(map[string]interface{})["disabled"]).To(Equal(false))
			rootPlc.Object["spec"].(map[string]interface{})["disabled"] = true
			rootPlc, err := clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Update(context.TODO(), rootPlc, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			Expect(rootPlc.Object["spec"].(map[string]interface{})["disabled"]).To(Equal(true))
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should be created in user ns", func() {
			By("Creating " + case6PolicyYaml)
			utils.Kubectl("apply",
				"-f", case6PolicyYaml,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			Expect(plc).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(clientHubDynamic, gvrPlacementDecision, case6PolicyName+"-plr-1", testNamespace, true, defaultTimeoutSeconds)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1")
			plr, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(context.TODO(), plr, metav1.UpdateOptions{})
			Expect(err).To(BeNil())
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed1", true, defaultTimeoutSeconds)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should update test-policy to a different policy template", func() {
			By("Creating ../resources/case6_placement_propagation/case6-test-policy2.yaml")
			utils.Kubectl("apply",
				"-f", "../resources/case6_placement_propagation/case6-test-policy2.yaml",
				"-n", testNamespace)
			rootPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds)
			Expect(rootPlc).NotTo(BeNil())
			yamlPlc := utils.ParseYaml("../resources/case6_placement_propagation/case6-test-policy2.yaml")
			Eventually(func() interface{} {
				replicatedPlc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed1", true, defaultTimeoutSeconds)
				return replicatedPlc.Object["spec"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
		})
		It("should clean up", func() {
			utils.Kubectl("delete",
				"-f", "../resources/case6_placement_propagation/case6-test-policy2.yaml",
				"-n", testNamespace)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, 10)
		})
	})
})
