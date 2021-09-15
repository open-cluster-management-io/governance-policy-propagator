// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package policymetrics

import (
	"context"
	"strings"

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/api/v1"
	"github.com/open-cluster-management/governance-policy-propagator/controllers/common"
	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const ControllerName string = "policy-metrics"

var log = logf.Log.WithName(ControllerName)

// SetupWithManager sets up the controller with the Manager.
func (r *MetricReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&policiesv1.Policy{}).
		Complete(r)
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &MetricReconciler{}

// MetricReconciler reconciles the metrics for the Policy
type MetricReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies/finalizers,verbs=update

// Reconcile reads the state of the cluster for the Policy object and ensures that the exported
// policy metrics are accurate, updating them as necessary.
func (r *MetricReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling metric for Policy...")

	// Need to know if the policy is a root policy to create the correct prometheus labels
	// Can't try to use a label on the policy, because the policy might have been deleted.
	clusterList := &clusterv1.ManagedClusterList{}
	err := r.List(context.TODO(), clusterList, &client.ListOptions{})
	if err != nil {
		reqLogger.Error(err, "Failed to list clusters, going to retry...")
		return reconcile.Result{}, err
	}

	var promLabels map[string]string
	if common.IsInClusterNamespace(request.Namespace, clusterList.Items) {
		// propagated policies should look like <namespace>.<name>
		// also note: k8s namespace names follow RFC 1123 (so no "." in it)
		splitName := strings.SplitN(request.Name, ".", 2)
		if len(splitName) < 2 {
			// Don't do any metrics if the policy is invalid.
			reqLogger.Info("Invalid policy in cluster namespace: missing root policy ns prefix")
			return reconcile.Result{}, nil
		}
		promLabels = prometheus.Labels{
			"type":              "propagated",
			"policy":            splitName[1],
			"policy_namespace":  splitName[0],
			"cluster_namespace": request.Namespace,
		}
	} else {
		promLabels = prometheus.Labels{
			"type":              "root",
			"policy":            request.Name,
			"policy_namespace":  request.Namespace,
			"cluster_namespace": "<null>", // this is basically a sentinel value
		}
	}

	pol := &policiesv1.Policy{}
	err = r.Get(context.TODO(), request.NamespacedName, pol)
	if err != nil {
		if errors.IsNotFound(err) {
			// Try to delete the gauge, but don't get hung up on errors. Log whether it was deleted.
			statusGaugeDeleted := policyStatusGauge.Delete(promLabels)
			reqLogger.Info("Policy not found - must have been deleted.",
				"status-gauge-deleted", statusGaugeDeleted)
			return reconcile.Result{}, nil
		}
		reqLogger.Error(err, "Failed to get Policy")
		return reconcile.Result{}, err
	}

	reqLogger.Info("Got active state", "pol.Spec.Disabled", pol.Spec.Disabled)
	if pol.Spec.Disabled {
		// The policy is no longer active, so delete its metric
		statusGaugeDeleted := policyStatusGauge.Delete(promLabels)
		reqLogger.Info("Metric removed for non-active policy",
			"status-gauge-deleted", statusGaugeDeleted)
		return reconcile.Result{}, nil
	}

	reqLogger.Info("Got ComplianceState", "pol.Status.ComplianceState", pol.Status.ComplianceState)
	statusMetric, err := policyStatusGauge.GetMetricWith(promLabels)
	if err != nil {
		reqLogger.Error(err, "Failed to get status metric from GaugeVec")
		return reconcile.Result{}, err
	}
	if pol.Status.ComplianceState == policiesv1.Compliant {
		statusMetric.Set(0)
	} else if pol.Status.ComplianceState == policiesv1.NonCompliant {
		statusMetric.Set(1)
	}

	return reconcile.Result{}, nil
}
