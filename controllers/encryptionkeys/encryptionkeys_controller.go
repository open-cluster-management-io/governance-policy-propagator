// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package encryptionkeys

import (
	"context"
	"crypto/aes"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	policyv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"open-cluster-management.io/governance-policy-propagator/controllers/common"
	"open-cluster-management.io/governance-policy-propagator/controllers/propagator"
)

const (
	ControllerName = "policy-encryption-keys"
	// This is used for when an administrator prefers to manually generate the encryption keys
	// instead of letting the Policy Propagator handle it.
	DisableRotationAnnotation = "policy.open-cluster-management.io/disable-rotation"
)

// SetupWithManager sets up the controller with the Manager.
func (r *EncryptionKeysReconciler) SetupWithManager(mgr ctrl.Manager, maxConcurrentReconciles uint16) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&corev1.Secret{}).
		// The work queue prevents the same item being reconciled concurrently:
		// https://github.com/kubernetes-sigs/controller-runtime/issues/1416#issuecomment-899833144
		WithOptions(controller.Options{MaxConcurrentReconciles: int(maxConcurrentReconciles)}).
		WithLogConstructor(func(req *reconcile.Request) logr.Logger {
			return common.LogConstructor(ControllerName, "Secret", req)
		}).
		Complete(r)
}

// blank assignment to verify that EncryptionKeysReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &EncryptionKeysReconciler{}

// EncryptionKeysReconciler is responsible for rotating the AES encryption key in the "policy-encryption-key" Secrets
// for all managed clusters.
type EncryptionKeysReconciler struct { //nolint:golint,revive
	client.Client
	KeyRotationDays uint32
	Scheme          *runtime.Scheme
}

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;patch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=create
//+kubebuilder:rbac:groups=core,resources=secrets,resourceNames=policy-encryption-key,verbs=get;list;update;watch

// Reconcile watches all "policy-encryption-key" Secrets on the Hub cluster. This periodically rotates the keys
// and resolves invalid modifications made to the Secret.
func (r *EncryptionKeysReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	clusterName := request.Namespace

	log.Info("Reconciling encryption key secret")

	// The cache configuration of SelectorsByObject should prevent this from happening, but add this as a precaution.
	if request.Name != propagator.EncryptionKeySecret {
		log.Info("Got a reconciliation request for an unexpected Secret. This should have been filtered out.")

		return reconcile.Result{}, nil
	}

	secret := &corev1.Secret{}

	err := r.Get(ctx, request.NamespacedName, secret)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("The Secret was not found on the server. Doing nothing.")

			return ctrl.Result{}, nil
		}

		log.Error(err, "Failed to get the Secret from the server. Will retry the reconcile request.")

		return ctrl.Result{}, err
	}

	log.V(2).Info("Successfully retrieved encryption key secret",
		"resourceVersion", secret.GetResourceVersion(),
		"hasKey", len(secret.Data["key"]) > 0,
		"hasPreviousKey", len(secret.Data["previousKey"]) > 0)

	annotations := secret.GetAnnotations()
	if strings.EqualFold(annotations[DisableRotationAnnotation], "true") {
		log.Info("Encryption key rotation is disabled, triggering policy template update",
			"annotation", DisableRotationAnnotation,
			"value", annotations[DisableRotationAnnotation])

		// In case the key was manually rotated, trigger a template update
		r.triggerTemplateUpdate(ctx, log, clusterName, secret)

		return reconcile.Result{}, nil
	}

	lastRotatedTS := annotations[propagator.LastRotatedAnnotation]
	log = log.WithValues(
		"lastRotated", lastRotatedTS,
		"keyRotationDays", r.KeyRotationDays,
	)

	log.V(2).Info("Checking rotation schedule")

	var nextRotation time.Duration

	if lastRotatedTS == "" {
		log.Info("The last rotated annotation is not set. Will rotate the key now.")

		nextRotation = 0
	} else {
		nextRotation, err = r.getNextRotationFromNow(log, secret)
		if err != nil {
			log.Error(err, "The last rotated annotation cannot be parsed. Will rotate the key now.")

			nextRotation = 0
		} else {
			log.V(2).Info("Calculated next rotation time",
				"nextRotation", nextRotation)
		}
	}

	currentKeySHA256 := sha256.Sum256(secret.Data["key"])

	logWithKey := log.WithValues("key-sha256", string(currentKeySHA256[:]))

	logWithKey.V(3).Info("Validating current encryption key")

	_, err = aes.NewCipher(secret.Data["key"])
	if err != nil {
		log.Error(err, "The encryption key in the Secret is invalid. Will rotate the key now.")

		nextRotation = 0
		// Set this to a null value so the bad key doesn't get stored as the previous key
		secret.Data["key"] = []byte{}
	} else {
		logWithKey.V(3).Info("Current encryption key is valid")
	}

	if nextRotation > 0 {
		log.V(2).Info("Key rotation not needed yet",
			"nextRotation", nextRotation)

		// previousKey only needs to be checked if there won't be a rotation since it would get overwritten anyways
		if len(secret.Data["previousKey"]) > 0 {
			previousKeySHA256 := sha256.Sum256(secret.Data["previousKey"])
			logWithKey = log.WithValues("previousKey-sha256", string(previousKeySHA256[:]))

			logWithKey.V(3).Info("Validating previous encryption key")

			// If previousKey is invalid, it'll cause go-template-utils to fail on the managed cluster, so remove it if
			// a user changed this accidentally
			_, err = aes.NewCipher(secret.Data["previousKey"])
			if err != nil {
				log.Info("The previous encryption key in the Secret is invalid. Will remove it.")

				secret.Data["previousKey"] = []byte{}

				err := r.Update(ctx, secret)
				if err != nil {
					log.Error(err, "Failed to update the Secret. Will retry the request.")

					return reconcile.Result{}, err
				}

				log.V(1).Info("Successfully removed invalid previous key")
			} else {
				logWithKey.V(3).Info("Previous encryption key is valid")
			}
		}

		log.V(2).Info("Scheduling next key rotation",
			"nextRotation", nextRotation)

		// Requeueing the same object multiple times is safe as the queue will drop any scheduled
		// requeues in favor of this one
		// https://github.com/kubernetes-sigs/controller-runtime/blob/7ba3e559790c5e3543002a0d9670b4cef3ccf743/pkg/internal/controller/controller.go#L323-L324
		return reconcile.Result{RequeueAfter: nextRotation}, nil
	}

	log.Info("Starting encryption key rotation")

	err = r.rotateKey(ctx, log, secret)
	if err != nil {
		log.Error(err, "Failed to rotate the encryption key. Will retry the request.")

		return reconcile.Result{}, err
	}

	log.Info("Successfully rotated encryption key")

	r.triggerTemplateUpdate(ctx, log, clusterName, secret)

	// The error is ignored since this can't fail since the annotation value was just set to a valid value
	nextRotation, _ = r.getNextRotationFromNow(log, secret)

	log.Info("Encryption key rotation completed successfully",
		"nextRotation", nextRotation)

	return reconcile.Result{RequeueAfter: nextRotation}, nil
}

// getNextRotationFromNow will return the duration from now until the next key rotation. An error is
// returned if the last rotated annotation cannot be parsed.
func (r *EncryptionKeysReconciler) getNextRotationFromNow(
	log logr.Logger, secret *corev1.Secret,
) (time.Duration, error) {
	annotations := secret.GetAnnotations()
	lastRotatedTS := annotations[propagator.LastRotatedAnnotation]

	log.V(3).Info("Parsing last rotation time",
		"lastRotatedTS", lastRotatedTS,
		"annotation", propagator.LastRotatedAnnotation)

	lastRotated, err := time.Parse(time.RFC3339, lastRotatedTS)
	if err != nil {
		log.Error(err, "Failed to parse last rotation timestamp",
			"lastRotatedTS", lastRotatedTS,
			"format", time.RFC3339)

		return 0, fmt.Errorf(`%w with value "%s": %w`,
			fmt.Errorf(`failed to parse the "%s" annotation`, propagator.LastRotatedAnnotation),
			lastRotatedTS, err)
	}

	nextRotation := lastRotated.Add(time.Hour * 24 * time.Duration(r.KeyRotationDays))
	durationUntilNext := time.Until(nextRotation)

	log.V(3).Info("Calculated rotation schedule",
		"lastRotated", lastRotated.Format(time.RFC3339),
		"nextRotation", nextRotation.Format(time.RFC3339),
		"keyRotationDays", r.KeyRotationDays,
		"durationUntilNext", durationUntilNext)

	return durationUntilNext, nil
}

// rotateKey will generate a new encryption key to replace the "key" field/key on the Secret. The
// old key is stored as the "previousKey" field/key on the Secret. The Secret is then updated
// with the Kubernetes API server. An error is returned if the key can't be generated or the
// update on the API servier fails.
func (r *EncryptionKeysReconciler) rotateKey(ctx context.Context, log logr.Logger, secret *corev1.Secret) error {
	log.V(1).Info("Generating new encryption key")

	newKey, err := propagator.GenerateEncryptionKey()
	if err != nil {
		log.Error(err, "Failed to generate new encryption key")

		return err
	}

	log.V(2).Info("Successfully generated new encryption key",
		"newKeyLength", len(newKey))

	// Store the current key as previous and set the new key
	oldKeyLength := len(secret.Data["key"])
	secret.Data["previousKey"] = secret.Data["key"]
	secret.Data["key"] = newKey

	log.V(2).Info("Updated secret key data",
		"oldKeyLength", oldKeyLength,
		"newKeyLength", len(newKey))

	annotations := secret.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	lastRotatedTS := time.Now().UTC().Format(time.RFC3339)
	annotations[propagator.LastRotatedAnnotation] = lastRotatedTS
	secret.SetAnnotations(annotations)

	log.V(2).Info("Updated rotation annotation",
		"annotation", propagator.LastRotatedAnnotation,
		"lastRotatedTS", lastRotatedTS)

	log.V(1).Info("Updating secret with new encryption key")

	err = r.Update(ctx, secret)
	if err != nil {
		return err
	}

	log.V(1).Info("Successfully updated secret with new encryption key",
		"resourceVersion", secret.GetResourceVersion())

	return nil
}

// triggerTemplateUpdate finds all the policies that this managed cluster uses that use encryption.
// It then updates those root policies with the trigger-update annotation to cause the policies to
// be reprocessed with the new key.
func (r *EncryptionKeysReconciler) triggerTemplateUpdate(
	ctx context.Context, log logr.Logger, clusterName string, secret *corev1.Secret,
) {
	log.V(1).Info("Triggering template updates on all policies that use encryption",
		"cluster", clusterName,
		"rotationTimestamp", secret.Annotations[propagator.LastRotatedAnnotation])

	// Get all the policies in the cluster namespace with timeout
	policies := policyv1.PolicyList{}

	log.V(2).Info("Querying policies in cluster namespace",
		"namespace", clusterName)

	err := r.List(ctx, &policies, client.InNamespace(clusterName))
	if err != nil {
		log.Error(err, "Failed to list policies for encryption key rotation",
			"namespace", clusterName)
	}

	log.V(2).Info("Found policies in cluster namespace",
		"policyCount", len(policies.Items),
		"namespace", clusterName)

	// Setting this value to something unique for this key rotation ensures the annotation will be updated to
	// a new value and thus trigger an update
	value := fmt.Sprintf("rotate-key-%s-%s", clusterName, secret.Annotations[propagator.LastRotatedAnnotation])
	patch := []byte(`{"metadata":{"annotations":{"` + propagator.TriggerUpdateAnnotation + `":"` + value + `"}}}`)

	log.V(3).Info("Prepared patch for template update trigger",
		"triggerValue", value,
		"annotation", propagator.TriggerUpdateAnnotation)

	encryptedPolicyCount := 0
	updatedPolicyCount := 0
	skippedPolicyCount := 0

	for _, policy := range policies.Items {
		policyLog := log.WithValues("policyName", policy.Name)

		// If the policy does not have the initialization vector annotation, then encryption is not
		// used and thus doesn't need to be reprocessed
		if _, ok := policy.Annotations[propagator.IVAnnotation]; !ok {
			policyLog.V(3).Info("Skipping policy without encryption IV annotation",
				"annotation", propagator.IVAnnotation)

			skippedPolicyCount++

			continue
		}

		encryptedPolicyCount++

		policyLog.V(2).Info("Found encrypted policy for template update",
			"hasIVAnnotation", true)

		// Find the root policy to patch with the annotation
		rootPlcName := policy.GetLabels()[common.RootPolicyLabel]
		if rootPlcName == "" {
			policyLog.Info("The replicated policy does not have the root policy label set",
				"label", common.RootPolicyLabel)

			skippedPolicyCount++

			continue
		}

		name, namespace, err := common.ParseRootPolicyLabel(rootPlcName)
		if err != nil {
			policyLog.Error(err, "Unable to parse name and namespace of root policy, ignoring this replicated policy",
				"rootPolicyLabelValue", rootPlcName)

			skippedPolicyCount++

			continue
		}

		policyLog = policyLog.WithValues("rootPolicyName", name, "rootPolicyNamespace", namespace)

		rootPolicy := &policyv1.Policy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
		}

		policyLog.V(2).Info("Patching root policy to trigger template update",
			"triggerValue", value)

		err = r.Patch(ctx, rootPolicy, client.RawPatch(types.MergePatchType, patch))
		if err != nil {
			policyLog.Error(err, "Failed to trigger the policy to be reprocessed after the key rotation")

			continue
		}

		policyLog.V(1).Info("Successfully triggered template update for root policy")

		updatedPolicyCount++
	}

	log.V(1).Info("Completed encryption key rotation",
		"totalPolicies", len(policies.Items),
		"encryptedPolicies", encryptedPolicyCount,
		"updatedPolicies", updatedPolicyCount,
		"skippedPolicies", skippedPolicyCount,
		"cluster", clusterName)
}
