package propagator

import (
	"k8s.io/apimachinery/pkg/api/equality"
	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
)

// labelsForRootPolicy returns the labels for given policy
func labelsForRootPolicy(plc *policiesv1.Policy) map[string]string {
	return map[string]string{common.RootPolicyLabel: fullNameForPolicy(plc)}
}

// fullNameForPolicy returns the fully qualified name for given policy
// full qualified name: ${namespace}.${name}
func fullNameForPolicy(plc *policiesv1.Policy) string {
	return plc.GetNamespace() + "." + plc.GetName()
}

// equivalentReplicatedPolicies compares replicated policies. Returns true if they match.
func equivalentReplicatedPolicies(plc1 *policiesv1.Policy, plc2 *policiesv1.Policy) bool {
	// Compare annotations
	if !equality.Semantic.DeepEqual(plc1.GetAnnotations(), plc2.GetAnnotations()) {
		return false
	}

	// Compare labels
	if !equality.Semantic.DeepEqual(plc1.GetLabels(), plc2.GetLabels()) {
		return false
	}

	// Compare the specs
	return equality.Semantic.DeepEqual(plc1.Spec, plc2.Spec)
}

func buildReplicatedPolicy(root *policiesv1.Policy, decision appsv1.PlacementDecision) *policiesv1.Policy {
	replicatedName := fullNameForPolicy(root)

	replicated := root.DeepCopy()
	replicated.SetName(replicatedName)
	replicated.SetNamespace(decision.ClusterNamespace)
	replicated.SetResourceVersion("")
	replicated.SetFinalizers(nil)
	replicated.SetOwnerReferences(nil)

	labels := root.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	// Extra labels on replicated policies
	labels[common.ClusterNameLabel] = decision.ClusterName
	labels[common.ClusterNamespaceLabel] = decision.ClusterNamespace
	labels[common.RootPolicyLabel] = replicatedName

	replicated.SetLabels(labels)

	return replicated
}
