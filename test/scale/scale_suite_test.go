// Copyright (c) 2024 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package scale

import (
	"context"
	"flag"
	"strconv"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterbeta1 "open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var (
	K8sClient         client.Client
	K8sConfig         *rest.Config
	kubeconfigHub     string
	scheme            *k8sruntime.Scheme
	testNamespace     string
	placementName     string
	plcBindingName    string
	plcDecisionName   string
	managedClusterNum int
	policyNum         int
	policyNamePrefix  string
	eventNum          int
)

func TestScale(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Policy Propagator scale Suite")
}

func init() {
	klog.SetOutput(GinkgoWriter)
	klog.InitFlags(nil)
	flag.StringVar(&kubeconfigHub, "kubeconfig_hub", "../../kubeconfig_hub_e2e",
		"Location of the kubeconfig to use; defaults to current kubeconfig if set to an empty string")

	scheme = k8sruntime.NewScheme()

	err := clusterv1.AddToScheme(scheme)
	Expect(err).ShouldNot(HaveOccurred())

	err = policyv1.AddToScheme(scheme)
	Expect(err).ShouldNot(HaveOccurred())

	err = corev1.AddToScheme(scheme)
	Expect(err).ShouldNot(HaveOccurred())

	err = clusterbeta1.AddToScheme(scheme)
	Expect(err).ShouldNot(HaveOccurred())

	testNamespace = "scale"
	placementName = "scale-test-placement"
	plcBindingName = "scale-test-plcbinding"
	plcDecisionName = "scale-test-decision"
	managedClusterNum = 3000
	policyNum = 30
	policyNamePrefix = "policy-scale-test-"
	eventNum = 4
}

var _ = BeforeSuite(func(ctx context.Context) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigHub)
	Expect(err).ShouldNot(HaveOccurred())

	K8sClient, err := client.New(config, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(K8sClient).NotTo(BeNil())

	By("Create Test Namespace")
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNamespace,
		},
	}

	err = K8sClient.Create(ctx, ns)
	Expect(err).NotTo(HaveOccurred())

	By("Create policies")
	for i := 0; i < policyNum; i++ {
		policyName := policyNamePrefix + strconv.Itoa(i)

		By("create policy: " + policyName)
		policy := &policyv1.Policy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      policyName,
				Namespace: testNamespace,
			},
			Spec: policyv1.PolicySpec{
				Disabled: false,
				PolicyTemplates: []*policyv1.PolicyTemplate{
					{
						ObjectDefinition: k8sruntime.RawExtension{
							Raw: []byte(`{
							"apiVersion": "policies.ibm.com/v1alpha1",
							"kind": "TrustedContainerPolicy",
							"metadata": null,
							"name": "case1-test-policy-trustedcontainerpolicy",
							"spec": null,
							"severity": "low",
							"namespaceSelector": null,
							"include": [
							  "default"
							],
							"exclude": [
							  "kube-system"
							],
							"remediationAction": "inform",
							"imageRegistry": "quay.io"
						  }`),
						},
					},
				},
			},
		}

		err := K8sClient.Create(ctx, policy)
		Expect(err).NotTo(HaveOccurred())
	}

	for i := 1; i < managedClusterNum+1; i++ {
		// managed1 managed2... managed6 are already created
		if i < 7 {
			continue
		}

		mcName := "managed" + strconv.Itoa(i)
		By("Create Namespace")
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: mcName,
			},
		}
		err := K8sClient.Create(ctx, ns)
		Expect(err).NotTo(HaveOccurred())

		By("Create managedCluster: " + mcName)
		managedcluster := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: mcName,
				Labels: map[string]string{
					"cloud":  "auto-detect",
					"vendor": "auto-detect",
					"name":   mcName,
				},
				Namespace: mcName,
			},
		}

		err = K8sClient.Create(ctx, managedcluster)
		Expect(err).NotTo(HaveOccurred())
	}

	By("Create placement: " + placementName)
	placement := &clusterbeta1.Placement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      placementName,
			Namespace: testNamespace,
		},
		Spec: clusterbeta1.PlacementSpec{
			Predicates: []clusterbeta1.ClusterPredicate{
				{RequiredClusterSelector: clusterbeta1.ClusterSelector{
					LabelSelector: metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{},
					},
				}},
			},
		},
	}

	err = K8sClient.Create(ctx, placement)
	Expect(err).NotTo(HaveOccurred())

	By("Create placementdecision")
	pd := &clusterbeta1.PlacementDecision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      plcDecisionName,
			Namespace: testNamespace,
			Labels: map[string]string{
				"cluster.open-cluster-management.io/placement": placementName,
			},
		},
	}

	err = K8sClient.Create(ctx, pd)
	Expect(err).ShouldNot(HaveOccurred())

	// Add status to decision
	fetchedPd := &clusterbeta1.PlacementDecision{}

	Eventually(func() error {
		return K8sClient.Get(ctx, types.NamespacedName{
			Name:      plcDecisionName,
			Namespace: testNamespace,
		}, fetchedPd)
	}, 10).ShouldNot(HaveOccurred())

	decisionStatus := make([]clusterbeta1.ClusterDecision,
		0, managedClusterNum)

	for k := 1; k < managedClusterNum; k++ {
		decisionStatus = append(decisionStatus, clusterbeta1.ClusterDecision{
			ClusterName: "managed" + strconv.Itoa(k),
		})
	}
	pdStatus := clusterbeta1.PlacementDecisionStatus{
		Decisions: decisionStatus,
	}

	fetchedPd.Status = pdStatus

	err = K8sClient.Status().Update(ctx, fetchedPd)
	Expect(err).ShouldNot(HaveOccurred())

	By("Create placementBinding: " + plcBindingName)

	subjects := make([]policyv1.Subject, 0, policyNum)
	for j := 0; j < policyNum; j++ {
		sb := policyv1.Subject{
			APIGroup: policyv1.GroupVersion.Group,
			Kind:     "Policy",
			Name:     "policy-scale-test-" + strconv.Itoa(j),
		}
		subjects = append(subjects, sb)
	}

	pb := &policyv1.PlacementBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      plcBindingName,
			Namespace: testNamespace,
		},
		PlacementRef: policyv1.PlacementSubject{
			APIGroup: clusterbeta1.GroupName,
			Kind:     "Placement",
			Name:     placementName,
		},
		Subjects: subjects,
	}

	err = K8sClient.Create(ctx, pb)
	Expect(err).NotTo(HaveOccurred())
})

// HIGHLY Recommend: It is better to remove cluster and recreate cluster again
// This cleanup takes a lot of time
var _ = AfterSuite(func(ctx context.Context) {
	By("Delete Parent Policy")
	for i := 0; i < policyNum; i++ {
		policyName := policyNamePrefix + strconv.Itoa(i)
		utils.Kubectl("delete", "policy", policyName, "-n", testNamespace,
			"--kubeconfig="+kubeconfigHub, "--ignore-not-found")
	}

	By("Delete Scale Namespace")
	utils.Kubectl("delete", "ns", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")

	for i := 1; i < managedClusterNum; i++ {
		// managed1 managed2 managed3 are already created
		if i < 4 {
			continue
		}

		mcName := "managed" + strconv.Itoa(i)

		By("Delete ManagedCluster Namespaces")
		utils.Kubectl("delete", "ns", mcName, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")

		Eventually(func() bool {
			By("Check ManagedCluster Namespace has been deleted")
			ns := &corev1.Namespace{}
			err := K8sClient.Get(ctx, types.NamespacedName{
				Name:      mcName,
				Namespace: "",
			}, ns)

			return k8serrors.IsNotFound(err)
		}, 30, 1).Should(BeTrue())

		By("Delete managedCluster: " + mcName)
		utils.Kubectl("delete", "managedcluster", mcName,
			"--kubeconfig="+kubeconfigHub, "--ignore-not-found")
	}

	By("Delete placement")
	utils.Kubectl("delete", "placement", placementName,
		"-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")

	By("Delete decisions")
	utils.Kubectl("delete", "placementdecision", plcDecisionName,
		"-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")

	By("Delete placementBinding")
	utils.Kubectl("delete", "placementbinding", plcBindingName,
		"-n", testNamespace, "--kubeconfig="+kubeconfigHub, "--ignore-not-found")
})
