// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

const (
	APIGroup              string = "policy.open-cluster-management.io"
	ClusterNameLabel      string = APIGroup + "/cluster-name"
	ClusterNamespaceLabel string = APIGroup + "/cluster-namespace"
	RootPolicyLabel       string = APIGroup + "/root-policy"
)

var ErrInvalidLabelValue = errors.New("unexpected format of label value")

// IsInClusterNamespace check if policy is in cluster namespace
func IsInClusterNamespace(c client.Client, ns string) (bool, error) {
	cluster := &clusterv1.ManagedCluster{}

	err := c.Get(context.TODO(), types.NamespacedName{Name: ns}, cluster)
	if k8serrors.IsNotFound(err) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("failed to get the managed cluster %s: %w", ns, err)
	}

	return true, nil
}

func IsReplicatedPolicy(c client.Client, policy client.Object) (bool, error) {
	rootPlcName := policy.GetLabels()[RootPolicyLabel]
	if rootPlcName == "" {
		return false, nil
	}

	_, _, err := ParseRootPolicyLabel(rootPlcName)
	if err != nil {
		return false, fmt.Errorf("invalid value set in %s: %w", RootPolicyLabel, err)
	}

	return IsInClusterNamespace(c, policy.GetNamespace())
}

// IsForPolicyOrPolicySet returns true if any of the subjects of the PlacementBinding are Policies
// or PolicySets.
func IsForPolicyOrPolicySet(pb *policiesv1.PlacementBinding) bool {
	if pb == nil {
		return false
	}

	for _, subject := range pb.Subjects {
		if subject.APIGroup == policiesv1.SchemeGroupVersion.Group &&
			(subject.Kind == policiesv1.Kind || subject.Kind == policiesv1.PolicySetKind) {
			return true
		}
	}

	return false
}

// IsPbForPolicySet compares group and kind with policyset group and kind for given pb
func IsPbForPolicySet(pb *policiesv1.PlacementBinding) bool {
	if pb == nil {
		return false
	}

	subjects := pb.Subjects
	for _, subject := range subjects {
		if subject.Kind == policiesv1.PolicySetKind && subject.APIGroup == policiesv1.SchemeGroupVersion.Group {
			return true
		}
	}

	return false
}

// GetPoliciesInPlacementBinding returns a list of the Policies that are either direct subjects of
// the given PlacementBinding, or are in PolicySets that are subjects of the PlacementBinding.
// The list items are not guaranteed to be unique (for example if a policy is in multiple sets).
func GetPoliciesInPlacementBinding(
	ctx context.Context, c client.Client, pb *policiesv1.PlacementBinding,
) []reconcile.Request {
	result := make([]reconcile.Request, 0)

	for _, subject := range pb.Subjects {
		if subject.APIGroup != policiesv1.SchemeGroupVersion.Group {
			continue
		}

		switch subject.Kind {
		case policiesv1.Kind:
			result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{
				Name:      subject.Name,
				Namespace: pb.GetNamespace(),
			}})
		case policiesv1.PolicySetKind:
			setNN := types.NamespacedName{
				Name:      subject.Name,
				Namespace: pb.GetNamespace(),
			}

			policySet := policiesv1beta1.PolicySet{}
			if err := c.Get(ctx, setNN, &policySet); err != nil {
				continue
			}

			for _, plc := range policySet.Spec.Policies {
				result = append(result, reconcile.Request{NamespacedName: types.NamespacedName{
					Name:      string(plc),
					Namespace: pb.GetNamespace(),
				}})
			}
		}
	}

	return result
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

func HasValidPlacementRef(pb *policiesv1.PlacementBinding) bool {
	switch pb.PlacementRef.Kind {
	case "PlacementRule":
		return pb.PlacementRef.APIGroup == appsv1.SchemeGroupVersion.Group
	case "Placement":
		return pb.PlacementRef.APIGroup == clusterv1beta1.SchemeGroupVersion.Group
	default:
		return false
	}
}

// GetDecisions returns the placement decisions from the Placement or PlacementRule referred to by
// the PlacementBinding
func GetDecisions(c client.Client, pb *policiesv1.PlacementBinding) ([]appsv1.PlacementDecision, error) {
	if !HasValidPlacementRef(pb) {
		return nil, fmt.Errorf("placement binding %s/%s reference is not valid", pb.Name, pb.Namespace)
	}

	refNN := types.NamespacedName{
		Namespace: pb.GetNamespace(),
		Name:      pb.PlacementRef.Name,
	}

	switch pb.PlacementRef.Kind {
	case "Placement":
		pl := &clusterv1beta1.Placement{}

		err := c.Get(context.TODO(), refNN, pl)
		if err != nil && !k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get Placement '%v': %w", pb.PlacementRef.Name, err)
		}

		if k8serrors.IsNotFound(err) {
			return nil, nil
		}

		list := &clusterv1beta1.PlacementDecisionList{}
		lopts := &client.ListOptions{Namespace: pb.GetNamespace()}

		opts := client.MatchingLabels{"cluster.open-cluster-management.io/placement": pl.GetName()}
		opts.ApplyToList(lopts)

		err = c.List(context.TODO(), list, lopts)
		if err != nil && !k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to list the PlacementDecisions for '%v', %w", pb.PlacementRef.Name, err)
		}

		decisions := make([]appsv1.PlacementDecision, 0)

		for _, item := range list.Items {
			for _, cluster := range item.Status.Decisions {
				decisions = append(decisions, appsv1.PlacementDecision{
					ClusterName:      cluster.ClusterName,
					ClusterNamespace: cluster.ClusterName,
				})
			}
		}

		return decisions, nil
	case "PlacementRule":
		plr := &appsv1.PlacementRule{}
		if err := c.Get(context.TODO(), refNN, plr); err != nil && !k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get PlacementRule '%v': %w", pb.PlacementRef.Name, err)
		}

		// if the PlacementRule was not found, the decisions will be empty
		return plr.Status.Decisions, nil
	}

	return nil, fmt.Errorf("placement binding %s/%s reference is not valid", pb.Name, pb.Namespace)
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
	// namespaces can't have a `.` (but names can) so this always correctly pulls the namespace out
	namespace, name, found := strings.Cut(rootPlc, ".")
	if !found {
		err = fmt.Errorf("required at least one `.` in value of label `%v`: %w",
			RootPolicyLabel, ErrInvalidLabelValue)

		return "", "", err
	}

	return name, namespace, nil
}

// LabelsForRootPolicy returns the labels for given policy
func LabelsForRootPolicy(plc *policiesv1.Policy) map[string]string {
	return map[string]string{RootPolicyLabel: FullNameForPolicy(plc)}
}

// fullNameForPolicy returns the fully qualified name for given policy
// full qualified name: ${namespace}.${name}
func FullNameForPolicy(plc *policiesv1.Policy) string {
	return plc.GetNamespace() + "." + plc.GetName()
}

// TypeConverter is a helper function to converter type struct a to b
func TypeConverter(a, b interface{}) error {
	js, err := json.Marshal(a)
	if err != nil {
		return err
	}

	return json.Unmarshal(js, b)
}
