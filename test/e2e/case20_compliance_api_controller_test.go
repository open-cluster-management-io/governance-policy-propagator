// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

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
			ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/api/v1/compliance-events/1", complianceAPIPort), nil,
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
