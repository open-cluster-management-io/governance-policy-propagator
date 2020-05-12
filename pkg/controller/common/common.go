// Copyright (c) 2020 Red Hat, Inc.
package common

import (
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/cluster-registry/pkg/apis/clusterregistry/v1alpha1"
)

const APIGroup string = "policies.open-cluster-management.io"
const ClusterNameLabel string = APIGroup + "/cluster-name"
const ClusterNamespaceLabel string = APIGroup + "/cluster-namespace"
const RootPolicyLabel string = APIGroup + "/root-policy"

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
	return map[string]string{RootPolicyLabel: FullNameForPolicy(plc)}
}

// FullNameForPolicy returns the fully qualified name for given policy
// full qualified name: ${namespace}.${name}
func FullNameForPolicy(plc *policiesv1.Policy) string {
	return plc.GetNamespace() + "." + plc.GetName()
}

// CompareSpecAndAnnotation compares annotation and spec for given policies
// true if matches, false if doesn't match
func CompareSpecAndAnnotation(plc1 *policiesv1.Policy, plc2 *policiesv1.Policy) bool {
	annotationMatch := equality.Semantic.DeepEqual(plc1.GetAnnotations(), plc2.GetAnnotations())
	specMatch := equality.Semantic.DeepEqual(plc1.Spec, plc2.Spec)
	return annotationMatch && specMatch
}

// IsPbForPoicy compares group and kind with policy group and kind for given pb
func IsPbForPoicy(pb *policiesv1.PlacementBinding) bool {
	subjects := pb.Subjects
	found := false
	for _, subject := range subjects {
		if subject.Kind == policiesv1.Kind && subject.APIGroup == policiesv1.SchemeGroupVersion.Group {
			found = true
			break
		}
	}
	return found
}
