// Copyright (c) 2023 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
)

// RootStatusUpdat update Root policy status with bound decisions and placements
func RootStatusUpdate(c client.Client, rootPolicy *policiesv1.Policy) error {
	placements, decisions, err := GetClusterDecisions(c, rootPolicy)
	if err != nil {
		log.Info("Failed to get any placement decisions. Giving up on the request.")

		return errors.New("could not get the placement decisions")
	}

	cpcs, cpcsErr := CalculatePerClusterStatus(c, rootPolicy, decisions)
	if cpcsErr != nil {
		// If there is a new replicated policy, then its lookup is expected to fail - it hasn't been created yet.
		log.Error(cpcsErr, "Failed to get at least one replicated policy, but that may be expected. Ignoring.")
	}

	err = c.Get(context.TODO(),
		types.NamespacedName{
			Namespace: rootPolicy.Namespace,
			Name:      rootPolicy.Name,
		}, rootPolicy)
	if err != nil {
		log.Error(err, "Failed to refresh the cached policy. Will use existing policy.")
	}

	// make a copy of the original status
	originalCPCS := make([]*policiesv1.CompliancePerClusterStatus, len(rootPolicy.Status.Status))
	copy(originalCPCS, rootPolicy.Status.Status)

	rootPolicy.Status.Status = cpcs
	rootPolicy.Status.ComplianceState = CalculateRootCompliance(cpcs)
	rootPolicy.Status.Placement = placements

	err = c.Status().Update(context.TODO(), rootPolicy)
	if err != nil {
		return err
	}

	return nil
}

// GetPolicyPlacementDecisions retrieves the placement decisions for a input PlacementBinding when
// the policy is bound within it. It can return an error if the PlacementBinding is invalid, or if
// a required lookup fails.
func GetPolicyPlacementDecisions(c client.Client,
	instance *policiesv1.Policy, pb *policiesv1.PlacementBinding,
) (decisions []appsv1.PlacementDecision, placements []*policiesv1.Placement, err error) {
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

				if IsPolicyInPolicySet(c, instance.GetName(), subject.Name, pb.GetNamespace()) {
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
		if err := c.Get(context.TODO(), refNN, plr); err != nil && !k8serrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("failed to check for PlacementRule '%v': %w", pb.PlacementRef.Name, err)
		}

		for i := range placements {
			placements[i].PlacementRule = plr.Name // will be empty if the PlacementRule was not found
		}
	case "Placement":
		pl := &clusterv1beta1.Placement{}
		if err := c.Get(context.TODO(), refNN, pl); err != nil && !k8serrors.IsNotFound(err) {
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

	decisions, err = GetDecisions(c, pb)

	return decisions, placements, err
}

// GetAllClusterDecisions calculates which managed clusters should have a replicated policy, and
// whether there are any BindingOverrides for that cluster. The placements array it returns is
// sorted by PlacementBinding name. It can return an error if the PlacementBinding is invalid, or if
// a required lookup fails.
func GetAllClusterDecisions(
	c client.Client,
	instance *policiesv1.Policy, pbList *policiesv1.PlacementBindingList,
) (
	decisions map[appsv1.PlacementDecision]policiesv1.BindingOverrides, placements []*policiesv1.Placement, err error,
) {
	decisions = make(map[appsv1.PlacementDecision]policiesv1.BindingOverrides)

	// Process all placement bindings without subFilter
	for i, pb := range pbList.Items {
		if pb.SubFilter == policiesv1.Restricted {
			continue
		}

		plcDecisions, plcPlacements, err := GetPolicyPlacementDecisions(c, instance, &pbList.Items[i])
		if err != nil {
			return nil, nil, err
		}

		if len(plcDecisions) == 0 {
			log.Info("No placement decisions to process for this policy from this binding",
				"policyName", instance.GetName(), "bindingName", pb.GetName())
		}

		for _, decision := range plcDecisions {
			if overrides, ok := decisions[decision]; ok {
				// Found cluster in the decision map
				if strings.EqualFold(pb.BindingOverrides.RemediationAction, string(policiesv1.Enforce)) {
					overrides.RemediationAction = strings.ToLower(string(policiesv1.Enforce))
					decisions[decision] = overrides
				}
			} else {
				// No found cluster in the decision map, add it to the map
				decisions[decision] = policiesv1.BindingOverrides{
					// empty string if pb.BindingOverrides.RemediationAction is not defined
					RemediationAction: strings.ToLower(pb.BindingOverrides.RemediationAction),
				}
			}
		}

		placements = append(placements, plcPlacements...)
	}

	if len(decisions) == 0 {
		sort.Slice(placements, func(i, j int) bool {
			return placements[i].PlacementBinding < placements[j].PlacementBinding
		})

		// No decisions, and subfilters can't add decisions, so we can stop early.
		return nil, placements, nil
	}

	// Process all placement bindings with subFilter:restricted
	for i, pb := range pbList.Items {
		if pb.SubFilter != policiesv1.Restricted {
			continue
		}

		foundInDecisions := false

		plcDecisions, plcPlacements, err := GetPolicyPlacementDecisions(c, instance, &pbList.Items[i])
		if err != nil {
			return nil, nil, err
		}

		if len(plcDecisions) == 0 {
			log.Info("No placement decisions to process for this policy from this binding",
				"policyName", instance.GetName(), "bindingName", pb.GetName())
		}

		for _, decision := range plcDecisions {
			if overrides, ok := decisions[decision]; ok {
				// Found cluster in the decision map
				foundInDecisions = true

				if strings.EqualFold(pb.BindingOverrides.RemediationAction, string(policiesv1.Enforce)) {
					overrides.RemediationAction = strings.ToLower(string(policiesv1.Enforce))
					decisions[decision] = overrides
				}
			}
		}

		if foundInDecisions {
			placements = append(placements, plcPlacements...)
		}
	}

	sort.Slice(placements, func(i, j int) bool {
		return placements[i].PlacementBinding < placements[j].PlacementBinding
	})

	return decisions, placements, nil
}

type DecisionSet map[appsv1.PlacementDecision]bool

// getClusterDecisions identifies all managed clusters which should have a replicated policy using RootPolicy
func GetClusterDecisions(
	c client.Client,
	instance *policiesv1.Policy,
) (
	[]*policiesv1.Placement, DecisionSet, error,
) {
	log := log.WithValues("policyName", instance.GetName(), "policyNamespace", instance.GetNamespace())
	decisions := make(map[appsv1.PlacementDecision]bool)

	pbList := &policiesv1.PlacementBindingList{}

	err := c.List(context.TODO(), pbList, &client.ListOptions{Namespace: instance.GetNamespace()})
	if err != nil {
		log.Error(err, "Could not list the placement bindings")

		return nil, decisions, err
	}

	allClusterDecisions, placements, err := GetAllClusterDecisions(c, instance, pbList)
	if err != nil {
		return placements, decisions, err
	}

	if allClusterDecisions == nil {
		allClusterDecisions = make(map[appsv1.PlacementDecision]policiesv1.BindingOverrides)
	}

	for dec := range allClusterDecisions {
		decisions[dec] = true
	}

	return placements, decisions, nil
}

// CalculatePerClusterStatus lists up all policies replicated from the input policy, and stores
// their compliance states in the result list. The result is sorted by cluster name. An error
// will be returned if lookup of a replicated policy fails, but all lookups will still be attempted.
func CalculatePerClusterStatus(
	c client.Client,
	instance *policiesv1.Policy, decisions DecisionSet,
) ([]*policiesv1.CompliancePerClusterStatus, error) {
	if instance.Spec.Disabled {
		return nil, nil
	}

	status := make([]*policiesv1.CompliancePerClusterStatus, 0, len(decisions))
	var lookupErr error // save until end, to attempt all lookups

	// Update the status based on the processed decisions
	for dec := range decisions {
		replicatedPolicy := &policiesv1.Policy{}
		key := types.NamespacedName{
			Namespace: dec.ClusterNamespace, Name: instance.Namespace + "." + instance.Name,
		}

		err := c.Get(context.TODO(), key, replicatedPolicy)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				status = append(status, &policiesv1.CompliancePerClusterStatus{
					ClusterName:      dec.ClusterName,
					ClusterNamespace: dec.ClusterNamespace,
				})

				continue
			}

			lookupErr = err
		}

		status = append(status, &policiesv1.CompliancePerClusterStatus{
			ComplianceState:  replicatedPolicy.Status.ComplianceState,
			ClusterName:      dec.ClusterName,
			ClusterNamespace: dec.ClusterNamespace,
		})
	}

	sort.Slice(status, func(i, j int) bool {
		return status[i].ClusterName < status[j].ClusterName
	})

	return status, lookupErr
}

func IsPolicyInPolicySet(c client.Client, policyName, policySetName, namespace string) bool {
	log := log.WithValues("policyName", policyName, "policySetName", policySetName, "policyNamespace", namespace)

	policySet := policiesv1beta1.PolicySet{}
	setNN := types.NamespacedName{
		Name:      policySetName,
		Namespace: namespace,
	}

	if err := c.Get(context.TODO(), setNN, &policySet); err != nil {
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
