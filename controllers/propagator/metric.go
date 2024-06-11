// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

//+kubebuilder:rbac:groups=authorization.k8s.io,resources=tokenreviews,verbs=create

var (
	hubTemplateActiveWatchesMetric = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hub_templates_active_watches",
			Help: "The number of active watch API requests for Hub policy templates",
		},
	)
	propagationFailureMetric = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_propagation_failure_total",
			Help: "The number of failed policy propagation attempts per policy",
		},
		[]string{"name", "namespace"},
	)
	roothandlerMeasure = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "ocm_handle_root_policy_duration_seconds_bucket",
		Help: "Time the handleRootPolicy function takes to complete.",
	})
)

func init() {
	metrics.Registry.MustRegister(roothandlerMeasure)
	metrics.Registry.MustRegister(propagationFailureMetric)
	metrics.Registry.MustRegister(hubTemplateActiveWatchesMetric)
}
