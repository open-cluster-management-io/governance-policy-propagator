package propagator

import (
	"context"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

const (
	ReplicatedPolicyControllerName     = "replicated-policy"
	managedClusterPolicyControllerName = "managed-cluster-policy-refresh"
)

func (r *ReplicatedPolicyReconciler) SetupWithManager(
	mgr ctrl.Manager,
	maxConcurrentReconciles uint16,
	dependenciesSource source.Source,
	updateSrc source.Source,
	templateSrc source.Source,
	saTemplateSrc source.Source,
	plrsEnabled bool,
) error {
	ctrlBldr := ctrl.NewControllerManagedBy(mgr).
		Named(ReplicatedPolicyControllerName).
		For(
			&policiesv1.Policy{},
			builder.WithPredicates(replicatedPolicyPredicates(r.ResourceVersions))).
		WatchesRawSource(dependenciesSource).
		WatchesRawSource(updateSrc).
		WatchesRawSource(templateSrc).
		WatchesRawSource(saTemplateSrc).
		Watches(
			&clusterv1beta1.PlacementDecision{},
			HandlerForDecision(mgr.GetClient()),
		).
		Watches(
			&policiesv1.PlacementBinding{},
			HandlerForBinding(mgr.GetClient()),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: int(maxConcurrentReconciles)}).
		WithLogConstructor(func(req *reconcile.Request) logr.Logger {
			log = common.LogConstructor(ReplicatedPolicyControllerName, "Policy", req)

			return log
		})

	if plrsEnabled {
		ctrlBldr = ctrlBldr.Watches(&appsv1.PlacementRule{},
			HandlerForRule(mgr.GetClient()))
	}

	if err := ctrlBldr.Complete(r); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named(managedClusterPolicyControllerName).
		For(
			&clusterv1.ManagedCluster{},
			builder.WithPredicates(managedClusterLifecycleChanges()),
		).
		WithLogConstructor(func(req *reconcile.Request) logr.Logger {
			return common.LogConstructor(managedClusterPolicyControllerName, "ManagedCluster", req)
		}).
		Complete(&ManagedClusterPolicyReconciler{
			Client:                  mgr.GetClient(),
			ReplicatedPolicyUpdates: r.ReplicatedPolicyUpdates, // the channel for the `updateSrc` Source
		})
}

// ManagedClusterPolicyReconciler enqueues replicated Policies when their target ManagedCluster
// is created, deleted, or has its UID changed. This requires an entire Reconciler as opposed to
// another watch and mapper because it needs to retry if its List call fails, and mappers do not
// allow for errors to be returned to facilitate that.
type ManagedClusterPolicyReconciler struct {
	client.Client
	ReplicatedPolicyUpdates chan event.GenericEvent
}

var _ reconcile.Reconciler = &ManagedClusterPolicyReconciler{}

func (r *ManagedClusterPolicyReconciler) Reconcile(
	ctx context.Context, request ctrl.Request,
) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	policyList := &policiesv1.PolicyList{}
	if err := r.List(ctx, policyList, &client.ListOptions{Namespace: request.Name}); err != nil {
		log.Error(err, "Failed to list replicated policies for ManagedCluster lifecycle event",
			"managedCluster", request.Name)

		return ctrl.Result{}, err
	}

	for i := range policyList.Items {
		policy := &policyList.Items[i]
		if _, isReplicated := policy.GetLabels()[common.RootPolicyLabel]; !isReplicated {
			continue
		}

		simpleObj := &common.GuttedObject{
			TypeMeta: metav1.TypeMeta{
				Kind:       policiesv1.Kind,
				APIVersion: policiesv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      policy.GetName(),
				Namespace: policy.GetNamespace(),
			},
		}

		r.ReplicatedPolicyUpdates <- event.GenericEvent{Object: simpleObj}
	}

	return ctrl.Result{}, nil
}

// managedClusterLifecycleChanges triggers reconciliation when a ManagedCluster is created,
// deleted, or its UID changes. A ManagedCluster UID is normally immutable, but the update case
// also covers unusual API implementations that replace it in place.
func managedClusterLifecycleChanges() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		DeleteFunc: func(event.DeleteEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectOld.GetUID() != e.ObjectNew.GetUID()
		},
	}
}

// replicatedPolicyPredicates triggers reconciliation if the policy is a replicated policy, and is
// not a pure status update. It will use the ResourceVersions cache to try and skip events caused
// by the replicated policy reconciler itself.
func replicatedPolicyPredicates(resourceVersions *sync.Map) predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			_, isReplicated := e.Object.GetLabels()[common.RootPolicyLabel]
			if !isReplicated {
				return false
			}

			key := e.Object.GetNamespace() + "/" + e.Object.GetName()

			version, loaded := safeReadLoad(resourceVersions, key)
			defer version.RUnlock()

			return !loaded || version.resourceVersion != e.Object.GetResourceVersion()
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, isReplicated := e.Object.GetLabels()[common.RootPolicyLabel]
			if !isReplicated {
				return false
			}

			key := e.Object.GetNamespace() + "/" + e.Object.GetName()

			version, loaded := safeReadLoad(resourceVersions, key)
			defer version.RUnlock()

			return !loaded || !strings.HasPrefix(version.resourceVersion, "deleted")
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			_, newIsReplicated := e.ObjectNew.GetLabels()[common.RootPolicyLabel]
			_, oldIsReplicated := e.ObjectOld.GetLabels()[common.RootPolicyLabel]

			// if neither has the label, it is not a replicated policy
			if !oldIsReplicated && !newIsReplicated {
				return false
			}

			key := e.ObjectNew.GetNamespace() + "/" + e.ObjectNew.GetName()

			version, loaded := safeReadLoad(resourceVersions, key)
			defer version.RUnlock()

			if loaded && version.resourceVersion == e.ObjectNew.GetResourceVersion() {
				return false
			}

			// Ignore pure status updates since those are handled by a separate controller
			return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() ||
				!equality.Semantic.DeepEqual(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels()) ||
				!equality.Semantic.DeepEqual(e.ObjectOld.GetAnnotations(), e.ObjectNew.GetAnnotations())
		},
	}
}

type lockingRsrcVersion struct {
	*sync.RWMutex
	resourceVersion string
}

// safeReadLoad gets the lockingRsrcVersion from the sync.Map if it already exists, or it puts a
// new empty lockingRsrcVersion in the sync.Map if it was missing. In either case, this function
// obtains the RLock before returning - the caller *must* call RUnlock() themselves. The bool
// returned indicates if the key already existed in the sync.Map.
func safeReadLoad(resourceVersions *sync.Map, key string) (*lockingRsrcVersion, bool) {
	newRsrc := &lockingRsrcVersion{
		RWMutex:         &sync.RWMutex{},
		resourceVersion: "",
	}

	newRsrc.RLock()

	rsrc, loaded := resourceVersions.LoadOrStore(key, newRsrc)
	if loaded {
		newRsrc.RUnlock()

		version := rsrc.(*lockingRsrcVersion)
		version.RLock()

		return version, true
	}

	return newRsrc, false
}

// safeWriteLoad gets the lockingRsrcVersion from the sync.Map if it already exists, or it puts a
// new empty lockingRsrcVersion in the sync.Map if it was missing. In either case, this function
// obtains the Lock (a write lock) before returning - the caller *must* call Unlock() themselves.
func safeWriteLoad(resourceVersions *sync.Map, key string) *lockingRsrcVersion {
	newRsrc := &lockingRsrcVersion{
		RWMutex:         &sync.RWMutex{},
		resourceVersion: "",
	}

	newRsrc.Lock()

	rsrc, loaded := resourceVersions.LoadOrStore(key, newRsrc)
	if loaded {
		newRsrc.Unlock()

		version := rsrc.(*lockingRsrcVersion)
		version.Lock()

		return version
	}

	return newRsrc
}
