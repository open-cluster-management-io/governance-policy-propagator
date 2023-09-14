// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

func TestInitializeConcurrencyPerPolicyEnvName(t *testing.T) {
	tests := []struct {
		envVarValue string
		expected    int
	}{
		{"", concurrencyPerPolicyDefault},
		{fmt.Sprint(concurrencyPerPolicyDefault + 2), concurrencyPerPolicyDefault + 2},
		{"0", concurrencyPerPolicyDefault},
		{"-3", concurrencyPerPolicyDefault},
	}

	for _, test := range tests {
		t.Run(
			fmt.Sprintf(`%s="%s"`, concurrencyPerPolicyEnvName, test.envVarValue),
			func(t *testing.T) {
				defer func() {
					// Reset to the default values
					concurrencyPerPolicy = 0

					err := os.Unsetenv(concurrencyPerPolicyEnvName)
					if err != nil {
						t.Fatalf("failed to unset the environment variable: %v", err)
					}
				}()

				t.Setenv(concurrencyPerPolicyEnvName, test.envVarValue)

				var k8sInterface kubernetes.Interface
				Initialize(&rest.Config{}, &k8sInterface)

				if concurrencyPerPolicy != test.expected {
					t.Fatalf("Expected concurrencyPerPolicy=%d, got %d", test.expected, concurrencyPerPolicy)
				}
			},
		)
	}
}

// A mock implementation of the PolicyReconciler for the handleDecisionWrapper function.
type MockPolicyReconciler struct {
	Err error
}

func (r MockPolicyReconciler) handleDecision(
	_ *policiesv1.Policy, _ clusterDecision,
) (
	map[k8sdepwatches.ObjectIdentifier]bool, error,
) {
	return map[k8sdepwatches.ObjectIdentifier]bool{}, r.Err
}

func TestHandleDecisionWrapper(t *testing.T) {
	tests := []struct {
		Error         error
		ExpectedError bool
	}{
		{nil, false},
		{errors.New("some error"), true},
	}

	for _, test := range tests {
		// Simulate three placement decisions for the policy.
		clusterDecisions := []clusterDecision{
			{
				Cluster:         appsv1.PlacementDecision{ClusterName: "cluster1", ClusterNamespace: "cluster1"},
				PolicyOverrides: policiesv1.BindingOverrides{},
			},
			{
				Cluster:         appsv1.PlacementDecision{ClusterName: "cluster2", ClusterNamespace: "cluster2"},
				PolicyOverrides: policiesv1.BindingOverrides{},
			},
			{
				Cluster:         appsv1.PlacementDecision{ClusterName: "cluster3", ClusterNamespace: "cluster3"},
				PolicyOverrides: policiesv1.BindingOverrides{},
			},
		}
		policy := policiesv1.Policy{
			ObjectMeta: metav1.ObjectMeta{Name: "gambling-age", Namespace: "laws"},
		}

		// Load up the decisionsChan channel with all the decisions so that handleDecisionWrapper
		// will call handleDecision with each.
		decisionsChan := make(chan clusterDecision, len(clusterDecisions))

		for _, decision := range clusterDecisions {
			decisionsChan <- decision
		}

		resultsChan := make(chan decisionResult, len(clusterDecisions))

		// Instantiate the mock PolicyReconciler to pass to handleDecisionWrapper.
		reconciler := MockPolicyReconciler{Err: test.Error}

		go func() {
			start := time.Now()
			// Wait until handleDecisionWrapper has completed its work. Then close
			// the channel so that handleDecisionWrapper returns. This times out
			// after five seconds.
			for len(resultsChan) != len(clusterDecisions) {
				if time.Since(start) > (time.Second * 5) {
					close(decisionsChan)
				}
			}
			close(decisionsChan)
		}()

		handleDecisionWrapper(reconciler, &policy, decisionsChan, resultsChan)

		// Expect a 1x1 mapping of results to decisions.
		if len(resultsChan) != len(clusterDecisions) {
			t.Fatalf(
				"Expected the results channel length of %d, got %d", len(clusterDecisions), len(resultsChan),
			)
		}

		// Ensure all the results from the channel are as expected.
		for i := 0; i < len(clusterDecisions); i++ {
			result := <-resultsChan
			if test.ExpectedError {
				if result.Err == nil {
					t.Fatal("Expected an error but didn't get one")
				} else if result.Err != test.Error { //nolint:errorlint
					t.Fatalf("Expected the error %v but got: %v", test.Error, result.Err)
				}
			} else if result.Err != nil {
				t.Fatalf("Didn't expect but got: %v", result.Err)
			}

			expectedIdentifier := appsv1.PlacementDecision{
				ClusterName:      fmt.Sprintf("cluster%d", i+1),
				ClusterNamespace: fmt.Sprintf("cluster%d", i+1),
			}
			if result.Identifier != expectedIdentifier {
				t.Fatalf("Expected the identifier %s, got %s", result.Identifier, expectedIdentifier)
			}
		}
		close(resultsChan)
	}
}

func (r MockPolicyReconciler) deletePolicy(
	_ *policiesv1.Policy,
) error {
	return r.Err
}

func TestPlcDeletionWrapper(t *testing.T) {
	tests := []struct {
		Error         error
		ExpectedError bool
	}{
		{nil, false},
		{errors.New("some error"), true},
	}

	for _, test := range tests {
		// Simulate three replicated policies
		policies := []policiesv1.Policy{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "laws.gambling-age", Namespace: "cluster1"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "laws.gambling-age", Namespace: "cluster2"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "laws.gambling-age", Namespace: "cluster3"},
			},
		}

		// Load up the plcChan channel with all the decisions so that plcDeletionWrapper
		// will call deletePolicy with each.
		plcChan := make(chan policiesv1.Policy, len(policies))

		for _, policy := range policies {
			plcChan <- policy
		}

		resultsChan := make(chan deletionResult, len(policies))

		// Instantiate the mock PolicyReconciler to pass to plcDeletionWrapper
		reconciler := MockPolicyReconciler{Err: test.Error}

		go func() {
			start := time.Now()
			// Wait until plcDeletionWrapper has completed its work. Then close
			// the channel so that plcDeletionWrapper returns. This times out
			// after five seconds.
			for len(resultsChan) != len(policies) {
				if time.Since(start) > (time.Second * 5) {
					close(plcChan)
				}
			}
			close(plcChan)
		}()

		plcDeletionWrapper(reconciler, plcChan, resultsChan)

		// Expect a 1x1 mapping of results to replicated policies.
		if len(resultsChan) != len(policies) {
			t.Fatalf(
				"Expected the results channel length of %d, got %d", len(policies), len(resultsChan),
			)
		}

		// Ensure all the results from the channel are as expected.
		for i := 0; i < len(policies); i++ {
			result := <-resultsChan
			if test.ExpectedError {
				if result.Err == nil {
					t.Fatal("Expected an error but didn't get one")
				} else if result.Err != test.Error { //nolint:errorlint
					t.Fatalf("Expected the error %v but got: %v", test.Error, result.Err)
				}
			} else if result.Err != nil {
				t.Fatalf("Didn't expect but got: %v", result.Err)
			}

			expectedIdentifier := fmt.Sprintf("cluster%d/laws.gambling-age", i+1)
			if result.Identifier != expectedIdentifier {
				t.Fatalf("Expected the identifier %s, got %s", result.Identifier, expectedIdentifier)
			}
		}
		close(resultsChan)
	}
}

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

	reconciler := &Propagator{
		Client: fake.NewClientBuilder().
			WithScheme(testscheme).
			WithObjects(&prInitial, &prSub, &prSub2, &prExtended).
			Build(),
	}

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

			assert.ElementsMatch(t, actualAllClusterDecisions, test.expectedClusterDecisions)
			assert.ElementsMatch(t, actualPlacements, test.expectedPlacements)
		})
	}
}
