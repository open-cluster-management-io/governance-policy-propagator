// Copyright (c) 2020 Red Hat, Inc.
package apis

import (
	appsv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1alpha1 "k8s.io/cluster-registry/pkg/apis/clusterregistry/v1alpha1"
)

// AddToSchemes may be used to add all resources defined in the project to a Scheme
var AddToSchemes runtime.SchemeBuilder

// AddToScheme adds all Resources to the Scheme
func AddToScheme(s *runtime.Scheme) error {
	// add cluster scheme
	if err := clusterv1alpha1.AddToScheme(s); err != nil {
		return err
	}
	if err := appsv1.SchemeBuilder.AddToScheme(s); err != nil {
		return err
	}

	return AddToSchemes.AddToScheme(s)
}
