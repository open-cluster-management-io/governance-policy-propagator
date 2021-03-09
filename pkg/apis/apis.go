// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package apis

import (
	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	appsv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AddToSchemes may be used to add all resources defined in the project to a Scheme
var AddToSchemes runtime.SchemeBuilder

// AddToScheme adds all Resources to the Scheme
func AddToScheme(s *runtime.Scheme) error {
	// add cluster scheme
	if err := clusterv1.AddToScheme(s); err != nil {
		return err
	}
	if err := appsv1.SchemeBuilder.AddToScheme(s); err != nil {
		return err
	}

	return AddToSchemes.AddToScheme(s)
}
