// Package common contains common definitions and shared utilities for the different controllers
package common

import "k8s.io/cluster-registry/pkg/apis/clusterregistry/v1alpha1"

// IsInClusterNamespace check if policy is in cluster namespace
func IsInClusterNamespace(ns string, allClusters []v1alpha1.Cluster) bool {
	for _, cluster := range allClusters {
		if ns == cluster.GetNamespace() {
			return true
		}
	}
	return false
}
