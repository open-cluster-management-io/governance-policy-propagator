package policymetrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	policyStatusGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ocm_policy_status",
			Help: "The compliance status of the named policy. 0 == Compliant. 1 == NonCompliant",
		},
		[]string{
			"type",              // "root" or "propagated"
			"name",              // The name of the root policy
			"policy_namespace",  // The namespace where the root policy is defined
			"cluster_namespace", // The namespace where the policy was propagated
		},
	)
)

func init() {
	metrics.Registry.MustRegister(
		policyStatusGauge,
	)
}
