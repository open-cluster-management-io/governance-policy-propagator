// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/go-logr/zapr"
	"github.com/spf13/pflag"
	"github.com/stolostron/go-log-utils/zaputil"
	templates "github.com/stolostron/go-template-utils/v4/pkg/templates"
	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/klog/v2"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/source"

	//+kubebuilder:scaffold:imports
	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policyv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	automationctrl "open-cluster-management.io/governance-policy-propagator/controllers/automation"
	"open-cluster-management.io/governance-policy-propagator/controllers/complianceeventsapi"
	encryptionkeysctrl "open-cluster-management.io/governance-policy-propagator/controllers/encryptionkeys"
	metricsctrl "open-cluster-management.io/governance-policy-propagator/controllers/policymetrics"
	policysetctrl "open-cluster-management.io/governance-policy-propagator/controllers/policyset"
	propagatorctrl "open-cluster-management.io/governance-policy-propagator/controllers/propagator"
	rootpolicystatusctrl "open-cluster-management.io/governance-policy-propagator/controllers/rootpolicystatus"
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
	klog.InitFlags(nil)

	zflags := zaputil.FlagConfig{
		LevelName:   "log-level",
		EncoderName: "log-encoder",
	}

	zflags.Bind(flag.CommandLine)

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)

	var (
		metricsAddr                 string
		enableLeaderElection        bool
		probeAddr                   string
		keyRotationDays             uint
		keyRotationMaxConcurrency   uint
		policyMetricsMaxConcurrency uint
		policyStatusMaxConcurrency  uint
		rootPolicyMaxConcurrency    uint
		replPolicyMaxConcurrency    uint
		enableComplianceEventsStore bool
		enableWebhooks              bool
		eventHistoryAPIHost         string
		eventHistoryAPIPort         string
	)

	pflag.StringVar(&metricsAddr, "metrics-bind-address", ":8383", "The address the metric endpoint binds to.")
	pflag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	pflag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	pflag.BoolVar(&enableComplianceEventsStore, "enable-compliance-events-store", false,
		"Enable the compliance events store")
	pflag.BoolVar(&enableWebhooks, "enable-webhooks", true,
		"Enable the policy validating webhook")
	pflag.UintVar(
		&keyRotationDays,
		"encryption-key-rotation",
		30,
		"The number of days until the policy encryption key is rotated",
	)
	pflag.UintVar(
		&keyRotationMaxConcurrency,
		"key-rotation-max-concurrency",
		10,
		"The maximum number of concurrent reconciles for the policy-encryption-keys controller",
	)
	pflag.UintVar(
		&policyMetricsMaxConcurrency,
		"policy-metrics-max-concurrency",
		5,
		"The maximum number of concurrent reconciles for the policy-metrics controller",
	)
	pflag.UintVar(
		&policyStatusMaxConcurrency,
		"policy-status-max-concurrency",
		5,
		"The maximum number of concurrent reconciles for the policy-status controller",
	)
	pflag.UintVar(
		&rootPolicyMaxConcurrency,
		"root-policy-max-concurrency",
		2,
		"The maximum number of concurrent reconciles for the root-policy controller",
	)
	pflag.UintVar(
		&replPolicyMaxConcurrency,
		"replicated-policy-max-concurrency",
		10,
		"The maximum number of concurrent reconciles for the replicated-policy controller",
	)
	pflag.StringVar(
		&eventHistoryAPIHost, "event-history-api-host", "localhost",
		"The hostname that the event history API will listen on",
	)
	pflag.StringVar(
		&eventHistoryAPIPort, "event-history-api-port", "5480",
		"The port that the event history API will listen on",
	)

	pflag.Parse()

	ctrlZap, err := zflags.BuildForCtrl()
	if err != nil {
		panic(fmt.Sprintf("Failed to build zap logger for controller: %v", err))
	}

	ctrl.SetLogger(zapr.NewLogger(ctrlZap))

	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)

	err = zaputil.SyncWithGlogFlags(klogFlags)
	if err != nil {
		log.Error(err, "Failed to synchronize klog and glog flags, continuing with what succeeded")
	}

	klogZap, err := zaputil.BuildForKlog(zflags.GetConfig(), klogFlags)
	if err != nil {
		log.Error(err, "Failed to build zap logger for klog, those logs will not go through zap")
	} else {
		klog.SetLogger(zapr.NewLogger(klogZap).WithName("klog"))
	}

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

	// Set default manager options
	options := ctrl.Options{
		Namespace:                  namespace,
		Scheme:                     scheme,
		MetricsBindAddress:         metricsAddr,
		HealthProbeBindAddress:     probeAddr,
		LeaderElection:             enableLeaderElection,
		LeaderElectionID:           "policy-propagator.open-cluster-management.io",
		LeaderElectionResourceLock: "leases",
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				// Set a field selector so that a watch on secrets will be limited to just the secret with
				// the policy template encryption key.
				&corev1.Secret{}: {
					Field: fields.SelectorFromSet(fields.Set{"metadata.name": propagatorctrl.EncryptionKeySecret}),
				},
				&clusterv1.ManagedCluster{}: {
					Transform: func(obj interface{}) (interface{}, error) {
						cluster := obj.(*clusterv1.ManagedCluster)
						// All that ManagedCluster objects are used for is to check their existence to see if a
						// namespace is a cluster namespace.
						guttedCluster := &clusterv1.ManagedCluster{}
						guttedCluster.SetName(cluster.Name)

						return guttedCluster, nil
					},
				},
				&policyv1.Policy{}: {
					Transform: func(obj interface{}) (interface{}, error) {
						policy := obj.(*policyv1.Policy)
						// Remove unused large fields
						delete(policy.Annotations, "kubectl.kubernetes.io/last-applied-configuration")
						policy.ManagedFields = nil

						return policy, nil
					},
				},
			},
		},
	}

	if strings.Contains(namespace, ",") {
		options.Cache.Namespaces = strings.Split(namespace, ",")
	}

	mgr, err := ctrl.NewManager(cfg, options)
	if err != nil {
		log.Error(err, "Unable to start manager")
		os.Exit(1)
	}

	log.Info("Registering components")

	controllerCtx := ctrl.SetupSignalHandler()

	// This is used to trigger reconciles when a related policy set changes due to a dependency on a policy set.
	dynamicWatcherReconciler, dynamicWatcherSource := k8sdepwatches.NewControllerRuntimeSource()

	dynamicWatcher, err := k8sdepwatches.New(cfg, dynamicWatcherReconciler, nil)
	if err != nil {
		log.Error(err, "Unable to create the dynamic watcher", "controller", propagatorctrl.ControllerName)
		os.Exit(1)
	}

	go func() {
		err := dynamicWatcher.Start(controllerCtx)
		if err != nil {
			log.Error(err, "Unable to start the dynamic watcher", "controller", propagatorctrl.ControllerName)
			os.Exit(1)
		}
	}()

	policiesLock := &sync.Map{}
	replicatedResourceVersions := &sync.Map{}

	bufferSize := 1024

	replicatedPolicyUpdates := make(chan event.GenericEvent, bufferSize)
	replicatedUpdatesSource := &source.Channel{
		Source:         replicatedPolicyUpdates,
		DestBufferSize: bufferSize,
	}

	propagator := propagatorctrl.Propagator{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		Recorder:                mgr.GetEventRecorderFor(propagatorctrl.ControllerName),
		RootPolicyLocks:         policiesLock,
		ReplicatedPolicyUpdates: replicatedPolicyUpdates,
	}

	if err = (&propagatorctrl.RootPolicyReconciler{
		Propagator: propagator,
	}).SetupWithManager(mgr, rootPolicyMaxConcurrency); err != nil {
		log.Error(err, "Unable to create the controller", "controller", "root-policy-spec")
		os.Exit(1)
	}

	templateResolver, templatesSource, err := templates.NewResolverWithCaching(
		controllerCtx,
		cfg,
		templates.Config{
			AdditionalIndentation: 8,
			DisabledFunctions:     []string{},
			StartDelim:            propagatorctrl.TemplateStartDelim,
			StopDelim:             propagatorctrl.TemplateStopDelim,
		},
	)
	if err != nil {
		log.Error(err, "Unable to setup the template resolver the controller", "controller", "replicated-policy")
		os.Exit(1)
	}

	if err = (&propagatorctrl.ReplicatedPolicyReconciler{
		Propagator:       propagator,
		ResourceVersions: replicatedResourceVersions,
		DynamicWatcher:   dynamicWatcher,
		TemplateResolver: templateResolver,
	}).SetupWithManager(
		mgr, replPolicyMaxConcurrency, dynamicWatcherSource, replicatedUpdatesSource, templatesSource,
	); err != nil {
		log.Error(err, "Unable to create the controller", "controller", "replicated-policy")
		os.Exit(1)
	}

	if reportMetrics() {
		if err = (&metricsctrl.MetricReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		}).SetupWithManager(mgr, policyMetricsMaxConcurrency); err != nil {
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
		Client:          mgr.GetClient(),
		KeyRotationDays: keyRotationDays,
		Scheme:          mgr.GetScheme(),
	}).SetupWithManager(mgr, keyRotationMaxConcurrency); err != nil {
		log.Error(err, "Unable to create controller", "controller", encryptionkeysctrl.ControllerName)
		os.Exit(1)
	}

	if err = (&rootpolicystatusctrl.RootPolicyStatusReconciler{
		Client:          mgr.GetClient(),
		RootPolicyLocks: policiesLock,
		Scheme:          mgr.GetScheme(),
	}).SetupWithManager(mgr, policyStatusMaxConcurrency); err != nil {
		log.Error(err, "Unable to create controller", "controller", rootpolicystatusctrl.ControllerName)
		os.Exit(1)
	}

	if enableWebhooks {
		if err = (&policyv1.Policy{}).SetupWebhookWithManager(mgr); err != nil {
			log.Error(err, "unable to create webhook", "webhook", "Policy")
			os.Exit(1)
		}
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

	log.Info("Waiting for the dynamic watcher to start")
	// This is important to avoid adding watches before the dynamic watcher is ready
	<-dynamicWatcher.Started()

	log.Info("Starting manager")

	historyAPIAddr := net.JoinHostPort(eventHistoryAPIHost, eventHistoryAPIPort)

	if enableComplianceEventsStore {
		err = complianceeventsapi.StartManager(controllerCtx, cfg, enableLeaderElection, historyAPIAddr)
		if err != nil {
			log.Error(err, "Ignoring since this is technical preview")
		}
	}

	if err := mgr.Start(controllerCtx); err != nil {
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
