// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"

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
	_ *policiesv1.Policy, _ appsv1.PlacementDecision,
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
		decisions := []appsv1.PlacementDecision{
			{ClusterName: "cluster1", ClusterNamespace: "cluster1"},
			{ClusterName: "cluster2", ClusterNamespace: "cluster2"},
			{ClusterName: "cluster3", ClusterNamespace: "cluster3"},
		}
		policy := policiesv1.Policy{
			ObjectMeta: metav1.ObjectMeta{Name: "gambling-age", Namespace: "laws"},
		}

		// Load up the decisionsChan channel with all the decisions so that handleDecisionWrapper
		// will call handleDecision with each.
		decisionsChan := make(chan appsv1.PlacementDecision, len(decisions))

		for _, decision := range decisions {
			decisionsChan <- decision
		}

		resultsChan := make(chan decisionResult, len(decisions))

		// Instantiate the mock PolicyReconciler to pass to handleDecisionWrapper.
		reconciler := MockPolicyReconciler{Err: test.Error}

		go func() {
			start := time.Now()
			// Wait until handleDecisionWrapper has completed its work. Then close
			// the channel so that handleDecisionWrapper returns. This times out
			// after five seconds.
			for len(resultsChan) != len(decisions) {
				if time.Since(start) > (time.Second * 5) {
					close(decisionsChan)
				}
			}
			close(decisionsChan)
		}()

		handleDecisionWrapper(reconciler, &policy, decisionsChan, resultsChan)

		// Expect a 1x1 mapping of results to decisions.
		if len(resultsChan) != len(decisions) {
			t.Fatalf(
				"Expected the results channel length of %d, got %d", len(decisions), len(resultsChan),
			)
		}

		// Ensure all the results from the channel are as expected.
		for i := 0; i < len(decisions); i++ {
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
