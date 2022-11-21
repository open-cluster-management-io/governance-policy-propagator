// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	appsv1 "open-cluster-management.io/multicloud-operators-subscription/pkg/apis/apps/placementrule/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policyv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
)

const ControllerName string = "policy-set"

var log = ctrl.Log.WithName(ControllerName)

// PolicySetReconciler reconciles a PolicySet object
type PolicySetReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// blank assignment to verify that PolicySetReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &PolicySetReconciler{}

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policysets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policysets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policysets/finalizers,verbs=update

func (r *PolicySetReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	log.Info("Reconciling policy sets...")
	// Fetch the PolicySet instance
	instance := &policyv1beta1.PolicySet{}

	err := r.Get(ctx, request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("Policy set not found, so it may have been deleted.")

			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to retrieve policy set")

		return reconcile.Result{}, err
	}

	log.V(1).Info("Policy set was found, processing it")

	originalInstance := instance.DeepCopy()
	setNeedsUpdate := r.processPolicySet(ctx, instance)

	if setNeedsUpdate {
		log.Info("Status update needed")

		err := r.Status().Patch(ctx, instance, client.MergeFrom(originalInstance))
		if err != nil {
			log.Error(err, "Failed to update policy set status")

			return reconcile.Result{}, err
		}
	}

	log.Info("Policy set successfully processed, reconcile complete.")

	r.Recorder.Event(
		instance,
		"Normal",
		fmt.Sprintf("policySet: %s", instance.GetName()),
		fmt.Sprintf("Status successfully updated for policySet %s in namespace %s", instance.GetName(),
			instance.GetNamespace()),
	)

	return reconcile.Result{}, nil
}

// processPolicySet compares the status of a policyset to its desired state and determines whether an update is needed
func (r *PolicySetReconciler) processPolicySet(ctx context.Context, plcSet *policyv1beta1.PolicySet) bool {
	log.V(1).Info("Processing policy sets")

	needsUpdate := false

	// compile results and compliance state from policy statuses
	compliancesFound := []string{}
	deletedPolicies := []string{}
	unknownPolicies := []string{}
	disabledPolicies := []string{}
	pendingPolicies := []string{}
	aggregatedCompliance := policyv1.Compliant
	placementsByBinding := map[string]policyv1beta1.PolicySetStatusPlacement{}

	// if there are no policies in the policyset, status should be empty
	if len(plcSet.Spec.Policies) == 0 {
		builtStatus := policyv1beta1.PolicySetStatus{}

		if !equality.Semantic.DeepEqual(plcSet.Status, builtStatus) {
			plcSet.Status = *builtStatus.DeepCopy()

			return true
		}

		return false
	}

	for i := range plcSet.Spec.Policies {
		childPlcName := plcSet.Spec.Policies[i]
		childNamespacedName := types.NamespacedName{
			Name:      string(childPlcName),
			Namespace: plcSet.Namespace,
		}

		childPlc := &policyv1.Policy{}

		err := r.Client.Get(ctx, childNamespacedName, childPlc)
		if err != nil {
			// policy does not exist, log error message and generate event
			var errMessage string
			if errors.IsNotFound(err) {
				errMessage = string(childPlcName) + " not found"
			} else {
				split := strings.Split(err.Error(), "Policy.policy.open-cluster-management.io ")
				if len(split) < 2 {
					errMessage = err.Error()
				} else {
					errMessage = split[1]
				}
			}

			log.V(2).Info(errMessage)

			r.Recorder.Event(plcSet, "Warning", "PolicyNotFound",
				fmt.Sprintf(
					"Policy %s is in PolicySet %s but could not be found in namespace %s",
					childPlcName,
					plcSet.GetName(),
					plcSet.GetNamespace(),
				),
			)

			deletedPolicies = append(deletedPolicies, string(childPlcName))
		} else {
			// aggregate placements
			for _, placement := range childPlc.Status.Placement {
				if placement.PolicySet == plcSet.GetName() {
					placementsByBinding[placement.PlacementBinding] = plcPlacementToSetPlacement(*placement)
				}
			}

			if childPlc.Spec.Disabled {
				// policy is disabled, do not process compliance
				disabledPolicies = append(disabledPolicies, string(childPlcName))

				continue
			}

			// create list of all relevant clusters
			clusters := []string{}
			for pbName := range placementsByBinding {
				pbNamespacedName := types.NamespacedName{
					Name:      pbName,
					Namespace: plcSet.Namespace,
				}

				pb := &policyv1.PlacementBinding{}

				err := r.Client.Get(ctx, pbNamespacedName, pb)
				if err != nil {
					log.V(1).Info("Error getting placement binding " + pbName)
				}

				var decisions []appsv1.PlacementDecision
				decisions, err = getDecisions(r.Client, *pb, childPlc)
				if err != nil {
					log.Error(err, "Error getting placement decisions for binding "+pbName)
				}

				for _, decision := range decisions {
					clusters = append(clusters, decision.ClusterName)
				}
			}

			// aggregate compliance state
			plcComplianceState := complianceInRelevantClusters(childPlc.Status.Status, clusters)
			if plcComplianceState == "" {
				unknownPolicies = append(unknownPolicies, string(childPlcName))
			} else {
				if plcComplianceState == policyv1.Pending {
					pendingPolicies = append(pendingPolicies, string(childPlcName))
					if aggregatedCompliance != policyv1.NonCompliant {
						aggregatedCompliance = policyv1.Pending
					}
				} else {
					compliancesFound = append(compliancesFound, string(childPlcName))
					if plcComplianceState == policyv1.NonCompliant {
						aggregatedCompliance = policyv1.NonCompliant
					}
				}
			}
		}
	}

	generatedPlacements := []policyv1beta1.PolicySetStatusPlacement{}
	for _, pcmt := range placementsByBinding {
		generatedPlacements = append(generatedPlacements, pcmt)
	}

	builtStatus := policyv1beta1.PolicySetStatus{
		Placement:     generatedPlacements,
		StatusMessage: getStatusMessage(disabledPolicies, unknownPolicies, deletedPolicies, pendingPolicies),
	}
	if showCompliance(compliancesFound, unknownPolicies, pendingPolicies) {
		builtStatus.Compliant = string(aggregatedCompliance)
	}

	if !equality.Semantic.DeepEqual(plcSet.Status, builtStatus) {
		plcSet.Status = *builtStatus.DeepCopy()
		needsUpdate = true
	}

	return needsUpdate
}

// getStatusMessage returns a message listing disabled, deleted and policies with no status
func getStatusMessage(
	disabledPolicies []string,
	unknownPolicies []string,
	deletedPolicies []string,
	pendingPolicies []string,
) string {
	statusMessage := ""
	separator := ""
	allReporting := true

	if len(pendingPolicies) > 0 {
		allReporting = false
		statusMessage += fmt.Sprintf("Policies awaiting pending dependencies: %s",
			strings.Join(pendingPolicies, ", "))
		separator = "; "
	}

	if len(disabledPolicies) > 0 {
		allReporting = false
		statusMessage += fmt.Sprintf(separator+"Disabled policies: %s", strings.Join(disabledPolicies, ", "))
		separator = "; "
	}

	if len(unknownPolicies) > 0 {
		allReporting = false
		statusMessage += fmt.Sprintf(separator+"No status provided while awaiting policy status: %s",
			strings.Join(unknownPolicies, ", "))
		separator = "; "
	}

	if len(deletedPolicies) > 0 {
		allReporting = false
		statusMessage += fmt.Sprintf(separator+"Deleted policies: %s", strings.Join(deletedPolicies, ", "))
	}

	if allReporting {
		return "All policies are reporting status"
	}

	return statusMessage
}

// showCompliance only if there are policies with compliance and none are still awaiting status
func showCompliance(compliancesFound []string, unknown []string, pending []string) bool {
	if len(unknown) > 0 {
		return false
	}

	if len(compliancesFound)+len(pending) > 0 {
		return true
	}

	return false
}

// getDecisions gets the PlacementDecisions for a PlacementBinding
func getDecisions(c client.Client, pb policyv1.PlacementBinding,
	instance *policyv1.Policy,
) ([]appsv1.PlacementDecision, error) {
	if pb.PlacementRef.APIGroup == appsv1.SchemeGroupVersion.Group &&
		pb.PlacementRef.Kind == "PlacementRule" {
		d, err := common.GetApplicationPlacementDecisions(c, pb, instance, log)
		if err != nil {
			return nil, err
		}

		return d, nil
	} else if pb.PlacementRef.APIGroup == clusterv1beta1.SchemeGroupVersion.Group &&
		pb.PlacementRef.Kind == "Placement" {
		d, err := common.GetClusterPlacementDecisions(c, pb, instance, log)
		if err != nil {
			return nil, err
		}

		return d, nil
	}

	return nil, fmt.Errorf("placement binding %s/%s reference is not valid", pb.Name, pb.Namespace)
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicySetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(
			&policyv1beta1.PolicySet{},
			builder.WithPredicates(policySetPredicateFuncs)).
		Watches(
			&source.Kind{Type: &policyv1.Policy{}},
			handler.EnqueueRequestsFromMapFunc(policyMapper(mgr.GetClient())),
			builder.WithPredicates(policyPredicateFuncs)).
		Watches(
			&source.Kind{Type: &policyv1.PlacementBinding{}},
			handler.EnqueueRequestsFromMapFunc(placementBindingMapper(mgr.GetClient())),
			builder.WithPredicates(pbPredicateFuncs)).
		Watches(
			&source.Kind{Type: &appsv1.PlacementRule{}},
			handler.EnqueueRequestsFromMapFunc(placementRuleMapper(mgr.GetClient()))).
		Watches(
			&source.Kind{Type: &clusterv1beta1.PlacementDecision{}},
			handler.EnqueueRequestsFromMapFunc(placementDecisionMapper(mgr.GetClient()))).
		Complete(r)
}

// Helper function to filter out compliance statuses that are not in scope
func complianceInRelevantClusters(
	status []*policyv1.CompliancePerClusterStatus,
	relevantClusters []string,
) policyv1.ComplianceState {
	complianceFound := false
	compliance := policyv1.Compliant

	for i := range status {
		if clusterInList(relevantClusters, status[i].ClusterName) {
			if status[i].ComplianceState == policyv1.NonCompliant {
				compliance = policyv1.NonCompliant
				complianceFound = true
			} else if status[i].ComplianceState == policyv1.Pending {
				complianceFound = true
				if compliance != policyv1.NonCompliant {
					compliance = policyv1.Pending
				}
			} else if status[i].ComplianceState != "" {
				complianceFound = true
			}
		}
	}

	if complianceFound {
		return compliance
	}

	return ""
}

// helper function to check whether a cluster is in a list of clusters
func clusterInList(list []string, cluster string) bool {
	for _, item := range list {
		if item == cluster {
			return true
		}
	}

	return false
}

// Helper function to convert policy placement to policyset placement
func plcPlacementToSetPlacement(plcPlacement policyv1.Placement) policyv1beta1.PolicySetStatusPlacement {
	return policyv1beta1.PolicySetStatusPlacement{
		PlacementBinding: plcPlacement.PlacementBinding,
		Placement:        plcPlacement.Placement,
		PlacementRule:    plcPlacement.PlacementRule,
	}
}
