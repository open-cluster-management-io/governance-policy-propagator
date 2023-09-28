package propagator

import (
	"context"
	"sort"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
)

// calculatePerClusterStatus lists up all policies replicated from the input policy, and stores
// their compliance states in the result list. The result is sorted by cluster name. An error
// will be returned if lookup of a replicated policy fails, but all lookups will still be attempted.
func (r *Propagator) calculatePerClusterStatus(
	instance *policiesv1.Policy, decisions decisionSet,
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

		err := r.Get(context.TODO(), key, replicatedPolicy)
		if err != nil {
			if errors.IsNotFound(err) {
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
