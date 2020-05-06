// Copyright (c) 2020 Red Hat, Inc.
// +build integration

package e2e

import (
	goctx "context"
	"io/ioutil"
	"os"
	"os/exec"
	"testing"
	"time"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/open-cluster-management/governance-policy-propagator/pkg/apis"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
)

var (
	retryInterval        = time.Second * 5
	timeout              = time.Second * 600
	cleanupRetryInterval = time.Second * 1
	cleanupTimeout       = time.Second * 5
)

func TestPolicyPropagator(t *testing.T) {
	policyList := &policiesv1.PolicyList{}
	err := framework.AddToFrameworkScheme(apis.AddToScheme, policyList)
	if err != nil {
		t.Fatalf("failed to add custom resource scheme to framework: %v", err)
	}
	// run subtests
	t.Run("policy-propagator", func(t *testing.T) {
		t.Run("Local", PolicyPropagatorLocal)
	})
}

func PolicyPropagatorLocal(t *testing.T) {
	// get global framework variables
	ctx := framework.NewContext(t)
	defer ctx.Cleanup()
	watchNamespace, err := ctx.GetWatchNamespace()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("watchNamespace %s", watchNamespace)
	cmd := exec.Command("operator-sdk", "run",
		"--local",
		"--watch-namespace="+watchNamespace)
	stderr, err := os.Create("stderr.txt")
	if err != nil {
		t.Fatalf("Failed to create stderr.txt: %v", err)
	}
	cmd.Stderr = stderr
	defer func() {
		if err := stderr.Close(); err != nil {
			t.Errorf("Failed to close stderr: (%v)", err)
		}
	}()

	err = cmd.Start()
	if err != nil {
		t.Fatalf("Error: %v", err)
	}
	ctx.AddCleanupFn(func() error { return cmd.Process.Signal(os.Interrupt) })

	// wait for operator to start (may take a minute to compile the command...)
	err = wait.Poll(time.Second*5, time.Second*100, func() (done bool, err error) {
		file, err := ioutil.ReadFile("stderr.txt")
		if err != nil {
			return false, err
		}
		if len(file) == 0 {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("Local operator not ready after 100 seconds: %v\n", err)
	}
	f := framework.Global
	if err := PolicyPropagationTest(t, f, ctx); err != nil {
		t.Error(err)
	}
}

func PolicyPropagationTest(t *testing.T, f *framework.Framework, ctx *framework.Context) error {
	rootPolicy := &policiesv1.Policy{
		TypeMeta: metav1.TypeMeta{
			Kind:       policiesv1.Kind,
			APIVersion: policiesv1.SchemeGroupVersion.Group,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "example-policy",
			Namespace: "default",
		},
		Spec: policiesv1.PolicySpec{
			Disabled:          false,
			RemediationAction: policiesv1.Inform,
		},
	}

	err := f.Client.Create(goctx.TODO(), rootPolicy, &framework.CleanupOptions{})
	if err != nil {
		return err
	}
	err = wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		return true, f.Client.Get(goctx.TODO(), types.NamespacedName{Name: "default.example-policy", Namespace: "hulk"}, rootPolicy)
	})

	return err
}
