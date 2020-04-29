// Package common contains common definitions and shared utilities for the different controllers
package common

import (
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	"k8s.io/cluster-registry/pkg/apis/clusterregistry/v1alpha1"
)

// IsInClusterNamespace check if policy is in cluster namespace
func IsInClusterNamespace(ns string, allClusters []v1alpha1.Cluster) bool {
	for _, cluster := range allClusters {
		if ns == cluster.GetNamespace() {
			return true
		}
	}
	return false
}

// LabelsForRootPolicy returns the labels for given policy
func LabelsForRootPolicy(plc *policiesv1.Policy) map[string]string {
	return map[string]string{"root-policy": FullNameForPolicy(plc)}
}

// FullNameForPolicy returns the fully qualified name for given policy
// full qualified name: ${namespace}.${name}
func FullNameForPolicy(plc *policiesv1.Policy) string {
	return plc.GetNamespace() + "." + plc.GetName()
}

// // GenerateLabelsForReplicatedPolicy generates labels needed for replicated policy
// func GenerateLabelsForReplicatedPolicy(plc *policiesv1.Policy) {
// 	labels := plc.GetLabels()
// 	if labels == nil {
// 		labels = map[string]string{}
// 	}
// 	labels["cluster-name"] = decision.ClusterName
// 	labels["cluster-namespace"] = decision.ClusterNamespace
// 	labels["root-policy"] = common.FullNameForPolicy(instance)
// }
