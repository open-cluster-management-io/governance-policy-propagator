package e2e

import (
	"context"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case19PolicyName string = "case19-test-policy"
	case19PolicyYaml string = "../resources/case19_rep_policy_placement/case19-test-policy.yaml"
)

var _ = Describe("Test replicated_policy controller and propagation", Ordered, Serial, func() {
	BeforeAll(func() {
		By("Creating " + case19PolicyName)
		utils.Kubectl("apply",
			"-f", case19PolicyYaml,
			"-n", testNamespace)
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case19PolicyName, testNamespace, true, defaultTimeoutSeconds,
		)
		Expect(plc).NotTo(BeNil())
	})
	AfterAll(func() {
		By("Creating " + case19PolicyName)
		utils.Kubectl("delete",
			"-f", case19PolicyYaml,
			"-n", testNamespace, "--ignore-not-found")
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case19PolicyName, testNamespace, false, defaultTimeoutSeconds,
		)
		Expect(plc).Should(BeNil())
		plr := utils.GetWithTimeout(
			clientHubDynamic, gvrPlacementDecision, case19PolicyName+"-plr-1",
			testNamespace, false, defaultTimeoutSeconds,
		)
		Expect(plr).Should(BeNil())
	})
	It("should propagate reconcile only managed2", func() {
		By("Patching test-policy-plr with decision of cluster managed1, managed2")
		plr := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPlacementDecision,
			case19PolicyName+"-plr-1",
			testNamespace,
			true,
			defaultTimeoutSeconds,
		)
		plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1", "managed2")
		_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
			context.TODO(),
			plr, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case19PolicyName, "managed1", true, defaultTimeoutSeconds,
		)
		Expect(plc).ToNot(BeNil())

		plc2 := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case19PolicyName, "managed2", true, defaultTimeoutSeconds,
		)
		Expect(plc2).ToNot(BeNil())
		beforeString := utils.GetMetrics("controller_runtime_reconcile_total",
			`controller=\"replicated-policy\"`,
			`,result=\"success\"`,
		)[0]
		beforeTotal, err := strconv.Atoi(beforeString)
		Expect(err).ShouldNot(HaveOccurred())
		// modify decision
		plr = utils.GetWithTimeout(
			clientHubDynamic,
			gvrPlacementDecision,
			case19PolicyName+"-plr-1",
			testNamespace,
			true,
			defaultTimeoutSeconds,
		)
		plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed2")
		_, err = clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
			context.TODO(),
			plr, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())
		plc = utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case19PolicyName, "managed1", false, defaultTimeoutSeconds,
		)
		Expect(plc).To(BeNil())
		plc2 = utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case19PolicyName, "managed2", true, defaultTimeoutSeconds,
		)
		Expect(plc2).ToNot(BeNil())
		afterString := utils.GetMetrics("controller_runtime_reconcile_total",
			`controller=\"replicated-policy\"`,
			`,result=\"success\"`,
		)[0]
		afterTotal, err := strconv.Atoi(afterString)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(afterTotal - beforeTotal).Should(Equal(1))
	})
	It("should reconcile twice when managed1 created and managed2 deleted By placementBinding", func() {
		// Now, connected cluster is managed2
		By("Get controller_runtime_reconcile_total number")
		beforeString := utils.GetMetrics("controller_runtime_reconcile_total",
			`controller=\"replicated-policy\"`,
			`,result=\"success\"`,
		)[0]
		beforeTotal, err := strconv.Atoi(beforeString)
		Expect(err).ShouldNot(HaveOccurred())
		By("Patching test-policy-plr with placementDecision of cluster managed1")
		plr := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPlacementDecision,
			case19PolicyName+"-plr-2",
			testNamespace,
			true,
			defaultTimeoutSeconds,
		)
		plr.Object["status"] = utils.GeneratePldStatus(plr.GetName(), plr.GetNamespace(), "managed1")
		_, err = clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
			context.TODO(),
			plr, metav1.UpdateOptions{},
		)
		Expect(err).ShouldNot(HaveOccurred())
		By("Patching gvrPlacementBinding placementRef")
		plb := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPlacementBinding,
			case19PolicyName+"-pb",
			testNamespace,
			true,
			defaultTimeoutSeconds,
		)
		plb.Object["placementRef"].(map[string]interface{})["name"] = case19PolicyName + "-plr-2"
		_, err = clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
			context.TODO(),
			plb, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())
		By("Check replicate policy changed")
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case19PolicyName, "managed1", true, defaultTimeoutSeconds,
		)
		Expect(plc).ToNot(BeNil())
		plc2 := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case19PolicyName, "managed2", false, defaultTimeoutSeconds,
		)
		Expect(plc2).To(BeNil())
		afterString := utils.GetMetrics("controller_runtime_reconcile_total",
			`controller=\"replicated-policy\"`,
			`,result=\"success\"`,
		)[0]
		afterTotal, err := strconv.Atoi(afterString)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(afterTotal - beforeTotal).Should(Equal(2))
	})
	It("should reconcile twice when managed1 and managed2 are created by placementRule", func() {
		// Now, connected cluster is managed1
		beforeString := utils.GetMetrics("controller_runtime_reconcile_total",
			`controller=\"replicated-policy\"`,
			`,result=\"success\"`,
		)[0]
		beforeTotal, err := strconv.Atoi(beforeString)
		Expect(err).ShouldNot(HaveOccurred())
		By("Patching gvrPlacementBinding placementRef")
		plb := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPlacementBinding,
			case19PolicyName+"-pb",
			testNamespace,
			true,
			defaultTimeoutSeconds,
		)
		plb.Object["placementRef"].(map[string]interface{})["name"] = case19PolicyName + "-plr"
		plb.Object["placementRef"].(map[string]interface{})["kind"] = "PlacementRule"
		plb.Object["placementRef"].(map[string]interface{})["apiGroup"] = gvrPlacementRule.Group
		_, err = clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
			context.TODO(),
			plb, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())
		afterString := utils.GetMetrics("controller_runtime_reconcile_total",
			`controller=\"replicated-policy\"`,
			`,result=\"success\"`,
		)[0]
		By("Managed1 deleted, Should be only 1 reconcile")
		afterTotal, err := strconv.Atoi(afterString)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(afterTotal - beforeTotal).Should(Equal(1))

		By("Get controller_runtime_reconcile_total number before change placementrule")
		beforeString = utils.GetMetrics("controller_runtime_reconcile_total",
			`controller=\"replicated-policy\"`,
			`,result=\"success\"`,
		)[0]
		beforeTotal, err = strconv.Atoi(beforeString)
		Expect(err).ShouldNot(HaveOccurred())

		By("Patching test-policy-plr with placementRule of cluster managed1, managed2")
		plr := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPlacementRule,
			case19PolicyName+"-plr",
			testNamespace,
			true,
			defaultTimeoutSeconds,
		)
		plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
		_, err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
			context.TODO(),
			plr, metav1.UpdateOptions{},
		)
		Expect(err).ShouldNot(HaveOccurred())

		By("Check replicate policy changed")
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case19PolicyName, "managed1", true, defaultTimeoutSeconds,
		)
		Expect(plc).ToNot(BeNil())
		plc2 := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case19PolicyName, "managed2", true, defaultTimeoutSeconds,
		)
		Expect(plc2).ToNot(BeNil())
		afterString = utils.GetMetrics("controller_runtime_reconcile_total",
			`controller=\"replicated-policy\"`,
			`,result=\"success\"`,
		)[0]
		afterTotal, err = strconv.Atoi(afterString)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(afterTotal - beforeTotal).Should(Equal(2))
	})
})
