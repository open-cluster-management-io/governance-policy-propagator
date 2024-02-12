// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	certv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	certutil "k8s.io/client-go/util/cert"

	"open-cluster-management.io/governance-policy-propagator/controllers/complianceeventsapi"
	"open-cluster-management.io/governance-policy-propagator/controllers/propagator"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	case20PolicyName string = "case20-policy"
	case20PolicyYAML string = "../resources/case20_compliance_api_controller/policy.yaml"
)

var _ = Describe("Test governance-policy-database secret changes and DB annotations", Serial, Ordered, func() {
	seededRand := rand.New(rand.NewSource(time.Now().UnixNano()))
	nsName := fmt.Sprintf("case20-%d", seededRand.Int31())

	AfterAll(func(ctx context.Context) {
		Eventually(func(g Gomega) {
			namespacedSecret := clientHub.CoreV1().Secrets("open-cluster-management")
			secret, err := namespacedSecret.Get(
				ctx, complianceeventsapi.DBSecretName, metav1.GetOptions{},
			)
			g.Expect(err).ToNot(HaveOccurred())

			if secret.Data["port"] != nil {
				delete(secret.Data, "port")

				_, err = namespacedSecret.Update(ctx, secret, metav1.UpdateOptions{})
				g.Expect(err).ToNot(HaveOccurred())
			}
		}, defaultTimeoutSeconds, 1).Should(Succeed())

		err := clientHub.CoreV1().Namespaces().Delete(ctx, nsName, metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred())
	})

	It("Updates the connection to be invalid", func(ctx context.Context) {
		bringDownDBConnection(ctx)
	})

	It("Adds missing database IDs once database connection is restored", func(ctx context.Context) {
		By("Creating a random namespace to avoid a cache hit")
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: nsName,
			},
		}
		_, err := clientHub.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("Creating " + case20PolicyName)
		utils.Kubectl("apply", "-f", case20PolicyYAML, "-n", nsName, "--kubeconfig="+kubeconfigHub)
		plc := utils.GetWithTimeout(
			clientHubDynamic, gvrPolicy, case20PolicyName, nsName, true, defaultTimeoutSeconds,
		)
		Expect(plc).NotTo(BeNil())

		By("Patching the placement with decision of cluster managed1")
		pld := utils.GetWithTimeout(
			clientHubDynamic,
			gvrPlacementDecision,
			case20PolicyName,
			nsName,
			true,
			defaultTimeoutSeconds,
		)
		pld.Object["status"] = utils.GeneratePldStatus(pld.GetName(), pld.GetNamespace(), "managed1")
		_, err = clientHubDynamic.Resource(gvrPlacementDecision).Namespace(nsName).UpdateStatus(
			ctx, pld, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for the replicated policy")
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
				clientHubDynamic, gvrPolicy, nsName+"."+case20PolicyName, "managed1", true, defaultTimeoutSeconds,
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

	It("Adds the database IDs by using the cache", func(ctx context.Context) {
		bringDownDBConnection(ctx)

		By("Deleting the replicated policy")
		err := clientHubDynamic.Resource(gvrPolicy).Namespace(nsName).Delete(
			ctx, case20PolicyName, metav1.DeleteOptions{},
		)
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for the replicated policy to have the database ID annotations")
		Eventually(func(g Gomega) {
			replicatedPolicy := utils.GetWithTimeout(
				clientHubDynamic, gvrPolicy, nsName+"."+case20PolicyName, "managed1", true, defaultTimeoutSeconds,
			)
			g.Expect(replicatedPolicy).NotTo(BeNil())

			annotations := replicatedPolicy.GetAnnotations()
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
		}, defaultTimeoutSeconds*2, 1).Should(Succeed())
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

var _ = Describe("Test compliance events API authentication and authorization", Serial, Ordered, func() {
	eventsEndpoint := fmt.Sprintf("https://localhost:%d/api/v1/compliance-events", complianceAPIPort)
	const saName string = "compliance-api-user"
	var token string
	var clientCert tls.Certificate

	getSamplePostRequest := func(clusterName string) *bytes.Buffer {
		payload := []byte(fmt.Sprintf(`{
			"cluster": {
				"name": "%s",
				"cluster_id": "%s"
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
		}`, clusterName, uuid.New().String(), uuid.New().String()))

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

		privateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
		Expect(err).ToNot(HaveOccurred())

		csr, err := certutil.MakeCSR(
			privateKey,
			&pkix.Name{CommonName: fmt.Sprintf("system:serviceaccount:%s:%s", testNamespace, saName)},
			nil,
			nil,
		)
		Expect(err).ToNot(HaveOccurred())

		csrObj, err := clientHub.CertificatesV1().CertificateSigningRequests().Create(
			ctx,
			&certv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name: saName,
				},
				Spec: certv1.CertificateSigningRequestSpec{
					Request:    csr,
					SignerName: certv1.KubeAPIServerClientSignerName,
					Usages: []certv1.KeyUsage{
						certv1.UsageDigitalSignature,
						certv1.UsageKeyEncipherment,
						certv1.UsageClientAuth,
					},
				},
			},
			metav1.CreateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())

		csrObj.Status.Conditions = append(csrObj.Status.Conditions, certv1.CertificateSigningRequestCondition{
			Type:           certv1.CertificateApproved,
			Reason:         "test",
			Message:        "This CSR was approved by the test",
			LastUpdateTime: metav1.Now(),
			Status:         corev1.ConditionTrue,
		})

		_, err = clientHub.CertificatesV1().CertificateSigningRequests().UpdateApproval(
			ctx, saName, csrObj, metav1.UpdateOptions{},
		)
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			csrObj, err = clientHub.CertificatesV1().CertificateSigningRequests().Get(ctx, saName, metav1.GetOptions{})
			g.Expect(csrObj.Status.Certificate).ToNot(BeNil())
		}, defaultTimeoutSeconds, 1).Should(Succeed())

		keyPem := pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
		})

		clientCert, err = tls.X509KeyPair(csrObj.Status.Certificate, keyPem)
		Expect(err).ToNot(HaveOccurred())
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

		err = clientHub.CertificatesV1().CertificateSigningRequests().Delete(ctx, saName, metav1.DeleteOptions{})
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

	It("Rejects recording the compliance event for the wrong namespace (token auth)", func(ctx context.Context) {
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

	It("Rejects recording the compliance event for the wrong namespace (cert auth)", func(ctx context.Context) {
		payload := getSamplePostRequest("cluster")

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventsEndpoint, payload)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")

		httpClient := http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true, Certificates: []tls.Certificate{clientCert}},
			},
		}

		resp, err := httpClient.Do(req)
		Expect(err).ToNot(HaveOccurred())

		if resp != nil {
			defer resp.Body.Close()
		}

		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	})

	It("Rejects recording the compliance event with cert signed by unknown CA (cert auth)", func(ctx context.Context) {
		payload := getSamplePostRequest(testNamespace)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventsEndpoint, payload)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")

		cert, key, err := certutil.GenerateSelfSignedCertKey("test.local", nil, nil)
		Expect(err).ToNot(HaveOccurred())

		badClientCert, err := tls.X509KeyPair(cert, key)
		Expect(err).ToNot(HaveOccurred())

		httpClient := http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true, Certificates: []tls.Certificate{badClientCert}},
			},
		}

		resp, err := httpClient.Do(req)
		Expect(err).ToNot(HaveOccurred())

		if resp != nil {
			defer resp.Body.Close()
		}

		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("Allows recording the compliance event (token auth)", func(ctx context.Context) {
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

	It("Allows recording the compliance event (cert auth)", func(ctx context.Context) {
		payload := getSamplePostRequest(testNamespace)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventsEndpoint, payload)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")

		httpClient := http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true, Certificates: []tls.Certificate{clientCert}},
			},
		}

		resp, err := httpClient.Do(req)
		Expect(err).ToNot(HaveOccurred())

		if resp != nil {
			defer resp.Body.Close()
		}

		Expect(resp.StatusCode).To(Equal(http.StatusCreated))
	})
})
