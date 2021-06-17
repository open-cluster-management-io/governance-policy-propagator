package propagator

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	roothandlerMeasure = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "ocm_handle_root_policy_duration_seconds",
		Help: "Time the handleRootPolicy function takes to complete.",
	})
)

func init() {
	metrics.Registry.MustRegister(roothandlerMeasure)
}
