// Copyright (c) 2026 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"context"
	"crypto/tls"
	"os"

	configv1 "github.com/openshift/api/config/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/rest"
	sdktls "open-cluster-management.io/sdk-go/pkg/tls"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// resolveEffectiveTLSConfig returns the TLSOpts function to apply to the metrics and webhook
// servers, whether it's worth registering the drift watcher, and the resolved TLS profile spec
// (used to seed that watcher), in order of precedence:
//  1. Explicit --tls-min-version/--tls-cipher-suites flags.
//  2. The OpenShift APIServer "cluster" resource's tlsSecurityProfile, when present.
//  3. Go's TLS defaults.
func resolveEffectiveTLSConfig(
	ctx context.Context, cfg *rest.Config, minVersion, cipherSuites string,
) (tlsOptsFunc func(*tls.Config), watchAPIServer bool, resolvedSpec configv1.TLSProfileSpec) {
	flagCfg, err := sdktls.ConfigFromFlags(minVersion, cipherSuites)
	if err != nil {
		log.Error(err, "Invalid --tls-min-version/--tls-cipher-suites; ignoring the flags")

		flagCfg = nil
	}

	if flagCfg != nil {
		log.Info("Effective TLS configuration determined from flags",
			"minVersion", sdktls.VersionToString(flagCfg.MinVersion),
			"cipherSuites", sdktls.CipherSuitesToString(flagCfg.CipherSuites))

		return sdktls.ConfigToFunc(flagCfg), false, configv1.TLSProfileSpec{}
	}

	spec, found, watchable := readAPIServerTLSProfileSpec(ctx, cfg)
	if !found {
		log.Info("Effective TLS configuration using Go defaults")

		return func(*tls.Config) {}, watchable, configv1.TLSProfileSpec{}
	}

	tlsConfigFunc, unsupportedCiphers := openshifttls.NewTLSConfigFromProfile(spec)
	if len(unsupportedCiphers) > 0 {
		log.Info("Some cipher suites in the APIServer TLS profile are not implemented by Go's "+
			"crypto/tls package and will be ignored", "unsupportedCiphers", unsupportedCiphers)
	}

	log.Info("Effective TLS configuration determined from OpenShift APIServer cluster resource",
		"minVersion", string(spec.MinTLSVersion), "ciphers", spec.Ciphers, "groups", spec.Groups)

	return tlsConfigFunc, watchable, spec
}

// readAPIServerTLSProfileSpec builds a client for the given rest.Config and fetches the
// OpenShift APIServer's resolved TLS profile spec. Errors here are just logged: failures should
// be handled by using defaults.
func readAPIServerTLSProfileSpec(
	ctx context.Context, cfg *rest.Config,
) (spec configv1.TLSProfileSpec, found, watchable bool) {
	apiServerClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "Failed to create a client for the OpenShift APIServer resource")

		return configv1.TLSProfileSpec{}, false, false
	}

	return fetchAPIServerTLSProfileSpec(ctx, apiServerClient)
}

// fetchAPIServerTLSProfileSpec returns the resolved profile spec when found. watchable is false
// only when the CRD/resource definitively doesn't exist; it stays true on other errors (e.g. RBAC,
// connectivity) since those might be transient and the drift watcher can recover once they clear.
func fetchAPIServerTLSProfileSpec(
	ctx context.Context, apiServerClient client.Client,
) (spec configv1.TLSProfileSpec, found, watchable bool) {
	spec, err := openshifttls.FetchAPIServerTLSProfile(ctx, apiServerClient)
	switch {
	case err == nil:
		return spec, true, true
	case apierrors.IsNotFound(err):
		log.Info("OpenShift APIServer cluster resource not found")

		return configv1.TLSProfileSpec{}, false, false
	case meta.IsNoMatchError(err):
		log.Info("OpenShift APIServer CRD not installed")

		return configv1.TLSProfileSpec{}, false, false
	default:
		log.Error(err, "Failed to read the OpenShift APIServer cluster resource; "+
			"will keep watching it in case this was transient")

		return configv1.TLSProfileSpec{}, false, true
	}
}

// setupAPIServerTLSWatcher registers a controller that restarts the process when the APIServer
// TLS security profile changes. This MUST only be invoked when the cluster might have the
// resource (i.e. not definitively CRD-less) and no overriding flags are set, to function properly.
func setupAPIServerTLSWatcher(mgr ctrl.Manager, initialSpec configv1.TLSProfileSpec) error {
	watcher := &openshifttls.SecurityProfileWatcher{
		Client:                mgr.GetClient(),
		InitialTLSProfileSpec: initialSpec,
		OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
			log.Info("The OpenShift APIServer TLS security profile changed; " +
				"exiting to apply the new settings")

			os.Exit(0)
		},
	}

	return watcher.SetupWithManager(mgr)
}
