// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package encryptionkeys

import (
	"context"
	"crypto/aes"
	"fmt"
	"strings"
	"time"

	"github.com/avast/retry-go/v3"
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

var (
	log                       = ctrl.Log.WithName(ControllerName)
	errLastRotationParseError = fmt.Errorf(`failed to parse the "%s" annotation`, propagator.LastRotatedAnnotation)
	// The number of retries when performing an operation that can fail temporarily and can't be
	// requeued for a retry later. This is not a const so it can be overwritten during the tests.
	retries uint = 4
)

// SetupWithManager sets up the controller with the Manager.
func (r *EncryptionKeysReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// The work queue prevents the same item being reconciled concurrently:
		// https://github.com/kubernetes-sigs/controller-runtime/issues/1416#issuecomment-899833144
		WithOptions(controller.Options{MaxConcurrentReconciles: int(r.MaxConcurrentReconciles)}).
		Named(ControllerName).
		For(&corev1.Secret{}).
		Complete(r)
}

// blank assignment to verify that EncryptionKeysReconciler implements reconcile.Reconciler
var _ reconcile.Reconciler = &EncryptionKeysReconciler{}

// EncryptionKeysReconciler is responsible for rotating the AES encryption key in the "policy-encryption-key" Secrets
// for all managed clusters.
type EncryptionKeysReconciler struct { // nolint:golint,revive
	client.Client
	KeyRotationDays         uint
	MaxConcurrentReconciles uint
	Scheme                  *runtime.Scheme
}

//+kubebuilder:rbac:groups=policy.open-cluster-management.io,resources=policies,verbs=get;list;patch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=create
//+kubebuilder:rbac:groups=core,resources=secrets,resourceNames=policy-encryption-key,verbs=get;list;update;watch

// Reconcile watches all "policy-encryption-key" Secrets on the Hub cluster. This periodically rotates the keys
// and resolves invalid modifications made to the Secret.
func (r *EncryptionKeysReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := log.WithValues("secretNamespace", request.Namespace, "secret", request.Name)
	log.Info("Reconciling a Secret")

	clusterName := request.Namespace

	// The cache configuration of SelectorsByObject should prevent this from happening, but add this as a precaution.
	if request.Name != propagator.EncryptionKeySecret {
		log.Info("Got a reconciliation request for an unexpected Secret. This should have been filtered out.")

		return reconcile.Result{}, nil
	}

	secret := &corev1.Secret{}

	err := r.Get(ctx, request.NamespacedName, secret)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.V(2).Info("The Secret was not found on the server. Doing nothing.")

			return ctrl.Result{}, nil
		}

		log.Error(err, "Failed to get the Secret from the server. Will retry the reconcile request.")

		return ctrl.Result{}, err
	}

	annotations := secret.GetAnnotations()
	if strings.EqualFold(annotations[DisableRotationAnnotation], "true") {
		log.Info(
			"Encountered an encryption key Secret with key rotation disabled. Will trigger a policy template update.",
			"annotation", DisableRotationAnnotation,
			"value", annotations[DisableRotationAnnotation],
		)

		// In case the key was manually rotated, trigger a template update
		r.triggerTemplateUpdate(ctx, clusterName, secret)

		return reconcile.Result{}, nil
	}

	lastRotatedTS := annotations[propagator.LastRotatedAnnotation]
	log = log.WithValues("annotation", propagator.LastRotatedAnnotation, "value", lastRotatedTS)

	var nextRotation time.Duration

	if lastRotatedTS == "" {
		log.Info("The annotation is not set. Will rotate the key now.")

		nextRotation = 0
	} else {
		nextRotation, err = r.getNextRotationFromNow(secret)
		if err != nil {
			log.Error(err, "The annotation cannot be parsed. Will rotate the key now.")
			nextRotation = 0
		}
	}

	_, err = aes.NewCipher(secret.Data["key"])
	if err != nil {
		log.Error(err, "The encryption key in the Secret is invalid. Will rotate the key now.")

		nextRotation = 0
		// Set this to a null value so the bad key doesn't get stored as the previous key
		secret.Data["key"] = []byte{}
	}

	if nextRotation > 0 {
		// previousKey only needs to be checked if there won't be a rotation since it would get overwritten anyways
		if len(secret.Data["previousKey"]) > 0 {
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
			}
		}

		log.V(2).Info("The key is not yet ready to be rotated")

		// Requeueing the same object multiple times is safe as the queue will drop any scheduled
		// requeues in favor of this one
		// https://github.com/kubernetes-sigs/controller-runtime/blob/7ba3e559790c5e3543002a0d9670b4cef3ccf743/pkg/internal/controller/controller.go#L323-L324
		return reconcile.Result{RequeueAfter: nextRotation}, nil
	}

	log.V(1).Info("Rotating the encryption key")

	err = r.rotateKey(ctx, secret)
	if err != nil {
		log.Error(err, "Failed to rotate the encryption key. Will retry the request.")

		return reconcile.Result{}, err
	}

	r.triggerTemplateUpdate(ctx, clusterName, secret)

	// The error is ignored since this can't fail since the annotation value was just set to a valid value
	nextRotation, _ = r.getNextRotationFromNow(secret)

	log.Info("Rotated the encryption key successfully", "nextRotation", nextRotation)

	return reconcile.Result{RequeueAfter: nextRotation}, nil
}

// getNextRotationFromNow will return the duration from now until the next key rotation. An error is
// returned if the last rotated annotation cannot be parsed.
func (r *EncryptionKeysReconciler) getNextRotationFromNow(secret *corev1.Secret) (time.Duration, error) {
	annotations := secret.GetAnnotations()
	lastRotatedTS := annotations[propagator.LastRotatedAnnotation]

	lastRotated, err := time.Parse(time.RFC3339, lastRotatedTS)
	if err != nil {
		return 0, fmt.Errorf(`%w with value "%s": %v`, errLastRotationParseError, lastRotatedTS, err)
	}

	nextRotation := lastRotated.Add(time.Hour * 24 * time.Duration(r.KeyRotationDays))

	return time.Until(nextRotation), nil
}

// rotateKey will generate a new encryption key to replace the "key" field/key on the Secret. The
// old key is stored as the "previousKey" field/key on the Secret. The Secret is then updated
// with the Kubernetes API server. An error is returned if the key can't be generated or the
// update on the API servier fails.
func (r *EncryptionKeysReconciler) rotateKey(ctx context.Context, secret *corev1.Secret) error {
	newKey, err := propagator.GenerateEncryptionKey()
	if err != nil {
		return err
	}

	secret.Data["previousKey"] = secret.Data["key"]
	secret.Data["key"] = newKey

	annotations := secret.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	lastRotatedTS := time.Now().UTC().Format(time.RFC3339)
	annotations[propagator.LastRotatedAnnotation] = lastRotatedTS
	secret.SetAnnotations(annotations)

	return r.Update(ctx, secret)
}

// triggerTemplateUpdate finds all the policies that this managed cluster uses that use encryption.
// It then updates those root policies with the trigger-update annotation to cause the policies to
// be reprocessed with the new key.
func (r *EncryptionKeysReconciler) triggerTemplateUpdate(
	ctx context.Context, clusterName string, secret *corev1.Secret,
) {
	log.Info(
		"Triggering template updates on all the managed cluster policies that use encryption", "cluster", clusterName,
	)

	policies := policyv1.PolicyList{}
	// Get all the policies in the cluster namespace
	err := retry.Do(
		func() error {
			return r.List(ctx, &policies, client.InNamespace(clusterName))
		},
		common.GetRetryOptions(log.V(1), "Retrying to list the managed cluster policy templates", retries+1)...,
	)
	if err != nil {
		log.Error(err, "Failed to trigger all the policies to be reprocessed after the key rotation")

		return
	}

	// Setting this value to something unique for this key rotation ensures the annotation will be updated to
	// a new value and thus trigger an update
	value := fmt.Sprintf("rotate-key-%s-%s", clusterName, secret.Annotations[propagator.LastRotatedAnnotation])
	patch := []byte(`{"metadata":{"annotations":{"` + propagator.TriggerUpdateAnnotation + `":"` + value + `"}}}`)

	for _, policy := range policies.Items {
		// If the policy does not have the initialization vector annotation, then encryption is not
		// used and thus doesn't need to be reprocessed
		if _, ok := policy.Annotations[propagator.IVAnnotation]; !ok {
			continue
		}

		// Find the root policy to patch with the annotation
		rootPlcName := policy.GetLabels()[common.RootPolicyLabel]
		if rootPlcName == "" {
			log.Info(
				"The replicated policy does not have the root policy label set",
				"policy", policy.ObjectMeta.Name,
				"label", common.RootPolicyLabel,
			)

			continue
		}

		name, namespace, err := common.ParseRootPolicyLabel(rootPlcName)
		if err != nil {
			log.Error(err, "Unable to parse name and namespace of root policy, ignoring this replicated policy",
				"rootPlcName", rootPlcName)

			continue
		}

		rootPolicy := &policyv1.Policy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
		}

		err = retry.Do(
			func() error {
				return r.Patch(ctx, rootPolicy, client.RawPatch(types.MergePatchType, patch))
			},
			common.GetRetryOptions(
				log.V(1), "Retrying to trigger the policy templates to be reprocessed", retries+1,
			)...,
		)
		if err != nil {
			log.Error(
				err,
				"Failed to trigger the policy to be reprocessed after the key rotation",
				"policyName",
				rootPlcName,
			)
		}
	}
}
