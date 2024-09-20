package propagator

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	templates "github.com/stolostron/go-template-utils/v6/pkg/templates"
	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

var (
	// A special NamespacedName to indicate the default service account is used
	defaultSANamespacedName types.NamespacedName = types.NamespacedName{Name: "<default>"}
	// A label selector to select policies that have a label indicating the parent/root policy.
	replicatedPlcLabelSelector labels.Selector
)

func init() {
	replicatedReq, err := labels.NewRequirement(common.RootPolicyLabel, selection.Exists, nil)
	if err != nil {
		panic(err)
	}

	replicatedPlcLabelSelector = labels.NewSelector().Add(*replicatedReq)
}

type TokenRefreshConfig struct {
	// The token lifetime in seconds.
	ExpirationSeconds int64
	// The minimum refresh minutes before expiration. This must be <= MaxRefreshMins.
	MinRefreshMins float64
	// The maximum refresh minutes before expiration. This must be >= MinRefreshMins.
	MaxRefreshMins float64
	// If a token refresh encountered an unrecoverable error, then this is called.
	OnFailedRefresh func(error)
}

type templateResolverWithCancel struct {
	// cancel will stop the TemplateResolver and the automatic token refresh.
	cancel context.CancelFunc
	// canceled will be set if resolver failed to start or was stopped. The atomic.Bool type is used to avoid using
	// a write lock.
	canceled atomic.Bool
	// referenceCount is a counter for how many replicated policies rely on this template resolver. Once it is 0,
	// the template resolver will be cleaned up. A pointer is used since the reference count can be copied to a new
	// templateResolverWithCancel instance if the TemplateResolver instance was unexpectedly canceled and was
	// restarted.
	referenceCount *atomic.Uint32
	// The template resolver instance. If the global lock was acquired, this will be set as long as canceled is false.
	resolver *templates.TemplateResolver
}

// TemplateResolvers handles managing the template resolvers that policies leverage, including the default template
// resolver. This also handles garbage collection of unused template resolvers except for the default template resolver.
// This is based on a reference count, so it's important to call RemoveReplicatedPolicy when a replicated policy is
// deleted. GetResolver handles all other reference count updates. All methods are concurrency safe, however, write lock
// contention could occur if there are many replicated policies with spec.hubTemplateOptions.serviceAccountName
// references to service accounts that don't exist since errors encountered instantiating the template resolver are not
// cached so that it can be retried.
type TemplateResolvers struct {
	// The main context used for all child contexts. Canceling this context will cause TemplateResolvers to shutdown.
	controllerCtx context.Context //nolint:containedctx
	// The Kubernetes configuration used as the base for template resolver clients. No authentication information is
	// copied, only configuration identifying the server and TLS configuration is used.
	config *rest.Config
	// The controller-runtime client to leverage the cache of policy objects and to generate service account tokens.
	mgrClient client.Client
	// A wait group to wait for clean up once controllerCtx is canceled.
	wg *sync.WaitGroup
	// A channel to trigger replicated policy reconciles if an asynchronous error occurs such as refreshing the token
	// failing.
	replicatedPolicyUpdates chan event.GenericEvent
	// globalLock has a write lock if a template resolver is being instantiated or getting cleaned up. All other
	// operations use a read lock.
	globalLock sync.RWMutex
	// The default template resolver when a replicated policy does not specify a service account.
	defaultTemplateResolver *templates.TemplateResolver
	// A map of service accounts to templateResolverWithCancel. Access to this must leverage globalLock.
	TemplateResolvers map[types.NamespacedName]*templateResolverWithCancel
	// A mapping of replicated policies as ObjectIdentifiers to the template resolver service account. This keeps
	// track of state so that if a replicated policy calls GetResolver with a new service account, the watches on
	// the previous template resolver are cleaned up and the reference count is decremented. This is a sync.Map to
	// reduce write locks on globalLock and this should be efficient since sync.Map is optimized for reading.
	policyToServiceAccount sync.Map
	// The reconciler to use when instantiating template resolvers. This is tied to a controller-runtime source
	// returned in NewTemplateResolvers to trigger reconciles on replicated policies when referenced objects in
	// templates are updated.
	dynamicWatcherReconciler *k8sdepwatches.ControllerRuntimeSourceReconciler
}

// NewTemplateResolvers instantiates a TemplateResolvers instance and returns a controller-runtime source to trigger
// reconciles when an object referenced in a template updates. Note that GetResolver should not be called until
// the controller-runtime manager has started since mgrClient may be used.
func NewTemplateResolvers(
	ctx context.Context,
	kubeconfig *rest.Config,
	mgrClient client.Client,
	defaultTemplateResolver *templates.TemplateResolver,
	replicatedPolicyUpdates chan event.GenericEvent,
) (*TemplateResolvers, source.TypedSource[reconcile.Request]) {
	dynamicWatcherReconciler, dynamicWatcherSource := k8sdepwatches.NewControllerRuntimeSource()

	return &TemplateResolvers{
		controllerCtx:            ctx,
		config:                   kubeconfig,
		defaultTemplateResolver:  defaultTemplateResolver,
		dynamicWatcherReconciler: dynamicWatcherReconciler,
		mgrClient:                mgrClient,
		replicatedPolicyUpdates:  replicatedPolicyUpdates,
		TemplateResolvers:        map[types.NamespacedName]*templateResolverWithCancel{},
		wg:                       &sync.WaitGroup{},
	}, dynamicWatcherSource
}

// WaitForShutdown waits for the context passed to NewTemplateResolvers to complete and then for all goroutines
// started from TemplateResolvers to end.
func (t *TemplateResolvers) WaitForShutdown() {
	<-t.controllerCtx.Done()
	t.wg.Wait()
}

// initResolver will instantiate a DynamicWatcher with a token from the input service account and then instantiate a
// TemplateResolver with that DynamicWatcher. Tokens will be automatically refreshed.
func (t *TemplateResolvers) initResolver(
	ctx context.Context,
	serviceAccount types.NamespacedName,
) (*templateResolverWithCancel, error) {
	resolverCtx, resolverCtxCancel := context.WithCancel(ctx)

	// Refresh the token anywhere between 1h50m and 1h55m after issuance.
	tokenConfig := TokenRefreshConfig{
		ExpirationSeconds: 7200, // 2 hours,
		MinRefreshMins:    5,
		MaxRefreshMins:    10,
		OnFailedRefresh: func(originalErr error) {
			if err := t.onBackgroundError(ctx, serviceAccount); err != nil {
				log.Error(err, "Failed to handle the token refresh unexpectedly stopping", "originalErr", originalErr)
			}
		},
	}

	// Use BearerTokenFile since it allows the token to be renewed without restarting the template resolver.
	tokenFile, err := GetToken(resolverCtx, t.wg, t.mgrClient, serviceAccount, tokenConfig)
	if err != nil {
		// Make this error identifiable so that up in the call stack, a watch can be add to the service account
		// to trigger a reconcile when the service account becomes available.
		if k8serrors.IsNotFound(err) {
			err = errors.Join(ErrSAMissing, err)
		}

		resolverCtxCancel()

		return nil, err
	}

	saConfig := getUserKubeConfig(t.config, tokenFile)

	dynamicWatcher, err := k8sdepwatches.New(
		saConfig,
		t.dynamicWatcherReconciler,
		&k8sdepwatches.Options{
			DisableInitialReconcile: true,
			EnableCache:             true,
		},
	)
	if err != nil {
		resolverCtxCancel()

		return nil, fmt.Errorf(
			"failed to instantiate the template resolver for the service account %s: %w", serviceAccount, err,
		)
	}

	rv := templateResolverWithCancel{
		cancel:         resolverCtxCancel,
		referenceCount: &atomic.Uint32{},
	}

	t.wg.Add(1)

	go func() {
		err := dynamicWatcher.Start(resolverCtx)

		// Start is blocking so regardless of the reason it stopped, canceled must be set to true.
		rv.canceled.Store(true)

		if err != nil {
			log.Error(err, "The DynamicWatcher for the service account unexpectedly stopped")

			if err := t.onBackgroundError(ctx, serviceAccount); err != nil {
				log.Error(err, "Failed to handle the DynamicWatcher unexpectedly stopping")
			}
		}

		resolverCtxCancel()
		t.wg.Done()
	}()

	<-dynamicWatcher.Started()

	templateResolver, err := templates.NewResolverWithDynamicWatcher(dynamicWatcher, templates.Config{
		AdditionalIndentation: 8,
		DisabledFunctions:     []string{},
		SkipBatchManagement:   true,
		StartDelim:            TemplateStartDelim,
		StopDelim:             TemplateStopDelim,
	})
	if err != nil {
		log.Error(err, "Failed to instantiate a template resolver for the service account")

		resolverCtxCancel()

		return nil, err
	}

	rv.resolver = templateResolver

	return &rv, nil
}

// onBackgroundError is called when a goroutine encounters an unrecoverable error. This will clean up the template
// resolver and trigger reconciles on all replicated policies that leverage this template resolver.
func (t *TemplateResolvers) onBackgroundError(ctx context.Context, serviceAccount types.NamespacedName) error {
	replicatedPolicies := policiesv1.PolicyList{}

	t.globalLock.Lock()

	if t.TemplateResolvers[serviceAccount] != nil {
		t.TemplateResolvers[serviceAccount].cancel()
		t.TemplateResolvers[serviceAccount].canceled.Store(true)
		t.TemplateResolvers[serviceAccount].resolver = nil
	}

	t.globalLock.Unlock()

	// This is pulled from the controller-runtime cache which is why client-side filtering with filterFunc is ok.
	err := t.mgrClient.List(ctx, &replicatedPolicies, &client.ListOptions{LabelSelector: replicatedPlcLabelSelector})
	if err != nil {
		return err
	}

	for i := range replicatedPolicies.Items {
		replicatedPolicy := replicatedPolicies.Items[i]

		if replicatedPolicy.Spec.HubTemplateOptions == nil {
			continue
		}

		if replicatedPolicy.Spec.HubTemplateOptions.ServiceAccountName != serviceAccount.Name {
			continue
		}

		_, rootNS, err := common.ParseRootPolicyLabel(replicatedPolicy.Name)
		if err != nil {
			continue
		}

		if rootNS != serviceAccount.Namespace {
			continue
		}

		t.replicatedPolicyUpdates <- event.GenericEvent{Object: &replicatedPolicy}
	}

	return nil
}

// RemoveReplicatedPolicy will clean up watches on the current template resolver (service account used in the last call
// to GetResolver) and if this was the last replicated policy using this template resolver, the template resolver will
// be cleaned up.
func (t *TemplateResolvers) RemoveReplicatedPolicy(replicatedPolicy k8sdepwatches.ObjectIdentifier) error {
	loadedServiceAccount, loaded := t.policyToServiceAccount.Load(replicatedPolicy)
	if !loaded {
		return nil
	}

	defer t.policyToServiceAccount.Delete(replicatedPolicy)

	serviceAccount := loadedServiceAccount.(types.NamespacedName)

	if serviceAccount == defaultSANamespacedName {
		return t.defaultTemplateResolver.UncacheWatcher(replicatedPolicy)
	}

	t.globalLock.RLock()
	defer t.globalLock.RUnlock()

	wrappedResolver, ok := t.TemplateResolvers[serviceAccount]
	if !ok {
		return nil
	}

	// Decrement the reference count (https://pkg.go.dev/sync/atomic#AddUint32)
	wrappedResolver.referenceCount.Add(^uint32(0))

	if wrappedResolver.canceled.Load() {
		return nil
	}

	uncacheErr := wrappedResolver.resolver.UncacheWatcher(replicatedPolicy)

	if wrappedResolver.referenceCount.Load() != 0 {
		return uncacheErr
	}

	// Run this in a separate goroutine so the caller doesn't need to wait for the write lock to become available.
	// It's clean up code and doesn't impact the caller.
	go func() {
		t.globalLock.Lock()
		defer t.globalLock.Unlock()

		if wrappedResolver.referenceCount.Load() != 0 {
			// A reference was added while waiting for the write lock, so no need to clean up.
			return
		}

		if wrappedResolver.canceled.Load() {
			return
		}

		log.Info("Cleaning up the no longer used template resolver", "serviceAccount", serviceAccount)

		wrappedResolver.cancel()
		wrappedResolver.canceled.Store(true)
		delete(t.TemplateResolvers, serviceAccount)
	}()

	return uncacheErr
}

// GetResolver will get the template resolver based on the input service account. If defaultSANamespacedName is
// provided, the default template resolver is returned. All references are updated and if the service account changed
// for the replicated policy, the watches on the previous template resolver are cleaned up.
func (t *TemplateResolvers) GetResolver(
	replicatedPolicy k8sdepwatches.ObjectIdentifier, serviceAccount types.NamespacedName,
) (*templates.TemplateResolver, error) {
	log := log.WithValues("serviceAccount", serviceAccount)

	addReference := false

	prevSAUntyped, ok := t.policyToServiceAccount.Load(replicatedPolicy)
	if ok {
		prevSA := prevSAUntyped.(types.NamespacedName)

		if prevSA != serviceAccount {
			err := t.RemoveReplicatedPolicy(replicatedPolicy)
			if err != nil {
				log.Error(err, "Failed to clean up the previous template resolver watches", "serviceAccount", prevSA)
			}

			addReference = true
		}
	} else {
		addReference = true
	}

	if serviceAccount == defaultSANamespacedName {
		t.policyToServiceAccount.Store(replicatedPolicy, defaultSANamespacedName)

		return t.defaultTemplateResolver, nil
	}

	t.globalLock.RLock()

	wrappedResolver, ok := t.TemplateResolvers[serviceAccount]

	if !ok || wrappedResolver.canceled.Load() {
		// If this is the first replicated policy to request this template resolver either for the first time or
		// after it was unexpectedly closed, upgrade the read lock to a write lock.
		t.globalLock.RUnlock()

		t.globalLock.Lock()
		defer t.globalLock.Unlock()

		// After the write lock is acquired, check to see if another replicated policy won the race and instantiated
		// the template resolver already.
		wrappedResolver, ok = t.TemplateResolvers[serviceAccount]
		if !ok || wrappedResolver.canceled.Load() {
			// This replicated policy won the race so instantiate the resolver
			newWrappedResolver, err := t.initResolver(t.controllerCtx, serviceAccount)
			if err != nil {
				return nil, err
			}

			if wrappedResolver != nil {
				// Copy over the reference count in the case where the template resolver was unexpectedly closed since
				// those replicated policies still reference this service account's template resolver.
				newWrappedResolver.referenceCount = wrappedResolver.referenceCount
			}

			t.TemplateResolvers[serviceAccount] = newWrappedResolver
			wrappedResolver = newWrappedResolver
		}
	} else {
		// The template resolver already exists and is good to be used.
		defer t.globalLock.RUnlock()
	}

	if addReference {
		// The service account changed or this is the first reconcile for this replicated policy.
		wrappedResolver.referenceCount.Add(1)

		t.policyToServiceAccount.Store(replicatedPolicy, serviceAccount)
	}

	// Create an additional pointer so future assignments such as wrappedResolver.resolver = nil don't affect
	// the returned value. This is a trick to avoid the caller from holding the read lock until it's done with this
	// value.
	resolver := *wrappedResolver.resolver

	return &resolver, nil
}

// GetWatchCount returns the total number of watches from all template resolvers.
func (t *TemplateResolvers) GetWatchCount() uint {
	var watchCount uint

	t.globalLock.RLock()
	defer t.globalLock.RUnlock()

	for _, wrappedResolver := range t.TemplateResolvers {
		if wrappedResolver.resolver != nil && !wrappedResolver.canceled.Load() {
			watchCount += wrappedResolver.resolver.GetWatchCount()
		}
	}

	return watchCount + t.defaultTemplateResolver.GetWatchCount()
}

// GetToken will use the TokenRequest API to get a token for the service account and return a file path to where the
// token is stored. A new token will be requested and stored in the file before the token expires. If an unrecoverable
// error occurs during a token refresh, refreshConfig.OnFailedRefresh is called if it's defined.
func GetToken(
	ctx context.Context,
	wg *sync.WaitGroup,
	client client.Client,
	serviceAccount types.NamespacedName,
	refreshConfig TokenRefreshConfig,
) (string, error) {
	namespace := serviceAccount.Namespace
	saName := serviceAccount.Name
	tokenReq := &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			ExpirationSeconds: &refreshConfig.ExpirationSeconds,
		},
	}
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: saName}}

	err := client.SubResource("token").Create(ctx, sa, tokenReq)
	if err != nil {
		return "", err
	}

	tokenFile, err := os.CreateTemp("", fmt.Sprintf("token-%s.%s-*", namespace, saName))
	if err != nil {
		return "", err
	}

	tokenFilePath, err := filepath.Abs(tokenFile.Name())
	if err != nil {
		return "", err
	}

	log := log.WithValues("path", tokenFilePath)

	var writeErr error

	defer func() {
		if writeErr != nil {
			if removeErr := os.Remove(tokenFilePath); removeErr != nil {
				log.Error(removeErr, "Failed to clean up the service account token file")
			}
		}
	}()

	if _, writeErr = tokenFile.Write([]byte(tokenReq.Status.Token)); writeErr != nil {
		log.Error(err, "Failed to write the service account token file")

		return "", writeErr
	}

	if writeErr = tokenFile.Close(); writeErr != nil {
		log.Error(err, "Failed to close the service account token file")

		if removeErr := os.Remove(tokenFilePath); removeErr != nil {
			log.Error(removeErr, "Failed to clean up the service account token file")
		}

		return "", err
	}

	expirationTimestamp := tokenReq.Status.ExpirationTimestamp

	wg.Add(1)

	go func() {
		log := log.WithValues("serviceAccount", saName, "namespace", namespace)

		log.V(2).Info("Got a token", "expiration", expirationTimestamp.UTC().Format(time.RFC3339))

		defer func() {
			if err := os.Remove(tokenFilePath); err != nil {
				log.Error(err, "Failed to clean up the service account token file")
			}

			wg.Done()
		}()

		// The latest refresh of the token is MinRefreshMins minutes before expiration
		minRefreshBuffer := time.Duration(refreshConfig.MinRefreshMins * float64(time.Minute))
		// Get the acceptable range of minutes that can be variable for the jitter below
		maxJitterMins := (refreshConfig.MaxRefreshMins - refreshConfig.MinRefreshMins) * float64(time.Minute)

		for {
			// Add a jitter between 0 and maxJitterMins to make the token renewals variable
			jitter := time.Duration(rand.Float64() * maxJitterMins).Round(time.Second) // #nosec G404
			// Refresh the token between 5 and 10 minutes from now
			refreshTime := expirationTimestamp.Add(-minRefreshBuffer).Add(-jitter)

			log.V(2).Info("Waiting to refresh the token", "refreshTime", refreshTime)

			deadlineCtx, deadlineCtxCancel := context.WithDeadline(ctx, refreshTime)

			<-deadlineCtx.Done()

			deadlineErr := deadlineCtx.Err()

			// This really does nothing but this satisfies the linter that cancel is called.
			deadlineCtxCancel()

			if !errors.Is(deadlineErr, context.DeadlineExceeded) {
				return
			}

			log.V(1).Info("Refreshing the token")

			tokenReq := &authv1.TokenRequest{
				Spec: authv1.TokenRequestSpec{
					ExpirationSeconds: &refreshConfig.ExpirationSeconds,
				},
			}

			err := client.SubResource("token").Create(ctx, sa, tokenReq)
			if err != nil {
				log.Error(err, "Failed to renew the service account token. Stopping the template resolver.")

				if refreshConfig.OnFailedRefresh != nil {
					refreshConfig.OnFailedRefresh(err)
				}

				return
			}

			err = os.WriteFile(tokenFilePath, []byte(tokenReq.Status.Token), 0o600)
			if err != nil {
				log.Error(err, "The refreshed token couldn't be written to. Stopping the template resolver.")

				if refreshConfig.OnFailedRefresh != nil {
					refreshConfig.OnFailedRefresh(err)
				}

				return
			}

			expirationTimestamp = tokenReq.Status.ExpirationTimestamp
		}
	}()

	return tokenFilePath, nil
}

// getUserKubeConfig will copy the URL and TLS related configuration from `config` and set the input token file when
// generating a new configuration. No authentication configuration is copied over.
func getUserKubeConfig(config *rest.Config, tokenFile string) *rest.Config {
	userConfig := &rest.Config{
		Host:    config.Host,
		APIPath: config.APIPath,
		TLSClientConfig: rest.TLSClientConfig{
			CAFile:     config.TLSClientConfig.CAFile,
			CAData:     config.TLSClientConfig.CAData,
			ServerName: config.TLSClientConfig.ServerName,
			Insecure:   config.TLSClientConfig.Insecure,
		},
	}

	userConfig.BearerTokenFile = tokenFile

	return userConfig
}

// setTemplateError sets an annotation on the policy template (e.g. ConfigurationPolicy) with the template resolution
// error. Managed clusters will use this when creating a NonCompliant status.
func setTemplateError(policyT *policiesv1.PolicyTemplate, tplErr error) error {
	policyTObjectUnstructured := &unstructured.Unstructured{}

	err := json.Unmarshal(policyT.ObjectDefinition.Raw, policyTObjectUnstructured)
	if err != nil {
		return err
	}

	policyTAnnotations := policyTObjectUnstructured.GetAnnotations()
	if policyTAnnotations == nil {
		policyTAnnotations = make(map[string]string)
	}

	policyTAnnotations["policy.open-cluster-management.io/hub-templates-error"] = tplErr.Error()

	policyTObjectUnstructured.SetAnnotations(policyTAnnotations)

	updatedPolicyT, err := json.Marshal(policyTObjectUnstructured)
	if err != nil {
		return err
	}

	policyT.ObjectDefinition.Raw = updatedPolicyT

	return nil
}
