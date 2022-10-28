package propagator

import (
	"context"
	"sort"

	retry "github.com/avast/retry-go/v3"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

// calculatePerClusterStatus lists up all policies replicated from the input policy, and stores
// their compliance states in the result list. Additionally, clusters in the failedClusters input
// will be marked as NonCompliant in the result. The result is sorted by cluster name. An error
// will be returned if lookup of the replicated policies fails, and the retries also fail.
func (r *PolicyReconciler) calculatePerClusterStatus(
	instance *policiesv1.Policy, failedClusters decisionSet,
) ([]*policiesv1.CompliancePerClusterStatus, error) {
	if instance.Spec.Disabled {
		return nil, nil
	}

	replicatedPlcList := &policiesv1.PolicyList{}

	log.V(1).Info("Getting the replicated policies")

	err := retry.Do(
		func() error {
			return r.List(
				context.TODO(),
				replicatedPlcList,
				client.MatchingLabels(LabelsForRootPolicy(instance)),
			)
		},
		getRetryOptions(log.V(1), "Retrying to list the replicated policies")...,
	)
	if err != nil {
		log.Info("Gave up on listing the replicated policies after too many retries")
		r.recordWarning(instance, "Could not list the replicated policies")

		return nil, err
	}

	status := make([]*policiesv1.CompliancePerClusterStatus, 0, len(replicatedPlcList.Items)+len(failedClusters))

	// Update the status based on the replicated policies
	for _, rPlc := range replicatedPlcList.Items {
		key := appsv1.PlacementDecision{
			ClusterName:      rPlc.GetLabels()[common.ClusterNameLabel],
			ClusterNamespace: rPlc.GetLabels()[common.ClusterNamespaceLabel],
		}

		if failed := failedClusters[key]; failed {
			// Skip the replicated policies that failed to be properly replicated
			// for now. This will be handled later.
			continue
		}

		status = append(status, &policiesv1.CompliancePerClusterStatus{
			ComplianceState:  rPlc.Status.ComplianceState,
			ClusterName:      key.ClusterName,
			ClusterNamespace: key.ClusterNamespace,
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

// calculateRootCompliance uses the input per-cluster statuses to determine what a root policy's
// ComplianceState should be. General precedence is: NonCompliant > Pending > Unknown > Compliant.
func calculateRootCompliance(clusters []*policiesv1.CompliancePerClusterStatus) policiesv1.ComplianceState {
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
