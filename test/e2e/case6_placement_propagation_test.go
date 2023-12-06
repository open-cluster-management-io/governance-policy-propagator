// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case6PolicyName string = "case6-test-policy"
	case6PolicyYaml string = "../resources/case6_placement_propagation/case6-test-policy.yaml"
)

var _ = Describe("Test policy propagation", func() {
	Describe("Create policy/pb/plc in ns:"+testNamespace+" and then update pb/plc", Ordered, func() {
		BeforeAll(func() {
			By("Creating " + case6PolicyYaml)
			utils.Kubectl("apply",
				"-f", case6PolicyYaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementDecision,
				case6PolicyName+"-plr-1",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				context.TODO(),
				plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed2", func() {
			By("Patching test-policy-plr with decision of cluster managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementDecision,
				case6PolicyName+"-plr-1",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed2", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementDecision,
				case6PolicyName+"-plr-1",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementDecision,
				case6PolicyName+"-plr-1",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			plc = utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				testNamespace+"."+case6PolicyName,
				"managed2",
				false,
				defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementDecision,
				case6PolicyName+"-plr-1",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Patching test-policy-pb with a non existing plr")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case6PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["placementRef"] = &policiesv1.Subject{
				APIGroup: "cluster.open-cluster-management.io",
				Kind:     "Placement",
				Name:     case6PolicyName + "-plr-nonexists",
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-pb with correct plr")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case6PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["placementRef"] = &policiesv1.Subject{
				APIGroup: "cluster.open-cluster-management.io",
				Kind:     "Placement",
				Name:     case6PolicyName + "-plr",
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Patching test-policy-pb with a plc with wrong apigroup")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case6PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy1.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case6PolicyName,
				},
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).To(HaveOccurred())
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-pb with correct plc")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case6PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case6PolicyName,
				},
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should prevent user from creating subject with unsupported kind", func() {
			By("Patching test-policy-pb with a plc with wrong kind")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case6PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy1",
					Name:     case6PolicyName,
				},
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).To(HaveOccurred())
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-pb with correct plc")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case6PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case6PolicyName,
				},
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Patching test-policy-pb with a plc with wrong name")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case6PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case6PolicyName + "1",
				},
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-pb with correct plc")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case6PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case6PolicyName,
				},
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Deleting decision for test-policy-plr")
			utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementDecision,
				case6PolicyName+"-plr-1",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			// remove the placementdecision will remove the policy from all managed clusters
			utils.Kubectl("delete",
				"placementdecision", case6PolicyName+"-plr-1",
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		AfterAll(func() {
			utils.Kubectl("delete",
				"-f", case6PolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, 30)
		})
	})

	Describe("Create policy/pb/plc in ns:"+testNamespace+" and then update policy", Ordered, func() {
		BeforeAll(func() {
			By("Creating " + case6PolicyYaml)
			utils.Kubectl("apply",
				"-f", case6PolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementDecision,
				case6PolicyName+"-plr-1",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should update replicated policy in ns managed1", func() {
			By("Patching test-policy with spec.remediationAction = enforce")
			rootPlc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(rootPlc).NotTo(BeNil())
			Expect(rootPlc.Object["spec"].(map[string]interface{})["remediationAction"]).To(Equal("inform"))
			rootPlc.Object["spec"].(map[string]interface{})["remediationAction"] = "enforce"
			rootPlc, err := clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Update(
				context.TODO(), rootPlc, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			Eventually(func() interface{} {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic,
					gvrPolicy,
					testNamespace+"."+case6PolicyName,
					"managed1",
					true,
					defaultTimeoutSeconds,
				)

				return replicatedPlc.Object["spec"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(rootPlc.Object["spec"]))
		})
		It("should remove replicated policy in ns managed1", func() {
			By("Patching test-policy with spec.disabled = true")
			rootPlc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(rootPlc).NotTo(BeNil())
			Expect(rootPlc.Object["spec"].(map[string]interface{})["disabled"]).To(BeFalse())
			rootPlc.Object["spec"].(map[string]interface{})["disabled"] = true
			rootPlc, err := clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Update(
				context.TODO(), rootPlc, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(rootPlc.Object["spec"].(map[string]interface{})["disabled"]).To(BeTrue())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should be created in user ns", func() {
			By("Creating " + case6PolicyYaml)
			utils.Kubectl("apply",
				"-f", case6PolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementDecision,
				case6PolicyName+"-plr-1",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				testNamespace+"."+case6PolicyName,
				"managed1",
				true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case6PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should update test-policy to a different policy template", func() {
			By("Creating ../resources/case6_placement_propagation/case6-test-policy2.yaml")
			utils.Kubectl("apply",
				"-f", "../resources/case6_placement_propagation/case6-test-policy2.yaml",
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			rootPlc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(rootPlc).NotTo(BeNil())
			yamlPlc := utils.ParseYaml("../resources/case6_placement_propagation/case6-test-policy2.yaml")
			Eventually(func() interface{} {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic,
					gvrPolicy,
					testNamespace+"."+case6PolicyName,
					"managed1",
					true,
					defaultTimeoutSeconds,
				)

				return replicatedPlc.Object["spec"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
		})
		AfterAll(func() {
			utils.Kubectl("delete",
				"-f", "../resources/case6_placement_propagation/case6-test-policy2.yaml",
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, 10)
		})
	})

	Describe("Handling propagation to terminating clusters", Ordered, func() {
		BeforeAll(func() {
			By("Creating " + case6PolicyYaml)
			utils.Kubectl("apply",
				"-f", case6PolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case6PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		It("sets up the terminating cluster", func() {
			By("Creating the namespace and cluster")
			// Note: the namespace is initially created with a finalizer
			_, err := utils.KubectlWithOutput("apply", "-f",
				"../resources/case6_placement_propagation/extra-cluster.yaml", "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())

			By("Deleting the namespace")
			out, err := utils.KubectlWithOutput("delete", "namespace", "test6-extra",
				"--timeout=2s", "--kubeconfig="+kubeconfigHub)
			Expect(err).To(HaveOccurred())
			Expect(out).To(MatchRegexp("waiting for the condition"))
		})
		It("should fail to propagate to the terminating cluster", func() {
			By("Patching test-policy-plr with decision of cluster test6-extra")
			plr := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementDecision,
				case6PolicyName+"-plr-1",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "test6-extra")
			_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying that the replicated policy is not created")
			Consistently(func() interface{} {
				return utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName,
					"test6-extra", false, defaultTimeoutSeconds)
			}, defaultTimeoutSeconds, 1).Should(BeNil())
		})
		It("should succeed when the cluster namespace is re-created", func() {
			By("Removing the finalizer from the namespace")
			utils.Kubectl("patch", "namespace", "test6-extra", "--type=json",
				`-p=[{"op":"remove","path":"/metadata/finalizers"}]`, "--kubeconfig="+kubeconfigHub)

			By("Verifying that the namespace is removed")
			ns := utils.GetClusterLevelWithTimeout(clientHubDynamic, gvrNamespace, "test6-extra", false,
				defaultTimeoutSeconds)
			Expect(ns).To(BeNil())

			By("Recreating the namespace")
			utils.Kubectl("create", "namespace", "test6-extra", "--kubeconfig="+kubeconfigHub)

			By("Verifying that the policy is now replicated")
			pol := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case6PolicyName,
				"test6-extra", true, defaultTimeoutSeconds)
			Expect(pol).NotTo(BeNil())
		})
		AfterAll(func() {
			utils.Kubectl("delete",
				"-f", case6PolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, 10)

			utils.Kubectl("patch", "namespace", "test6-extra", "--type=json",
				`-p=[{"op":"remove","path":"/metadata/finalizers"}]`, "--kubeconfig="+kubeconfigHub)
			utils.Kubectl("delete", "namespace", "test6-extra", "--timeout=2s", "--kubeconfig="+kubeconfigHub)
			utils.Kubectl("delete", "managedcluster", "test6-extra", "--kubeconfig="+kubeconfigHub)
		})
	})
})
