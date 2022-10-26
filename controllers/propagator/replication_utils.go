package propagator

import (
	"context"

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

func (r *PolicyReconciler) canonicalizeDependencies(
	rawDeps []policiesv1.PolicyDependency, defaultNamespace string,
) ([]policiesv1.PolicyDependency, error) {
	deps := make([]policiesv1.PolicyDependency, 0)

	for _, dep := range rawDeps {
		if dep.Kind == policiesv1.PolicySetKind && dep.APIVersion == common.APIGroup+"/v1beta1" {
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
						APIVersion: common.APIGroup + "/v1",
					},
					Name:       dep.Namespace + "." + string(plc),
					Compliance: dep.Compliance,
				})
			}
		} else if dep.Kind == policiesv1.Kind && dep.APIVersion == common.APIGroup+"/v1" {
			if dep.Namespace == "" {
				dep.Namespace = defaultNamespace
			}

			deps = append(deps, policiesv1.PolicyDependency{
				TypeMeta: v1.TypeMeta{
					Kind:       policiesv1.Kind,
					APIVersion: common.APIGroup + "/v1",
				},
				Name:       dep.Namespace + "." + dep.Name,
				Compliance: dep.Compliance,
			})
		} else {
			deps = append(deps, dep)
		}
	}

	return deps, nil
}

func getPolicySetDependencies(root *policiesv1.Policy) map[k8sdepwatches.ObjectIdentifier]bool {
	policySetIDs := make(map[k8sdepwatches.ObjectIdentifier]bool)

	for _, dep := range root.Spec.Dependencies {
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

	for _, tmpl := range root.Spec.PolicyTemplates {
		for _, dep := range tmpl.ExtraDependencies {
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

	return policySetIDs
}
