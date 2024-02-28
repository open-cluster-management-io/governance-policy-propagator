// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"open-cluster-management.io/governance-policy-propagator/controllers/complianceeventsapi"
	"open-cluster-management.io/governance-policy-propagator/controllers/propagator"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

var _ = Describe("Test governance-policy-database secret changes, DB annotations, and events", Serial, Ordered, func() {
	const (
		case20PolicyName string = "case20-policy"
		case20PolicyYAML string = "../resources/case20_compliance_api_controller/policy.yaml"
	)

	seededRand := rand.New(rand.NewSource(time.Now().UnixNano()))
	nsName := fmt.Sprintf("case20-%d", seededRand.Int31())

	createCase20Policy := func(ctx context.Context) {
		By("Creating " + case20PolicyName)
		utils.Kubectl("apply", "-f", case20PolicyYAML, "-n", nsName, "--kubeconfig="+kubeconfigHub)
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case20PolicyName, nsName, true, defaultTimeoutSeconds,
		)
		ExpectWithOffset(1, plc).NotTo(BeNil())

		By("Patching the placement with decision of cluster local-cluster")
		pld := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPlacementDecision,
			case20PolicyName,
			nsName,
			true,
			defaultTimeoutSeconds,
		)
		pld.Object["status"] = utils.GeneratePldStatus(pld.GetName(), pld.GetNamespace(), "local-cluster")
		_, err := clientHubDynamic.Resource(gvrPlacementDecision).Namespace(nsName).UpdateStatus(
			ctx, pld, metav1.UpdateOptions{},
		)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())

		By("Waiting for the replicated policy")
		replicatedPolicy := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case20PolicyName, nsName, true, defaultTimeoutSeconds,
		)
		ExpectWithOffset(1, replicatedPolicy).NotTo(BeNil())
	}

	waitForDisabledEvent := func(ctx context.Context, after time.Time) {
		afterStr := after.Format(time.RFC3339Nano)

		By("Waiting for the disabled compliance event after " + afterStr)
		EventuallyWithOffset(1, func(g Gomega) {
			endpoint := fmt.Sprintf(
				"https://localhost:%d/api/v1/compliance-events?cluster.name=local-cluster&event.compliance=Disabled"+
					"&event.timestamp_after=%s&policy.name=%s",
				complianceAPIPort,
				afterStr,
				case20PolicyName,
			)

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			g.Expect(err).ToNot(HaveOccurred())

			req.Header.Set("Authorization", "Bearer "+clientToken)

			resp, err := httpClient.Do(req)
			if err != nil {
				return
			}

			defer resp.Body.Close()

			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))

			body, err := io.ReadAll(resp.Body)
			g.Expect(err).ToNot(HaveOccurred())

			result := map[string]any{}

			err = json.Unmarshal(body, &result)
			g.Expect(err).ToNot(HaveOccurred())

			metadata, ok := result["metadata"].(map[string]interface{})
			g.Expect(ok).To(BeTrue(), "The metadata key was the wrong type")

			g.Expect(metadata["total"]).To(BeEquivalentTo(1))
		}, defaultTimeoutSeconds*2, 1).Should(Succeed())
	}

	BeforeAll(func(ctx context.Context) {
		Expect(clientToken).ToNot(BeEmpty(), "Ensure you use the service account kubeconfig (kubeconfig_hub)")

		By("Creating a random namespace to avoid a cache hit")
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: nsName,
			},
		}
		_, err := clientHub.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func(ctx context.Context) {
		restoreDBConnection(ctx)
	})

	AfterAll(func(ctx context.Context) {
		err := clientHub.CoreV1().Namespaces().Delete(ctx, nsName, metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred())
	})

	It("Adds missing database IDs once database connection is restored", func(ctx context.Context) {
		By("Updating the connection to be invalid")
		bringDownDBConnection(ctx)

		createCase20Policy(ctx)

		By("Getting the replicated policy")
		replicatedPolicy := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case20PolicyName, nsName, true, defaultTimeoutSeconds,
		)
		Expect(replicatedPolicy).NotTo(BeNil())

		annotations := replicatedPolicy.GetAnnotations()
		Expect(annotations[propagator.ParentPolicyIDAnnotation]).To(BeEmpty())
		Expect(annotations[propagator.PolicyIDAnnotation]).To(BeEmpty())

		By("Restoring the database connection")
		Eventually(func(g Gomega) {
			namespacedSecret := clientHub.CoreV1().Secrets("open-cluster-management")
			secret, err := namespacedSecret.Get(
				ctx, complianceeventsapi.DBSecretName, metav1.GetOptions{},
			)
			g.Expect(err).ToNot(HaveOccurred())

			delete(secret.Data, "port")

			_, err = namespacedSecret.Update(ctx, secret, metav1.UpdateOptions{})
			g.Expect(err).ToNot(HaveOccurred())
		}, defaultTimeoutSeconds, 1).Should(Succeed())

		By("Waiting for the replicated policy to have the database ID annotations")
		Eventually(func(g Gomega) {
			replicatedPolicy = utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, nsName+"."+case20PolicyName, "local-cluster", true, defaultTimeoutSeconds,
			)
			g.Expect(replicatedPolicy).NotTo(BeNil())

			annotations = replicatedPolicy.GetAnnotations()
			g.Expect(annotations[propagator.ParentPolicyIDAnnotation]).ToNot(BeEmpty())

			templates, _, _ := unstructured.NestedSlice(replicatedPolicy.Object, "spec", "policy-templates")
			g.Expect(templates).To(HaveLen(1))

			policyID, _, _ := unstructured.NestedString(
				templates[0].(map[string]interface{}),
				"objectDefinition",
				"metadata",
				"annotations",
				propagator.PolicyIDAnnotation,
			)
			g.Expect(policyID).ToNot(BeEmpty())
		}, defaultTimeoutSeconds, 1).Should(Succeed())
	})

	It("Creates a disabled event for local-cluster", func(ctx context.Context) {
		now := time.Now().UTC().Add(-1 * time.Second)

		By("Deleting " + case20PolicyName)
		utils.Kubectl("delete", "-f", case20PolicyYAML, "-n", nsName, "--kubeconfig="+kubeconfigHub)
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case20PolicyName, nsName, false, defaultTimeoutSeconds,
		)
		Expect(plc).To(BeNil())

		waitForDisabledEvent(ctx, now)
	})

	It("Creates a disabled event for local-cluster when the database is down and restored", func(ctx context.Context) {
		createCase20Policy(ctx)

		bringDownDBConnection(ctx)

		now := time.Now().UTC().Add(-1 * time.Second)

		By("Deleting " + case20PolicyName)
		utils.Kubectl("delete", "-f", case20PolicyYAML, "-n", nsName, "--kubeconfig="+kubeconfigHub)
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case20PolicyName, nsName, false, defaultTimeoutSeconds,
		)
		Expect(plc).To(BeNil())

		By("Waiting for the replicated policy to be deleted")
		replicatedPolicy := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case20PolicyName, nsName, false, defaultTimeoutSeconds,
		)
		Expect(replicatedPolicy).To(BeNil())

		restoreDBConnection(ctx)

		waitForDisabledEvent(ctx, now)
	})
})

func bringDownDBConnection(ctx context.Context) {
	By("Setting the port to 12345")
	EventuallyWithOffset(1, func(g Gomega) {
		namespacedSecret := clientHub.CoreV1().Secrets("open-cluster-management")
		secret, err := namespacedSecret.Get(
			ctx, complianceeventsapi.DBSecretName, metav1.GetOptions{},
		)
		g.Expect(err).ToNot(HaveOccurred())

		secret.StringData = map[string]string{"port": "12345"}

		_, err = namespacedSecret.Update(ctx, secret, metav1.UpdateOptions{})
		g.Expect(err).ToNot(HaveOccurred())
	}, defaultTimeoutSeconds, 1).Should(Succeed())

	By("Waiting for the database connection to be down")
	EventuallyWithOffset(1, func(g Gomega) {
		req, err := http.NewRequestWithContext(
			ctx, http.MethodGet, fmt.Sprintf("https://localhost:%d/api/v1/compliance-events/1", complianceAPIPort), nil,
		)
		g.Expect(err).ToNot(HaveOccurred())

		req.Header.Set("Authorization", "Bearer "+clientToken)

		resp, err := httpClient.Do(req)
		if err != nil {
			return
		}

		defer resp.Body.Close()

		g.Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))

		body, err := io.ReadAll(resp.Body)
		g.Expect(err).ToNot(HaveOccurred())

		respJSON := map[string]any{}

		err = json.Unmarshal(body, &respJSON)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(respJSON["message"]).To(Equal("The database is unavailable"))
	}, defaultTimeoutSeconds, 1).Should(Succeed())
}

func restoreDBConnection(ctx context.Context) {
	By("Restoring the database connection")
	EventuallyWithOffset(1, func(g Gomega) {
		namespacedSecret := clientHub.CoreV1().Secrets("open-cluster-management")
		secret, err := namespacedSecret.Get(
			ctx, complianceeventsapi.DBSecretName, metav1.GetOptions{},
		)
		g.Expect(err).ToNot(HaveOccurred())

		if secret.Data["port"] == nil {
			return
		}

		delete(secret.Data, "port")

		_, err = namespacedSecret.Update(ctx, secret, metav1.UpdateOptions{})
		g.Expect(err).ToNot(HaveOccurred())
	}, defaultTimeoutSeconds, 1).Should(Succeed())

	By("Waiting for the database connection to be up")
	EventuallyWithOffset(1, func(g Gomega) {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			fmt.Sprintf("https://localhost:%d/api/v1/compliance-events?per_page=1", complianceAPIPort),
			nil,
		)
		g.Expect(err).ToNot(HaveOccurred())

		req.Header.Set("Authorization", "Bearer "+clientToken)

		resp, err := httpClient.Do(req)
		if err != nil {
			return
		}

		defer resp.Body.Close()

		g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
	}, defaultTimeoutSeconds, 1).Should(Succeed())
}

var _ = Describe("Test compliance events API authentication and authorization", Serial, Ordered, func() {
	eventsEndpoint := fmt.Sprintf("https://localhost:%d/api/v1/compliance-events", complianceAPIPort)
	const saName string = "compliance-api-user"
	var token string

	getSamplePostRequest := func(clusterName string) *bytes.Buffer {
		payload := []byte(fmt.Sprintf(`{
			"cluster": {
				"name": "%s",
				"cluster_id": "%s"
			},
			"parent_policy": {
				"name": "parent-policy",
				"namespace": "%s"
			},
			"policy": {
				"apiGroup": "policy.open-cluster-management.io",
				"kind": "ConfigurationPolicy",
				"name": "etcd-encryption",
				"spec": {"uid": "%s"}
			},
			"event": {
				"compliance": "NonCompliant",
				"message": "configmaps [etcd] not found in namespace default",
				"timestamp": "2023-02-02T02:02:02.222Z"
			}
		}`, clusterName, uuid.New().String(), uuid.New().String(), uuid.New().String()))

		return bytes.NewBuffer(payload)
	}

	BeforeAll(func(ctx context.Context) {
		By("Creating the service account " + saName + " in the namespace" + testNamespace)
		_, err := clientHub.CoreV1().ServiceAccounts(testNamespace).Create(
			ctx,
			&corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: testNamespace,
				},
			},
			metav1.CreateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = clientHub.CoreV1().Secrets(testNamespace).Create(
			ctx,
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: testNamespace,
					Annotations: map[string]string{
						corev1.ServiceAccountNameKey: saName,
					},
				},
				Type: corev1.SecretTypeServiceAccountToken,
			},
			metav1.CreateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())

		By("Granting the service account " + saName + " permission to the cluster namespace " + testNamespace)
		_, err = clientHub.RbacV1().Roles(testNamespace).Create(
			ctx,
			&rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: testNamespace,
				},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{"policy.open-cluster-management.io"},
						Resources: []string{"policies/status"},
						Verbs:     []string{"patch"},
					},
				},
			},
			metav1.CreateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())

		_, err = clientHub.RbacV1().RoleBindings(testNamespace).Create(
			ctx,
			&rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: testNamespace,
				},
				Subjects: []rbacv1.Subject{
					{
						Kind:      "ServiceAccount",
						Name:      saName,
						Namespace: testNamespace,
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "Role",
					Name:     saName,
				},
			},
			metav1.CreateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			secret, err := clientHub.CoreV1().Secrets(testNamespace).Get(ctx, saName, metav1.GetOptions{})
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(secret.Data["token"]).ToNot(BeNil())

			token = string(secret.Data["token"])
		}, defaultTimeoutSeconds, 1).Should(Succeed())
	})

	AfterAll(func(ctx context.Context) {
		By("Deleting the service account")
		err := clientHub.CoreV1().ServiceAccounts(testNamespace).Delete(ctx, saName, metav1.DeleteOptions{})
		if !k8serrors.IsNotFound(err) {
			Expect(err).ToNot(HaveOccurred())
		}

		err = clientHub.CoreV1().Secrets(testNamespace).Delete(ctx, saName, metav1.DeleteOptions{})
		if !k8serrors.IsNotFound(err) {
			Expect(err).ToNot(HaveOccurred())
		}

		err = clientHub.RbacV1().Roles(testNamespace).Delete(ctx, saName, metav1.DeleteOptions{})
		if !k8serrors.IsNotFound(err) {
			Expect(err).ToNot(HaveOccurred())
		}

		err = clientHub.RbacV1().RoleBindings(testNamespace).Delete(ctx, saName, metav1.DeleteOptions{})
		if !k8serrors.IsNotFound(err) {
			Expect(err).ToNot(HaveOccurred())
		}

		By("Deleting all database records")
		connectionURL := "postgresql://grc:grc@localhost:5432/ocm-compliance-history?sslmode=disable"
		db, err := sql.Open("postgres", connectionURL)
		DeferCleanup(func() {
			Expect(db.Close()).To(Succeed())
		})

		Expect(err).ToNot(HaveOccurred())

		_, err = db.ExecContext(ctx, "DELETE FROM compliance_events")
		Expect(err).ToNot(HaveOccurred())
		_, err = db.ExecContext(ctx, "DELETE FROM clusters")
		Expect(err).ToNot(HaveOccurred())
		_, err = db.ExecContext(ctx, "DELETE FROM parent_policies")
		Expect(err).ToNot(HaveOccurred())
		_, err = db.ExecContext(ctx, "DELETE FROM policies")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Rejects recording the compliance event without authentication", func(ctx context.Context) {
		payload := getSamplePostRequest("cluster")

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventsEndpoint, payload)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		Expect(err).ToNot(HaveOccurred())

		if resp != nil {
			defer resp.Body.Close()
		}

		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("Rejects recording the compliance event for the wrong namespace", func(ctx context.Context) {
		payload := getSamplePostRequest("cluster")

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventsEndpoint, payload)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := httpClient.Do(req)
		Expect(err).ToNot(HaveOccurred())

		if resp != nil {
			defer resp.Body.Close()
		}

		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	})

	It("Allows recording the compliance event", func(ctx context.Context) {
		payload := getSamplePostRequest(testNamespace)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventsEndpoint, payload)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := httpClient.Do(req)
		Expect(err).ToNot(HaveOccurred())

		if resp != nil {
			defer resp.Body.Close()
		}

		Expect(resp.StatusCode).To(Equal(http.StatusCreated))
	})

	It("Clears its database ID cache when the database loses data", func(ctx context.Context) {
		By("Creating a compliance event")
		payloadStr := getSamplePostRequest(testNamespace).String()
		payload := bytes.NewBufferString(payloadStr)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventsEndpoint, payload)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := httpClient.Do(req)
		Expect(err).ToNot(HaveOccurred())

		if resp != nil {
			defer resp.Body.Close()
		}

		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		By("Deleting all compliance events and policy references")
		connectionURL := "postgresql://grc:grc@localhost:5432/ocm-compliance-history?sslmode=disable"
		db, err := sql.Open("postgres", connectionURL)
		DeferCleanup(func() {
			Expect(db.Close()).To(Succeed())
		})

		Expect(err).ToNot(HaveOccurred())

		_, err = db.ExecContext(ctx, "DELETE FROM compliance_events")
		Expect(err).ToNot(HaveOccurred())
		_, err = db.ExecContext(ctx, "DELETE FROM parent_policies")
		Expect(err).ToNot(HaveOccurred())
		_, err = db.ExecContext(ctx, "DELETE FROM policies")
		Expect(err).ToNot(HaveOccurred())

		By("Verifying an internal error is returned the first time an invalid ID is provided")
		payload = bytes.NewBufferString(payloadStr)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, eventsEndpoint, payload)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err = httpClient.Do(req)
		Expect(err).ToNot(HaveOccurred())

		if resp != nil {
			defer resp.Body.Close()
		}

		body, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError), fmt.Sprintf("Got response %s", string(body)))

		By("Verifying a success after the cache is cleared")
		payload = bytes.NewBufferString(payloadStr)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, eventsEndpoint, payload)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err = httpClient.Do(req)
		Expect(err).ToNot(HaveOccurred())

		if resp != nil {
			defer resp.Body.Close()
		}

		Expect(resp.StatusCode).To(Equal(http.StatusCreated))
	})
})
