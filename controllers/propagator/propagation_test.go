// Copyright Contributors to the Open Cluster Management project

package propagator

import (
	"fmt"
	"os"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestInitializeAttempts(t *testing.T) {
	tests := []struct {
		envVarValue string
		expected    int
	}{
		{"", attemptsDefault},
		{fmt.Sprint(attemptsDefault + 2), attemptsDefault + 2},
		{"0", attemptsDefault},
		{"-3", attemptsDefault},
	}

	for _, test := range tests {
		t.Run(
			fmt.Sprintf(`%s="%s"`, attemptsEnvName, test.envVarValue),
			func(t *testing.T) {
				defer func() {
					// Reset to the default values
					attempts = 0
					err := os.Unsetenv(attemptsEnvName)
					if err != nil {
						t.Fatalf("failed to unset the environment variable: %v", err)
					}
				}()

				err := os.Setenv(attemptsEnvName, test.envVarValue)
				if err != nil {
					t.Fatalf("failed to set the environment variable: %v", err)
				}
				var k8sInterface kubernetes.Interface
				Initialize(&rest.Config{}, &k8sInterface)

				if attempts != test.expected {
					t.Fatalf("Expected attempts=%d, got %d", test.expected, attempts)
				}
			},
		)
	}
}

func TestInitializeRequeueErrorDelay(t *testing.T) {
	tests := []struct {
		envVarValue string
		expected    int
	}{
		{"", requeueErrorDelayDefault},
		{fmt.Sprint(requeueErrorDelayDefault + 2), requeueErrorDelayDefault + 2},
		{"0", requeueErrorDelayDefault},
		{"-3", requeueErrorDelayDefault},
	}

	for _, test := range tests {
		t.Run(
			fmt.Sprintf(`%s="%s"`, requeueErrorDelayEnvName, test.envVarValue),
			func(t *testing.T) {
				defer func() {
					// Reset to the default values
					requeueErrorDelay = 0
					err := os.Unsetenv(requeueErrorDelayEnvName)
					if err != nil {
						t.Fatalf("failed to unset the environment variable: %v", err)
					}
				}()

				err := os.Setenv(requeueErrorDelayEnvName, test.envVarValue)
				if err != nil {
					t.Fatalf("failed to set the environment variable: %v", err)
				}
				var k8sInterface kubernetes.Interface
				Initialize(&rest.Config{}, &k8sInterface)

				if requeueErrorDelay != test.expected {
					t.Fatalf("Expected requeueErrorDelay=%d, got %d", test.expected, attempts)
				}
			},
		)
	}
}
