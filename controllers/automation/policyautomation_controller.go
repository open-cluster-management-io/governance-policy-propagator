// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package automation

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	policyv1beta1 "open-cluster-management.io/governance-policy-propagator/api/v1beta1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/controllers/propagator"
)

const ControllerName string = "policy-automation"

var log = ctrl.Log.WithName(ControllerName)

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policyautomations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policyautomations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policyautomations/finalizers,verbs=update
//+kubebuilder:rbac:groups=tower.ansible.com,resources=ansiblejobs,verbs=get;list;watch;create;update;patch;delete;deletecollection

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyAutomationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		Watches(
			&source.Kind{Type: &policyv1.Policy{}},
			&common.EnqueueRequestsFromMapFunc{ToRequests: policyMapper(mgr.GetClient())},
			builder.WithPredicates(policyPredicateFuncs)).
		For(
			&policyv1beta1.PolicyAutomation{},
			builder.WithPredicates(policyAuomtationPredicateFuncs)).
		Complete(r)
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &PolicyAutomationReconciler{}

// PolicyAutomationReconciler reconciles a PolicyAutomation object
type PolicyAutomationReconciler struct {
	client.Client
	DynamicClient dynamic.Interface
	Scheme        *runtime.Scheme
	Recorder      record.EventRecorder
	counter       int
}

// setOwnerReferences will set the input policy as the sole owner of the input policyAutomation and make the update
// with the API. In practice, this will cause the input policyAutomation to be deleted when the policy is deleted.
func (r *PolicyAutomationReconciler) setOwnerReferences(
	ctx context.Context,
	policyAutomation *policyv1beta1.PolicyAutomation,
	policy *policyv1.Policy,
) error {
	var policyOwnerRefFound bool

	for _, ownerRef := range policyAutomation.GetOwnerReferences() {
		if ownerRef.UID == policy.UID {
			policyOwnerRefFound = true

			break
		}
	}

	if !policyOwnerRefFound {
		log.V(3).Info(fmt.Sprintf("Setting the owner reference on the PolicyAutomation %s", policyAutomation.GetName()))
		policyAutomation.SetOwnerReferences([]metav1.OwnerReference{
			*metav1.NewControllerRef(policy, policy.GroupVersionKind()),
		})

		return r.Update(ctx, policyAutomation)
	}

	return nil
}

// getTargetListMap will convert slice targetList to map for search efficiency
func getTargetListMap(targetList []string) map[string]bool {
	targetListMap := map[string]bool{}
	for _, target := range targetList {
		targetListMap[target] = true
	}

	return targetListMap
}

// getClusterName will get the hub cluster name information
func getClusterName() string {
	hubCluster := ""
	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err == nil {
		hubCluster = cfg.Host
	} else {
		log.Error(err, "Unable to get the Kubernetes configuration.")
	}

	if len(hubCluster) > 0 {
		// remove possible https head
		hubCluster = strings.ReplaceAll(hubCluster, "https://", "")
		// remove possible http head
		hubCluster = strings.ReplaceAll(hubCluster, "http://", "")
		// remove possible port number
		hubCluster = strings.SplitN(hubCluster, ":", 2)[0]
		// convert host name to URL if it is IP address
		if net.ParseIP(hubCluster) != nil {
			names, _ := net.LookupAddr(hubCluster)
			if len(names) != 0 {
				hubCluster = names[0]
			}
		}
	}

	log.V(3).Info("Hub Cluster information: %s", hubCluster)

	return hubCluster
}

// getViolationContext will put the root policy information into violationContext
// It also puts the status of the non-compliant replicated policies into violationContext
func (r *PolicyAutomationReconciler) getViolationContext(
	policy *policyv1.Policy,
	targetList []string,
	policyAutomation *policyv1beta1.PolicyAutomation,
) (policyv1beta1.ViolationContext, error) {
	log.V(3).Info(
		"Get the violation context from the root policy %s/%s",
		policy.GetNamespace(),
		policy.GetName(),
	)

	violationContext := policyv1beta1.ViolationContext{}
	// 1) get the target cluster list
	violationContext.TargetClusters = targetList
	// 2) get the root policy name
	violationContext.PolicyName = policy.GetName()
	// 3) get the root policy namespace
	violationContext.PolicyNamespace = policy.GetNamespace()
	// 4) get the root policy hub cluster name
	violationContext.HubCluster = getClusterName()

	// 5) get the policy sets of the root policy
	plcPlacement := policy.Status.Placement
	policySets := []string{}

	for _, placement := range plcPlacement {
		if placement.PolicySet != "" {
			policySets = append(policySets, placement.PolicySet)
		}
	}

	violationContext.PolicySets = policySets

	// skip policy_violation_context if all clusters are compliant
	if len(targetList) == 0 {
		return violationContext, nil
	}

	replicatedPlcList := &policyv1.PolicyList{}

	err := r.List(
		context.TODO(),
		replicatedPlcList,
		client.MatchingLabels(propagator.LabelsForRootPolicy(policy)),
	)
	if err != nil {
		log.Error(err, "Failed to list the replicated policies")

		return violationContext, err
	}

	if len(replicatedPlcList.Items) == 0 {
		log.V(2).Info("The replicated policies cannot be found.")

		return violationContext, nil
	}

	policyViolationContextLimit := policyAutomation.Spec.Automation.PolicyViolationContextLimit
	if policyViolationContextLimit == nil {
		policyViolationContextLimit = new(uint)
		*policyViolationContextLimit = policyv1beta1.DefaultPolicyViolationContextLimit
	}

	contextLimit := int(*policyViolationContextLimit)

	targetListMap := getTargetListMap(targetList)
	violationContext.PolicyViolationContext = make(
		map[string]policyv1beta1.ReplicatedPolicyStatus,
		len(replicatedPlcList.Items),
	)

	// 6) get the status of the non-compliance replicated policies
	for _, rPlc := range replicatedPlcList.Items {
		clusterName := rPlc.GetLabels()[common.ClusterNameLabel]
		if !targetListMap[clusterName] {
			continue // skip the compliance replicated policies
		}

		rPlcStatus := policyv1beta1.ReplicatedPolicyStatus{}
		// Convert PolicyStatus to ReplicatedPolicyStatus and skip the unnecessary items
		err := common.TypeConverter(rPlc.Status, &rPlcStatus)
		if err != nil { // still assign the empty rPlcStatus to PolicyViolationContext later
			log.Error(err, "The PolicyStatus cannot be converted to the type ReplicatedPolicyStatus.")
		}

		// get the latest violation message from the replicated policy
		statusDetails := rPlc.Status.Details
		if len(statusDetails) > 0 && len(statusDetails[0].History) > 0 {
			rPlcStatus.ViolationMessage = statusDetails[0].History[0].Message
		}

		violationContext.PolicyViolationContext[clusterName] = rPlcStatus
		if contextLimit > 0 && len(violationContext.PolicyViolationContext) == contextLimit {
			log.V(2).Info(
				"PolicyViolationContextLimit is %s so skipping %s remaining replicated policies violations.",
				fmt.Sprint(contextLimit),
				fmt.Sprint(len(replicatedPlcList.Items)-contextLimit),
			)

			break
		}
	}

	return violationContext, nil
}

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *PolicyAutomationReconciler) Reconcile(
	ctx context.Context, request ctrl.Request,
) (ctrl.Result, error) {
	log := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	// Fetch the PolicyAutomation instance
	policyAutomation := &policyv1beta1.PolicyAutomation{}

	err := r.Get(ctx, request.NamespacedName, policyAutomation)
	if err != nil {
		if errors.IsNotFound(err) {
			log.V(2).Info("Automation was deleted. Nothing to do.")

			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	if policyAutomation.Spec.PolicyRef == "" {
		log.Info("No policyRef in PolicyAutomation. Will ignore it.")

		return reconcile.Result{}, nil
	}

	log = log.WithValues("policyRef", policyAutomation.Spec.PolicyRef)

	policy := &policyv1.Policy{}

	err = r.Get(ctx, types.NamespacedName{
		Name:      policyAutomation.Spec.PolicyRef,
		Namespace: policyAutomation.GetNamespace(),
	}, policy)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Policy specified in policyRef field not found, may have been deleted, doing nothing")

			return reconcile.Result{}, nil
		}

		log.Error(err, "Failed to retrieve the policy specified in the policyRef field")

		return reconcile.Result{}, err
	}

	err = r.setOwnerReferences(ctx, policyAutomation, policy)
	if err != nil {
		log.Error(err, "Failed to set the owner reference. Will requeue.")

		return reconcile.Result{}, err
	}

	if policyAutomation.Annotations["policy.open-cluster-management.io/rerun"] == "true" {
		targetList := common.FindNonCompliantClustersForPolicy(policy)
		log.Info(
			"Creating an Ansible job", "mode", "manual",
			"clusterCount", strconv.Itoa(len(targetList)))

		violationContext, _ := r.getViolationContext(policy, targetList, policyAutomation)

		err = common.CreateAnsibleJob(
			policyAutomation,
			r.DynamicClient,
			"manual",
			violationContext,
		)
		if err != nil {
			log.Error(err, "Failed to create the Ansible job", "mode", "manual")

			return reconcile.Result{}, err
		}
		// manual run succeeded, remove annotation
		delete(policyAutomation.Annotations, "policy.open-cluster-management.io/rerun")

		err = r.Update(ctx, policyAutomation, &client.UpdateOptions{})
		if err != nil {
			log.Error(err, "Failed to remove the annotation `policy.open-cluster-management.io/rerun`")

			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil
	} else if policyAutomation.Spec.Mode == policyv1beta1.Disabled {
		log.Info("Automation is disabled, doing nothing")

		return reconcile.Result{}, nil
	} else {
		if policy.Spec.Disabled {
			log.Info("The policy is disabled. Doing nothing.")

			return reconcile.Result{}, nil
		}

		if policyAutomation.Spec.Mode == "scan" {
			log := log.WithValues("mode", "scan")
			log.V(2).Info("Triggering scan mode")

			requeueAfter, err := time.ParseDuration(policyAutomation.Spec.RescanAfter)
			if err != nil {
				if policyAutomation.Spec.RescanAfter != "" {
					log.Error(err, "Invalid spec.rescanAfter value")
				}

				return reconcile.Result{RequeueAfter: requeueAfter}, err
			}

			targetList := common.FindNonCompliantClustersForPolicy(policy)
			if len(targetList) > 0 {
				log.Info("Creating An Ansible job", "targetList", targetList)
				violationContext, _ := r.getViolationContext(policy, targetList, policyAutomation)
				err = common.CreateAnsibleJob(policyAutomation, r.DynamicClient, "scan", violationContext)
				if err != nil {
					return reconcile.Result{RequeueAfter: requeueAfter}, err
				}
			} else {
				log.Info("All clusters are compliant. Doing nothing.")
			}

			// no violations found, doing nothing
			r.counter++
			log.V(2).Info(
				"RequeueAfter.", "RequeueAfter", requeueAfter.String(), "Counter", fmt.Sprintf("%d", r.counter),
			)

			return reconcile.Result{RequeueAfter: requeueAfter}, nil
		} else if policyAutomation.Spec.Mode == policyv1beta1.Once {
			log := log.WithValues("mode", string(policyv1beta1.Once))
			targetList := common.FindNonCompliantClustersForPolicy(policy)
			if len(targetList) > 0 {
				log.Info("Creating an Ansible job", "targetList", targetList)

				violationContext, _ := r.getViolationContext(policy, targetList, policyAutomation)
				err = common.CreateAnsibleJob(
					policyAutomation,
					r.DynamicClient,
					string(policyv1beta1.Once),
					violationContext,
				)

				if err != nil {
					log.Error(err, "Failed to create the Ansible job")

					return reconcile.Result{}, err
				}

				policyAutomation.Spec.Mode = policyv1beta1.Disabled

				err = r.Update(ctx, policyAutomation, &client.UpdateOptions{})
				if err != nil {
					log.Error(err, "Failed to update the mode to disabled")

					return reconcile.Result{}, err
				}
			} else {
				log.Info("All clusters are compliant. Doing nothing.")
			}
		} else if policyAutomation.Spec.Mode == policyv1beta1.EveryEvent {
			log := log.WithValues("mode", string(policyv1beta1.EveryEvent))
			targetList := common.FindNonCompliantClustersForPolicy(policy)
			targetListMap := getTargetListMap(targetList)
			// The clusters map that the new ansible job will target
			trimmedTargetMap := map[string]bool{}
			// delayAfterRunSeconds and requeueDuration default value = zero
			delayAfterRunSeconds := policyAutomation.Spec.DelayAfterRunSeconds
			requeueDuration := 0
			requeueFlag := false
			// Automation event time grouped by the cluster name
			eventMap := map[string]policyv1beta1.ClusterEvent{}
			if len(policyAutomation.Status.ClustersWithEvent) > 0 {
				eventMap = policyAutomation.Status.ClustersWithEvent
			}

			now := time.Now().UTC()
			nowStr := now.Format(time.RFC3339)

			for clusterName, clusterEvent := range eventMap {
				originalStartTime, err := time.Parse(time.RFC3339, clusterEvent.AutomationStartTime)
				if err != nil {
					log.Error(err, "Failed to retrieve AutomationStartTime in ClustersWithEvent")
					delete(eventMap, clusterName)
				}

				preEventTime, err := time.Parse(time.RFC3339, clusterEvent.EventTime)
				if err != nil {
					log.Error(err, "Failed to retrieve EventTime in ClustersWithEvent")
					delete(eventMap, clusterName)
				}

				// The time that delayAfterRunSeconds setting expires
				delayUntil := originalStartTime.Add(time.Duration(delayAfterRunSeconds) * time.Second)

				// The policy is non-compliant with the target cluster
				if targetListMap[clusterName] {
					// Policy status changed from non-compliant to compliant
					// then back to non-compliant during the delay period
					if delayAfterRunSeconds > 0 && preEventTime.After(originalStartTime) {
						if now.After(delayUntil) {
							// The delay period passed so remove the previous event
							delete(eventMap, clusterName)
							// Add the cluster name to create a new ansible job
							if !trimmedTargetMap[clusterName] {
								trimmedTargetMap[clusterName] = true
							}
						} else {
							requeueFlag = true
							// Within the delay period and use the earliest requeueDuration to requeue
							if (requeueDuration == 0) || (requeueDuration > int(delayUntil.Sub(now)+1)) {
								requeueDuration = int(delayUntil.Sub(now) + 1)
							}
							// keep the event and update eventTime
							clusterEvent.EventTime = nowStr
							// new event from compliant to non-compliant
							eventMap[clusterName] = clusterEvent
						}
					} // Otherwise, the policy keeps non-compliant since originalStartTime, do nothing
				} else { // The policy is compliant with the target cluster
					if delayAfterRunSeconds > 0 && now.Before(delayUntil) {
						// Within the delay period, keep the event and update eventTime
						clusterEvent.EventTime = nowStr
						// new event from non-compliant to compliant
						eventMap[clusterName] = clusterEvent
					} else { // No delay period or it is expired, remove the event
						delete(eventMap, clusterName)
					}
				}
			}

			for _, clusterName := range targetList {
				if _, ok := eventMap[clusterName]; !ok {
					// Add the non-compliant clusters without previous automation event
					if !trimmedTargetMap[clusterName] {
						trimmedTargetMap[clusterName] = true
					}
				}
			}

			if len(trimmedTargetMap) > 0 {
				trimmedTargetList := []string{}
				for clusterName := range trimmedTargetMap {
					trimmedTargetList = append(trimmedTargetList, clusterName)
				}
				log.Info("Creating An Ansible job", "trimmedTargetList", trimmedTargetList)
				violationContext, _ := r.getViolationContext(policy, trimmedTargetList, policyAutomation)
				err = common.CreateAnsibleJob(
					policyAutomation,
					r.DynamicClient,
					string(policyv1beta1.EveryEvent),
					violationContext,
				)

				if err != nil {
					log.Error(err, "Failed to create the Ansible job")

					return reconcile.Result{}, err
				}

				automationStartTimeStr := time.Now().UTC().Format(time.RFC3339)

				for _, clusterName := range trimmedTargetList {
					eventMap[clusterName] = policyv1beta1.ClusterEvent{
						AutomationStartTime: automationStartTimeStr,
						EventTime:           nowStr,
					}
				}
			} else {
				log.Info("All clusters are compliant. No new Ansible job. Just update ClustersWithEvent.")
			}

			policyAutomation.Status.ClustersWithEvent = eventMap
			// use StatusWriter to update status subresource of a Kubernetes object
			err = r.Status().Update(ctx, policyAutomation, &client.UpdateOptions{})
			if err != nil {
				log.Error(err, "Failed to update ClustersWithEvent in policyAutomation status")

				return reconcile.Result{}, err
			}

			if requeueFlag {
				log.Info(
					"Requeue for the new non-compliant event during the delay period",
					"Delay in seconds", delayAfterRunSeconds,
					"Requeue After", requeueDuration,
				)

				return reconcile.Result{RequeueAfter: time.Duration(requeueDuration)}, nil
			}
		}
	}

	return ctrl.Result{}, nil
}
