// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package utils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
)

// GeneratePlrStatus generate plr status with given clusters
func GeneratePlrStatus(clusters ...string) *appsv1.PlacementRuleStatus {
	plrDecision := []appsv1.PlacementDecision{}
	for _, cluster := range clusters {
		plrDecision = append(plrDecision, appsv1.PlacementDecision{
			ClusterName:      cluster,
			ClusterNamespace: cluster,
		})
	}

	return &appsv1.PlacementRuleStatus{Decisions: plrDecision}
}

// GeneratePldStatus generate pld status with given clusters
func GeneratePldStatus(
	_ string, _ string, clusters ...string,
) *clusterv1beta1.PlacementDecisionStatus {
	plrDecision := []clusterv1beta1.ClusterDecision{}
	for _, cluster := range clusters {
		plrDecision = append(plrDecision, clusterv1beta1.ClusterDecision{
			ClusterName: cluster,
			Reason:      "test",
		})
	}

	return &clusterv1beta1.PlacementDecisionStatus{Decisions: plrDecision}
}

// Pause sleep for given seconds
func Pause(s uint) {
	if s < 1 {
		s = 1
	}

	time.Sleep(time.Duration(float64(s)) * time.Second)
}

// ParseYaml read given yaml file and unmarshal it to &unstructured.Unstructured{}
func ParseYaml(file string) *unstructured.Unstructured {
	yamlFile, err := os.ReadFile(file)
	Expect(err).ToNot(HaveOccurred())

	yamlPlc := &unstructured.Unstructured{}
	err = yaml.Unmarshal(yamlFile, yamlPlc)
	Expect(err).ToNot(HaveOccurred())

	return yamlPlc
}

// GetClusterLevelWithTimeout keeps polling to get the object for timeout seconds until wantFound is met
// (true for found, false for not found)
func GetClusterLevelWithTimeout(
	clientHubDynamic dynamic.Interface,
	gvr schema.GroupVersionResource,
	name string,
	wantFound bool,
	timeout int,
) *unstructured.Unstructured {
	if timeout < 1 {
		timeout = 1
	}

	var obj *unstructured.Unstructured

	EventuallyWithOffset(1, func() error {
		var err error
		namespace := clientHubDynamic.Resource(gvr)

		obj, err = namespace.Get(context.TODO(), name, metav1.GetOptions{})
		if wantFound && err != nil {
			return err
		}

		if !wantFound && err == nil {
			return fmt.Errorf("expected to return IsNotFound error")
		}

		if !wantFound && err != nil && !errors.IsNotFound(err) {
			return err
		}

		return nil
	}, timeout, 1).Should(BeNil())

	if wantFound {
		return obj
	}

	return nil
}

// GetWithTimeout keeps polling to get the object for timeout seconds until wantFound is met
// (true for found, false for not found)
func GetWithTimeout(
	clientHubDynamic dynamic.Interface,
	gvr schema.GroupVersionResource,
	name, namespace string,
	wantFound bool,
	timeout int,
) *unstructured.Unstructured {
	if timeout < 1 {
		timeout = 1
	}

	var obj *unstructured.Unstructured

	EventuallyWithOffset(1, func() error {
		var err error
		namespace := clientHubDynamic.Resource(gvr).Namespace(namespace)

		obj, err = namespace.Get(context.TODO(), name, metav1.GetOptions{})
		if wantFound && err != nil {
			return err
		}

		if !wantFound && err == nil {
			return fmt.Errorf("expected to return IsNotFound error")
		}

		if !wantFound && err != nil && !errors.IsNotFound(err) {
			return err
		}

		return nil
	}, timeout, 1).Should(BeNil())

	if wantFound {
		return obj
	}

	return nil
}

// ListWithTimeout keeps polling to list the object for timeout seconds until wantFound is met
// (true for found, false for not found)
func ListWithTimeout(
	clientHubDynamic dynamic.Interface,
	gvr schema.GroupVersionResource,
	opts metav1.ListOptions,
	size int,
	wantFound bool,
	timeout int,
) *unstructured.UnstructuredList {
	if timeout < 1 {
		timeout = 1
	}

	var list *unstructured.UnstructuredList

	EventuallyWithOffset(1, func() error {
		var err error
		list, err = clientHubDynamic.Resource(gvr).List(context.TODO(), opts)

		if err != nil {
			return err
		}

		if len(list.Items) != size {
			return fmt.Errorf("list size doesn't match, expected %d actual %d", size, len(list.Items))
		}

		return nil
	}, timeout, 1).Should(BeNil())

	if wantFound {
		return list
	}

	return nil
}

// ListWithTimeoutByNamespace keeps polling to list the object for timeout seconds until wantFound is met
// (true for found, false for not found)
func ListWithTimeoutByNamespace(
	clientHubDynamic dynamic.Interface,
	gvr schema.GroupVersionResource,
	opts metav1.ListOptions,
	ns string,
	size int,
	wantFound bool,
	timeout int,
) *unstructured.UnstructuredList {
	if timeout < 1 {
		timeout = 1
	}

	var list *unstructured.UnstructuredList

	EventuallyWithOffset(1, func() error {
		var err error
		list, err = clientHubDynamic.Resource(gvr).Namespace(ns).List(context.TODO(), opts)

		if err != nil {
			return err
		}

		if len(list.Items) != size {
			return fmt.Errorf("list size doesn't match, expected %d actual %d", size, len(list.Items))
		}

		return nil
	}, timeout, 1).Should(BeNil())

	if wantFound {
		return list
	}

	return nil
}

// Kubectl execute kubectl cli
func Kubectl(args ...string) {
	cmd := exec.Command("kubectl", args...)

	err := cmd.Start()
	if err != nil {
		Fail(fmt.Sprintf("Error: %v", err))
	}
}

// KubectlWithOutput execute kubectl cli and return output and error
func KubectlWithOutput(args ...string) (string, error) {
	output, err := exec.Command("kubectl", args...).CombinedOutput()
	//nolint:forbidigo
	fmt.Println(string(output))

	return string(output), err
}

// GetMetrics execs into the propagator pod and curls the metrics endpoint, filters
// the response with the given patterns, and returns the value(s) for the matching
// metric(s).
func GetMetrics(metricPatterns ...string) []string {
	propPodInfo, err := KubectlWithOutput("get", "pod", "-n=open-cluster-management",
		"-l=name=governance-policy-propagator", "--no-headers")
	if err != nil {
		return []string{err.Error()}
	}

	var cmd *exec.Cmd

	metricFilter := " | grep " + strings.Join(metricPatterns, " | grep ")
	metricsCmd := `curl localhost:8383/metrics` + metricFilter

	// The pod name is "No" when the response is "No resources found"
	propPodName := strings.Split(propPodInfo, " ")[0]
	if propPodName == "No" {
		// A missing pod could mean the controller is running locally
		cmd = exec.Command("bash", "-c", metricsCmd)
	} else {
		cmd = exec.Command("kubectl", "exec", "-n=open-cluster-management", propPodName, "-c",
			"governance-policy-propagator", "--", "bash", "-c", metricsCmd)
	}

	matchingMetricsRaw, err := cmd.Output()
	if err != nil {
		if err.Error() == "exit status 1" {
			return []string{} // exit 1 indicates that grep couldn't find a match.
		}

		return []string{err.Error()}
	}

	matchingMetrics := strings.Split(strings.TrimSpace(string(matchingMetricsRaw)), "\n")
	values := make([]string, len(matchingMetrics))

	for i, metric := range matchingMetrics {
		fields := strings.Fields(metric)
		if len(fields) > 0 {
			values[i] = fields[len(fields)-1]
		}
	}

	return values
}
