package propagator

import (
	"context"
	"sort"

	"k8s.io/apimachinery/pkg/types"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

// calculatePerClusterStatus lists up all policies replicated from the input policy, and stores
// their compliance states in the result list. Additionally, clusters in the failedClusters input
// will be marked as NonCompliant in the result. The result is sorted by cluster name. An error
// will be returned if lookup of the replicated policies fails, and the retries also fail.
func (r *PolicyReconciler) calculatePerClusterStatus(
	instance *policiesv1.Policy, allDecisions, failedClusters decisionSet,
) ([]*policiesv1.CompliancePerClusterStatus, error) {
	if instance.Spec.Disabled {
		return nil, nil
	}

	status := make([]*policiesv1.CompliancePerClusterStatus, 0, len(allDecisions))

	// Update the status based on the processed decisions
	for decision := range allDecisions {
		if failedClusters[decision] {
			// Skip the replicated policies that failed to be properly replicated
			// for now. This will be handled later.
			continue
		}

		rPlc := &policiesv1.Policy{}
		key := types.NamespacedName{
			Namespace: decision.ClusterNamespace, Name: instance.Namespace + "." + instance.Name,
		}

		err := r.Get(context.TODO(), key, rPlc)
		if err != nil {
			return nil, err
		}

		status = append(status, &policiesv1.CompliancePerClusterStatus{
			ComplianceState:  rPlc.Status.ComplianceState,
			ClusterName:      decision.ClusterName,
			ClusterNamespace: decision.ClusterNamespace,
		})
	}

	// Add cluster statuses for the clusters that did not get their policies properly
	// replicated. This is not done in the previous loop since some replicated polices may not
	// have been created at all.
	for clusterDecision := range failedClusters {
		log.Info(
			"Setting the policy to noncompliant since the replication failed", "cluster", clusterDecision,
		)

		status = append(status, &policiesv1.CompliancePerClusterStatus{
			ComplianceState:  policiesv1.NonCompliant,
			ClusterName:      clusterDecision.ClusterName,
			ClusterNamespace: clusterDecision.ClusterNamespace,
		})
	}

	sort.Slice(status, func(i, j int) bool {
		return status[i].ClusterName < status[j].ClusterName
	})

	return status, nil
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
