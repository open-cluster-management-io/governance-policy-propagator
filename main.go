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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	//+kubebuilder:scaffold:imports
	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policyv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	automationctrl "open-cluster-management.io/governance-policy-propagator/controllers/automation"
	encryptionkeysctrl "open-cluster-management.io/governance-policy-propagator/controllers/encryptionkeys"
	metricsctrl "open-cluster-management.io/governance-policy-propagator/controllers/policymetrics"
	policysetctrl "open-cluster-management.io/governance-policy-propagator/controllers/policyset"
	propagatorctrl "open-cluster-management.io/governance-policy-propagator/controllers/propagator"
	"open-cluster-management.io/governance-policy-propagator/version"
)

var (
	scheme = k8sruntime.NewScheme()
	log    = ctrl.Log.WithName("setup")
)

func printVersion() {
	log.Info(
		"Using",
		"OperatorVersion", version.Version,
		"GoVersion", runtime.Version(),
		"GOOS", runtime.GOOS,
		"GOARCH", runtime.GOARCH,
	)
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(clusterv1beta1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
	utilruntime.Must(policyv1.AddToScheme(scheme))
	utilruntime.Must(policyv1beta1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var keyRotationDays, keyRotationMaxConcurrency uint

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8383", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.UintVar(
		&keyRotationDays,
		"encryption-key-rotation",
		30,
		"The number of days until the policy encryption key is rotated",
	)
	flag.UintVar(
		&keyRotationMaxConcurrency,
		"key-rotation-max-concurrency",
		10,
		"The maximum number of concurrent reconciles for the policy-encryption-keys controller",
	)

	opts := zap.Options{
		Development: true,
	}

	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	printVersion()

	if keyRotationDays < 1 {
		log.Info("the encryption-key-rotation flag must be greater than 0")
		os.Exit(1)
	}

	if keyRotationMaxConcurrency < 1 {
		log.Info("the key-rotation-max-concurrency flag must be greater than 0")
		os.Exit(1)
	}

	namespace, err := getWatchNamespace()
	if err != nil {
		log.Error(err, "Failed to get watch namespace")
		os.Exit(1)
	}

	// Get a config to talk to the apiserver
	cfg := config.GetConfigOrDie()

	// Some default tuned values here, but can be overridden via env vars
	cfg.QPS = 200.0
	cfg.Burst = 400

	qpsOverride, found := os.LookupEnv("CONTROLLER_CONFIG_QPS")
	if found {
		qpsVal, err := strconv.ParseFloat(qpsOverride, 32)
		if err == nil {
			cfg.QPS = float32(qpsVal)
			log.Info("Using QPS override", "value", cfg.QPS)
		}
	}

	burstOverride, found := os.LookupEnv("CONTROLLER_CONFIG_BURST")
	if found {
		burstVal, err := strconv.Atoi(burstOverride)
		if err == nil {
			cfg.Burst = burstVal
			log.Info("Using Burst override", "value", cfg.Burst)
		}
	}

	// Set a field selector so that a watch on secrets will be limited to just the secret with the policy template
	// encryption key.
	newCacheFunc := cache.BuilderWithOptions(
		cache.Options{
			SelectorsByObject: cache.SelectorsByObject{
				&corev1.Secret{}: {
					Field: fields.SelectorFromSet(fields.Set{"metadata.name": propagatorctrl.EncryptionKeySecret}),
				},
			},
		},
	)

	// Set default manager options
	options := ctrl.Options{
		Namespace:              namespace,
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "policy-propagator.open-cluster-management.io",
		NewCache:               newCacheFunc,
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
		log.Error(err, "Unable to start manager")
		os.Exit(1)
	}

	log.Info("Registering components")

	if err = (&propagatorctrl.PolicyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor(propagatorctrl.ControllerName),
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "Unable to create the controller", "controller", propagatorctrl.ControllerName)
		os.Exit(1)
	}

	if reportMetrics() {
		if err = (&metricsctrl.MetricReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr); err != nil {
			log.Error(err, "Unable to create the controller", "controller", metricsctrl.ControllerName)
			os.Exit(1)
		}
	}

	if err = (&automationctrl.PolicyAutomationReconciler{
		Client:        mgr.GetClient(),
		DynamicClient: dynamic.NewForConfigOrDie(mgr.GetConfig()),
		Scheme:        mgr.GetScheme(),
		Recorder:      mgr.GetEventRecorderFor(automationctrl.ControllerName),
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "Unable to create the controller", "controller", automationctrl.ControllerName)
		os.Exit(1)
	}

	if err = (&policysetctrl.PolicySetReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor(policysetctrl.ControllerName),
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "Unable to create controller", "controller", policysetctrl.ControllerName)
		os.Exit(1)
	}

	if err = (&encryptionkeysctrl.EncryptionKeysReconciler{
		Client:                  mgr.GetClient(),
		KeyRotationDays:         keyRotationDays,
		MaxConcurrentReconciles: keyRotationMaxConcurrency,
		Scheme:                  mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "Unable to create controller", "controller", encryptionkeysctrl.ControllerName)
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "Unable to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "Unable to set up ready check")
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

	if err := cache.IndexField(
		context.TODO(), &policyv1.PlacementBinding{}, "placementRef.name", indexFunc,
	); err != nil {
		panic(err)
	}

	log.Info("Starting manager")

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "Problem running manager")
		os.Exit(1)
	}
}

// reportMetrics returns a bool on whether to report GRC metrics from the propagator
func reportMetrics() bool {
	metrics, _ := os.LookupEnv("DISABLE_REPORT_METRICS")

	return !strings.EqualFold(metrics, "true")
}

// getWatchNamespace returns the Namespace the operator should be watching for changes
func getWatchNamespace() (string, error) {
	// WatchNamespaceEnvVar is the constant for env variable WATCH_NAMESPACE
	// which specifies the Namespace to watch.
	// An empty value means the operator is running with cluster scope.
	watchNamespaceEnvVar := "WATCH_NAMESPACE"

	ns, found := os.LookupEnv(watchNamespaceEnvVar)
	if !found {
		return "", fmt.Errorf("%s must be set", watchNamespaceEnvVar)
	}

	return ns, nil
}
