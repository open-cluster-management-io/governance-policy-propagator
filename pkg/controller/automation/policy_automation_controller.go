// Copyright Contributors to the Open Cluster Management project

package automation

import (
	"context"
	"fmt"
	"time"

	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
	policyv1beta1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1beta1"
	"github.com/open-cluster-management/governance-policy-propagator/pkg/controller/common"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName string = "policy-automation"

var log = logf.Log.WithName(controllerName)

// Add creates a new Policy Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	dyamicClient, err := dynamic.NewForConfig(mgr.GetConfig())
	if err != nil {
		panic(err)
	}
	return &ReconcilePolicy{client: mgr.GetClient(), scheme: mgr.GetScheme(), dyamicClient: dyamicClient,
		recorder: mgr.GetEventRecorderFor(controllerName)}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New(controllerName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Policy
	err = c.Watch(&source.Kind{Type: &policiesv1.Policy{}},
		&common.EnqueueRequestsFromMapFunc{ToRequests: &policyMapper{mgr.GetClient()}},
		policyPredicateFuncs)
	if err != nil {
		return err
	}

	// Watch for changes to resource PolicyAutomation
	err = c.Watch(&source.Kind{Type: &policyv1beta1.PolicyAutomation{}},
		&common.EnqueueRequestsFromMapFunc{ToRequests: &policyAutomationMapper{mgr.GetClient()}},
		configMapPredicateFuncs)
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcilePolicy{}

// ReconcilePolicy reconciles a Policy object
type ReconcilePolicy struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client       client.Client
	dyamicClient dynamic.Interface
	scheme       *runtime.Scheme
	recorder     record.EventRecorder
	counter      int
}

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePolicy) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	// Fetch the ConfigMap instance
	policyAutomation := &policyv1beta1.PolicyAutomation{}
	err := r.client.Get(context.TODO(), request.NamespacedName, policyAutomation)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Info("Automation was deleted, doing nothing...")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}
	reqLogger = log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name,
		"policyRef", policyAutomation.Spec.PolicyRef)

	reqLogger.Info("Handling automation...")
	if policyAutomation.Annotations["policy.open-cluster-management.io/rerun"] == "true" {
		reqLogger.Info("Triggering manual run...")
		err = common.CreateAnsibleJob(policyAutomation, r.dyamicClient, "manual", nil)
		if err != nil {
			reqLogger.Error(err, "Failed to create ansible job...")
			return reconcile.Result{}, err
		}
		// manual run suceeded, remove annotation
		delete(policyAutomation.Annotations, "policy.open-cluster-management.io/rerun")
		err = r.client.Update(context.TODO(), policyAutomation, &client.UpdateOptions{})
		if err != nil {
			reqLogger.Error(err, "Failed to remove annotation `policy.open-cluster-management.io/rerun`...")
			return reconcile.Result{}, err
		}
		reqLogger.Info("Manual run complete...")
		return reconcile.Result{}, nil
	} else if policyAutomation.Spec.Mode == "disabled" {
		reqLogger.Info("Automation is disabled, doing nothing...")
		return reconcile.Result{}, nil
	} else {
		policy := &policiesv1.Policy{}
		err := r.client.Get(context.TODO(), types.NamespacedName{
			Name:      policyAutomation.Spec.PolicyRef,
			Namespace: policyAutomation.GetNamespace(),
		}, policy)
		if err != nil {
			if errors.IsNotFound(err) {
				//policy is gone, need to delete automation
				reqLogger.Info("Policy specified in policyRef field not found, may have been deleted, doing nothing...")
				return reconcile.Result{}, nil
			}
			// Error reading the object - requeue the request.
			reqLogger.Error(err, "Failed to retrieve policy specified in policyRef field...")
			return reconcile.Result{}, err
		}
		if policy.Spec.Disabled {
			reqLogger.Info("Policy is disabled, doing nothing...")
			return reconcile.Result{}, nil
		}
		if policyAutomation.Spec.Mode == "scan" {
			reqLogger.Info("Triggering scan mode...")
			requeueAfter, err := time.ParseDuration(policyAutomation.Spec.RescanAfter)
			if err != nil {
				return reconcile.Result{RequeueAfter: requeueAfter}, err
			}
			targetList := common.FindNonCompliantClustersForPolicy(policy)
			if len(targetList) > 0 {
				reqLogger.Info("Creating ansible job with targetList", "targetList", targetList)
				err = common.CreateAnsibleJob(policyAutomation, r.dyamicClient, "scan", targetList)
				if err != nil {
					return reconcile.Result{RequeueAfter: requeueAfter}, err
				}
			} else {
				reqLogger.Info("No cluster is in noncompliant status, doing nothing...")
			}

			// no violations found, doing nothing
			r.counter++
			reqLogger.Info("RequeueAfter.", "RequeueAfter", requeueAfter.String(), "counter", fmt.Sprintf("%d", r.counter))
			return reconcile.Result{RequeueAfter: requeueAfter}, nil
		} else if policyAutomation.Spec.Mode == "once" {
			reqLogger.Info("Triggering once mode...")
			targetList := common.FindNonCompliantClustersForPolicy(policy)
			if len(targetList) > 0 {
				reqLogger.Info("Creating ansible job with targetList", "targetList", targetList)
				err = common.CreateAnsibleJob(policyAutomation, r.dyamicClient, "once", targetList)
				if err != nil {
					reqLogger.Error(err, "Failed to create ansible job...")
					return reconcile.Result{}, err
				}
				policyAutomation.Spec.Mode = "disabled"
				err = r.client.Update(context.TODO(), policyAutomation, &client.UpdateOptions{})
				if err != nil {
					reqLogger.Error(err, "Failed to update mode to disabled...")
					return reconcile.Result{}, err
				}
			} else {
				reqLogger.Info("No cluster is in noncompliant status, doing nothing...")
			}
		}
	}
	return reconcile.Result{}, nil
}
