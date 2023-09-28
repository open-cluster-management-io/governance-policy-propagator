// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func fakeRootPolicy(name, namespace string) policiesv1.Policy {
	return policiesv1.Policy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: policiesv1.PolicySpec{},
	}
}

//nolint:unparam
func fakePlacementRule(
	name, namespace string, decisions []appsv1.PlacementDecision,
) appsv1.PlacementRule {
	return appsv1.PlacementRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: appsv1.PlacementRuleStatus{
			Decisions: decisions,
		},
	}
}

//nolint:unparam
func fakePlacementBinding(
	name, namespace string, placementRef policiesv1.PlacementSubject, subjects []policiesv1.Subject,
) policiesv1.PlacementBinding {
	return policiesv1.PlacementBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		PlacementRef: placementRef,
		Subjects:     subjects,
	}
}

func fakePlacementDecisions(num int) []appsv1.PlacementDecision {
	var decisions []appsv1.PlacementDecision

	for i := 0; i < num; i++ {
		decision := appsv1.PlacementDecision{
			ClusterName:      fmt.Sprintf("cluster%d", i+1),
			ClusterNamespace: fmt.Sprintf("cluster%d", i+1),
		}
		decisions = append(decisions, decision)
	}

	return decisions
}

func TestGetAllClusterDecisions(t *testing.T) {
	// Create a root policy
	testPolicy := fakeRootPolicy("test-policy", "default")

	// Fake placement decisions
	clusters := fakePlacementDecisions(6)

	// Create the placement rules
	prInitial := fakePlacementRule("pr-initial", "default", []appsv1.PlacementDecision{
		clusters[0], clusters[1], clusters[2], clusters[3],
	})

	prSub := fakePlacementRule("pr-sub", "default", []appsv1.PlacementDecision{
		clusters[0], clusters[1],
	})

	prSub2 := fakePlacementRule("pr-sub2", "default", []appsv1.PlacementDecision{
		clusters[4], clusters[5],
	})

	prExtended := fakePlacementRule("pr-extended", "default", []appsv1.PlacementDecision{
		clusters[0], clusters[1], clusters[4], clusters[5],
	})

	// Create the placement bindings
	placementRef := policiesv1.PlacementSubject{
		APIGroup: appsv1.SchemeGroupVersion.Group,
		Kind:     "PlacementRule",
		Name:     "",
	}
	subjects := []policiesv1.Subject{
		{
			APIGroup: policiesv1.SchemeGroupVersion.Group,
			Kind:     policiesv1.Kind,
			Name:     testPolicy.Name,
		},
	}

	pbInitial := fakePlacementBinding("pb-initial", "default", placementRef, subjects)
	pbInitial.PlacementRef.Name = prInitial.Name

	pbSub := fakePlacementBinding("pb-sub", "default", placementRef, subjects)
	pbSub.PlacementRef.Name = prSub.Name

	pbSub2 := fakePlacementBinding("pb-sub2", "default", placementRef, subjects)
	pbSub2.PlacementRef.Name = prSub2.Name

	pbExtended := fakePlacementBinding("pb-extended", "default", placementRef, subjects)
	pbExtended.PlacementRef.Name = prExtended.Name

	testscheme := scheme.Scheme
	if err := appsv1.AddToScheme(testscheme); err != nil {
		t.Fatalf("Unexpected error building scheme: %v", err)
	}

	reconciler := &RootPolicyReconciler{Propagator{
		Client: fake.NewClientBuilder().
			WithScheme(testscheme).
			WithObjects(&prInitial, &prSub, &prSub2, &prExtended).
			Build(),
	}}

	tests := map[string]struct {
		policy                   policiesv1.Policy
		pbList                   policiesv1.PlacementBindingList
		expectedPlacements       []*policiesv1.Placement
		expectedClusterDecisions []clusterDecision
		updatePlacementBindings  func(*policiesv1.PlacementBindingList)
	}{
		"With just subFilter": {
			policy: testPolicy,
			pbList: policiesv1.PlacementBindingList{
				Items: []policiesv1.PlacementBinding{pbInitial, pbSub},
			},
			updatePlacementBindings: func(pbList *policiesv1.PlacementBindingList) {
				for id, pb := range pbList.Items {
					if pb.GetName() == pbSub.Name {
						pbList.Items[id].SubFilter = policiesv1.Restricted
					}
				}
			},
			expectedPlacements: []*policiesv1.Placement{
				{PlacementBinding: pbInitial.Name, PlacementRule: prInitial.Name},
				{PlacementBinding: pbSub.Name, PlacementRule: prSub.Name},
			},
			expectedClusterDecisions: []clusterDecision{
				{Cluster: clusters[0], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[1], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[2], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[3], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
			},
		},
		"Enforcing with subFilter": {
			policy: testPolicy,
			pbList: policiesv1.PlacementBindingList{
				Items: []policiesv1.PlacementBinding{pbInitial, pbSub},
			},
			updatePlacementBindings: func(pbList *policiesv1.PlacementBindingList) {
				for id, pb := range pbList.Items {
					if pb.GetName() == pbSub.Name {
						pbList.Items[id].BindingOverrides.RemediationAction = "enforce"
						pbList.Items[id].SubFilter = policiesv1.Restricted
					}
				}
			},
			expectedPlacements: []*policiesv1.Placement{
				{PlacementBinding: pbInitial.Name, PlacementRule: prInitial.Name},
				{PlacementBinding: pbSub.Name, PlacementRule: prSub.Name},
			},
			expectedClusterDecisions: []clusterDecision{
				{Cluster: clusters[0], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
				{Cluster: clusters[1], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
				{Cluster: clusters[2], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[3], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
			},
		},
		"Enforcing without subFilter/extended clusters": {
			policy: testPolicy,
			pbList: policiesv1.PlacementBindingList{
				Items: []policiesv1.PlacementBinding{pbInitial, pbExtended},
			},
			updatePlacementBindings: func(pbList *policiesv1.PlacementBindingList) {
				for id, pb := range pbList.Items {
					if pb.GetName() == pbExtended.Name {
						pbList.Items[id].BindingOverrides.RemediationAction = "enforce"
					}
				}
			},
			expectedPlacements: []*policiesv1.Placement{
				{PlacementBinding: pbInitial.Name, PlacementRule: prInitial.Name},
				{PlacementBinding: pbExtended.Name, PlacementRule: prExtended.Name},
			},
			expectedClusterDecisions: []clusterDecision{
				{Cluster: clusters[0], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
				{Cluster: clusters[1], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
				{Cluster: clusters[2], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[3], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[4], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
				{Cluster: clusters[5], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
			},
		},
		"Enforcing with subFilter/extended clusters": {
			policy: testPolicy,
			pbList: policiesv1.PlacementBindingList{
				Items: []policiesv1.PlacementBinding{pbInitial, pbExtended},
			},
			updatePlacementBindings: func(pbList *policiesv1.PlacementBindingList) {
				for id, pb := range pbList.Items {
					if pb.GetName() == pbExtended.Name {
						pbList.Items[id].BindingOverrides.RemediationAction = "enforce"
						pbList.Items[id].SubFilter = policiesv1.Restricted
					}
				}
			},
			expectedPlacements: []*policiesv1.Placement{
				{PlacementBinding: pbInitial.Name, PlacementRule: prInitial.Name},
				{PlacementBinding: pbExtended.Name, PlacementRule: prExtended.Name},
			},
			expectedClusterDecisions: []clusterDecision{
				{Cluster: clusters[0], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
				{Cluster: clusters[1], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
				{Cluster: clusters[2], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[3], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
			},
		},
		"Enforcing with subFilter/no overlapped clusters": {
			policy: testPolicy,
			pbList: policiesv1.PlacementBindingList{
				Items: []policiesv1.PlacementBinding{pbInitial, pbSub2},
			},
			updatePlacementBindings: func(pbList *policiesv1.PlacementBindingList) {
				for id, pb := range pbList.Items {
					if pb.GetName() == pbSub2.Name {
						pbList.Items[id].BindingOverrides.RemediationAction = "enforce"
						pbList.Items[id].SubFilter = policiesv1.Restricted
					}
				}
			},
			expectedPlacements: []*policiesv1.Placement{
				{PlacementBinding: pbInitial.Name, PlacementRule: prInitial.Name},
			},
			expectedClusterDecisions: []clusterDecision{
				{Cluster: clusters[0], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[1], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[2], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[3], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
			},
		},
		"Enforcing with subFilter/multiple default placementbindings": {
			policy: testPolicy,
			pbList: policiesv1.PlacementBindingList{
				Items: []policiesv1.PlacementBinding{pbInitial, pbSub2, pbExtended},
			},
			updatePlacementBindings: func(pbList *policiesv1.PlacementBindingList) {
				for id, pb := range pbList.Items {
					if pb.GetName() == pbExtended.Name {
						pbList.Items[id].BindingOverrides.RemediationAction = "enforce"
						pbList.Items[id].SubFilter = policiesv1.Restricted
					}
				}
			},
			expectedPlacements: []*policiesv1.Placement{
				{PlacementBinding: pbInitial.Name, PlacementRule: prInitial.Name},
				{PlacementBinding: pbSub2.Name, PlacementRule: prSub2.Name},
				{PlacementBinding: pbExtended.Name, PlacementRule: prExtended.Name},
			},
			expectedClusterDecisions: []clusterDecision{
				{Cluster: clusters[0], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
				{Cluster: clusters[1], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
				{Cluster: clusters[2], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[3], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[4], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
				{Cluster: clusters[5], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
			},
		},
		"Multiple enforcement placementbindings with overlapped bound clusters": {
			policy: testPolicy,
			pbList: policiesv1.PlacementBindingList{
				Items: []policiesv1.PlacementBinding{pbInitial, pbSub, pbExtended},
			},
			updatePlacementBindings: func(pbList *policiesv1.PlacementBindingList) {
				for id, pb := range pbList.Items {
					if pb.GetName() != pbInitial.Name {
						pbList.Items[id].BindingOverrides.RemediationAction = "enforce"
						pbList.Items[id].SubFilter = policiesv1.Restricted
					}
				}
			},
			expectedPlacements: []*policiesv1.Placement{
				{PlacementBinding: pbInitial.Name, PlacementRule: prInitial.Name},
				{PlacementBinding: pbSub.Name, PlacementRule: prSub.Name},
				{PlacementBinding: pbExtended.Name, PlacementRule: prExtended.Name},
			},
			expectedClusterDecisions: []clusterDecision{
				{Cluster: clusters[0], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
				{Cluster: clusters[1], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: "enforce"}},
				{Cluster: clusters[2], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
				{Cluster: clusters[3], PolicyOverrides: policiesv1.BindingOverrides{RemediationAction: ""}},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if test.updatePlacementBindings != nil {
				test.updatePlacementBindings(&test.pbList)
			}

			actualAllClusterDecisions, actualPlacements, err := reconciler.getAllClusterDecisions(
				&test.policy, &test.pbList)
			if err != nil {
				t.Fatal("Got unexpected error", err.Error())
			}

			actualDecisions := make([]clusterDecision, 0, len(actualAllClusterDecisions))

			for decision, overrides := range actualAllClusterDecisions {
				actualDecisions = append(actualDecisions, clusterDecision{
					Cluster:         decision,
					PolicyOverrides: overrides,
				})
			}

			assert.ElementsMatch(t, actualDecisions, test.expectedClusterDecisions)
			assert.ElementsMatch(t, actualPlacements, test.expectedPlacements)
		})
	}
}
