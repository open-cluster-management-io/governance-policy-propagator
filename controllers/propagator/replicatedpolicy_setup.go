package propagator

import (
	"sync"

	"k8s.io/apimachinery/pkg/api/equality"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

func (r *ReplicatedPolicyReconciler) SetupWithManager(
	mgr ctrl.Manager,
	maxConcurrentReconciles uint,
	dependenciesSource source.Source,
	updateSrc source.Source,
	templateSrc source.Source,
) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: int(maxConcurrentReconciles)}).
		Named("replicated-policy").
		For(
			&policiesv1.Policy{},
			builder.WithPredicates(replicatedPolicyPredicates(r.ResourceVersions))).
		WatchesRawSource(dependenciesSource, &handler.EnqueueRequestForObject{}).
		WatchesRawSource(updateSrc, &handler.EnqueueRequestForObject{}).
		WatchesRawSource(templateSrc, &handler.EnqueueRequestForObject{}).
		Watches(
			&clusterv1beta1.PlacementDecision{},
			HandlerForDecision(mgr.GetClient()),
		).
		Watches(
			&policiesv1.PlacementBinding{},
			HandlerForBinding(mgr.GetClient()),
		).
		Watches(
			&appsv1.PlacementRule{},
			HandlerForRule(mgr.GetClient()),
		).
		Complete(r)
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

			return !loaded || version.resourceVersion != "deleted"
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			_, newIsReplicated := e.ObjectNew.GetLabels()[common.RootPolicyLabel]
			_, oldIsReplicated := e.ObjectOld.GetLabels()[common.RootPolicyLabel]

			// if neither has the label, it is not a replicated policy
			if !(oldIsReplicated || newIsReplicated) {
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
