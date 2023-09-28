// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var (
	testNamespace         string
	clientHub             kubernetes.Interface
	clientHubDynamic      dynamic.Interface
	gvrPolicy             schema.GroupVersionResource
	gvrPolicyAutomation   schema.GroupVersionResource
	gvrPolicySet          schema.GroupVersionResource
	gvrPlacementBinding   schema.GroupVersionResource
	gvrPlacementRule      schema.GroupVersionResource
	gvrPlacement          schema.GroupVersionResource
	gvrPlacementDecision  schema.GroupVersionResource
	gvrSecret             schema.GroupVersionResource
	gvrAnsibleJob         schema.GroupVersionResource
	gvrNamespace          schema.GroupVersionResource
	defaultTimeoutSeconds int
	defaultImageRegistry  string
)

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Policy Propagator e2e Suite")
}

func init() {
	klog.SetOutput(GinkgoWriter)
}

var _ = BeforeSuite(func() {
	By("Setup Hub client")
	gvrPolicy = schema.GroupVersionResource{
		Group: "policy.open-cluster-management.io", Version: "v1", Resource: "policies",
	}
	gvrPolicySet = schema.GroupVersionResource{
		Group: "policy.open-cluster-management.io", Version: "v1beta1", Resource: "policysets",
	}
	gvrPlacementBinding = schema.GroupVersionResource{
		Group: "policy.open-cluster-management.io", Version: "v1", Resource: "placementbindings",
	}
	gvrPlacementRule = schema.GroupVersionResource{
		Group: "apps.open-cluster-management.io", Version: "v1", Resource: "placementrules",
	}
	gvrPlacement = schema.GroupVersionResource{
		Group: "cluster.open-cluster-management.io", Version: "v1beta1", Resource: "placements",
	}
	gvrPlacementDecision = schema.GroupVersionResource{
		Group: "cluster.open-cluster-management.io", Version: "v1beta1", Resource: "placementdecisions",
	}
	gvrSecret = schema.GroupVersionResource{
		Group: "", Version: "v1", Resource: "secrets",
	}
	gvrPolicyAutomation = schema.GroupVersionResource{
		Group: "policy.open-cluster-management.io", Version: "v1beta1", Resource: "policyautomations",
	}
	gvrAnsibleJob = schema.GroupVersionResource{
		Group: "tower.ansible.com", Version: "v1alpha1", Resource: "ansiblejobs",
	}
	gvrNamespace = schema.GroupVersionResource{
		Group: "", Version: "v1", Resource: "namespaces",
	}
	clientHub = NewKubeClient("", "", "")
	clientHubDynamic = NewKubeClientDynamic("", "", "")
	defaultImageRegistry = "quay.io/open-cluster-management"
	testNamespace = "policy-propagator-test"
	defaultTimeoutSeconds = 30
	By("Create Namespace if needed")
	namespaces := clientHub.CoreV1().Namespaces()
	if _, err := namespaces.Get(
		context.TODO(), testNamespace, metav1.GetOptions{},
	); err != nil && errors.IsNotFound(err) {
		Expect(namespaces.Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		}, metav1.CreateOptions{})).NotTo(BeNil())
	}
	Expect(namespaces.Get(context.TODO(), testNamespace, metav1.GetOptions{})).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	By("Collecting workqueue_adds_total metrics")
	wqAddsLines, err := utils.MetricsLines("workqueue_adds_total")
	if err != nil {
		GinkgoWriter.Println("Error getting workqueue_adds_total metrics: ", err)
	}

	GinkgoWriter.Println(wqAddsLines)

	By("Collecting controller_runtime_reconcile_total metrics")
	ctrlReconcileTotalLines, err := utils.MetricsLines("controller_runtime_reconcile_total")
	if err != nil {
		GinkgoWriter.Println("Error getting controller_runtime_reconcile_total metrics: ", err)
	}

	GinkgoWriter.Println(ctrlReconcileTotalLines)

	By("Collecting controller_runtime_reconcile_time_seconds_sum metrics")
	ctrlReconcileTimeLines, err := utils.MetricsLines("controller_runtime_reconcile_time_seconds_sum")
	if err != nil {
		GinkgoWriter.Println("Error getting controller_runtime_reconcile_time_seconds_sum metrics: ", err)
	}

	GinkgoWriter.Println(ctrlReconcileTimeLines)
})

func NewKubeClient(url, kubeconfig, context string) kubernetes.Interface {
	klog.V(5).Infof("Create kubeclient for url %s using kubeconfig path %s\n", url, kubeconfig)

	config, err := LoadConfig(url, kubeconfig, context)
	if err != nil {
		panic(err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	return clientset
}

func NewKubeClientDynamic(url, kubeconfig, context string) dynamic.Interface {
	klog.V(5).Infof("Create kubeclient dynamic for url %s using kubeconfig path %s\n", url, kubeconfig)

	config, err := LoadConfig(url, kubeconfig, context)
	if err != nil {
		panic(err)
	}

	clientset, err := dynamic.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	return clientset
}

func LoadConfig(url, kubeconfig, context string) (*rest.Config, error) {
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	klog.V(5).Infof("Kubeconfig path %s\n", kubeconfig)

	// If we have an explicit indication of where the kubernetes config lives, read that.
	if kubeconfig != "" {
		if context == "" {
			return clientcmd.BuildConfigFromFlags(url, kubeconfig)
		}

		return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
			&clientcmd.ConfigOverrides{
				CurrentContext: context,
			}).ClientConfig()
	}

	// If not, try the in-cluster config.
	if c, err := rest.InClusterConfig(); err == nil {
		return c, nil
	}

	// If no in-cluster config, try the default location in the user's home directory.
	if usr, err := user.Current(); err == nil {
		klog.V(5).Infof(
			"clientcmd.BuildConfigFromFlags for url %s using %s\n",
			url,
			filepath.Join(usr.HomeDir, ".kube", "config"),
		)

		if c, err := clientcmd.BuildConfigFromFlags("", filepath.Join(usr.HomeDir, ".kube", "config")); err == nil {
			return c, nil
		}
	}

	return nil, fmt.Errorf("could not create a valid kubeconfig")
}
