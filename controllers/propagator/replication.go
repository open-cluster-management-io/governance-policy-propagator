package propagator

import (
	"context"
	"strings"

	k8sdepwatches "github.com/stolostron/kubernetes-dependency-watches/client"
	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"

	policiesv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policiesv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

// LabelsForRootPolicy returns the labels for given policy
func LabelsForRootPolicy(plc *policiesv1.Policy) map[string]string {
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

// buildReplicatedPolicy constructs a replicated policy based on a root policy and a placementDecision.
// In particular, it adds labels that the policy framework uses, and ensures that policy dependencies
// are in a consistent format suited for use on managed clusters.
func (r *PolicyReconciler) buildReplicatedPolicy(
	root *policiesv1.Policy, decision appsv1.PlacementDecision,
) (*policiesv1.Policy, error) {
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

	var err error

	replicated.Spec.Dependencies, err = r.canonicalizeDependencies(replicated.Spec.Dependencies, root.Namespace)
	if err != nil {
		return replicated, err
	}

	for i, template := range replicated.Spec.PolicyTemplates {
		replicated.Spec.PolicyTemplates[i].ExtraDependencies, err = r.canonicalizeDependencies(
			template.ExtraDependencies, root.Namespace)
		if err != nil {
			return replicated, err
		}
	}

	return replicated, nil
}

// depIsPolicySet returns true if the given PolicyDependency is a PolicySet
func depIsPolicySet(dep policiesv1.PolicyDependency) bool {
	return dep.Kind == policiesv1.PolicySetKind &&
		dep.APIVersion == policiesv1beta1.GroupVersion.String()
}

// depIsPolicy returns true if the given PolicyDependency is a Policy
func depIsPolicy(dep policiesv1.PolicyDependency) bool {
	return dep.Kind == policiesv1.Kind &&
		dep.APIVersion == policiesv1.GroupVersion.String()
}

// canonicalizeDependencies returns an adjusted list of the input dependencies, ensuring that
// Policies are in a consistent format (omitting the namespace, and using the <namespace>.<name>
// format as in replicated Policies), and that PolicySets are replaced with their constituent
// Policies. If a PolicySet could not be found, that dependency will be copied as-is. It will
// return an error if there is an unexpected error looking up a PolicySet to replace.
func (r *PolicyReconciler) canonicalizeDependencies(
	rawDeps []policiesv1.PolicyDependency, defaultNamespace string,
) ([]policiesv1.PolicyDependency, error) {
	deps := make([]policiesv1.PolicyDependency, 0)

	for _, dep := range rawDeps {
		if depIsPolicySet(dep) {
			plcset := &policiesv1beta1.PolicySet{}

			if dep.Namespace == "" {
				dep.Namespace = defaultNamespace
			}

			err := r.Get(context.TODO(), types.NamespacedName{
				Namespace: dep.Namespace,
				Name:      dep.Name,
			}, plcset)
			if err != nil {
				if k8serrors.IsNotFound(err) {
					// If the PolicySet does not exist, that's ok - the propagator doesn't need to
					// do anything special because there will be a useful status message created by
					// the framework on the managed cluster.
					deps = append(deps, dep)

					continue
				}

				return deps, err
			}

			for _, plc := range plcset.Spec.Policies {
				deps = append(deps, policiesv1.PolicyDependency{
					TypeMeta: v1.TypeMeta{
						Kind:       policiesv1.Kind,
						APIVersion: policiesv1.GroupVersion.String(),
					},
					Name:       dep.Namespace + "." + string(plc),
					Namespace:  "",
					Compliance: dep.Compliance,
				})
			}
		} else if depIsPolicy(dep) {
			split := strings.Split(dep.Name, ".")
			if len(split) == 2 { // assume it's already in the correct <namespace>.<name> format
				deps = append(deps, dep)
			} else {
				if dep.Namespace == "" {
					// use the namespace from the dependent policy when otherwise not provided
					dep.Namespace = defaultNamespace
				}

				dep.Name = dep.Namespace + "." + dep.Name
				dep.Namespace = ""

				deps = append(deps, dep)
			}
		} else {
			deps = append(deps, dep)
		}
	}

	return deps, nil
}

// getPolicySetDependencies find all dependencies and extraDependencies in the given policy that
// are PolicySets, since those objects will need to be watched.
func getPolicySetDependencies(root *policiesv1.Policy) map[k8sdepwatches.ObjectIdentifier]bool {
	policySetIDs := make(map[k8sdepwatches.ObjectIdentifier]bool)

	for _, dep := range root.Spec.Dependencies {
		if depIsPolicySet(dep) {
			ns := dep.Namespace
			if ns == "" {
				ns = root.Namespace
			}

			policySetIDs[k8sdepwatches.ObjectIdentifier{
				Group:     common.APIGroup,
				Version:   "v1beta1",
				Kind:      policiesv1.PolicySetKind,
				Namespace: ns,
				Name:      dep.Name,
			}] = true
		}
	}

	for _, tmpl := range root.Spec.PolicyTemplates {
		for _, dep := range tmpl.ExtraDependencies {
			if depIsPolicySet(dep) {
				ns := dep.Namespace
				if ns == "" {
					ns = root.Namespace
				}

				policySetIDs[k8sdepwatches.ObjectIdentifier{
					Group:     common.APIGroup,
					Version:   "v1beta1",
					Kind:      policiesv1.PolicySetKind,
					Namespace: ns,
					Name:      dep.Name,
				}] = true
			}
		}
	}

	return policySetIDs
}
