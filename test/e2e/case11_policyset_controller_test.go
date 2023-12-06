// Copyright (c) 2022 Red Hat, Inc.
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
	case11PolicyName               string = "case11-test-policy"
	case11PolicySetName            string = "case11-test-policyset"
	case11PolicySetNameManaged1    string = "test-plcset-managed1"
	case11PolicyNameManaged2       string = "case11-multiple-placements-rule"
	case11PolicySetEmpty           string = "case11-empty-policyset"
	case11PolicySetMultiStatus     string = "case11-multistatus-policyset"
	case11PolicyCompliant          string = "case11-compliant-plc"
	case11PolicyYaml               string = "../resources/case11_policyset_controller/case11-test-policy.yaml"
	case11PolicySetPatchYaml       string = "../resources/case11_policyset_controller/case11-patch-plcset.yaml"
	case11PolicySetPatch2Yaml      string = "../resources/case11_policyset_controller/case11-patch-plcset-2.yaml"
	case11DisablePolicyYaml        string = "../resources/case11_policyset_controller/case11-disable-plc.yaml"
	case11PolicySetManaged1Yaml    string = "../resources/case11_policyset_controller/case11-plcset-managed1.yaml"
	case11PolicyManaged2Yaml       string = "../resources/case11_policyset_controller/case11-plc-managed2.yaml"
	case11PolicySetEmptyYaml       string = "../resources/case11_policyset_controller/case11-empty-plcset.yaml"
	case11PolicySetMultiStatusYaml string = "../resources/case11_policyset_controller/case11-plcset-multistatus.yaml"
	case11PolicyCompliantYaml      string = "../resources/case11_policyset_controller/case11-compliant-plc.yaml"
)

var _ = Describe("Test policyset controller status updates", func() {
	Describe("Create policy, policyset, and placement in ns:"+testNamespace, func() {
		It("should create and process policy and policyset", func() {
			By("Creating " + case11PolicyYaml)
			utils.Kubectl("apply",
				"-f", case11PolicyYaml,
				"-n", testNamespace,
				"--kubeconfig="+kubeconfigHub)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case11PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			By("Patching test-policy-plr with decision of cluster managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, "case11-test-policyset-plr", testNamespace,
				true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case11PolicyName, "managed2", true,
				60,
			)
			Expect(plc).ToNot(BeNil())

			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case11PolicyName,
			}
			By("Patching both replicated policy statuses")
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true,
				defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.NonCompliant,
					Status: []*policiesv1.CompliancePerClusterStatus{
						{
							ClusterName:      "managed1",
							ClusterNamespace: "managed1",
							ComplianceState:  policiesv1.NonCompliant,
						},
					},
				}
				_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
			}
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case11PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
			By("Checking the status of policy set")
			yamlPlc := utils.ParseYaml("../resources/case11_policyset_controller/case11-statuscheck-1.yaml")
			Eventually(func() interface{} {
				rootPlcSet := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicySet, case11PolicySetName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlcSet.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should add a status entry in policyset for a policy that does not exist", func() {
			By("Creating " + case11PolicySetPatchYaml)
			utils.Kubectl("apply",
				"-f", case11PolicySetPatchYaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case11PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
			By("Checking the status of policy set")
			yamlPlc := utils.ParseYaml("../resources/case11_policyset_controller/case11-statuscheck-2.yaml")
			Eventually(func() interface{} {
				rootPlcSet := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicySet, case11PolicySetName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlcSet.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
			utils.Kubectl(
				"apply",
				"-f",
				"../resources/case11_policyset_controller/case11-reset-plcset.yaml", "-n",
				testNamespace, "--kubeconfig="+kubeconfigHub,
			)
		})
		It("should update to compliant if all its child policy violations have been remediated", func() {
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case11PolicyName,
			}
			By("Patching both replicated policy statuses")
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true,
				defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.Compliant,
					Status: []*policiesv1.CompliancePerClusterStatus{
						{
							ClusterName:      "managed1",
							ClusterNamespace: "managed1",
							ComplianceState:  policiesv1.Compliant,
						},
					},
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
			}
			By("Checking the status of policy set")
			yamlPlc := utils.ParseYaml("../resources/case11_policyset_controller/case11-statuscheck-3.yaml")
			Eventually(func() interface{} {
				rootPlcSet := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicySet, case11PolicySetName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlcSet.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should show pending if its child policies are pending", func() {
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case11PolicyName,
			}
			By("Patching both replicated policy statuses")
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true,
				defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.Pending,
					Status: []*policiesv1.CompliancePerClusterStatus{
						{
							ClusterName:      "managed1",
							ClusterNamespace: "managed1",
							ComplianceState:  policiesv1.Pending,
						},
					},
				}
				_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
			}
			By("Checking the status of policy set")
			yamlPlc := utils.ParseYaml("../resources/case11_policyset_controller/case11-statuscheck-8.yaml")
			Eventually(func() interface{} {
				rootPlcSet := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicySet, case11PolicySetName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlcSet.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should update status properly if a policy is disabled", func() {
			By("Creating " + case11DisablePolicyYaml)
			utils.Kubectl("apply",
				"-f", case11DisablePolicyYaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case11PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			By("Checking the status of policy set")
			yamlPlc := utils.ParseYaml("../resources/case11_policyset_controller/case11-statuscheck-4.yaml")
			Eventually(func() interface{} {
				rootPlcSet := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicySet, case11PolicySetName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlcSet.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))

			By("Creating " + case11PolicyCompliantYaml)
			utils.Kubectl("apply",
				"-f", case11PolicyCompliantYaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case11PolicyCompliant, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			utils.Kubectl("apply",
				"-f", case11PolicySetPatch2Yaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case11PolicySetName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())

			By("Patching test-policy-plr with decision of cluster managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, "case11-test-policyset-plr", testNamespace,
				true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case11PolicyCompliant, "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())

			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case11PolicyCompliant,
			}
			By("Patching both replicated policy statuses")
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true,
				defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.Compliant,
					Status: []*policiesv1.CompliancePerClusterStatus{
						{
							ClusterName:      "managed1",
							ClusterNamespace: "managed1",
							ComplianceState:  policiesv1.Compliant,
						},
					},
				}
				_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
			}

			By("Checking the status of policy set")
			yamlPlc = utils.ParseYaml("../resources/case11_policyset_controller/case11-statuscheck-5.yaml")
			Eventually(func() interface{} {
				rootPlcSet := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicySet, case11PolicySetName, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlcSet.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should scope status to policyset placement", func() {
			By("Creating " + case11PolicyManaged2Yaml)
			utils.Kubectl("apply",
				"-f", case11PolicySetManaged1Yaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			utils.Kubectl("apply",
				"-f", case11PolicyManaged2Yaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case11PolicyNameManaged2, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			By("Patching test-policyset-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, "test-plcset-managed1-plr", testNamespace,
				true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case11PolicyNameManaged2, "managed1", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())

			By("Patching test-policy-plr with decision of cluster managed2")
			plr = utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, "placement-case11-multiple-placements-rule", testNamespace, true,
				defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed2")
			_, err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case11PolicyNameManaged2, "managed2", true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())

			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case11PolicyNameManaged2,
			}
			By("Patching both replicated policy statuses")
			replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true,
				defaultTimeoutSeconds)
			for _, replicatedPlc := range replicatedPlcList.Items {
				replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
					ComplianceState: policiesv1.NonCompliant,
					Status: []*policiesv1.CompliancePerClusterStatus{
						{
							ClusterName:      "managed1",
							ClusterNamespace: "managed1",
							ComplianceState:  policiesv1.NonCompliant,
						},
					},
				}
				_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
					context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
			}
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case11PolicySetNameManaged1, testNamespace, true,
				defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
			By("Checking the status of policy set")
			yamlPlc := utils.ParseYaml("../resources/case11_policyset_controller/case11-statuscheck-6.yaml")
			Eventually(func() interface{} {
				rootPlcSet := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicySet, case11PolicySetNameManaged1, testNamespace, true,
					defaultTimeoutSeconds,
				)

				return rootPlcSet.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should have no status if no policies are contained in the policySet", func() {
			By("Creating " + case11PolicySetEmpty)
			utils.Kubectl("apply",
				"-f", case11PolicySetEmptyYaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case11PolicySetEmpty, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
			By("Checking the status of policy set")
			Eventually(func() interface{} {
				rootPlcSet := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicySet, case11PolicySetEmpty, testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlcSet.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(BeNil())
		})
		It("must have combined statusMessage when disabled/deleted policies are contained in the policySet", func() {
			By("Creating " + case11PolicySetMultiStatus)
			utils.Kubectl("apply",
				"-f", case11PolicySetMultiStatusYaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			plcSet := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicySet, case11PolicySetMultiStatus, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plcSet).NotTo(BeNil())
			By("Checking the status of policy set")
			yamlPlc := utils.ParseYaml("../resources/case11_policyset_controller/case11-statuscheck-7.yaml")
			Eventually(func() interface{} {
				rootPlcSet := utils.GetWithTimeout(
					clientHubDynamic, gvrPolicySet, case11PolicySetMultiStatus,
					testNamespace, true, defaultTimeoutSeconds,
				)

				return rootPlcSet.Object["status"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
		})
		It("should clean up", func() {
			utils.Kubectl("delete",
				"-f", "../resources/case11_policyset_controller/case11-test-policy.yaml",
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			utils.Kubectl("delete",
				"-f", "../resources/case11_policyset_controller/case11-empty-plcset.yaml",
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			utils.Kubectl("delete",
				"-f", case11PolicySetManaged1Yaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			utils.Kubectl("delete",
				"-f", case11PolicyManaged2Yaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			utils.Kubectl("delete",
				"-f", case11PolicyCompliantYaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			utils.Kubectl("delete",
				"-f", case11PolicySetMultiStatusYaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, defaultTimeoutSeconds)
		})
	})
})
