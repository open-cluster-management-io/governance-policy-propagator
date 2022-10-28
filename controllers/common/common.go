// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/avast/retry-go/v3"
	"github.com/go-logr/logr"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

const (
	APIGroup              string = "policy.open-cluster-management.io"
	ClusterNameLabel      string = APIGroup + "/cluster-name"
	ClusterNamespaceLabel string = APIGroup + "/cluster-namespace"
	RootPolicyLabel       string = APIGroup + "/root-policy"
)

var ErrInvalidLabelValue = errors.New("unexpected format of label value")

// IsInClusterNamespace check if policy is in cluster namespace
func IsInClusterNamespace(ns string, allClusters []clusterv1.ManagedCluster) bool {
	for _, cluster := range allClusters {
		if ns == cluster.GetName() {
			return true
		}
	}

	return false
}

// IsPbForPoicy compares group and kind with policy group and kind for given pb
func IsPbForPoicy(pb *policiesv1.PlacementBinding) bool {
	found := false

	subjects := pb.Subjects
	for _, subject := range subjects {
		if subject.Kind == policiesv1.Kind && subject.APIGroup == policiesv1.SchemeGroupVersion.Group {
			found = true

			break
		}
	}

	return found
}

// IsPbForPoicySet compares group and kind with policyset group and kind for given pb
func IsPbForPoicySet(pb *policiesv1.PlacementBinding) bool {
	found := false

	subjects := pb.Subjects
	for _, subject := range subjects {
		if subject.Kind == policiesv1.PolicySetKind && subject.APIGroup == policiesv1.SchemeGroupVersion.Group {
			found = true

			break
		}
	}

	return found
}

// FindNonCompliantClustersForPolicy returns cluster in noncompliant status with given policy
func FindNonCompliantClustersForPolicy(plc *policiesv1.Policy) []string {
	clusterList := []string{}

	for _, clusterStatus := range plc.Status.Status {
		if clusterStatus.ComplianceState == policiesv1.NonCompliant {
			clusterList = append(clusterList, clusterStatus.ClusterName)
		}
	}

	return clusterList
}

// GetRetryOptions returns reasonable options to call retry.Do with.
func GetRetryOptions(logger logr.Logger, retryMsg string, attempts uint) []retry.Option {
	return []retry.Option{
		retry.Attempts(attempts),
		retry.Delay(2 * time.Second),
		retry.MaxDelay(10 * time.Second),
		retry.OnRetry(func(n uint, err error) { logger.Info(retryMsg) }),
		retry.LastErrorOnly(true),
	}
}

// GetClusterPlacementDecisions return the placement decisions from cluster
// placement decisions
func GetClusterPlacementDecisions(
	c client.Client, pb policiesv1.PlacementBinding, instance *policiesv1.Policy, log logr.Logger,
) ([]appsv1.PlacementDecision, error) {
	log = log.WithValues("name", pb.PlacementRef.Name, "namespace", instance.GetNamespace())
	pl := &clusterv1beta1.Placement{}

	err := c.Get(context.TODO(), types.NamespacedName{
		Namespace: instance.GetNamespace(),
		Name:      pb.PlacementRef.Name,
	}, pl)
	// no error when not found
	if err != nil && !k8serrors.IsNotFound(err) {
		log.Error(err, "Failed to get the Placement")

		return nil, err
	}

	list := &clusterv1beta1.PlacementDecisionList{}
	lopts := &client.ListOptions{Namespace: instance.GetNamespace()}

	opts := client.MatchingLabels{"cluster.open-cluster-management.io/placement": pl.GetName()}
	opts.ApplyToList(lopts)
	err = c.List(context.TODO(), list, lopts)

	// do not error out if not found
	if err != nil && !k8serrors.IsNotFound(err) {
		log.Error(err, "Failed to get the PlacementDecision")

		return nil, err
	}

	var decisions []appsv1.PlacementDecision
	decisions = make([]appsv1.PlacementDecision, 0, len(list.Items))

	for _, item := range list.Items {
		for _, cluster := range item.Status.Decisions {
			decided := &appsv1.PlacementDecision{
				ClusterName:      cluster.ClusterName,
				ClusterNamespace: cluster.ClusterName,
			}
			decisions = append(decisions, *decided)
		}
	}

	return decisions, nil
}

// GetApplicationPlacementDecisions return the placement decisions from an application
// lifecycle placementrule
func GetApplicationPlacementDecisions(
	c client.Client, pb policiesv1.PlacementBinding, instance *policiesv1.Policy, log logr.Logger,
) ([]appsv1.PlacementDecision, error) {
	log = log.WithValues("name", pb.PlacementRef.Name, "namespace", instance.GetNamespace())
	plr := &appsv1.PlacementRule{}

	err := c.Get(context.TODO(), types.NamespacedName{
		Namespace: instance.GetNamespace(),
		Name:      pb.PlacementRef.Name,
	}, plr)
	// no error when not found
	if err != nil && !k8serrors.IsNotFound(err) {
		log.Error(
			err,
			"Failed to get the PlacementRule",
			"namespace", instance.GetNamespace(),
			"name", pb.PlacementRef.Name,
		)

		return nil, err
	}

	return plr.Status.Decisions, nil
}

// GetNumWorkers is a helper function to return the number of workers to handle concurrent tasks
func GetNumWorkers(listLength int, concurrencyPerPolicy int) int {
	var numWorkers int
	if listLength > concurrencyPerPolicy {
		numWorkers = concurrencyPerPolicy
	} else {
		numWorkers = listLength
	}

	return numWorkers
}

func ParseRootPolicyLabel(rootPlc string) (name, namespace string, err error) {
	rootSplit := strings.Split(rootPlc, ".")
	if len(rootSplit) != 2 {
		err = fmt.Errorf("required exactly one `.` in value of label `%v`: %w",
			RootPolicyLabel, ErrInvalidLabelValue)

		return "", "", err
	}

	return rootSplit[1], rootSplit[0], nil
}

// TypeConverter is a helper function to converter type struct a to b
func TypeConverter(a, b interface{}) error {
	js, err := json.Marshal(a)
	if err != nil {
		return err
	}

	return json.Unmarshal(js, b)
}
