// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package utils

import (
	"context"
	"fmt"
	"io/ioutil"
	"os/exec"
	"time"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
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

// Pause sleep for given seconds
func Pause(s uint) {
	if s < 1 {
		s = 1
	}
	time.Sleep(time.Duration(float64(s)) * time.Second)
}

// ParseYaml read given yaml file and unmarshal it to &unstructured.Unstructured{}
func ParseYaml(file string) *unstructured.Unstructured {
	yamlFile, err := ioutil.ReadFile(file)
	Expect(err).To(BeNil())
	yamlPlc := &unstructured.Unstructured{}
	err = yaml.Unmarshal(yamlFile, yamlPlc)
	Expect(err).To(BeNil())
	return yamlPlc
}

// GetClusterLevelWithTimeout keeps polling to get the object for timeout seconds until wantFound is met (true for found, false for not found)
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

	Eventually(func() error {
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

// GetWithTimeout keeps polling to get the object for timeout seconds until wantFound is met (true for found, false for not found)
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

	Eventually(func() error {
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

// ListWithTimeout keeps polling to list the object for timeout seconds until wantFound is met (true for found, false for not found)
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

	Eventually(func() error {
		var err error
		list, err = clientHubDynamic.Resource(gvr).List(context.TODO(), opts)
		if err != nil {
			return err
		} else {
			if len(list.Items) != size {
				return fmt.Errorf("list size doesn't match, expected %d actual %d", size, len(list.Items))
			} else {
				return nil
			}
		}
	}, timeout, 1).Should(BeNil())
	if wantFound {
		return list
	}
	return nil

}

// ListWithTimeoutByNamespace keeps polling to list the object for timeout seconds until wantFound is met (true for found, false for not found)
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

	Eventually(func() error {
		var err error
		list, err = clientHubDynamic.Resource(gvr).Namespace(ns).List(context.TODO(), opts)
		if err != nil {
			return err
		} else {
			if len(list.Items) != size {
				return fmt.Errorf("list size doesn't match, expected %d actual %d", size, len(list.Items))
			} else {
				return nil
			}
		}
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
	fmt.Println(string(output))
	return string(output), err
}
