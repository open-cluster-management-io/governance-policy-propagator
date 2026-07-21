// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test policyset exclusions propagation", Ordered, func() {
	const (
		path                                  string = "../resources/case18_policyset_exclusions/"
		case18Policy                          string = "case18-test-policy"
		case18PolicySet                       string = "case18-test-policyset"
		case18PolicySetPB                     string = "case18-test-policyset-pb"
		case18PolicySetPLD                    string = "case18-test-policyset-plm-decision"
		case18Yaml                            string = path + "case18-test-policyset.yaml"
		case18SecondPolicy                    string = "case18-second-policy"
		case18SecondPolicyYaml                string = path + "case18-second-policy.yaml"
		case18PolicySetReportingStatusMessage string = "All policies are reporting status"
		case18PolicySetExclusionStatusMessage string = "All policies are reporting status; " +
			"One or more policies were excluded. For details, see status.exclusions."
	)

	AfterAll(func(ctx SpecContext) {
		cleanupCase18Resources(ctx, case18PolicySet,
			[]string{case18Yaml, case18SecondPolicyYaml},
			[]string{case18Policy, case18SecondPolicy},
		)
	})

	It("should create the policyset resources", func(ctx SpecContext) {
		bootstrapCase18PolicySet(ctx, case18Yaml, case18PolicySet, case18PolicySetPLD)
	})

	It("should not propagate to an excluded cluster before initial deployment", func(ctx SpecContext) {
		expectReplicatedPolicy(case18Policy, "managed1", true)
		expectReplicatedPolicy(case18Policy, "managed2", false)

		By("Patching replicated policy status on managed1")
		patchReplicatedPolicyComplianceStatus(ctx, case18Policy, 1)

		Eventually(func(g Gomega) {
			expectPolicySetStatusMessage(g, case18PolicySet, case18PolicySetExclusionStatusMessage)
			expectPolicySetStatusExclusions(g, case18PolicySet, case18Policy, "managed2")
		}, defaultTimeoutSeconds, 1).Should(Succeed())

		By("Checking root policy status")
		Eventually(func(g Gomega) {
			expectRootPolicyStatus(g, case18Policy, case18PolicySet, case18PolicySetPB, "managed2")
			rootStatus := getRootPolicyStatus(g, case18Policy)

			clusterStatuses, ok := rootStatus["status"].([]any)
			g.Expect(ok).To(BeTrue())
			g.Expect(clusterStatuses).ToNot(BeEmpty())

			for _, clusterStatus := range clusterStatuses {
				clusterStatusMap, ok := clusterStatus.(map[string]any)
				g.Expect(ok).To(BeTrue())

				_, hasRemainingBindings := clusterStatusMap["remainingBindings"]
				g.Expect(hasRemainingBindings).To(BeFalse())
			}
		}, defaultTimeoutSeconds, 1).Should(Succeed())
	})

	It("should leave other policies in the policyset unaffected", func(ctx SpecContext) {
		By("Adding a second policy to the policyset without excluding it")
		applyCase18Resources(ctx, case18SecondPolicyYaml)

		expectReplicatedPolicy(case18SecondPolicy, "managed1", true)
		expectReplicatedPolicy(case18SecondPolicy, "managed2", true)

		By("Patching replicated policy status for the second policy")
		patchReplicatedPolicyComplianceStatus(ctx, case18SecondPolicy, 2)

		By("Patching policyset to exclude the second policy from managed1")
		patchPolicySetSpec(ctx, case18PolicySet, func(spec map[string]any) {
			spec["exclusions"] = []map[string]any{
				{"policyName": case18Policy, "clusterNames": []string{"managed2"}},
				{"policyName": case18SecondPolicy, "clusterNames": []string{"managed1"}},
			}
		})

		By("Checking each policy propagates only to non-excluded clusters")
		expectReplicatedPolicy(case18Policy, "managed1", true)
		expectReplicatedPolicy(case18Policy, "managed2", false)
		expectReplicatedPolicy(case18SecondPolicy, "managed1", false)
		expectReplicatedPolicy(case18SecondPolicy, "managed2", true)
	})

	It("should re-propagate when exclusions is cleared", func(ctx SpecContext) {
		By("Clearing exclusions")
		patchPolicySetSpec(ctx, case18PolicySet, func(spec map[string]any) { delete(spec, "exclusions") })

		expectReplicatedPolicy(case18Policy, "managed2", true)

		By("Patching replicated policy statuses for all policyset policies")
		patchReplicatedPolicyComplianceStatus(ctx, case18Policy, 2)
		patchReplicatedPolicyComplianceStatus(ctx, case18SecondPolicy, 2)

		By("Checking PolicySet and root policy status after exclusions are cleared")
		Eventually(func(g Gomega) {
			expectPolicySetStatusMessage(g, case18PolicySet, case18PolicySetReportingStatusMessage)
			_, hasExclusions := getPolicySetStatus(g, case18PolicySet)["exclusions"]
			g.Expect(hasExclusions).To(BeFalse())
			placement := getPolicySetPlacement(g, case18Policy, case18PolicySet)
			_, hasPlacementExclusions := placement["exclusions"]
			g.Expect(hasPlacementExclusions).To(BeFalse())
		}, defaultTimeoutSeconds, 1).Should(Succeed())
	})

	It("should remove an already deployed policy from a newly excluded cluster", func(ctx SpecContext) {
		By("Excluding managed2 after the policy is deployed")
		patchPolicySetSpec(ctx, case18PolicySet, func(spec map[string]any) {
			spec["exclusions"] = exclusionSpec(case18Policy, "managed2")
		})

		expectReplicatedPolicy(case18Policy, "managed2", false)
		expectReplicatedPolicy(case18Policy, "managed1", true)
	})

	It("should move the exclusion to a different cluster when patched", func(ctx SpecContext) {
		expectReplicatedPolicy(case18Policy, "managed2", false)
		expectReplicatedPolicy(case18Policy, "managed1", true)

		By("Patching exclusion from managed2 to managed1")
		patchPolicySetSpec(ctx, case18PolicySet, func(spec map[string]any) {
			spec["exclusions"] = exclusionSpec(case18Policy, "managed1")
		})

		expectReplicatedPolicy(case18Policy, "managed1", false)
		expectReplicatedPolicy(case18Policy, "managed2", true)

		By("Patching replicated policy statuses for all policyset policies")
		patchReplicatedPolicyComplianceStatus(ctx, case18Policy, 1)
		patchReplicatedPolicyComplianceStatus(ctx, case18SecondPolicy, 2)

		Eventually(func(g Gomega) {
			expectPolicySetStatusMessage(g, case18PolicySet, case18PolicySetExclusionStatusMessage)
			expectPolicySetStatusExclusions(g, case18PolicySet, case18Policy, "managed1")
			expectRootPolicyStatus(g, case18Policy, case18PolicySet, case18PolicySetPB, "managed1")
		}, defaultTimeoutSeconds, 1).Should(Succeed())
	})

	It("should remove the policy when it is removed from the policyset", func(ctx SpecContext) {
		By("Patching policyset with only the second policy")
		patchPolicySetSpec(ctx, case18PolicySet, func(spec map[string]any) {
			spec["policies"] = []string{case18SecondPolicy}
			spec["exclusions"] = []map[string]any{}
		})

		expectReplicatedPolicyCount(case18Policy, 0)
	})

	It("should clean up when the policyset is deleted with an active exclusion", func(ctx SpecContext) {
		By("Restoring policyset membership with an active exclusion")
		patchPolicySetSpec(ctx, case18PolicySet, func(spec map[string]any) {
			spec["policies"] = []string{case18Policy, case18SecondPolicy}
			spec["exclusions"] = exclusionSpec(case18Policy, "managed2")
		})

		expectReplicatedPolicy(case18Policy, "managed1", true)
		expectReplicatedPolicy(case18Policy, "managed2", false)
		expectReplicatedPolicyCount(case18Policy, 1)

		By("Deleting policyset while the exclusion is still active")

		_, err := utils.KubectlWithOutput(ctx, "delete", "policyset", case18PolicySet,
			"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
		Expect(err).ToNot(HaveOccurred())

		expectReplicatedPolicyCount(case18Policy, 0)
		expectReplicatedPolicy(case18Policy, "managed2", false)
	})
})

var _ = Describe("Test policyset exclusions webhook", Label("webhook"), Ordered, func() {
	const (
		path               string = "../resources/case18_policyset_exclusions/"
		case18Policy       string = "case18-test-policy"
		case18PolicySet    string = "case18-test-policyset"
		case18PolicySetPLD string = "case18-test-policyset-plm-decision"
		case18Yaml         string = path + "case18-test-policyset.yaml"
	)

	BeforeAll(func(ctx SpecContext) {
		bootstrapCase18PolicySet(ctx, case18Yaml, case18PolicySet, case18PolicySetPLD)
	})

	AfterAll(func(ctx SpecContext) {
		_, _ = utils.KubectlWithOutput(ctx, "delete",
			"-f", case18Yaml, "-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
	})

	It("should reject invalid exclusions for policies not in spec.policies", func(ctx SpecContext) {
		plcSet := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
		)
		spec := plcSet.Object["spec"].(map[string]any)
		spec["exclusions"] = exclusionSpec("unknown-policy", "managed1")

		_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
			ctx, plcSet, metav1.UpdateOptions{},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("policy in spec.exclusions is not listed in spec.policies"))
		Expect(err.Error()).To(ContainSubstring("unknown-policy"))
	})

	It("should reject duplicate exclusions for the same policy", func(ctx SpecContext) {
		plcSet := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicySet, case18PolicySet, testNamespace, true, defaultTimeoutSeconds,
		)
		spec := plcSet.Object["spec"].(map[string]any)
		spec["exclusions"] = []map[string]any{
			{"policyName": case18Policy, "clusterNames": []string{"managed1"}},
			{"policyName": case18Policy, "clusterNames": []string{"managed2"}},
		}

		_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
			ctx, plcSet, metav1.UpdateOptions{},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("policy in spec.exclusions has a duplicate entry"))
		Expect(err.Error()).To(ContainSubstring(case18Policy))
	})
})

var _ = Describe("Test policyset exclusions with multiple bindings", Ordered, func() {
	const (
		path                           string = "../resources/case18_policyset_exclusions/"
		case18MultiPolicy              string = "case18-multi-policy"
		case18MultiPolicySet           string = "case18-multi-policyset"
		case18MultiSecondPolicySet     string = "case18-multi-second-policyset"
		case18MultiBindingYaml         string = path + "case18-multi-binding.yaml"
		case18MultiSecondPolicySetYaml        = path + "case18-multi-second-policyset.yaml"
		case18MultiDirectPB            string = "case18-multi-policy-direct-pb"
		case18MultiPolicySetPB         string = "case18-multi-policyset-pb"
		case18MultiSecondPolicySetPB          = "case18-multi-second-policyset-pb"
		case18MultiDirectPLD           string = "case18-multi-policy-direct-plm-decision"
		case18MultiPolicySetPLD        string = "case18-multi-policyset-plm-decision"
		case18MultiSecondPolicySetPLD         = "case18-multi-second-policyset-plm-decision"
	)

	AfterAll(func(ctx SpecContext) {
		_, _ = utils.KubectlWithOutput(ctx, "delete", "policyset", case18MultiSecondPolicySet,
			"-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
		cleanupCase18Resources(ctx, case18MultiPolicySet,
			[]string{case18MultiBindingYaml, case18MultiSecondPolicySetYaml},
			[]string{case18MultiPolicy},
		)
	})

	It("should create the multi-binding resources", func(ctx SpecContext) {
		applyCase18Resources(ctx, case18MultiBindingYaml)
		patchPDStatus(ctx, case18MultiDirectPLD, "managed2")
		patchPDStatus(ctx, case18MultiPolicySetPLD, "managed1", "managed2")
	})

	It("should propagate via direct binding when policyset path excludes the cluster", func(ctx SpecContext) {
		By("Waiting for replicated policies on managed1 and managed2")
		expectReplicatedPolicy(case18MultiPolicy, "managed1", true)
		expectReplicatedPolicy(case18MultiPolicy, "managed2", true)
		expectReplicatedPolicyCount(case18MultiPolicy, 2)

		By("Checking root policy status for placement exclusions and remainingBindings")
		Eventually(func(g Gomega) {
			status := getRootPolicyStatus(g, case18MultiPolicy)
			expectRootPolicyStatus(g, case18MultiPolicy, case18MultiPolicySet, case18MultiPolicySetPB, "managed2")

			managed2Status := getClusterStatus(g, status, "managed2")
			remainingBindings, ok := managed2Status["remainingBindings"].([]any)
			g.Expect(ok).To(BeTrue())
			g.Expect(remainingBindings).To(HaveLen(1))
			g.Expect(remainingBindings[0].(map[string]any)["placementBinding"]).To(Equal(case18MultiDirectPB))
		}, defaultTimeoutSeconds, 1).Should(Succeed())
	})

	It("should retain remainingBindings when the policy belongs to a second PolicySet with the same exclusion",
		func(ctx SpecContext) {
			By("Adding a second policyset with the same managed2 exclusion")
			applyCase18Resources(ctx, case18MultiSecondPolicySetYaml)
			patchPDStatus(ctx, case18MultiSecondPolicySetPLD, "managed1", "managed2")

			expectReplicatedPolicy(case18MultiPolicy, "managed1", true)
			expectReplicatedPolicy(case18MultiPolicy, "managed2", true)
			expectReplicatedPolicyCount(case18MultiPolicy, 2)

			By("Checking root policy remainingBindings still lists only the direct binding on managed2")
			Eventually(func(g Gomega) {
				status := getRootPolicyStatus(g, case18MultiPolicy)
				expectRootPolicyStatus(
					g, case18MultiPolicy, case18MultiPolicySet, case18MultiPolicySetPB, "managed2",
				)
				expectRootPolicyStatus(
					g, case18MultiPolicy, case18MultiSecondPolicySet, case18MultiSecondPolicySetPB, "managed2",
				)

				managed2Status := getClusterStatus(g, status, "managed2")
				remainingBindings, ok := managed2Status["remainingBindings"].([]any)
				g.Expect(ok).To(BeTrue())
				g.Expect(remainingBindings).To(HaveLen(1))
				g.Expect(remainingBindings[0].(map[string]any)["placementBinding"]).To(Equal(case18MultiDirectPB))
			}, defaultTimeoutSeconds, 1).Should(Succeed())
		})

	It("should retain direct-bound placement when policyset is deleted with active exclusion", func(ctx SpecContext) {
		By("Removing the second policyset to restore the single-policyset scenario")

		_, err := utils.KubectlWithOutput(ctx, "delete", "policyset", case18MultiSecondPolicySet,
			"-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
		Expect(err).ToNot(HaveOccurred())

		_, err = utils.KubectlWithOutput(ctx, "delete",
			"-f", case18MultiSecondPolicySetYaml,
			"-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
		Expect(err).ToNot(HaveOccurred())

		By("Deleting policyset while the exclusion remains active")

		_, err = utils.KubectlWithOutput(ctx, "delete", "policyset", case18MultiPolicySet,
			"-n", testNamespace, "--kubeconfig="+kubeconfigHub)
		Expect(err).ToNot(HaveOccurred())

		expectReplicatedPolicy(case18MultiPolicy, "managed2", true)
		expectReplicatedPolicy(case18MultiPolicy, "managed1", false)
		expectReplicatedPolicyCount(case18MultiPolicy, 1)

		Eventually(func(g Gomega) {
			status := getRootPolicyStatus(g, case18MultiPolicy)
			placements, ok := status["placement"].([]any)
			g.Expect(ok).To(BeTrue())
			g.Expect(placements).To(HaveLen(1))
			g.Expect(placements[0].(map[string]any)["placementBinding"]).To(Equal(case18MultiDirectPB))
			_, hasPolicySet := placements[0].(map[string]any)["policySet"]
			g.Expect(hasPolicySet).To(BeFalse())

			managed2Status := getClusterStatus(g, status, "managed2")
			_, hasRemainingBindings := managed2Status["remainingBindings"]
			g.Expect(hasRemainingBindings).To(BeFalse())
		}, defaultTimeoutSeconds, 1).Should(Succeed())
	})
})

func replicatedPolicyName(rootPolicy string) string {
	return testNamespace + "." + rootPolicy
}

func rootPolicySelector(rootPolicy string) metav1.ListOptions {
	return metav1.ListOptions{LabelSelector: common.RootPolicyLabel + "=" + replicatedPolicyName(rootPolicy)}
}

func getPolicySetStatus(g Gomega, policySetName string) map[string]any {
	plcSet := utils.GetWithTimeout(
		clientHubDynamic, gvrPolicySet, policySetName, testNamespace, true, defaultTimeoutSeconds,
	)
	status, ok := plcSet.Object["status"].(map[string]any)
	g.Expect(ok).To(BeTrue())

	return status
}

func getRootPolicyStatus(g Gomega, rootPolicy string) map[string]any {
	rootPlc := utils.GetWithTimeout(
		clientHubDynamic, gvrPolicy, rootPolicy, testNamespace, true, defaultTimeoutSeconds,
	)
	status, ok := rootPlc.Object["status"].(map[string]any)
	g.Expect(ok).To(BeTrue())

	return status
}

func getPolicySetPlacement(g Gomega, rootPolicy, policySetName string) map[string]any {
	placements, ok := getRootPolicyStatus(g, rootPolicy)["placement"].([]any)
	g.Expect(ok).To(BeTrue())

	for _, placement := range placements {
		placementMap, ok := placement.(map[string]any)
		g.Expect(ok).To(BeTrue())

		if placementMap["policySet"] == policySetName {
			return placementMap
		}
	}

	return nil
}

func getClusterStatus(g Gomega, rootStatus map[string]any, cluster string) map[string]any {
	clusterStatuses, ok := rootStatus["status"].([]any)
	g.Expect(ok).To(BeTrue())

	for _, item := range clusterStatuses {
		clusterStatusMap, ok := item.(map[string]any)
		g.Expect(ok).To(BeTrue())

		if clusterStatusMap["clustername"] == cluster {
			return clusterStatusMap
		}
	}

	return nil
}

func exclusionSpec(policy string, clusters ...string) []map[string]any {
	return []map[string]any{{"policyName": policy, "clusterNames": clusters}}
}

func applyCase18Resources(ctx SpecContext, yaml string) {
	By("Creating " + yaml)
	_, err := utils.KubectlWithOutput(ctx, "apply",
		"-f", yaml, "-n", testNamespace, "--kubeconfig="+kubeconfigHub)
	Expect(err).ToNot(HaveOccurred())
}

func patchPDStatus(ctx SpecContext, pldName string, clusters ...string) {
	By("Ensuring placement decisions are available")

	pld := utils.GetWithTimeout(
		clientHubDynamic, gvrPlacementDecision, pldName, testNamespace, true, defaultTimeoutSeconds,
	)
	pld.Object["status"] = utils.GeneratePldStatus(pld.GetName(), pld.GetNamespace(), clusters...)
	_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(testNamespace).UpdateStatus(
		ctx, pld, metav1.UpdateOptions{},
	)
	Expect(err).ToNot(HaveOccurred())
}

//nolint:unparam // policySetName is passed from describe-scoped constants
func patchPolicySetSpec(ctx SpecContext, policySetName string, mutate func(map[string]any)) {
	Eventually(func(g Gomega) {
		plcSet := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicySet, policySetName, testNamespace, true, defaultTimeoutSeconds,
		)
		spec, ok := plcSet.Object["spec"].(map[string]any)
		g.Expect(ok).To(BeTrue())
		mutate(spec)

		_, err := clientHubDynamic.Resource(gvrPolicySet).Namespace(testNamespace).Update(
			ctx, plcSet, metav1.UpdateOptions{},
		)
		g.Expect(err).ToNot(HaveOccurred())
	}, defaultTimeoutSeconds, 1).Should(Succeed())
}

func patchReplicatedPolicyComplianceStatus(ctx SpecContext, rootPolicyName string, expectedCount int) {
	replicatedPlcList := utils.ListWithTimeout(
		clientHubDynamic, gvrPolicy, rootPolicySelector(rootPolicyName), expectedCount, true, defaultTimeoutSeconds,
	)

	for _, replicatedPlc := range replicatedPlcList.Items {
		clusterName := replicatedPlc.GetNamespace()
		status := &policiesv1.PolicyStatus{
			ComplianceState: policiesv1.Compliant,
			Status: []*policiesv1.CompliancePerClusterStatus{{
				ClusterName: clusterName, ClusterNamespace: clusterName, ComplianceState: policiesv1.Compliant,
			}},
		}
		statusMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(status)
		Expect(err).ToNot(HaveOccurred())

		replicatedPlc.Object["status"] = statusMap
		_, err = clientHubDynamic.Resource(gvrPolicy).Namespace(replicatedPlc.GetNamespace()).UpdateStatus(
			ctx, &replicatedPlc, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())
	}
}

func bootstrapCase18PolicySet(ctx SpecContext, yaml, policySetName, pldName string) {
	applyCase18Resources(ctx, yaml)

	plcSet := utils.GetWithTimeout(
		clientHubDynamic, gvrPolicySet, policySetName, testNamespace, true, defaultTimeoutSeconds,
	)
	Expect(plcSet).NotTo(BeNil())
	patchPDStatus(ctx, pldName, "managed1", "managed2")
}

func cleanupCase18Resources(ctx SpecContext, policySetName string, yamlPaths, rootPolicies []string) {
	By("Cleaning up case18 resources")

	_, _ = utils.KubectlWithOutput(ctx, "delete", "policyset", policySetName,
		"-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
	for _, yaml := range yamlPaths {
		_, _ = utils.KubectlWithOutput(ctx, "delete",
			"-f", yaml, "-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
	}

	for _, policyName := range rootPolicies {
		_, _ = utils.KubectlWithOutput(ctx, "delete", "policy", policyName,
			"-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
	}

	for _, policyName := range rootPolicies {
		utils.ListWithTimeout(
			clientHubDynamic, gvrPolicy, rootPolicySelector(policyName), 0, false, defaultTimeoutSeconds,
		)
	}
}

func expectReplicatedPolicy(rootPolicy, cluster string, exists bool) {
	plc := utils.GetWithTimeout(
		clientHubDynamic, gvrPolicy, replicatedPolicyName(rootPolicy), cluster, exists, defaultTimeoutSeconds,
	)
	if exists {
		Expect(plc).NotTo(BeNil())
	} else {
		Expect(plc).To(BeNil())
	}
}

func expectReplicatedPolicyCount(rootPolicy string, count int) {
	utils.ListWithTimeout(
		clientHubDynamic, gvrPolicy, rootPolicySelector(rootPolicy), count, true, defaultTimeoutSeconds,
	)
}

func expectPolicySetStatusMessage(g Gomega, policySetName, message string) {
	g.Expect(getPolicySetStatus(g, policySetName)["statusMessage"]).To(Equal(message))
}

func expectPolicySetStatusExclusions(g Gomega, policySetName, policyName string, clusters ...string) {
	exclusions, ok := getPolicySetStatus(g, policySetName)["exclusions"].([]any)
	g.Expect(ok).To(BeTrue())
	g.Expect(exclusions).To(HaveLen(1))

	statusExclusion, ok := exclusions[0].(map[string]any)
	g.Expect(ok).To(BeTrue())
	g.Expect(statusExclusion["policyName"]).To(Equal(policyName))

	statusClusters, ok := statusExclusion["clusters"].([]any)
	g.Expect(ok).To(BeTrue())

	expectedClusters := make([]any, len(clusters))
	for i, cluster := range clusters {
		expectedClusters[i] = cluster
	}

	g.Expect(statusClusters).To(ConsistOf(expectedClusters...))
}

func expectRootPolicyStatus(g Gomega, rootPolicy, policySetName, binding, excludedCluster string) {
	placement := getPolicySetPlacement(g, rootPolicy, policySetName)
	g.Expect(placement).ToNot(BeNil())
	g.Expect(placement["placementBinding"]).To(Equal(binding))
	pathExclusions, ok := placement["exclusions"].([]any)
	g.Expect(ok).To(BeTrue())
	g.Expect(pathExclusions).To(HaveLen(1))
	g.Expect(pathExclusions[0].(map[string]any)["clusterName"]).To(Equal(excludedCluster))

	rootStatus := getRootPolicyStatus(g, rootPolicy)
	clusterStatuses, ok := rootStatus["status"].([]any)
	g.Expect(ok).To(BeTrue())

	for _, clusterStatus := range clusterStatuses {
		clusterStatusMap, ok := clusterStatus.(map[string]any)
		g.Expect(ok).To(BeTrue())

		clusterName, ok := clusterStatusMap["clustername"].(string)
		g.Expect(ok).To(BeTrue())

		// For excluded clusters, compliant field should be nil
		if clusterName == excludedCluster {
			_, hasCompliant := clusterStatusMap["compliant"]
			g.Expect(hasCompliant).To(BeFalse())
		}
	}
}
