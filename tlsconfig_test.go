// Copyright (c) 2026 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"context"
	"crypto/tls"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func newAPIServerTestScheme(t *testing.T) *k8sruntime.Scheme {
	t.Helper()

	s := k8sruntime.NewScheme()

	// Mirror main.go's init(): register only the two OpenShift resources this
	// controller uses, rather than `configv1.Install(s)`, which would pull in ~24 kinds.
	s.AddKnownTypes(configv1.GroupVersion, &configv1.APIServer{}, &configv1.APIServerList{})
	metav1.AddToGroupVersion(s, configv1.GroupVersion)

	return s
}

// applyToConfig runs a TLSOpts function against a fresh tls.Config and returns it, to make
// assertions on the result easier to read.
func applyToConfig(f func(*tls.Config)) *tls.Config {
	cfg := &tls.Config{} //nolint:gosec // test-only, never used to actually serve traffic

	f(cfg)

	return cfg
}

func TestFetchAPIServerTLSProfileSpecNotFound(t *testing.T) {
	RegisterFailHandler(Fail)

	c := fakeclient.NewClientBuilder().WithScheme(newAPIServerTestScheme(t)).Build()

	spec, found, watchable := fetchAPIServerTLSProfileSpec(t.Context(), c)

	Expect(found).To(BeFalse())
	Expect(watchable).To(BeFalse(), "no CRD/resource means there's nothing to watch")
	Expect(spec).To(Equal(configv1.TLSProfileSpec{}))
}

func TestFetchAPIServerTLSProfileSpecFound(t *testing.T) {
	RegisterFailHandler(Fail)

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: openshifttls.APIServerName},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
		},
	}

	c := fakeclient.NewClientBuilder().WithScheme(newAPIServerTestScheme(t)).WithObjects(apiServer).Build()

	spec, found, watchable := fetchAPIServerTLSProfileSpec(t.Context(), c)

	Expect(found).To(BeTrue())
	Expect(watchable).To(BeTrue())
	Expect(spec).To(Equal(*configv1.TLSProfiles[configv1.TLSProfileModernType]))
}

// TestFetchAPIServerTLSProfileSpecForbiddenStaysWatchable proves that an RBAC-denied Get is
// treated as possibly transient: found is false (fall back to defaults now), but watchable stays
// true so the drift watcher still gets registered. If the permission is later granted, the
// watcher's informer will sync, see the real profile differs from the zero-value spec it was
// seeded with, and trigger a restart to pick it up -- without any dedicated retry loop.
func TestFetchAPIServerTLSProfileSpecForbiddenStaysWatchable(t *testing.T) {
	RegisterFailHandler(Fail)

	c := fakeclient.NewClientBuilder().
		WithScheme(newAPIServerTestScheme(t)).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(
				_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption,
			) error {
				return apierrors.NewForbidden(configv1.Resource("apiservers"), openshifttls.APIServerName, nil)
			},
		}).
		Build()

	spec, found, watchable := fetchAPIServerTLSProfileSpec(t.Context(), c)

	Expect(found).To(BeFalse())
	Expect(watchable).To(BeTrue(), "an RBAC/connectivity error might be transient, so it's still worth watching")
	Expect(spec).To(Equal(configv1.TLSProfileSpec{}))
}

func TestResolveEffectiveTLSConfigFlagsTakePrecedence(t *testing.T) {
	RegisterFailHandler(Fail)

	// Flags must be honored without ever consulting the (nil, in this test) rest.Config/APIServer.
	tlsOptsFunc, watchAPIServer, spec := resolveEffectiveTLSConfig(t.Context(), nil, "VersionTLS13", "")

	Expect(watchAPIServer).To(BeFalse())
	Expect(spec).To(Equal(configv1.TLSProfileSpec{}))
	Expect(applyToConfig(tlsOptsFunc).MinVersion).To(Equal(uint16(tls.VersionTLS13)))
}

func TestResolveEffectiveTLSConfigInvalidFlagFallsBackToGoDefaults(t *testing.T) {
	RegisterFailHandler(Fail)

	tlsOptsFunc, watchAPIServer, spec := resolveEffectiveTLSConfig(t.Context(), nil, "not-a-real-version", "")

	Expect(watchAPIServer).To(BeFalse())
	Expect(spec).To(Equal(configv1.TLSProfileSpec{}))
	Expect(applyToConfig(tlsOptsFunc)).To(Equal(&tls.Config{})) //nolint:gosec // test-only
}

// TestReadAPIServerTLSProfileSpecWithoutOpenShiftCRD runs against a real API server (envtest) with
// no CRDs installed, proving the "not an OpenShift cluster" fallback works against real
// client-go/controller-runtime discovery behavior. A fake client can't exercise this: it resolves
// GVKs straight from its scheme instead of doing real API discovery, so it can only ever return
// NotFound, never the meta.IsNoMatchError that fetchAPIServerTLSProfileSpec also checks for.
func TestReadAPIServerTLSProfileSpecWithoutOpenShiftCRD(t *testing.T) {
	RegisterFailHandler(Fail)

	testEnv := &envtest.Environment{}

	cfg, err := testEnv.Start()
	Expect(err).ToNot(HaveOccurred())

	defer func() {
		Expect(testEnv.Stop()).To(Succeed())
	}()

	spec, found, watchable := readAPIServerTLSProfileSpec(t.Context(), cfg)

	Expect(found).To(BeFalse())
	Expect(watchable).To(BeFalse(), "no CRD installed means there's nothing to watch")
	Expect(spec).To(Equal(configv1.TLSProfileSpec{}))
}
