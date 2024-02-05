// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

// RootStatusUpdate updates the root policy status with bound decisions, placements, and cluster status.
func RootStatusUpdate(ctx context.Context, c client.Client, rootPolicy *policiesv1.Policy) (DecisionSet, error) {
	placements, decisions, err := GetClusterDecisions(ctx, c, rootPolicy)
	if err != nil {
		log.Info("Failed to get any placement decisions. Giving up on the request.")

		return nil, err
	}

	cpcs, cpcsErr := CalculatePerClusterStatus(ctx, c, rootPolicy, decisions)
	if cpcsErr != nil {
		// If there is a new replicated policy, then its lookup is expected to fail - it hasn't been created yet.
		log.Error(cpcsErr, "Failed to get at least one replicated policy, but that may be expected. Ignoring.")
	}

	err = c.Get(ctx,
		types.NamespacedName{
			Namespace: rootPolicy.Namespace,
			Name:      rootPolicy.Name,
		}, rootPolicy)
	if err != nil {
		log.Error(err, "Failed to refresh the cached policy. Will use existing policy.")
	}

	complianceState := CalculateRootCompliance(cpcs)

	if reflect.DeepEqual(rootPolicy.Status.Status, cpcs) &&
		rootPolicy.Status.ComplianceState == complianceState &&
		reflect.DeepEqual(rootPolicy.Status.Placement, placements) {
		return decisions, nil
	}

	log.Info("Updating the root policy status", "RootPolicyName", rootPolicy.Name, "Namespace", rootPolicy.Namespace)
	rootPolicy.Status.Status = cpcs
	rootPolicy.Status.ComplianceState = complianceState
	rootPolicy.Status.Placement = placements

	err = c.Status().Update(ctx, rootPolicy)
	if err != nil {
		return nil, err
	}

	return decisions, nil
}

// GetPolicyPlacementDecisions retrieves the placement decisions for a input PlacementBinding when
// the policy is bound within it. It can return an error if the PlacementBinding is invalid, or if
// a required lookup fails.
func GetPolicyPlacementDecisions(ctx context.Context, c client.Client,
	instance *policiesv1.Policy, pb *policiesv1.PlacementBinding,
) (clusterDecisions []string, placements []*policiesv1.Placement, err error) {
	if !HasValidPlacementRef(pb) {
		return nil, nil, fmt.Errorf("placement binding %s/%s reference is not valid", pb.Name, pb.Namespace)
	}

	policySubjectFound := false
	policySetSubjects := make(map[string]struct{}) // a set, to prevent duplicates

	for _, subject := range pb.Subjects {
		if subject.APIGroup != policiesv1.SchemeGroupVersion.Group {
			continue
		}

		switch subject.Kind {
		case policiesv1.Kind:
			if !policySubjectFound && subject.Name == instance.GetName() {
				policySubjectFound = true

				placements = append(placements, &policiesv1.Placement{
					PlacementBinding: pb.GetName(),
				})
			}
		case policiesv1.PolicySetKind:
			if _, exists := policySetSubjects[subject.Name]; !exists {
				policySetSubjects[subject.Name] = struct{}{}

				if IsPolicyInPolicySet(ctx, c, instance.GetName(), subject.Name, pb.GetNamespace()) {
					placements = append(placements, &policiesv1.Placement{
						PlacementBinding: pb.GetName(),
						PolicySet:        subject.Name,
					})
				}
			}
		}
	}

	if len(placements) == 0 {
		// None of the subjects in the PlacementBinding were relevant to this Policy.
		return nil, nil, nil
	}

	// If the placementRef exists, then it needs to be added to the placement item
	refNN := types.NamespacedName{
		Namespace: pb.GetNamespace(),
		Name:      pb.PlacementRef.Name,
	}

	switch pb.PlacementRef.Kind {
	case "PlacementRule":
		plr := &appsv1.PlacementRule{}
		if err := c.Get(ctx, refNN, plr); err != nil && !k8serrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("failed to check for PlacementRule '%v': %w", pb.PlacementRef.Name, err)
		}

		for i := range placements {
			placements[i].PlacementRule = plr.Name // will be empty if the PlacementRule was not found
		}
	case "Placement":
		pl := &clusterv1beta1.Placement{}
		if err := c.Get(ctx, refNN, pl); err != nil && !k8serrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("failed to check for Placement '%v': %w", pb.PlacementRef.Name, err)
		}

		for i := range placements {
			placements[i].Placement = pl.Name // will be empty if the Placement was not found
		}
	}

	// If there are no placements, then the PlacementBinding is not for this Policy.
	if len(placements) == 0 {
		return nil, nil, nil
	}

	// If the policy is disabled, don't return any decisions, so that the policy isn't put on any clusters
	if instance.Spec.Disabled {
		return nil, placements, nil
	}

	clusterDecisions, err = GetDecisions(ctx, c, pb)

	return clusterDecisions, placements, err
}

type DecisionSet map[string]bool

// GetClusterDecisions identifies all managed clusters which should have a replicated policy using the root policy
// This returns unique decisions and placements that are NOT under Restricted subset.
// Also this function returns placements that are under restricted subset.
// But these placements include decisions which are under non-restricted subset.
// In other words, this function returns placements which include at least one decision under non-restricted subset.
func GetClusterDecisions(
	ctx context.Context,
	c client.Client,
	rootPolicy *policiesv1.Policy,
) (
	[]*policiesv1.Placement, DecisionSet, error,
) {
	log := log.WithValues("policyName", rootPolicy.GetName(), "policyNamespace", rootPolicy.GetNamespace())
	decisions := make(map[string]bool)

	pbList := &policiesv1.PlacementBindingList{}

	err := c.List(ctx, pbList, &client.ListOptions{Namespace: rootPolicy.GetNamespace()})
	if err != nil {
		log.Error(err, "Could not list the placement bindings")

		return nil, decisions, err
	}

	placements := []*policiesv1.Placement{}

	// Gather all placements and decisions when it is NOT policiesv1.Restricted
	for i, pb := range pbList.Items {
		if pb.SubFilter == policiesv1.Restricted {
			continue
		}

		plcDecisions, plcPlacements, err := GetPolicyPlacementDecisions(ctx, c, rootPolicy, &pbList.Items[i])
		if err != nil {
			return nil, nil, err
		}

		if len(plcDecisions) == 0 {
			log.Info("No placement decisions to process for this policy from this non-restricted binding",
				"policyName", rootPolicy.GetName(), "bindingName", pb.GetName())
		}

		// Decisions are all unique
		for _, clusterName := range plcDecisions {
			decisions[clusterName] = true
		}

		placements = append(placements, plcPlacements...)
	}

	// Gather placements which have at least one decision that is included in NON-Restricted
	for i, pb := range pbList.Items {
		if pb.SubFilter != policiesv1.Restricted {
			continue
		}

		foundInDecisions := false

		plcDecisions, plcPlacements, err := GetPolicyPlacementDecisions(ctx, c, rootPolicy, &pbList.Items[i])
		if err != nil {
			return nil, nil, err
		}

		if len(plcDecisions) == 0 {
			log.Info("No placement decisions to process for this policy from this restricted binding",
				"policyName", rootPolicy.GetName(), "bindingName", pb.GetName())
		}

		// Decisions are all unique
		for _, clusterName := range plcDecisions {
			if _, ok := decisions[clusterName]; ok {
				foundInDecisions = true
			}

			decisions[clusterName] = true
		}

		if foundInDecisions {
			placements = append(placements, plcPlacements...)
		}
	}

	log.V(2).Info("Sorting placements", "RootPolicyName", rootPolicy.Name, "Namespace", rootPolicy.Namespace)
	sort.SliceStable(placements, func(i, j int) bool {
		pi := placements[i].PlacementBinding + " " + placements[i].Placement + " " +
			placements[i].PlacementRule + " " + placements[i].PolicySet
		pj := placements[j].PlacementBinding + " " + placements[j].Placement + " " +
			placements[j].PlacementRule + " " + placements[j].PolicySet

		return pi < pj
	})

	return placements, decisions, nil
}

// CalculatePerClusterStatus lists up all policies replicated from the input policy, and stores
// their compliance states in the result list. The result is sorted by cluster name. An error
// will be returned if lookup of a replicated policy fails, but all lookups will still be attempted.
func CalculatePerClusterStatus(
	ctx context.Context,
	c client.Client,
	rootPolicy *policiesv1.Policy,
	decisions DecisionSet,
) ([]*policiesv1.CompliancePerClusterStatus, error) {
	if rootPolicy.Spec.Disabled {
		return nil, nil
	}

	status := make([]*policiesv1.CompliancePerClusterStatus, 0, len(decisions))
	var lookupErr error // save until end, to attempt all lookups

	// Update the status based on the processed decisions
	for clusterName := range decisions {
		replicatedPolicy := &policiesv1.Policy{}
		key := types.NamespacedName{
			Namespace: clusterName, Name: rootPolicy.Namespace + "." + rootPolicy.Name,
		}

		err := c.Get(ctx, key, replicatedPolicy)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				status = append(status, &policiesv1.CompliancePerClusterStatus{
					ClusterName:      clusterName,
					ClusterNamespace: clusterName,
				})

				continue
			}

			lookupErr = err
		}

		status = append(status, &policiesv1.CompliancePerClusterStatus{
			ComplianceState:  replicatedPolicy.Status.ComplianceState,
			ClusterName:      clusterName,
			ClusterNamespace: clusterName,
		})
	}

	sort.Slice(status, func(i, j int) bool {
		return status[i].ClusterName < status[j].ClusterName
	})

	return status, lookupErr
}

func IsPolicyInPolicySet(ctx context.Context, c client.Client, policyName, policySetName, namespace string) bool {
	log := log.WithValues("policyName", policyName, "policySetName", policySetName, "policyNamespace", namespace)

	policySet := policiesv1beta1.PolicySet{}
	setNN := types.NamespacedName{
		Name:      policySetName,
		Namespace: namespace,
	}

	if err := c.Get(ctx, setNN, &policySet); err != nil {
		log.Error(err, "Failed to get the policyset")

		return false
	}

	for _, plc := range policySet.Spec.Policies {
		if string(plc) == policyName {
			return true
		}
	}

	return false
}

// CalculateRootCompliance uses the input per-cluster statuses to determine what a root policy's
// ComplianceState should be. General precedence is: NonCompliant > Pending > Unknown > Compliant.
func CalculateRootCompliance(clusters []*policiesv1.CompliancePerClusterStatus) policiesv1.ComplianceState {
	if len(clusters) == 0 {
		// No clusters == no status
		return ""
	}

	unknownFound := false
	pendingFound := false

	for _, status := range clusters {
		switch status.ComplianceState {
		case policiesv1.NonCompliant:
			// NonCompliant has the highest priority, so we can skip checking the others
			return policiesv1.NonCompliant
		case policiesv1.Pending:
			pendingFound = true
		case policiesv1.Compliant:
			continue
		default:
			unknownFound = true
		}
	}

	if pendingFound {
		return policiesv1.Pending
	}

	if unknownFound {
		return ""
	}

	// Returns compliant if, and only if, *all* cluster statuses are Compliant
	return policiesv1.Compliant
}
