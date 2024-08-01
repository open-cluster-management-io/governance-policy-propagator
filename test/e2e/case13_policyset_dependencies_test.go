// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test replacement of policysets in dependencies", Ordered, func() {
	const (
		case13PolicyName     string = "case13-test-policy"
		case13PolicySetName  string = "case13-test-policyset"
		case13PolicyYaml     string = "../resources/case13_policyset_dependencies/case13-resources.yaml"
		case13Set2Yaml       string = "../resources/case13_policyset_dependencies/policyset2.yaml"
		case13Set2updateYaml string = "../resources/case13_policyset_dependencies/policyset2update.yaml"
	)

	setup := func() {
		By("Creating the policy, policyset, binding, and rule")
		utils.Kubectl("apply", "-f", case13PolicyYaml, "-n", testNamespace, "--kubeconfig="+kubeconfigHub)
		rootplc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case13PolicyName, testNamespace, true, defaultTimeoutSeconds,
		)
		Expect(rootplc).NotTo(BeNil())
		plcset := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicySet, case13PolicySetName, testNamespace, true, defaultTimeoutSeconds,
		)
		Expect(plcset).NotTo(BeNil())

		By("Patching the rule with a decision of cluster managed1")
		plr := utils.GetWithTimeout(
			clientHubDynamic, gvrPlacementRule, case13PolicyName+"-plr", testNamespace, true, defaultTimeoutSeconds,
		)
		plr.Object["status"] = utils.GeneratePlrStatus("managed1")
		_, err := clientHubDynamic.Resource(gvrPlacementRule).Namespace(testNamespace).UpdateStatus(
			context.TODO(), plr, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())

		By("Verifying the replicated policy was created")
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, testNamespace+"."+case13PolicyName, "managed1", true, defaultTimeoutSeconds,
		)
		Expect(plc).ToNot(BeNil())
		opt := metav1.ListOptions{
			LabelSelector: common.RootPolicyLabel + "=" + testNamespace + "." + case13PolicyName,
		}
		utils.ListWithTimeout(clientHubDynamic, gvrPolicy, opt, 1, true, defaultTimeoutSeconds)

		DeferCleanup(func() {
			By("Running cleanup")
			utils.Kubectl(
				"delete", "-f", case13PolicyYaml, "-n", testNamespace,
				"--ignore-not-found", "--kubeconfig="+kubeconfigHub,
			)
			utils.Kubectl("delete", "-f", case13Set2Yaml, "--ignore-not-found", "--kubeconfig="+kubeconfigHub)
			time.Sleep(5 * time.Second) // this helps everything get cleaned up completely
		})
	}

	Describe("testing top-level dependencies", Ordered, func() {
		const (
			case13Test1Yaml string = "../resources/case13_policyset_dependencies/test1.yaml"
			case13Test2Yaml string = "../resources/case13_policyset_dependencies/test2.yaml"
		)

		BeforeAll(setup)

		getDependencies := func(g Gomega) []string {
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy,
				testNamespace+"."+case13PolicyName, "managed1", true, defaultTimeoutSeconds)
			deps, found, err := unstructured.NestedSlice(plc.Object, "spec", "dependencies")
			g.Expect(found).To(BeTrue())
			g.Expect(err).ToNot(HaveOccurred())

			gotdeps := make([]string, 0)

			for _, d := range deps {
				dep, ok := d.(map[string]interface{})
				g.Expect(ok).To(BeTrue())

				kind, ok := dep["kind"].(string)
				g.Expect(ok).To(BeTrue())

				name, ok := dep["name"].(string)
				g.Expect(ok).To(BeTrue())

				gotdeps = append(gotdeps, kind+":"+name)
			}

			return gotdeps
		}

		It("should replace a PolicySet dependency with each policy in the existing set", func() {
			By("Updating the root policy to have a PolicySet dependency")
			_, err := utils.KubectlWithOutput("apply", "-f", case13Test1Yaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())

			By("Checking that the replicated policy has the correct list of dependencies")
			Eventually(getDependencies, defaultTimeoutSeconds, 1).Should(ConsistOf(
				"Policy:"+testNamespace+".case13-geodude",
				"Policy:"+testNamespace+".case13-slowpoke",
			))
		})

		It("should keep a non-existent PolicySet dependency as-is", func() {
			By("Updating the root policy to have a non-existent PolicySet dependency")
			_, err := utils.KubectlWithOutput("apply", "-f", case13Test2Yaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())

			By("Checking that the replicated policy has the correct list of dependencies")
			Eventually(getDependencies, defaultTimeoutSeconds, 1).Should(ConsistOf(
				"Policy:"+testNamespace+".case13-geodude",
				"Policy:"+testNamespace+".case13-slowpoke",
				"PolicySet:case13-test-policyset2",
			))
		})

		It("should update when the policy set is created", func() {
			By("Waiting some time, to make sure extra policy reconciles aren't pending")
			// This helps verify that the policy is correctly watching the PolicySets,
			// and isn't just coincidently being re-reconiled for some other reason.
			time.Sleep(5 * time.Second)

			By("Creating the PolicySet that was non-existent")
			_, err := utils.KubectlWithOutput("apply", "-f", case13Set2Yaml, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())

			By("Checking that the replicated policy has the correct list of dependencies")
			Eventually(getDependencies, defaultTimeoutSeconds, 1).Should(ConsistOf(
				"Policy:"+testNamespace+".case13-geodude",
				"Policy:"+testNamespace+".case13-slowpoke",
				"Policy:case13-namespace.case13-zubat",
				"Policy:case13-namespace.case13-psyduck",
			))
		})

		It("should update when a dependency policy set is updated", func() {
			By("Waiting some time, to make sure extra policy reconciles aren't pending")
			// This helps verify that the policy is correctly watching the PolicySets,
			// and isn't just coincidently being re-reconiled for some other reason.
			time.Sleep(5 * time.Second)

			By("Updating the PolicySet")
			_, err := utils.KubectlWithOutput("apply", "-f", case13Set2updateYaml, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())

			By("Checking that the replicated policy has the correct list of dependencies")
			Eventually(getDependencies, defaultTimeoutSeconds, 1).Should(ConsistOf(
				"Policy:"+testNamespace+".case13-geodude",
				"Policy:"+testNamespace+".case13-slowpoke",
				"Policy:case13-namespace.case13-golbat", // note: these names changed
				"Policy:case13-namespace.case13-golduck",
			))
		})
	})

	Describe("testing extraDependencies", Ordered, func() {
		const (
			case13Test3Yaml string = "../resources/case13_policyset_dependencies/test3.yaml"
			case13Test4Yaml string = "../resources/case13_policyset_dependencies/test4.yaml"
		)

		BeforeAll(setup)

		getExtraDependencies := func(g Gomega) []string {
			plc := utils.GetWithTimeout(clientHubDynamic, gvrPolicy,
				testNamespace+"."+case13PolicyName, "managed1", true, defaultTimeoutSeconds)

			templates, found, err := unstructured.NestedSlice(plc.Object, "spec", "policy-templates")
			g.Expect(found).To(BeTrue())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(templates).To(HaveLen(1))

			template, ok := templates[0].(map[string]interface{})
			g.Expect(ok).To(BeTrue())

			extraDeps, ok := template["extraDependencies"].([]interface{})
			g.Expect(ok).To(BeTrue())

			gotdeps := make([]string, 0)

			for _, d := range extraDeps {
				dep, ok := d.(map[string]interface{})
				g.Expect(ok).To(BeTrue())

				kind, ok := dep["kind"].(string)
				g.Expect(ok).To(BeTrue())

				name, ok := dep["name"].(string)
				g.Expect(ok).To(BeTrue())

				gotdeps = append(gotdeps, kind+":"+name)
			}

			return gotdeps
		}

		It("should replace a PolicySet extraDependency with each policy in the existing set", func() {
			By("Updating the root policy to have a PolicySet extraDependency")
			_, err := utils.KubectlWithOutput("apply", "-f", case13Test3Yaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())

			By("Checking that the replicated policy has the correct list of extraDependencies")
			Eventually(getExtraDependencies, defaultTimeoutSeconds, 1).Should(ConsistOf(
				"Policy:"+testNamespace+".case13-geodude",
				"Policy:"+testNamespace+".case13-slowpoke",
			))
		})

		It("should keep a non-existent PolicySet extraDependency as-is", func() {
			By("Updating the root policy to have a non-existent PolicySet dependency")
			_, err := utils.KubectlWithOutput("apply", "-f", case13Test4Yaml,
				"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())

			By("Checking that the replicated policy has the correct list of extraDependencies")
			Eventually(getExtraDependencies, defaultTimeoutSeconds, 1).Should(ConsistOf(
				"Policy:"+testNamespace+".case13-geodude",
				"Policy:"+testNamespace+".case13-slowpoke",
				"PolicySet:case13-test-policyset2",
			))
		})

		It("should update when the policy set is created", func() {
			By("Waiting some time, to make sure extra policy reconciles aren't pending")
			// This helps verify that the policy is correctly watching the PolicySets,
			// and isn't just coincidently being re-reconiled for some other reason.
			time.Sleep(5 * time.Second)

			By("Creating the PolicySet that was non-existent")
			_, err := utils.KubectlWithOutput("apply", "-f", case13Set2Yaml, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())

			By("Checking that the replicated policy has the correct list of extraDependencies")
			Eventually(getExtraDependencies, defaultTimeoutSeconds, 1).Should(ConsistOf(
				"Policy:"+testNamespace+".case13-geodude",
				"Policy:"+testNamespace+".case13-slowpoke",
				"Policy:case13-namespace.case13-zubat",
				"Policy:case13-namespace.case13-psyduck",
			))
		})

		It("should update when a dependency policy set is updated", func() {
			By("Waiting some time, to make sure extra policy reconciles aren't pending")
			// This helps verify that the policy is correctly watching the PolicySets,
			// and isn't just coincidently being re-reconiled for some other reason.
			time.Sleep(5 * time.Second)

			By("Updating the PolicySet")
			_, err := utils.KubectlWithOutput("apply", "-f", case13Set2updateYaml, "--kubeconfig="+kubeconfigHub)
			Expect(err).ToNot(HaveOccurred())

			By("Checking that the replicated policy has the correct list of dependencies")
			Eventually(getExtraDependencies, defaultTimeoutSeconds, 1).Should(ConsistOf(
				"Policy:"+testNamespace+".case13-geodude",
				"Policy:"+testNamespace+".case13-slowpoke",
				"Policy:case13-namespace.case13-golbat", // note: these names changed
				"Policy:case13-namespace.case13-golduck",
			))
		})
	})
})
