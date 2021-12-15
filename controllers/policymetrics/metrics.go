// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package policymetrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var policyStatusGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "policy_governance_info",
		Help: "The compliance status of the named policy. 0 == Compliant. 1 == NonCompliant",
	},
	[]string{
		"type",              // "root" or "propagated"
		"policy",            // The name of the root policy
		"policy_namespace",  // The namespace where the root policy is defined
		"cluster_namespace", // The namespace where the policy was propagated
	},
)

func init() {
	metrics.Registry.MustRegister(
		policyStatusGauge,
	)
}
