// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test CRD validation", func() {
	basicPolicy := func() *unstructured.Unstructured {
		return utils.ParseYaml("../resources/case15_dep_crd_validation/basic-policy.yaml")
	}

	addDependency := func(pol *unstructured.Unstructured, name, ns, kind string) *unstructured.Unstructured {
		deps := []interface{}{map[string]interface{}{
			"apiVersion": "policy.open-cluster-management.io/v1",
			"kind":       kind,
			"name":       name,
			"namespace":  ns,
			"compliance": "Compliant",
		}}

		err := unstructured.SetNestedSlice(pol.Object, deps, "spec", "dependencies")
		Expect(err).ToNot(HaveOccurred())

		return pol
	}

	addExtraDependency := func(pol *unstructured.Unstructured, name, ns, kind string) *unstructured.Unstructured {
		templates, found, err := unstructured.NestedSlice(pol.Object, "spec", "policy-templates")
		Expect(found).To(BeTrue())
		Expect(err).ToNot(HaveOccurred())

		tmpl0 := templates[0].(map[string]interface{})
		tmpl0["extraDependencies"] = []interface{}{map[string]interface{}{
			"apiVersion": "policy.open-cluster-management.io/v1",
			"kind":       kind,
			"name":       name,
			"namespace":  ns,
			"compliance": "Compliant",
		}}

		err = unstructured.SetNestedSlice(pol.Object, templates, "spec", "policy-templates")
		Expect(err).ToNot(HaveOccurred())

		return pol
	}

	policyClient := func() dynamic.ResourceInterface {
		return clientHubDynamic.Resource(gvrPolicy).Namespace("default")
	}

	AfterEach(func() {
		By("Removing the policy")
		// ignore error, because invalid policies will not have been created
		_ = policyClient().Delete(context.TODO(), "basic", v1.DeleteOptions{})
	})

	Describe("Test dependency namespace validation", func() {
		tests := map[string]struct {
			validWithNamespace    bool
			validWithoutNamespace bool
		}{
			"ConfigurationPolicy": {false, true},
			"CertificatePolicy":   {false, true},
			"IamPolicy":           {false, true},
			"Policy":              {true, true},
			"PolicySet":           {true, true},
			"OtherType":           {true, true},
		}

		for kind, tc := range tests {
			kind := kind
			tc := tc

			It("checks creating a policy with a "+kind+" dependency with a namespace", func() {
				pol := addDependency(basicPolicy(), "foo", "default", kind)
				_, err := policyClient().Create(context.TODO(), pol, v1.CreateOptions{})
				Expect(err == nil).To(Equal(tc.validWithNamespace))
			})
			It("checks creating a policy with a "+kind+" dependency without a namespace", func() {
				pol := addDependency(basicPolicy(), "foo", "", kind)
				_, err := policyClient().Create(context.TODO(), pol, v1.CreateOptions{})
				Expect(err == nil).To(Equal(tc.validWithoutNamespace))
			})
		}
	})

	Describe("Test extraDependency namespace validation", func() {
		tests := map[string]struct {
			validWithNamespace    bool
			validWithoutNamespace bool
		}{
			"ConfigurationPolicy": {false, true},
			"CertificatePolicy":   {false, true},
			"IamPolicy":           {false, true},
			"Policy":              {true, true},
			"PolicySet":           {true, true},
			"OtherType":           {true, true},
		}

		for kind, tc := range tests {
			kind := kind
			tc := tc

			It("checks creating a policy with a "+kind+" extraDependency with a namespace", func() {
				pol := addExtraDependency(basicPolicy(), "foo", "default", kind)
				_, err := policyClient().Create(context.TODO(), pol, v1.CreateOptions{})
				Expect(err == nil).To(Equal(tc.validWithNamespace))
			})
			It("checks creating a policy with a "+kind+" extraDependency without a namespace", func() {
				pol := addExtraDependency(basicPolicy(), "foo", "", kind)
				_, err := policyClient().Create(context.TODO(), pol, v1.CreateOptions{})
				Expect(err == nil).To(Equal(tc.validWithoutNamespace))
			})
		}
	})
})
