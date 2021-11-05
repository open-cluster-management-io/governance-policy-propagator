// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	clusterv1alpha1 "github.com/open-cluster-management/api/cluster/v1alpha1"
	policyv1 "github.com/open-cluster-management/governance-policy-propagator/api/v1"
	policyv1beta1 "github.com/open-cluster-management/governance-policy-propagator/api/v1beta1"
	automationctrl "github.com/open-cluster-management/governance-policy-propagator/controllers/automation"
	metricsctrl "github.com/open-cluster-management/governance-policy-propagator/controllers/policymetrics"
	propagatorctrl "github.com/open-cluster-management/governance-policy-propagator/controllers/propagator"
	"github.com/open-cluster-management/governance-policy-propagator/version"
	appsv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = k8sruntime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func printVersion() {
	setupLog.Info(fmt.Sprintf("Operator Version: %s", version.Version))
	setupLog.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	setupLog.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(clusterv1alpha1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))

	utilruntime.Must(policyv1.AddToScheme(scheme))
	utilruntime.Must(policyv1beta1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8383", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	printVersion()

	namespace, err := getWatchNamespace()
	if err != nil {
		setupLog.Error(err, "Failed to get watch namespace")
		os.Exit(1)
	}

	// Get a config to talk to the apiserver
	cfg := config.GetConfigOrDie()

	// Some default tuned values here, but can be overriden via env vars
	cfg.QPS = 200.0
	cfg.Burst = 400
	qpsOverride, found := os.LookupEnv("CONTROLLER_CONFIG_QPS")
	if found {
		qpsVal, err := strconv.ParseFloat(qpsOverride, 32)
		if err == nil {
			cfg.QPS = float32(qpsVal)
			setupLog.Info(fmt.Sprintf("Using QPS override: %v", cfg.QPS))
		}
	}
	burstOverride, found := os.LookupEnv("CONTROLLER_CONFIG_BURST")
	if found {
		burstVal, err := strconv.Atoi(burstOverride)
		if err == nil {
			cfg.Burst = burstVal
			setupLog.Info(fmt.Sprintf("Using Burst override: %v", cfg.Burst))
		}
	}

	// Set default manager options
	options := ctrl.Options{
		Namespace:              namespace,
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "c6e0b7c1.open-cluster-management.io",
	}

	// Add support for MultiNamespace set in WATCH_NAMESPACE (e.g ns1,ns2)
	// Note that this is not intended to be used for excluding namespaces, this is better done via a Predicate
	// Also note that you may face performance issues when using this with a high number of namespaces.
	// More Info: https://godoc.org/github.com/kubernetes-sigs/controller-runtime/pkg/cache#MultiNamespacedCacheBuilder
	if strings.Contains(namespace, ",") {
		options.Namespace = ""
		options.NewCache = cache.MultiNamespacedCacheBuilder(strings.Split(namespace, ","))
	}

	mgr, err := ctrl.NewManager(cfg, options)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	setupLog.Info("Registering Components.")

	if err = (&propagatorctrl.PolicyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor(propagatorctrl.ControllerName),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", propagatorctrl.ControllerName)
		os.Exit(1)
	}

	if reportMetrics() {
		if err = (&metricsctrl.MetricReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", metricsctrl.ControllerName)
			os.Exit(1)
		}
	}

	if err = (&automationctrl.PolicyAutomationReconciler{
		Client:        mgr.GetClient(),
		DynamicClient: dynamic.NewForConfigOrDie(mgr.GetConfig()),
		Scheme:        mgr.GetScheme(),
		Recorder:      mgr.GetEventRecorderFor(automationctrl.ControllerName),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", automationctrl.ControllerName)
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// Setup config and client for propagator to talk to the apiserver
	var generatedClient kubernetes.Interface = kubernetes.NewForConfigOrDie(mgr.GetConfig())
	propagatorctrl.Initialize(cfg, &generatedClient)

	cache := mgr.GetCache()

	// The following index for the PlacementRef Name is being added to the
	// client cache to improve the performance of querying PlacementBindings
	indexFunc := func(obj client.Object) []string {
		return []string{obj.(*policyv1.PlacementBinding).PlacementRef.Name}
	}

	if err := cache.IndexField(context.TODO(), &policyv1.PlacementBinding{}, "placementRef.name", indexFunc); err != nil {
		panic(err)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// reportMetrics returns a bool on whether to report GRC metrics from the propagator
func reportMetrics() bool {
	metrics, found := os.LookupEnv("DISABLE_REPORT_METRICS")
	if found && strings.ToLower(metrics) == "true" {
		return false
	}
	return true
}

// getWatchNamespace returns the Namespace the operator should be watching for changes
func getWatchNamespace() (string, error) {
	// WatchNamespaceEnvVar is the constant for env variable WATCH_NAMESPACE
	// which specifies the Namespace to watch.
	// An empty value means the operator is running with cluster scope.
	var watchNamespaceEnvVar = "WATCH_NAMESPACE"

	ns, found := os.LookupEnv(watchNamespaceEnvVar)
	if !found {
		return "", fmt.Errorf("%s must be set", watchNamespaceEnvVar)
	}
	return ns, nil
}
