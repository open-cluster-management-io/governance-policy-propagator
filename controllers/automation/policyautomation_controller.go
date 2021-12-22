// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package automation

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policyv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

const ControllerName string = "policy-automation"

var log = ctrl.Log.WithName(ControllerName)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policyautomations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policyautomations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policyautomations/finalizers,verbs=update
//+kubebuilder:rbac:groups=tower.ansible.com,resources=ansiblejobs,verbs=get;list;watch;create;update;patch;delete;deletecollection

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyAutomationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		Watches(
			&source.Kind{Type: &policyv1.Policy{}},
			&common.EnqueueRequestsFromMapFunc{ToRequests: policyMapper(mgr.GetClient())},
			builder.WithPredicates(policyPredicateFuncs)).
		For(
			&policyv1beta1.PolicyAutomation{},
			builder.WithPredicates(policyAuomtationPredicateFuncs)).
		Complete(r)
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &PolicyAutomationReconciler{}

// PolicyAutomationReconciler reconciles a PolicyAutomation object
type PolicyAutomationReconciler struct {
	client.Client
	DynamicClient dynamic.Interface
	Scheme        *runtime.Scheme
	Recorder      record.EventRecorder
	counter       int
}

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *PolicyAutomationReconciler) Reconcile(
	ctx context.Context, request ctrl.Request,
) (ctrl.Result, error) {
	log := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	// Fetch the PolicyAutomation instance
	policyAutomation := &policyv1beta1.PolicyAutomation{}

	err := r.Get(ctx, request.NamespacedName, policyAutomation)
	if err != nil {
		if errors.IsNotFound(err) {
			log.V(2).Info("Automation was deleted. Nothing to do.")

			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	if policyAutomation.Spec.PolicyRef == "" {
		log.Info("No policyRef in PolicyAutomation. Will ignore it.")

		return reconcile.Result{}, nil
	}

	log = log.WithValues("policyRef", policyAutomation.Spec.PolicyRef)

	if policyAutomation.Annotations["policy.open-cluster-management.io/rerun"] == "true" {
		log.Info("Creating an Ansible job", "mode", "manual")

		err = common.CreateAnsibleJob(policyAutomation, r.DynamicClient, "manual", nil)
		if err != nil {
			log.Error(err, "Failed to create the Ansible job", "mode", "manual")

			return reconcile.Result{}, err
		}
		// manual run succeeded, remove annotation
		delete(policyAutomation.Annotations, "policy.open-cluster-management.io/rerun")

		err = r.Update(ctx, policyAutomation, &client.UpdateOptions{})
		if err != nil {
			log.Error(err, "Failed to remove the annotation `policy.open-cluster-management.io/rerun`")

			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	} else if policyAutomation.Spec.Mode == "disabled" {
		log.Info("Automation is disabled, doing nothing")

		return reconcile.Result{}, nil
	} else {
		policy := &policyv1.Policy{}

		err := r.Get(ctx, types.NamespacedName{
			Name:      policyAutomation.Spec.PolicyRef,
			Namespace: policyAutomation.GetNamespace(),
		}, policy)
		if err != nil {
			if errors.IsNotFound(err) {
				// policy is gone, need to delete automation
				log.Info("Policy specified in policyRef field not found, may have been deleted, doing nothing")

				return reconcile.Result{}, nil
			}

			// Error reading the object - requeue the request.
			log.Error(err, "Failed to retrieve the policy specified in the policyRef field")

			return reconcile.Result{}, err
		}

		if policy.Spec.Disabled {
			log.Info("The policy is disabled. Doing nothing.")

			return reconcile.Result{}, nil
		}

		if policyAutomation.Spec.Mode == "scan" {
			log := log.WithValues("mode", "scan")
			log.V(2).Info("Triggering scan mode")

			requeueAfter, err := time.ParseDuration(policyAutomation.Spec.RescanAfter)
			if err != nil {
				if policyAutomation.Spec.RescanAfter != "" {
					log.Error(err, "Invalid spec.rescanAfter value")
				}

				return reconcile.Result{RequeueAfter: requeueAfter}, err
			}

			targetList := common.FindNonCompliantClustersForPolicy(policy)
			if len(targetList) > 0 {
				log.Info("Creating An Ansible job", "targetList", targetList)

				err = common.CreateAnsibleJob(policyAutomation, r.DynamicClient, "scan", targetList)
				if err != nil {
					return reconcile.Result{RequeueAfter: requeueAfter}, err
				}
			} else {
				log.Info("All clusters are compliant. Doing nothing.")
			}

			// no violations found, doing nothing
			r.counter++
			log.V(2).Info(
				"RequeueAfter.", "RequeueAfter", requeueAfter.String(), "Counter", fmt.Sprintf("%d", r.counter),
			)

			return reconcile.Result{RequeueAfter: requeueAfter}, nil
		} else if policyAutomation.Spec.Mode == "once" {
			log := log.WithValues("mode", "once")
			targetList := common.FindNonCompliantClustersForPolicy(policy)
			if len(targetList) > 0 {
				log.Info("Creating an Ansible job", "targetList", targetList)

				err = common.CreateAnsibleJob(policyAutomation, r.DynamicClient, "once", targetList)
				if err != nil {
					log.Error(err, "Failed to create the Ansible job")

					return reconcile.Result{}, err
				}

				policyAutomation.Spec.Mode = "disabled"

				err = r.Update(ctx, policyAutomation, &client.UpdateOptions{})
				if err != nil {
					log.Error(err, "Failed to update the mode to disabled")

					return reconcile.Result{}, err
				}
			} else {
				log.Info("All clusters are compliant. Doing nothing.")
			}
		}
	}

	return ctrl.Result{}, nil
}
