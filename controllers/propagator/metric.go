// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	hubTemplateActiveWatchesMetric = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hub_templates_active_watches",
			Help: "The number of active watch API requests for Hub policy templates",
		},
	)
	roothandlerMeasure = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "ocm_handle_root_policy_duration_seconds_bucket",
		Help: "Time the handleRootPolicy function takes to complete.",
	})
)

func init() {
	metrics.Registry.MustRegister(roothandlerMeasure)
	metrics.Registry.MustRegister(hubTemplateActiveWatchesMetric)
}
