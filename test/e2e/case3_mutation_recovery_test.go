// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case3PolicyName string = "case3-test-policy"
	case3PolicyYaml string = "../resources/case3_mutation_recovery/case3-test-policy.yaml"
)

var _ = Describe("Test unexpected policy mutation", func() {
	BeforeEach(func() {
		By("Creating " + case3PolicyYaml)
		utils.Kubectl("apply",
			"-f", case3PolicyYaml,
			"-n", testNamespace)
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case3PolicyName, testNamespace, true, defaultTimeoutSeconds,
		)
		Expect(plc).NotTo(BeNil())
		By("Patching test-policy-plr with decision of cluster managed1 and managed2")
		plr := utils.GetWithTimeout(
			clientHubDynamic, gvrPlacementRule, case3PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
		)
		plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
		_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
			context.TODO(), plr, metav1.UpdateOptions{},
		)
		Expect(err).To(BeNil())
		opt := metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case3PolicyName}
		By("Patching both replicated policy status to compliant")
		replicatedPlcList := utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		for _, replicatedPlc := range replicatedPlcList.Items {
			replicatedPlc.Object["status"] = &policiesv1.PolicyStatus{
				ComplianceState: policiesv1.Compliant,
			}
			_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
				context.TODO(), &replicatedPlc, metav1.UpdateOptions{},
			)
			Expect(err).To(BeNil())
		}
		By("Checking the status of root policy")
		yamlPlc := utils.ParseYaml("../resources/case3_mutation_recovery/managed-both-status-compliant.yaml")
		Eventually(func() interface{} {
			rootPlc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case3PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)

			return rootPlc.Object["status"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["status"]))
	})
	AfterEach(func() {
		utils.Kubectl("delete",
			"-f", case3PolicyYaml,
			"-n", testNamespace)
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
	})
	It("Should recreate replicated policy when deleted", func() {
		By("Deleting policy in cluster ns")
		utils.Kubectl("delete", "policy", "-n", "managed1", "--all")
		utils.Kubectl("delete", "policy", "-n", "managed2", "--all")
		By("Checking number of policy left in all ns")
		opt := metav1.ListOptions{}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 3, true, defaultTimeoutSeconds)
	})
	It("Should recover replicated policy when modified field disabled", func() {
		By("Modifiying policy in cluster ns managed2")
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds,
		)
		Expect(plc).ToNot(BeNil())
		plc.Object["spec"].(map[string]interface{})["disabled"] = true
		plc, err := clientHubDynamic.Resource(gvrPolicy).Namespace("managed2").Update(
			context.TODO(), plc, metav1.UpdateOptions{},
		)
		Expect(err).To(BeNil())
		Expect(plc.Object["spec"].(map[string]interface{})["disabled"]).To(Equal(true))
		By("Get policy in cluster ns managed2 again")
		Eventually(func() interface{} {
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds,
			)

			return plc.Object["spec"].(map[string]interface{})["disabled"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(false))
	})
	It("Should recover replicated policy when modified field remediationAction", func() {
		By("Modifiying policy in cluster ns managed2")
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds,
		)
		Expect(plc).ToNot(BeNil())
		plc.Object["spec"].(map[string]interface{})["remediationAction"] = "enforce"
		plc, err := clientHubDynamic.Resource(gvrPolicy).Namespace("managed2").Update(
			context.TODO(), plc, metav1.UpdateOptions{},
		)
		Expect(err).To(BeNil())
		Expect(plc.Object["spec"].(map[string]interface{})["remediationAction"]).To(Equal("enforce"))
		By("Getting policy in cluster ns managed2 again")
		Eventually(func() interface{} {
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds,
			)

			return plc.Object["spec"].(map[string]interface{})["remediationAction"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual("inform"))
	})
	It("Should recover replicated policy when modified field policy-templates", func() {
		By("Modifiying policy in cluster ns managed2")
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds,
		)
		Expect(plc).ToNot(BeNil())
		plc.Object["spec"].(map[string]interface{})["policy-templates"] = []*policiesv1.PolicyTemplate{}
		plc, err := clientHubDynamic.Resource(gvrPolicy).Namespace("managed2").Update(
			context.TODO(), plc, metav1.UpdateOptions{},
		)
		Expect(err).To(BeNil())
		By("Getting policy in cluster ns managed2 again")
		rootPlc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case3PolicyName, testNamespace, true, defaultTimeoutSeconds,
		)
		Eventually(func() interface{} {
			plc = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds,
			)

			return plc.Object["spec"]
		}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(rootPlc.Object["spec"]))
	})
	It("Should remove labels added to replicated policies", func() {
		By("Adding a label to the replicated policy in ns managed2")
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds,
		)
		Expect(plc).ToNot(BeNil())
		labels := plc.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels["test.io/grc-prop-case3-label"] = "caterpie"
		err := unstructured.SetNestedStringMap(plc.Object, labels, "metadata", "labels")
		Expect(err).To(BeNil())
		_, err = clientHubDynamic.Resource(gvrPolicy).Namespace("managed2").Update(
			context.TODO(), plc, metav1.UpdateOptions{},
		)
		Expect(err).To(BeNil())

		By("Checking that the label is removed")
		Eventually(func() map[string]string {
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds,
			)

			return plc.GetLabels()
		}, defaultTimeoutSeconds, 1).ShouldNot(HaveKey("test.io/grc-prop-case3-label"))
	})
	It("Should remove annotations added to replicated policies", func() {
		By("Adding an annotation to the replicated policy in ns managed2")
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds,
		)
		Expect(plc).ToNot(BeNil())
		annos := plc.GetAnnotations()
		if annos == nil {
			annos = make(map[string]string)
		}
		annos["test.io/grc-prop-case3-annotation"] = "weedle"
		err := unstructured.SetNestedStringMap(plc.Object, annos, "metadata", "annotations")
		Expect(err).To(BeNil())
		_, err = clientHubDynamic.Resource(gvrPolicy).Namespace("managed2").Update(
			context.TODO(), plc, metav1.UpdateOptions{},
		)
		Expect(err).To(BeNil())

		By("Checking that the annotation is removed")
		Eventually(func() map[string]string {
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case3PolicyName, "managed2", true, defaultTimeoutSeconds,
			)

			return plc.GetAnnotations()
		}, defaultTimeoutSeconds, 1).ShouldNot(HaveKey("test.io/grc-prop-case3-annotation"))
	})
})
