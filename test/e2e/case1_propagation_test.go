// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case1PolicyName string = "case1-test-policy"
	case1PolicyYaml string = "../resources/case1_propagation/case1-test-policy.yaml"
)

var _ = Describe("Test policy propagation", func() {
	Describe("Test event emission when policy is disabled", Ordered, func() {
		BeforeAll(func() {
			By("Creating the policy, placementrule, and placementbinding")
			utils.Kubectl("apply", "-f", case1PolicyYaml, "-n", testNamespace)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())

			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case1PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})

		AfterAll(func() {
			By("Removing the policy")
			err := clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Delete(
				context.TODO(),
				case1PolicyName,
				metav1.DeleteOptions{},
			)
			if !errors.IsNotFound(err) {
				Expect(err).ToNot(HaveOccurred())
			}

			By("Removing the placementrule")
			err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).Delete(
				context.TODO(),
				case1PolicyName+"-plr",
				metav1.DeleteOptions{},
			)
			if !errors.IsNotFound(err) {
				Expect(err).ToNot(HaveOccurred())
			}

			By("Removing the PlacementBinding")
			err = clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Delete(
				context.TODO(),
				case1PolicyName+"-pb",
				metav1.DeleteOptions{},
			)
			if !errors.IsNotFound(err) {
				Expect(err).ToNot(HaveOccurred())
			}
		})

		It("Should not create duplicate policy disable events", func() {
			By("Disabling the root policy")
			patch := []byte(`{"spec": {"disabled": ` + strconv.FormatBool(true) + `}}`)

			_, err := clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Patch(
				context.TODO(),
				case1PolicyName,
				types.MergePatchType,
				patch,
				metav1.PatchOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Checking the events emitted by the propagator in the root policy ns")
			Consistently(func() int {
				numEvents := len(utils.GetMatchingEvents(
					clientHub,
					testNamespace,
					case1PolicyName,
					"PolicyPropagation",
					"disabled",
					defaultTimeoutSeconds,
				))

				return numEvents
			}, defaultTimeoutSeconds, 1).Should(BeNumerically("<", 2))
		})
	})

	Describe("Create policy/pb/plc in ns:"+testNamespace+" and then update pb/plc", func() {
		It("should be created in user ns", func() {
			By("Creating " + case1PolicyYaml)
			utils.Kubectl("apply",
				"-f", case1PolicyYaml,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case1PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case1PolicyName, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed2", func() {
			By("Patching test-policy-plr with decision of cluster managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case1PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case1PolicyName, "managed2", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case1PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case1PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				testNamespace+"."+case1PolicyName,
				"managed1",
				true,
				defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			plc = utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				testNamespace+"."+case1PolicyName,
				"managed2",
				false,
				defaultTimeoutSeconds,
			)
			Expect(plc).To(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-plr with decision of both managed1 and managed2")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case1PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1", "managed2")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Patching test-policy-pb with a non existing plr")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case1PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["placementRef"] = &policiesv1.Subject{
				APIGroup: "apps.open-cluster-management.io",
				Kind:     "PlacementRule",
				Name:     case1PolicyName + "-plr-nonexists",
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-pb with correct plr")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case1PolicyName+"-pb",
				testNamespace, true,
				defaultTimeoutSeconds,
			)
			pb.Object["placementRef"] = &policiesv1.Subject{
				APIGroup: "apps.open-cluster-management.io",
				Kind:     "PlacementRule",
				Name:     case1PolicyName + "-plr",
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Patching test-policy-pb with a plc with wrong apigroup")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case1PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy1.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case1PolicyName,
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
				case1PolicyName+"-pb",
				testNamespace, true,
				defaultTimeoutSeconds,
			)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case1PolicyName,
				},
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should prevent user from creating subject with unsupported kind", func() {
			By("Patching test-policy-pb with a plc with wrong kind")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case1PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy1",
					Name:     case1PolicyName,
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
				case1PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case1PolicyName,
				},
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Patching test-policy-pb with a plc with wrong name")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case1PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case1PolicyName + "1",
				},
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should propagate to cluster ns managed1 and managed2", func() {
			By("Patching test-policy-pb with correct plc")
			pb := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPlacementBinding,
				case1PolicyName+"-pb",
				testNamespace,
				true,
				defaultTimeoutSeconds,
			)
			pb.Object["subjects"] = []policiesv1.Subject{
				{
					APIGroup: "policy.open-cluster-management.io",
					Kind:     "Policy",
					Name:     case1PolicyName,
				},
			}
			_, err := clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Update(
				context.TODO(), pb, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 2, true, defaultTimeoutSeconds)
		})
		It("should remove policy from ns managed1 and managed2", func() {
			By("Patching test-policy-plr with no decision")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case1PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = &appsv1.PlacementRuleStatus{}
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should clean up", func() {
			utils.Kubectl("delete",
				"-f", case1PolicyYaml,
				"-n", testNamespace)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, 10)
		})
	})

	Describe("Create policy/pb/plc in ns:"+testNamespace+" and then update policy", func() {
		It("should be created in user ns", func() {
			By("Creating " + case1PolicyYaml)
			utils.Kubectl("apply",
				"-f", case1PolicyYaml,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case1PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case1PolicyName, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should update replicated policy in ns managed1", func() {
			By("Patching test-policy with spec.remediationAction = enforce")
			rootPlc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds,
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
					testNamespace+"."+case1PolicyName,
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
				clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds,
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
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, true, defaultTimeoutSeconds)
		})
		It("should be created in user ns", func() {
			By("Creating " + case1PolicyYaml)
			utils.Kubectl("apply",
				"-f", case1PolicyYaml,
				"-n", testNamespace)
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(plc).NotTo(BeNil())
		})
		It("should propagate to cluster ns managed1", func() {
			By("Patching test-policy-plr with decision of cluster managed1")
			plr := utils.GetWithTimeout(
				clientHubDynamic, gvrPlacementRule, case1PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
			)
			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
			plc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, testNamespace+"."+case1PolicyName, "managed1", true, defaultTimeoutSeconds,
			)
			Expect(plc).ToNot(BeNil())
			opt := metav1.ListOptions{
				LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case1PolicyName,
			}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)
		})
		It("should update test-policy to a different policy template", func() {
			By("Creating ../resources/case1_propagation/case1-test-policy2.yaml")
			utils.Kubectl("apply",
				"-f", "../resources/case1_propagation/case1-test-policy2.yaml",
				"-n", testNamespace)
			rootPlc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			Expect(rootPlc).NotTo(BeNil())
			yamlPlc := utils.ParseYaml("../resources/case1_propagation/case1-test-policy2.yaml")
			Eventually(func() interface{} {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic,
					gvrPolicy,
					testNamespace+"."+case1PolicyName,
					"managed1",
					true,
					defaultTimeoutSeconds,
				)

				return replicatedPlc.Object["spec"]
			}, defaultTimeoutSeconds, 1).Should(utils.SemanticEqual(yamlPlc.Object["spec"]))
		})
		It("should update labels on replicated policies when added to the root policy", func() {
			By("Adding a label to the root policy")
			rootPlc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			labels := rootPlc.GetLabels()
			if labels == nil {
				labels = make(map[string]string)
			}
			labels["test.io/grc-prop-case1-label"] = "bellossom"
			err := unstructured.SetNestedStringMap(rootPlc.Object, labels, "metadata", "labels")
			Expect(err).ToNot(HaveOccurred())
			_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Update(
				context.TODO(), rootPlc, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Checking the replicated policy for the label")
			Eventually(func() map[string]string {
				plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case1PolicyName,
					"managed1", true, defaultTimeoutSeconds)

				return plc.GetLabels()
			}, defaultTimeoutSeconds, 1).Should(HaveKey("test.io/grc-prop-case1-label"))
		})
		It("should update annotations on replicated policies when added to the root policy", func() {
			By("Adding an annotation to the root policy")
			rootPlc := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, case1PolicyName, testNamespace, true, defaultTimeoutSeconds,
			)
			annos := rootPlc.GetAnnotations()
			if annos == nil {
				annos = make(map[string]string)
			}
			annos["test.io/grc-prop-case1-annotation"] = "scizor"
			err := unstructured.SetNestedStringMap(rootPlc.Object, annos, "metadata", "annotations")
			Expect(err).ToNot(HaveOccurred())
			_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace).Update(
				context.TODO(), rootPlc, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Checking the replicated policy for the annotation")
			Eventually(func() map[string]string {
				plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy, testNamespace+"."+case1PolicyName,
					"managed1", true, defaultTimeoutSeconds)

				return plc.GetAnnotations()
			}, defaultTimeoutSeconds, 1).Should(HaveKey("test.io/grc-prop-case1-annotation"))
		})
		It("should clean up", func() {
			utils.Kubectl("delete",
				"-f", "../resources/case1_propagation/case1-test-policy2.yaml",
				"-n", testNamespace)
			opt := metav1.ListOptions{}
			utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 0, false, 10)
		})
	})

	Describe("Test spec.copyPolicyMetadata", Ordered, func() {
		const policyName = "case1-metadata"
		policyClient := func() dynamic.ResourceInterface {
			return clientHubDynamic.Resource(gvrPolicy).Namespace(testNamespace)
		}

		getPolicy := func() *policiesv1.Policy {
			return &policiesv1.Policy{
				TypeMeta: metav1.TypeMeta{
					Kind:       policiesv1.Kind,
					APIVersion: policiesv1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      policyName,
					Namespace: testNamespace,
				},
				Spec: policiesv1.PolicySpec{
					Disabled:        false,
					PolicyTemplates: []*policiesv1.PolicyTemplate{},
				},
			}
		}

		BeforeAll(func() {
			By("Creating a PlacementRule")
			plr := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "apps.open-cluster-management.io/v1",
					"kind":       "PlacementRule",
					"metadata": map[string]interface{}{
						"name":      policyName,
						"namespace": testNamespace,
					},
					"spec": map[string]interface{}{},
				},
			}
			var err error
			plr, err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).Create(
				context.TODO(), plr, metav1.CreateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			plr.Object["status"] = utils.GeneratePlrStatus("managed1")
			_, err = clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
				context.TODO(), plr, metav1.UpdateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Creating a PlacementBinding")
			plb := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": policiesv1.GroupVersion.String(),
					"kind":       "PlacementBinding",
					"metadata": map[string]interface{}{
						"name":      policyName,
						"namespace": testNamespace,
					},
					"placementRef": map[string]interface{}{
						"apiGroup": gvrPlacementRule.Group,
						"kind":     "PlacementRule",
						"name":     policyName,
					},
					"subjects": []interface{}{
						map[string]interface{}{
							"apiGroup": policiesv1.GroupVersion.Group,
							"kind":     policiesv1.Kind,
							"name":     policyName,
						},
					},
				},
			}

			_, err = clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Create(
				context.TODO(), plb, metav1.CreateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			By("Removing the policy")
			err := policyClient().Delete(context.TODO(), policyName, metav1.DeleteOptions{})
			if !errors.IsNotFound(err) {
				Expect(err).ToNot(HaveOccurred())
			}

			replicatedPolicy := utils.GetWithTimeout(
				clientHubDynamic,
				gvrPolicy,
				testNamespace+"."+policyName,
				"managed1",
				false,
				defaultTimeoutSeconds,
			)
			Expect(replicatedPolicy).To(BeNil())
		})

		AfterAll(func() {
			By("Removing the PlacementRule")
			err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).Delete(
				context.TODO(), policyName, metav1.DeleteOptions{},
			)
			if !errors.IsNotFound(err) {
				Expect(err).ToNot(HaveOccurred())
			}

			By("Removing the PlacementBinding")
			err = clientHubDynamic.Resource(gvrPlacementBinding).Namespace(testNamespace).Delete(
				context.TODO(), policyName, metav1.DeleteOptions{},
			)
			if !errors.IsNotFound(err) {
				Expect(err).ToNot(HaveOccurred())
			}
		})

		It("Verifies that spec.copyPolicyMetadata defaults to unset", func() {
			By("Creating an empty policy")
			policy := getPolicy()
			policyMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(policy)
			Expect(err).ToNot(HaveOccurred())

			_, err = policyClient().Create(
				context.TODO(), &unstructured.Unstructured{Object: policyMap}, metav1.CreateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic,
					gvrPolicy,
					testNamespace+"."+policyName,
					"managed1",
					true,
					defaultTimeoutSeconds,
				)

				_, found, _ := unstructured.NestedBool(replicatedPlc.Object, "spec", "copyPolicyMetadata")
				g.Expect(found).To(BeFalse())
			}, defaultTimeoutSeconds, 1).Should(Succeed())
		})

		It("verifies that the labels and annotations are copied with spec.copyPolicyMetadata=true", func() {
			By("Creating a policy with labels and annotations")
			policy := getPolicy()
			copyPolicyMetadata := true
			policy.Spec.CopyPolicyMetadata = &copyPolicyMetadata
			policy.SetAnnotations(map[string]string{"do": "copy", "please": "do-copy"})
			policy.SetLabels(map[string]string{"do": "copy", "please": "do-copy"})

			policyMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(policy)
			Expect(err).ToNot(HaveOccurred())

			_, err = policyClient().Create(
				context.TODO(), &unstructured.Unstructured{Object: policyMap}, metav1.CreateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic,
					gvrPolicy,
					testNamespace+"."+policyName,
					"managed1",
					true,
					defaultTimeoutSeconds,
				)

				annotations := replicatedPlc.GetAnnotations()
				g.Expect(annotations["do"]).To(Equal("copy"))
				g.Expect(annotations["please"]).To(Equal("do-copy"))
				// This annotation is always set.
				g.Expect(annotations["argocd.argoproj.io/compare-options"]).To(Equal("IgnoreExtraneous"))

				labels := replicatedPlc.GetLabels()
				g.Expect(labels["do"]).To(Equal("copy"))
				g.Expect(labels["please"]).To(Equal("do-copy"))
			}, defaultTimeoutSeconds, 1).Should(Succeed())
		})

		It("verifies that the labels and annotations are not copied with spec.copyPolicyMetadata=false", func() {
			By("Creating a policy with labels and annotations")
			policy := getPolicy()
			copyPolicyMetadata := false
			policy.Spec.CopyPolicyMetadata = &copyPolicyMetadata
			policy.SetAnnotations(map[string]string{"do-not": "copy", "please": "do-not-copy"})
			policy.SetLabels(map[string]string{"do-not": "copy", "please": "do-not-copy"})

			policyMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(policy)
			Expect(err).ToNot(HaveOccurred())

			_, err = policyClient().Create(
				context.TODO(), &unstructured.Unstructured{Object: policyMap}, metav1.CreateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				replicatedPlc := utils.GetWithTimeout(
					clientHubDynamic,
					gvrPolicy,
					testNamespace+"."+policyName,
					"managed1",
					true,
					defaultTimeoutSeconds,
				)

				annotations := replicatedPlc.GetAnnotations()
				g.Expect(annotations["do-not"]).To(Equal(""))
				g.Expect(annotations["please"]).To(Equal(""))
				// This annotation is always set.
				g.Expect(annotations["argocd.argoproj.io/compare-options"]).To(Equal("IgnoreExtraneous"))

				labels := replicatedPlc.GetLabels()
				g.Expect(labels["do-not"]).To(Equal(""))
				g.Expect(labels["please"]).To(Equal(""))
			}, defaultTimeoutSeconds, 1).Should(Succeed())
		})
	})
})
