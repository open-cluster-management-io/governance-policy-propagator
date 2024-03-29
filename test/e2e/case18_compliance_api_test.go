// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"open-cluster-management.io/governance-policy-propagator/controllers/complianceeventsapi"
	"open-cluster-management.io/governance-policy-propagator/test/utils"
)

const (
	eventsEndpoint = "http://localhost:8385/api/v1/compliance-events"
	csvEndpoint    = "http://localhost:8385/api/v1/reports/compliance-events"
)

var httpClient = http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
}

var (
	wrongSAToken  string
	subsetSAToken string
)

const (
	wrongSAYaml         = "../resources/case18_compliance_api_test/wrong_service_account.yaml"
	subsetSAYaml        = "../resources/case18_compliance_api_test/subset_service_account.yaml"
	policiesQueryByName = "SELECT policies.id, policies.kind, policies.api_group, policies.name, " +
		"policies.namespace, policies.severity, specs.spec FROM policies " +
		"LEFT JOIN specs ON policies.spec_id=specs.id WHERE name = $1 "
)

func getTableNames(db *sql.DB) ([]string, error) {
	tableNameRows, err := db.Query("SELECT tablename FROM pg_tables WHERE schemaname = current_schema()")
	if err != nil {
		return nil, err
	} else if tableNameRows.Err() != nil {
		return nil, err
	}

	defer tableNameRows.Close()

	tableNames := []string{}

	for tableNameRows.Next() {
		var tableName string

		err := tableNameRows.Scan(&tableName)
		if err != nil {
			return nil, err
		}

		tableNames = append(tableNames, tableName)
	}

	return tableNames, nil
}

// Note: These tests require a running Postgres server running in the Kind cluster from the "postgres" Make target.
var _ = Describe("Test the compliance events API", Label("compliance-events-api"), Serial, Ordered, func() {
	var k8sConfig *rest.Config
	var k8sClient *kubernetes.Clientset
	var db *sql.DB

	BeforeAll(func(ctx context.Context) {
		var err error

		k8sConfig, err = LoadConfig("", "", "")
		Expect(err).ToNot(HaveOccurred())

		Expect(clientToken).ToNot(BeEmpty(), "Ensure you use the service account kubeconfig (kubeconfig_hub)")

		k8sClient, err = kubernetes.NewForConfig(k8sConfig)
		Expect(err).ToNot(HaveOccurred())

		connectionURL := "postgresql://grc:grc@localhost:5432/ocm-compliance-history?sslmode=disable"
		db, err = sql.Open("postgres", connectionURL)
		DeferCleanup(func() {
			if db == nil {
				return
			}

			Expect(db.Close()).To(Succeed())
		})

		Expect(err).ToNot(HaveOccurred())

		Expect(db.PingContext(ctx)).To(Succeed())

		// Drop all tables to start fresh
		tableNameRows, err := db.Query("SELECT tablename FROM pg_tables WHERE schemaname = current_schema()")
		Expect(err).ToNot(HaveOccurred())

		defer tableNameRows.Close()

		tableNames, err := getTableNames(db)
		Expect(err).ToNot(HaveOccurred())

		for _, tableName := range tableNames {
			_, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+tableName+" CASCADE")
			Expect(err).ToNot(HaveOccurred())
		}

		ctrllog.SetLogger(GinkgoLogr)

		complianceServerCtx, err := complianceeventsapi.NewComplianceServerCtx(connectionURL, "unknown")
		Expect(err).ToNot(HaveOccurred())

		err = complianceServerCtx.MigrateDB(ctx, k8sClient, "open-cluster-management")
		Expect(err).ToNot(HaveOccurred())

		complianceAPI := complianceeventsapi.NewComplianceAPIServer("localhost:8385", k8sConfig, nil)

		httpCtx, httpCtxCancel := context.WithCancel(context.Background())

		go func() {
			defer GinkgoRecover()

			err = complianceAPI.Start(httpCtx, complianceServerCtx)
			Expect(err).ToNot(HaveOccurred())
		}()

		DeferCleanup(func() {
			httpCtxCancel()
		})

		Expect(err).ToNot(HaveOccurred())

		By("Add a new wrong-service account")
		utils.Kubectl("apply", "-f", wrongSAYaml, "--kubeconfig="+kubeconfigHub)
		utils.Kubectl("apply", "-f", subsetSAYaml, "--kubeconfig="+kubeconfigHub)

		wrongSAToken = getToken(ctx, "default", "wrong-sa")
		subsetSAToken = getToken(ctx, "default", "subset-sa")
	})

	Describe("Test the database migrations", func() {
		It("Migrates from a clean database", func(ctx context.Context) {
			tableNames, err := getTableNames(db)
			Expect(err).ToNot(HaveOccurred())
			Expect(tableNames).To(ContainElements(
				"clusters", "parent_policies", "policies", "compliance_events", "specs",
			))

			migrationVersionRows := db.QueryRow("SELECT version, dirty FROM schema_migrations")
			var version int
			var dirty bool
			err = migrationVersionRows.Scan(&version, &dirty)
			Expect(err).ToNot(HaveOccurred())
			Expect(version).To(Equal(2))
			Expect(dirty).To(BeFalse())
		})
	})

	Describe("Test POSTing Events", func() {
		Describe("POST one valid event with including all the optional fields", func() {
			payload := []byte(`{
				"cluster": {
					"name": "managed1",
					"cluster_id": "test1-managed1-fake-uuid-1"
				},
				"parent_policy": {
					"name": "etcd-encryption1",
					"namespace": "policies",
					"categories": ["cat-1", "cat-2"],
					"controls": ["ctrl-1"],
					"standards": ["stand-1"]
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "etcd-encryption1",
					"namespace": "local-cluster",
					"spec": {"test": "one", "severity": "low"},
					"severity": "low"
				},
				"event": {
					"compliance": "NonCompliant",
					"message": "configmaps [etcd] not found in namespace default",
					"timestamp": "2023-01-01T01:01:01.111Z",
					"metadata": {"test": true},
					"reported_by": "optional-test"
				}
			}`)

			BeforeAll(func(ctx context.Context) {
				By("POST the event")
				Eventually(postEvent(ctx, payload, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
			})

			It("Should have created the cluster in a table", func() {
				rows, err := db.Query("SELECT * FROM clusters WHERE cluster_id = $1", "test1-managed1-fake-uuid-1")
				Expect(err).ToNot(HaveOccurred())

				count := 0
				for rows.Next() {
					var (
						id        int
						name      string
						clusterID string
					)
					err := rows.Scan(&id, &name, &clusterID)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					Expect(name).To(Equal("managed1"))
					count++
				}

				Expect(count).To(Equal(1))
			})

			It("Should have created the parent policy in a table", func() {
				rows, err := db.Query(
					"SELECT * FROM parent_policies WHERE name = $1 AND namespace= $2", "etcd-encryption1", "policies",
				)
				Expect(err).ToNot(HaveOccurred())

				count := 0
				for rows.Next() {
					var (
						id        int
						name      string
						namespace string
						cats      pq.StringArray
						ctrls     pq.StringArray
						stands    pq.StringArray
					)

					err := rows.Scan(&id, &name, &namespace, &cats, &ctrls, &stands)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					Expect(cats).To(ContainElements("cat-1", "cat-2"))
					Expect(ctrls).To(ContainElements("ctrl-1"))
					Expect(stands).To(ContainElements("stand-1"))
					count++
				}

				Expect(count).To(Equal(1))
			})

			It("Should have created the policy in a table", func() {
				rows, err := db.Query(policiesQueryByName, "etcd-encryption1")
				Expect(err).ToNot(HaveOccurred())

				count := 0
				for rows.Next() {
					var (
						id       int
						kind     string
						apiGroup string
						name     string
						ns       *string
						severity *string
						spec     complianceeventsapi.JSONMap
					)

					err := rows.Scan(&id, &kind, &apiGroup, &name, &ns, &severity, &spec)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					Expect(kind).To(Equal("ConfigurationPolicy"))
					Expect(apiGroup).To(Equal("policy.open-cluster-management.io"))
					Expect(ns).ToNot(BeNil())
					Expect(*ns).To(Equal("local-cluster"))
					Expect(spec).ToNot(BeNil())
					Expect(spec).To(BeEquivalentTo(map[string]any{"test": "one", "severity": "low"}))
					Expect(severity).ToNot(BeNil())
					Expect(*severity).To(Equal("low"))

					count++
				}

				Expect(count).To(Equal(1))
			})

			It("Should have created the event in a table", func() {
				rows, err := db.Query("SELECT * FROM compliance_events WHERE timestamp = $1",
					"2023-01-01T01:01:01.111Z")
				Expect(err).ToNot(HaveOccurred())

				count := 0
				for rows.Next() {
					var (
						id             int
						clusterID      int
						policyID       int
						parentPolicyID *int
						compliance     string
						message        string
						timestamp      string
						metadata       complianceeventsapi.JSONMap
						reportedBy     *string
						messageHash    string
					)

					err := rows.Scan(&id, &clusterID, &policyID, &parentPolicyID, &compliance, &message, &timestamp,
						&metadata, &reportedBy, &messageHash)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).To(Equal(1))
					Expect(clusterID).To(Equal(1))
					Expect(policyID).To(Equal(1))
					Expect(parentPolicyID).NotTo(BeNil())
					Expect(*parentPolicyID).To(Equal(1))
					Expect(compliance).To(Equal("NonCompliant"))
					Expect(message).To(Equal("configmaps [etcd] not found in namespace default"))
					Expect(timestamp).To(Equal("2023-01-01T01:01:01.111Z"))
					Expect(metadata).To(HaveKeyWithValue("test", true))
					Expect(messageHash).To(Equal("3feb697b1df4585ef4ac1623ca233a34b2a2ea84"))
					Expect(reportedBy).ToNot(BeNil())
					Expect(*reportedBy).To(Equal("optional-test"))
					count++
				}

				Expect(count).To(Equal(1))
			})

			It("Should return the compliance event from the API", func(ctx context.Context) {
				respJSON, err := listEvents(ctx, clientToken)
				Expect(err).ToNot(HaveOccurred())

				complianceEvent := map[string]any{
					"cluster": map[string]any{
						"cluster_id": "test1-managed1-fake-uuid-1",
						"name":       "managed1",
					},
					"event": map[string]any{
						"compliance":  "NonCompliant",
						"message":     "configmaps [etcd] not found in namespace default",
						"metadata":    map[string]any{"test": true},
						"reported_by": "optional-test",
						"timestamp":   "2023-01-01T01:01:01.111Z",
					},
					"id": float64(1),
					"parent_policy": map[string]any{
						"categories": []any{"cat-1", "cat-2"},
						"controls":   []any{"ctrl-1"},
						"id":         float64(1),
						"name":       "etcd-encryption1",
						"namespace":  "policies",
						"standards":  []any{"stand-1"},
					},
					"policy": map[string]any{
						"apiGroup":  "policy.open-cluster-management.io",
						"id":        float64(1),
						"kind":      "ConfigurationPolicy",
						"name":      "etcd-encryption1",
						"namespace": "local-cluster",
						"severity":  "low",
					},
				}

				expected := map[string]any{
					"data": []any{complianceEvent},
					"metadata": map[string]any{
						"page":     float64(1),
						"pages":    float64(1),
						"per_page": float64(20),
						"total":    float64(1),
					},
				}

				Expect(respJSON).To(Equal(expected))

				// Get just the single compliance event
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsEndpoint+"/1", nil)
				Expect(err).ToNot(HaveOccurred())

				// Set auth token
				req.Header.Set("Authorization", "Bearer "+clientToken)

				resp, err := httpClient.Do(req)
				Expect(err).ToNot(HaveOccurred())

				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())

				respJSON = map[string]any{}

				err = json.Unmarshal(body, &respJSON)
				Expect(err).ToNot(HaveOccurred())

				complianceEvent["policy"].(map[string]any)["spec"] = map[string]any{
					"severity": "low",
					"test":     "one",
				}

				Expect(respJSON).To(Equal(complianceEvent))
			})

			It("Should return the compliance event with the spec from the API", func(ctx context.Context) {
				respJSON, err := listEvents(ctx, clientToken, "include_spec")
				Expect(err).ToNot(HaveOccurred())

				data := respJSON["data"].([]any)
				Expect(data).To(HaveLen(1))

				spec := data[0].(map[string]any)["policy"].(map[string]any)["spec"]
				expected := map[string]any{"test": "one", "severity": "low"}

				Expect(spec).To(Equal(expected))
			})
		})

		Describe("POST two minimally-valid events on different clusters and policies", func() {
			payload1 := []byte(`{
				"cluster": {
					"name": "managed2",
					"cluster_id": "test2-managed2-fake-uuid-2"
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "etcd-encryption2",
					"spec": {"test": "two"}
				},
				"event": {
					"compliance": "NonCompliant",
					"message": "configmaps [etcd] not found in namespace default",
					"timestamp": "2023-02-02T02:02:02.222Z"
				}
			}`)

			payload2 := []byte(`{
				"cluster": {
					"name": "managed3",
					"cluster_id": "test2-managed3-fake-uuid-3"
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "etcd-encryption2",
					"spec": {"different-spec-test": "two-and-a-half"}
				},
				"event": {
					"compliance": "Compliant",
					"message": "configmaps [etcd] found in namespace default",
					"timestamp": "2023-02-02T02:02:02.223Z"
				}
			}`)

			BeforeAll(func(ctx context.Context) {
				By("POST the events")
				Eventually(postEvent(ctx, payload1, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
				Eventually(postEvent(ctx, payload2, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
			})

			It("Should have created both clusters in a table", func() {
				rows, err := db.Query("SELECT * FROM clusters")
				Expect(err).ToNot(HaveOccurred())

				clusternames := make([]string, 0)

				for rows.Next() {
					var (
						id        int
						name      string
						clusterID string
					)
					err := rows.Scan(&id, &name, &clusterID)
					Expect(err).ToNot(HaveOccurred())

					clusternames = append(clusternames, name)
				}

				Expect(clusternames).To(ContainElements("managed2", "managed3"))
			})

			It("Should have created two policies in a table despite having the same name", func() {
				rows, err := db.Query(policiesQueryByName, "etcd-encryption2")
				Expect(err).ToNot(HaveOccurred())

				rowCount := 0

				for rows.Next() {
					var (
						id       int
						kind     string
						apiGroup string
						name     string
						ns       *string
						severity *string
						spec     complianceeventsapi.JSONMap
					)

					err := rows.Scan(&id, &kind, &apiGroup, &name, &ns, &severity, &spec)
					Expect(err).ToNot(HaveOccurred())

					rowCount++
					Expect(id).To(Equal(1 + rowCount))
				}

				Expect(rowCount).To(Equal(2))
			})

			It("Should have created both events in a table", func() {
				rows, err := db.Query("SELECT * FROM compliance_events WHERE timestamp > $1",
					"2023-02-02T02:02:02.221Z")
				Expect(err).ToNot(HaveOccurred())

				messages := make([]string, 0)
				for rows.Next() {
					var (
						id             int
						clusterID      int
						policyID       int
						parentPolicyID *int
						compliance     string
						message        string
						timestamp      string
						metadata       *string
						reportedBy     *string
						messageHash    string
					)

					err := rows.Scan(&id, &clusterID, &policyID, &parentPolicyID, &compliance, &message, &timestamp,
						&metadata, &reportedBy, &messageHash)
					Expect(err).ToNot(HaveOccurred())

					messages = append(messages, message)

					Expect(id).NotTo(Equal(0))
					Expect(clusterID).NotTo(Equal(0))
					Expect(policyID).To(Equal(1 + len(messages)))
					Expect(parentPolicyID).To(BeNil())
				}

				Expect(messages).To(ConsistOf(
					"configmaps [etcd] found in namespace default",
					"configmaps [etcd] not found in namespace default",
				))
			})
		})

		Describe("API pagination", func() {
			It("Should have correct default pagination", func(ctx context.Context) {
				respJSON, err := listEvents(ctx, clientToken)
				Expect(err).ToNot(HaveOccurred())

				metadata := respJSON["metadata"].(map[string]interface{})
				Expect(metadata["page"]).To(BeEquivalentTo(1))
				Expect(metadata["pages"]).To(BeEquivalentTo(1))
				Expect(metadata["per_page"]).To(BeEquivalentTo(20))
				Expect(metadata["total"]).To(BeEquivalentTo(3))

				data := respJSON["data"].([]any)
				Expect(data).To(HaveLen(3))
			})
			It("Should have accept page=2", func(ctx context.Context) {
				respJSON, err := listEvents(ctx, clientToken, "page=2")
				Expect(err).ToNot(HaveOccurred())

				metadata := respJSON["metadata"].(map[string]interface{})
				Expect(metadata["page"]).To(BeEquivalentTo(2))
				Expect(metadata["pages"]).To(BeEquivalentTo(1))
				Expect(metadata["per_page"]).To(BeEquivalentTo(20))
				Expect(metadata["total"]).To(BeEquivalentTo(3))

				data := respJSON["data"].([]any)
				Expect(data).To(BeEmpty())
			})

			It("Should accept per_page=2 and page=2", func(ctx context.Context) {
				respJSON, err := listEvents(ctx, clientToken, "per_page=2", "page=2")
				Expect(err).ToNot(HaveOccurred())

				metadata := respJSON["metadata"].(map[string]interface{})
				Expect(metadata["page"]).To(BeEquivalentTo(2))
				Expect(metadata["pages"]).To(BeEquivalentTo(2))
				Expect(metadata["per_page"]).To(BeEquivalentTo(2))
				Expect(metadata["total"]).To(BeEquivalentTo(3))

				data := respJSON["data"].([]any)
				Expect(data).To(HaveLen(1))
				// The default sort is descending order by event timestamp, so the last event in the pagination is
				// the first event.
				Expect(data[0].(map[string]any)["id"]).To(BeEquivalentTo(1))
			})

			It("Should not accept page=150", func(ctx context.Context) {
				// Too many per_page
				_, err := listEvents(ctx, clientToken, "per_page=150", "page=2")
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("per_page must be a value between 1 and 100")))
			})

			It("Should not accept per_page=-5", func(ctx context.Context) {
				// Too little per_page
				_, err := listEvents(ctx, clientToken, "per_page=-5", "page=2")
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("per_page must be a value between 1 and 100")))
			})

			It("Should not accept page=-5", func(ctx context.Context) {
				// Too little per_page
				_, err := listEvents(ctx, clientToken, "page=-5")
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("page must be a positive integer")))
			})
		})

		DescribeTable("API sorting",
			func(ctx context.Context, queryArgs []string, expectedIDs []float64) {
				respJSON, err := listEvents(ctx, clientToken, queryArgs...)
				Expect(err).ToNot(HaveOccurred())

				data, ok := respJSON["data"].([]any)
				Expect(ok).To(BeTrue())
				Expect(data).To(HaveLen(3))

				actualIDs := make([]float64, 0, 3)

				for _, event := range data {
					actualIDs = append(actualIDs, event.(map[string]any)["id"].(float64))
				}

				Expect(actualIDs).To(Equal(expectedIDs))
			},
			Entry(
				"Sort descending by cluster.cluster_id",
				[]string{"sort=cluster.cluster_id", "direction=desc"},
				[]float64{3, 2, 1},
			),
			Entry(
				"Sort ascending by cluster.cluster_id",
				[]string{"sort=cluster.cluster_id", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by cluster.name",
				[]string{"sort=cluster.name", "direction=desc"},
				[]float64{3, 2, 1},
			),
			Entry(
				"Sort ascending by cluster.name",
				[]string{"sort=cluster.name", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by event.compliance",
				[]string{"sort=event.compliance", "direction=desc"},
				[]float64{2, 1, 3},
			),
			Entry(
				"Sort ascending by event.compliance",
				[]string{"sort=event.compliance", "direction=asc"},
				[]float64{3, 1, 2},
			),
			Entry(
				"Sort descending by event.message",
				[]string{"sort=event.message", "direction=desc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort ascending by event.message",
				[]string{"sort=event.message", "direction=asc"},
				[]float64{3, 1, 2},
			),
			Entry(
				"Sort descending by event.reported_by",
				[]string{"sort=event.reported_by", "direction=desc"},
				[]float64{3, 2, 1},
			),
			Entry(
				"Sort ascending by event.reported_by",
				[]string{"sort=event.reported_by", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by event.timestamp (default)",
				[]string{},
				[]float64{3, 2, 1},
			),
			Entry(
				"Sort descending by event.timestamp",
				[]string{"sort=event.timestamp", "direction=desc"},
				[]float64{3, 2, 1},
			),
			Entry(
				"Sort ascending by event.timestamp",
				[]string{"sort=event.timestamp", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by parent_policy.categories",
				[]string{"sort=parent_policy.categories", "direction=desc"},
				[]float64{2, 3, 1},
			),
			Entry(
				"Sort ascending by parent_policy.categories",
				[]string{"sort=parent_policy.categories", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by parent_policy.controls",
				[]string{"sort=parent_policy.controls", "direction=desc"},
				[]float64{2, 3, 1},
			),
			Entry(
				"Sort ascending by parent_policy.controls",
				[]string{"sort=parent_policy.controls", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by parent_policy.id",
				[]string{"sort=parent_policy.id", "direction=desc"},
				[]float64{2, 3, 1},
			),
			Entry(
				"Sort ascending by parent_policy.id",
				[]string{"sort=parent_policy.id", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by parent_policy.name",
				[]string{"sort=parent_policy.name", "direction=desc"},
				[]float64{2, 3, 1},
			),
			Entry(
				"Sort ascending by parent_policy.name",
				[]string{"sort=parent_policy.name", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by parent_policy.namespace",
				[]string{"sort=parent_policy.namespace", "direction=desc"},
				[]float64{2, 3, 1},
			),
			Entry(
				"Sort ascending by parent_policy.namespace",
				[]string{"sort=parent_policy.namespace", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by parent_policy.standards",
				[]string{"sort=parent_policy.standards", "direction=desc"},
				[]float64{2, 3, 1},
			),
			Entry(
				"Sort ascending by parent_policy.standards",
				[]string{"sort=parent_policy.standards", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by policy.apiGroup",
				[]string{"sort=policy.apiGroup", "direction=desc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort ascending by policy.apiGroup",
				[]string{"sort=policy.apiGroup", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by policy.id",
				[]string{"sort=policy.id", "direction=desc"},
				[]float64{3, 2, 1},
			),
			Entry(
				"Sort ascending by policy.id",
				[]string{"sort=policy.id", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by policy.kind",
				[]string{"sort=policy.kind", "direction=desc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort ascending by policy.kind",
				[]string{"sort=policy.kind", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by policy.name",
				[]string{"sort=policy.name", "direction=desc"},
				[]float64{2, 3, 1},
			),
			Entry(
				"Sort ascending by policy.name",
				[]string{"sort=policy.name", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by policy.namespace",
				[]string{"sort=policy.namespace", "direction=desc"},
				[]float64{2, 3, 1},
			),
			Entry(
				"Sort ascending by policy.namespace",
				[]string{"sort=policy.namespace", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by policy.severity",
				[]string{"sort=policy.severity", "direction=desc"},
				[]float64{2, 3, 1},
			),
			Entry(
				"Sort ascending by policy.severity",
				[]string{"sort=policy.severity", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by parent_policy.id and policy.id",
				[]string{"sort=parent_policy.id,policy.id", "direction=asc"},
				[]float64{1, 2, 3},
			),
			Entry(
				"Sort descending by id",
				[]string{"sort=id", "direction=desc"},
				[]float64{3, 2, 1},
			),
			Entry(
				"Sort ascending by id",
				[]string{"sort=id", "direction=asc"},
				[]float64{1, 2, 3},
			),
		)

		Describe("Invalid event ID", func() {
			It("Compliance event is not found", func(ctx context.Context) {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsEndpoint+"/1231291", nil)
				Expect(err).ToNot(HaveOccurred())

				// Set auth token
				req.Header.Set("Authorization", "Bearer "+clientToken)

				resp, err := httpClient.Do(req)
				Expect(err).ToNot(HaveOccurred())

				defer resp.Body.Close()

				Expect(resp.StatusCode).To(Equal(http.StatusNotFound))

				body, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())

				respJSON := map[string]any{}

				err = json.Unmarshal(body, &respJSON)
				Expect(err).ToNot(HaveOccurred())

				Expect(respJSON["message"].(string)).To(Equal("The requested compliance event was not found"))
			})

			It("Compliance event ID is invalid", func(ctx context.Context) {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsEndpoint+"/sql-injections-lose", nil)
				Expect(err).ToNot(HaveOccurred())

				// Set auth token
				req.Header.Set("Authorization", "Bearer "+clientToken)

				resp, err := httpClient.Do(req)
				Expect(err).ToNot(HaveOccurred())

				defer resp.Body.Close()

				Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))

				body, err := io.ReadAll(resp.Body)
				Expect(err).ToNot(HaveOccurred())

				respJSON := map[string]any{}

				err = json.Unmarshal(body, &respJSON)
				Expect(err).ToNot(HaveOccurred())

				Expect(respJSON["message"].(string)).To(Equal("The provided compliance event ID is invalid"))
			})
		})

		Describe("Invalid sort options", func() {
			It("An invalid sort of sort=my-laundry", func(ctx context.Context) {
				_, err := listEvents(ctx, clientToken, "sort=my-laundry")
				Expect(err).To(HaveOccurred())
				expected := "an invalid sort option was provided, choose from: cluster.cluster_id, cluster.name, " +
					"event.compliance, event.message, event.reported_by, event.timestamp, id, " +
					"parent_policy.categories, parent_policy.controls, parent_policy.id, parent_policy.name, " +
					"parent_policy.namespace, parent_policy.standards, policy.apiGroup, policy.id, policy.kind, " +
					"policy.name, policy.namespace, policy.severity"
				Expect(err).To(MatchError(ContainSubstring(expected)))
			})

			It("An invalid sort direction", func(ctx context.Context) {
				_, err := listEvents(ctx, clientToken, "direction=up")
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("direction must be one of: asc, desc")))
			})
		})

		Describe("Invalid query arguments", func() {
			It("An invalid query argument", func(ctx context.Context) {
				_, err := listEvents(ctx, clientToken, "make_it_compliant=please")
				expected := "an invalid query argument was provided, choose from: cluster.cluster_id, cluster.name, " +
					"direction, event.compliance, event.message, event.message_includes, event.message_like, " +
					"event.reported_by, event.timestamp, event.timestamp_after, event.timestamp_before, id, " +
					"include_spec, page, parent_policy.categories, parent_policy.controls, parent_policy.id, " +
					"parent_policy.name, parent_policy.namespace, parent_policy.standards, per_page, " +
					"policy.apiGroup, policy.id, policy.kind, policy.name, policy.namespace, policy.severity, sort"
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring(expected)))
			})

			It("An invalid include_spec=yes-please", func(ctx context.Context) {
				_, err := listEvents(ctx, clientToken, "include_spec=yes-please")
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("include_spec is a flag and does not accept a value")))
			})

			It("An invalid sort direction", func(ctx context.Context) {
				_, err := listEvents(ctx, clientToken, "direction=up")
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("direction must be one of: asc, desc")))
			})
		})

		Describe("POST three events on the same cluster and policy", func() {
			// payload1 defines most things, and should cause the cluster, parent, and policy to be created.
			payload1 := []byte(`{
				"cluster": {
					"name": "managed4",
					"cluster_id": "test3-managed4-fake-uuid-4"
				},
				"parent_policy": {
					"name": "common-parent",
					"namespace": "policies",
					"categories": ["cat-3", "cat-4"],
					"controls": ["ctrl-2"],
					"standards": ["stand-2"]
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common",
					"spec": {"test": "three", "severity": "low"},
					"severity": "low"
				},
				"event": {
					"compliance": "NonCompliant",
					"message": "configmaps [common] not found in namespace default",
					"timestamp": "2023-03-03T03:03:03.333Z"
				}
			}`)

			// payload2 just uses the ids for the policy and parent_policy.
			payload2 := []byte(`{
				"cluster": {
					"name": "managed4",
					"cluster_id": "test3-managed4-fake-uuid-4"
				},
				"parent_policy": {
					"id": 2
				},
				"policy": {
					"id": 4
				},
				"event": {
					"compliance": "NonCompliant",
					"message": "configmaps [common] not found in namespace default",
					"timestamp": "2023-04-04T04:04:04.444Z"
				}
			}`)

			// payload3 redefines most things, and should cause the cluster, parent, and policy to be reused from the
			// cache.
			payload3 := []byte(`{
				"cluster": {
					"name": "managed4",
					"cluster_id": "test3-managed4-fake-uuid-4"
				},
				"parent_policy": {
					"name": "common-parent",
					"namespace": "policies",
					"categories": ["cat-3", "cat-4"],
					"controls": ["ctrl-2"],
					"standards": ["stand-2"]
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common",
					"spec": {"test": "three", "severity": "low"},
					"severity": "low"
				},
				"event": {
					"compliance": "NonCompliant",
					"message": "configmaps [common] not found in namespace default",
					"timestamp": "2023-05-05T05:05:05.555Z"
				}
			}`)

			BeforeAll(func(ctx context.Context) {
				By("POST the events")
				Eventually(postEvent(ctx, payload1, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
				Eventually(postEvent(ctx, payload2, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
				Eventually(postEvent(ctx, payload3, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
			})

			It("Should have only created one cluster in the table", func() {
				rows, err := db.Query("SELECT * FROM clusters WHERE name = $1", "managed4")
				Expect(err).ToNot(HaveOccurred())

				count := 0
				for rows.Next() {
					var (
						id        int
						name      string
						clusterID string
					)
					err := rows.Scan(&id, &name, &clusterID)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					count++
				}

				Expect(count).To(Equal(1))
			})

			It("Should have only created one parent policy in a table", func() {
				rows, err := db.Query(
					"SELECT * FROM parent_policies WHERE name = $1 AND namespace = $2", "common-parent", "policies",
				)
				Expect(err).ToNot(HaveOccurred())

				count := 0
				for rows.Next() {
					var (
						id        int
						name      string
						namespace string
						cats      pq.StringArray
						ctrls     pq.StringArray
						stands    pq.StringArray
					)

					err := rows.Scan(&id, &name, &namespace, &cats, &ctrls, &stands)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					count++
				}

				Expect(count).To(Equal(1))
			})

			It("Should have only created one policy in a table", func() {
				rows, err := db.Query(policiesQueryByName, "common")
				Expect(err).ToNot(HaveOccurred())

				specs := make([]complianceeventsapi.JSONMap, 0, 1)
				for rows.Next() {
					var (
						id       int
						kind     string
						apiGroup string
						name     string
						ns       *string
						severity *string
						spec     complianceeventsapi.JSONMap
					)

					err := rows.Scan(&id, &kind, &apiGroup, &name, &ns, &severity, &spec)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					specs = append(specs, spec)
				}

				Expect(specs).To(HaveLen(1))
				Expect(specs[0]).To(BeEquivalentTo(map[string]any{"test": "three", "severity": "low"}))
			})

			It("Should have created three events in a table", func() {
				rows, err := db.Query("SELECT * FROM compliance_events WHERE message = $1",
					"configmaps [common] not found in namespace default")
				Expect(err).ToNot(HaveOccurred())

				timestamps := make([]string, 0, 3)
				for rows.Next() {
					var (
						id             int
						clusterID      int
						policyID       int
						parentPolicyID *int
						compliance     string
						message        string
						timestamp      string
						metadata       *string
						reportedBy     *string
						messageHash    string
					)

					err := rows.Scan(&id, &clusterID, &policyID, &parentPolicyID, &compliance, &message, &timestamp,
						&metadata, &reportedBy, &messageHash)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					Expect(clusterID).NotTo(Equal(0))
					Expect(policyID).To(Equal(4))
					Expect(parentPolicyID).NotTo(BeNil())
					Expect(*parentPolicyID).To(Equal(2))

					timestamps = append(timestamps, timestamp)
				}

				Expect(timestamps).To(ConsistOf(
					"2023-03-03T03:03:03.333Z",
					"2023-04-04T04:04:04.444Z",
					"2023-05-05T05:05:05.555Z",
				))
			})
		})

		Describe("POST events to check parent policy matching", func() {
			// payload1 defines most things, and should cause the cluster, parent, and policy to be created.
			payload1 := []byte(`{
				"cluster": {
					"name": "managed5",
					"cluster_id": "test5-managed5-fake-uuid-5"
				},
				"parent_policy": {
					"name": "parent-a",
					"namespace": "policies",
					"standards": ["stand-3"]
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common-a",
					"spec": {"test": "four", "severity": "low"},
					"severity": "low"
				},
				"event": {
					"compliance": "Compliant",
					"message": "configmaps [common] found in namespace default",
					"timestamp": "2023-05-05T05:05:05.555Z"
				}
			}`)

			// payload2 skips the standards array on the parent policy,
			// which should create a new parent policy
			payload2 := []byte(`{
				"cluster": {
					"name": "managed5",
					"cluster_id": "test5-managed5-fake-uuid-5"
				},
				"parent_policy": {
					"name": "parent-a",
					"namespace": "policies"
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common-a",
					"spec": {"test": "four", "severity": "low"},
					"severity": "low"
				},
				"event": {
					"compliance": "Compliant",
					"message": "configmaps [common] found in namespace default",
					"timestamp": "2023-06-06T06:06:06.666Z"
				}
			}`)

			// payload3 defines the standards with an empty array,
			// which should be the same as not specifying it at all (payload2)
			payload3 := []byte(`{
				"cluster": {
					"name": "managed5",
					"cluster_id": "test5-managed5-fake-uuid-5"
				},
				"parent_policy": {
					"name": "parent-a",
					"namespace": "policies",
					"standards": []
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common-a",
					"spec": {"test": "four", "severity": "low"},
					"severity": "low"
				},
				"event": {
					"compliance": "Compliant",
					"message": "configmaps [common] found in namespace default",
					"timestamp": "2023-07-07T07:07:07.777Z"
				}
			}`)

			BeforeAll(func(ctx context.Context) {
				By("POST the events")
				Eventually(postEvent(ctx, payload1, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
				Eventually(postEvent(ctx, payload2, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
				Eventually(postEvent(ctx, payload3, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
			})

			It("Should have created two parent policies", func() {
				rows, err := db.Query(
					"SELECT * FROM parent_policies WHERE name = $1 AND namespace = $2", "parent-a", "policies",
				)
				Expect(err).ToNot(HaveOccurred())

				standardArrays := make([]pq.StringArray, 0)
				for rows.Next() {
					var (
						id        int
						name      string
						namespace string
						cats      pq.StringArray
						ctrls     pq.StringArray
						stands    pq.StringArray
					)

					err := rows.Scan(&id, &name, &namespace, &cats, &ctrls, &stands)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					standardArrays = append(standardArrays, stands)
				}

				Expect(standardArrays).To(ConsistOf(
					pq.StringArray{"stand-3"},
					nil,
				))
			})

			It("Should have created a single policy", func() {
				rows, err := db.Query(policiesQueryByName, "common-a")
				Expect(err).ToNot(HaveOccurred())

				ids := make([]int, 0)
				for rows.Next() {
					var (
						id       int
						kind     string
						apiGroup string
						name     string
						ns       *string
						severity *string
						spec     complianceeventsapi.JSONMap
					)

					err := rows.Scan(&id, &kind, &apiGroup, &name, &ns, &severity, &spec)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					ids = append(ids, id)
				}

				Expect(ids).To(HaveLen(1))
			})
		})

		Describe("POST events to check policy namespace matching", func() {
			// payload1 should cause the cluster, parent, and policy to be created.
			payload1 := []byte(`{
				"cluster": {
					"name": "managed6",
					"cluster_id": "test6-managed6-fake-uuid-6"
				},
				"parent_policy": {
					"name": "parent-b",
					"namespace": "policies"
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common-b",
					"spec": {"test": "four", "severity": "low"},
					"severity": "low",
					"namespace": "default"
				},
				"event": {
					"compliance": "Compliant",
					"message": "configmaps [common] found in namespace default",
					"timestamp": "2023-01-02T03:04:05.111Z"
				}
			}`)

			// payload2 skips the namespace, which should create a new policy
			payload2 := []byte(`{
				"cluster": {
					"name": "managed6",
					"cluster_id": "test6-managed6-fake-uuid-6"
				},
				"parent_policy": {
					"name": "parent-b",
					"namespace": "policies"
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common-b",
					"spec": {"test": "four", "severity": "low"},
					"severity": "low"
				},
				"event": {
					"compliance": "Compliant",
					"message": "configmaps [common] found in namespace default",
					"timestamp": "2023-01-02T03:04:05.222Z"
				}
			}`)

			BeforeAll(func(ctx context.Context) {
				By("POST the events")
				Eventually(postEvent(ctx, payload1, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
				Eventually(postEvent(ctx, payload2, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
			})

			It("Should have created one parent policy", func() {
				rows, err := db.Query(
					"SELECT * FROM parent_policies WHERE name = $1 AND namespace = $2", "parent-b", "policies",
				)
				Expect(err).ToNot(HaveOccurred())

				count := 0
				for rows.Next() {
					var (
						id        int
						name      string
						namespace string
						cats      pq.StringArray
						ctrls     pq.StringArray
						stands    pq.StringArray
					)

					err := rows.Scan(&id, &name, &namespace, &cats, &ctrls, &stands)
					Expect(err).ToNot(HaveOccurred())
					Expect(id).NotTo(Equal(0))
					count++
				}

				Expect(count).To(Equal(1))
			})

			It("Should have created two policies in the table, with different namespaces", func() {
				rows, err := db.Query(policiesQueryByName, "common-b")
				Expect(err).ToNot(HaveOccurred())

				ids := make([]int, 0)
				names := make([]string, 0)
				namespaces := make([]string, 0)
				specs := make([]complianceeventsapi.JSONMap, 0, 2)
				for rows.Next() {
					var (
						id       int
						kind     string
						apiGroup string
						name     string
						ns       *string
						severity *string
						spec     complianceeventsapi.JSONMap
					)

					err := rows.Scan(&id, &kind, &apiGroup, &name, &ns, &severity, &spec)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					ids = append(ids, id)
					names = append(names, name)
					specs = append(specs, spec)

					if ns != nil {
						namespaces = append(namespaces, *ns)
					}
				}

				Expect(ids).To(HaveLen(2))
				Expect(ids[0]).ToNot(Equal(ids[1]))
				Expect(names[0]).To(Equal(names[1]))
				Expect(namespaces).To(ConsistOf("default"))
				Expect(specs[0]).To(Equal(specs[1]))
			})
		})

		Describe("POST invalid events", func() {
			It("should require the cluster to be specified", func(ctx context.Context) {
				Eventually(postEvent(ctx, []byte(`{
					"parent_policy": {
						"name": "validity-parent",
						"namespace": "policies"
					},
					"policy": {
						"apiGroup": "policy.open-cluster-management.io",
						"kind": "ConfigurationPolicy",
						"name": "validity",
						"spec": {"test":"validity", "severity": "low"}
					},
					"event": {
						"compliance": "Compliant",
						"message": "configmaps [valid] valid in namespace valid",
						"timestamp": "2023-09-09T09:09:09.999Z"
					}
				}`), clientToken), "5s", "1s").Should(
					MatchError(ContainSubstring("Got non-201 status code 400")),
				)
			})

			It("should require the parent policy namespace to be specified", func(ctx context.Context) {
				Eventually(postEvent(ctx, []byte(`{
					"cluster": {
						"name": "validity-test",
						"cluster_id": "test-validity-fake-uuid"
					},
					"parent_policy": {
						"name": "validity-parent"
					},
					"policy": {
						"apiGroup": "policy.open-cluster-management.io",
						"kind": "ConfigurationPolicy",
						"name": "validity",
						"spec": {"test":"validity", "severity": "low"},
						"severity": "low"
					},
					"event": {
						"compliance": "Compliant",
						"message": "configmaps [valid] valid in namespace valid",
						"timestamp": "2023-09-09T09:09:09.999Z"
					}
				}`), clientToken), "5s", "1s").Should(
					MatchError(ContainSubstring("Got non-201 status code 400")),
				)
			})

			It("should require the event time to be specified", func(ctx context.Context) {
				Eventually(postEvent(ctx, []byte(`{
					"cluster": {
						"name": "validity-test",
						"cluster_id": "test-validity-fake-uuid"
					},
					"parent_policy": {
						"name": "validity-parent",
						"namespace": "policies"
					},
					"policy": {
						"apiGroup": "policy.open-cluster-management.io",
						"kind": "ConfigurationPolicy",
						"name": "validity",
						"spec": {"test": "validity", "severity": "low"},
						"severity": "low"
					},
					"event": {
						"compliance": "Compliant",
						"message": "configmaps [valid] valid in namespace valid"
					}
				}`), clientToken), "5s", "1s").Should(
					MatchError(ContainSubstring("Got non-201 status code 400")),
				)
			})

			It("should require the parent policy to have fields when specified", func(ctx context.Context) {
				Eventually(postEvent(ctx, []byte(`{
					"cluster": {
						"name": "validity-test",
						"cluster_id": "test-validity-fake-uuid"
					},
					"parent_policy": {},
					"policy": {
						"apiGroup": "policy.open-cluster-management.io",
						"kind": "ConfigurationPolicy",
						"name": "validity",
						"spec": {"test": "validity", "severity": "low"},
						"severity": "low"
					},
					"event": {
						"compliance": "Compliant",
						"message": "configmaps [valid] valid in namespace valid",
						"timestamp": "2023-09-09T09:09:09.999Z"
					}
				}`), clientToken), "5s", "1s").Should(
					MatchError(ContainSubstring("Got non-201 status code 400")),
				)
			})

			It("should require the policy to be defined", func(ctx context.Context) {
				Eventually(postEvent(ctx, []byte(`{
					"cluster": {
						"name": "validity-test",
						"cluster_id": "test-validity-fake-uuid"
					},
					"parent_policy": {
						"name": "validity-parent",
						"namespace": "policies"
					},
					"policy": {},
					"event": {
						"compliance": "Compliant",
						"message": "configmaps [valid] valid in namespace valid",
						"timestamp": "2023-09-09T09:09:09.999Z"
					}
				}`), clientToken), "5s", "1s").Should(
					MatchError(ContainSubstring("Got non-201 status code 400")),
				)
			})

			It("should require the input to be valid JSON", func(ctx context.Context) {
				Eventually(postEvent(ctx, []byte(`{
					foo: bar: baz
					"cluster": {
						"name": "validity-test",
						"cluster_id": "test-validity-fake-uuid"
					},
					"parent_policy": {
						"name": "validity-parent",
						"namespace": "policies"
					},
					"policy": {
						"apiGroup": "policy.open-cluster-management.io",
						"kind": "ConfigurationPolicy",
						"name": "validity",
						"spec": {"test": "validity", "severity": "low"},
						"severity": "low",
						"specHash": "foobar"
					},
					"event": {
						"compliance": "Compliant",
						"message": "configmaps [valid] valid in namespace valid",
						"timestamp": "2023-09-09T09:09:09.999Z"
					}
				}`), clientToken), "5s", "1s").Should(
					MatchError(ContainSubstring("Got non-201 status code 400")),
				)
			})

			It("should require the spec when inputting a new policy", func(ctx context.Context) {
				Eventually(postEvent(ctx, []byte(`{
					"cluster": {
						"name": "validity-test",
						"cluster_id": "test-validity-fake-uuid"
					},
					"parent_policy": {
						"id": 1231234
					},
					"policy": {
						"id": 123123
					},
					"event": {
						"compliance": "Compliant",
						"message": "configmaps [valid] valid in namespace valid",
						"timestamp": "2023-09-09T09:09:09.999Z"
					}
				}`), clientToken), "5s", "1s").Should(MatchError(ContainSubstring(
					`invalid input: parent_policy.id not found\\ninvalid input: policy.id not found`,
				)))
			})
		})

		DescribeTable("API filtering",
			func(ctx context.Context, queryArgs []string, expectedIDs []float64) {
				respJSON, err := listEvents(ctx, clientToken, queryArgs...)
				Expect(err).ToNot(HaveOccurred())

				data, ok := respJSON["data"].([]any)
				Expect(ok).To(BeTrue())

				actualIDs := []float64{}

				for _, event := range data {
					actualIDs = append(actualIDs, event.(map[string]any)["id"].(float64))
				}

				Expect(actualIDs).To(Equal(expectedIDs))
			},
			Entry(
				"Filter by cluster.cluster_id",
				[]string{"cluster.cluster_id=test1-managed1-fake-uuid-1,test6-managed6-fake-uuid-6"},
				[]float64{11, 10, 1},
			),
			Entry(
				"Filter by cluster.name",
				[]string{"cluster.name=managed1,managed6"},
				[]float64{11, 10, 1},
			),
			Entry(
				"Filter by event.compliance",
				[]string{"event.compliance=Compliant"},
				[]float64{9, 8, 7, 3, 11, 10},
			),
			Entry(
				"Filter by event.message",
				[]string{"event.message=configmaps%20%5Bcommon%5D%20not%20found%20in%20namespace%20default"},
				[]float64{6, 5, 4},
			),
			Entry(
				"Filter by event.message_includes",
				[]string{"event.message_includes=etcd"},
				[]float64{3, 2, 1},
			),
			Entry(
				"Filter by event.message_includes and ensure special characters are escaped",
				[]string{"event.message_includes=co_m%25n"},
				[]float64{},
			),
			Entry(
				"Filter by event.message_like",
				[]string{"event.message_like=configmaps%20%5B%25common%25%5D%25"},
				[]float64{9, 8, 6, 7, 5, 4, 11, 10},
			),
			Entry(
				"Filter by event.timestamp",
				[]string{"event.timestamp=2023-01-01T01:01:01.111Z"},
				[]float64{1},
			),
			Entry(
				"Filter by event.timestamp_after",
				[]string{"event.timestamp_after=2023-04-01T01:01:01.111Z"},
				[]float64{9, 8, 7, 6, 5},
			),
			Entry(
				"Filter by event.timestamp_before",
				[]string{"event.timestamp_before=2023-04-01T01:01:01.111Z"},
				[]float64{4, 3, 2, 11, 10, 1},
			),
			Entry(
				"Filter by event.timestamp_after and event.timestamp_before",
				[]string{
					"event.timestamp_after=2023-01-01T01:01:01.111Z", "event.timestamp_before=2023-04-01T01:01:01.111Z",
				},
				[]float64{4, 3, 2, 11, 10},
			),
			Entry(
				"Filter by parent_policy.categories",
				[]string{"parent_policy.categories=cat-1,cat-3"},
				[]float64{6, 5, 4, 1},
			),
			Entry(
				"Filter by parent_policy.categories is null",
				[]string{"parent_policy.categories"},
				[]float64{9, 8, 7, 3, 2, 11, 10},
			),
			Entry(
				"Filter by parent_policy.controls",
				[]string{"parent_policy.controls=ctrl-2"},
				[]float64{6, 5, 4},
			),
			Entry(
				"Filter by parent_policy.controls is null",
				[]string{"parent_policy.controls"},
				[]float64{9, 8, 7, 3, 2, 11, 10},
			),
			Entry(
				"Filter by parent_policy.id",
				[]string{"parent_policy.id=2"},
				[]float64{6, 5, 4},
			),
			Entry(
				"Filter by parent_policy.name",
				[]string{"parent_policy.name=etcd-encryption1"},
				[]float64{1},
			),
			Entry(
				"Filter by parent_policy.namespace",
				[]string{"parent_policy.namespace=policies"},
				[]float64{9, 8, 6, 7, 5, 4, 11, 10, 1},
			),
			Entry(
				"Filter by parent_policy.standards",
				[]string{"parent_policy.standards=stand-2"},
				[]float64{6, 5, 4},
			),
			Entry(
				"Filter by parent_policy.standards is null",
				[]string{"parent_policy.standards"},
				[]float64{9, 8, 3, 2, 11, 10},
			),
			Entry(
				"Filter by policy.apiGroup",
				[]string{"policy.apiGroup=policy.open-cluster-management.io"},
				[]float64{9, 8, 6, 7, 5, 4, 3, 2, 11, 10, 1},
			),
			Entry(
				"Filter by policy.apiGroup no results",
				[]string{"policy.apiGroup=does-not-exist"},
				[]float64{},
			),
			Entry(
				"Filter by policy.id",
				[]string{"policy.id=4"},
				[]float64{6, 5, 4},
			),
			Entry(
				"Filter by policy.kind",
				[]string{"policy.kind=ConfigurationPolicy"},
				[]float64{9, 8, 6, 7, 5, 4, 3, 2, 11, 10, 1},
			),
			Entry(
				"Filter by policy.kind no results",
				[]string{"policy.kind=something-else"},
				[]float64{},
			),
			Entry(
				"Filter by policy.name",
				[]string{"policy.name=common-b"},
				[]float64{11, 10},
			),
			Entry(
				"Filter by policy.namespace",
				[]string{"policy.namespace=default"},
				[]float64{10},
			),
			Entry(
				"Filter by policy.namespace is null",
				[]string{"policy.namespace"},
				[]float64{9, 8, 6, 7, 5, 4, 3, 2, 11},
			),
			Entry(
				"Filter by policy.severity",
				[]string{"policy.severity=low"},
				[]float64{9, 8, 6, 7, 5, 4, 11, 10, 1},
			),
			Entry(
				"Filter by policy.severity is null",
				[]string{"policy.severity"},
				[]float64{3, 2},
			),
		)

		DescribeTable("Invalid API filtering",
			func(ctx context.Context, queryArgs []string, expectedErrMsg string) {
				_, err := listEvents(ctx, clientToken, queryArgs...)
				Expect(err).To(MatchError(ContainSubstring(expectedErrMsg)))
			},
			Entry(
				"Filter by empty event.timestamp_before is invalid",
				[]string{"event.timestamp_before"},
				"invalid query argument: event.timestamp_before must have a value",
			),
			Entry(
				"Filter by invalid event.timestamp_before",
				[]string{"event.timestamp_before=1993"},
				"invalid query argument: event.timestamp_before must be in the format of RFC 3339",
			),
			Entry(
				"Filter by invalid event.timestamp_after",
				[]string{"event.timestamp_after=1993"},
				"invalid query argument: event.timestamp_after must be in the format of RFC 3339",
			),
		)

		Describe("Test the /api/v1/reports/compliance-events endpoint", func() {
			It("should send CSV file in http response", func(ctx context.Context) {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, csvEndpoint, nil)
				Expect(err).ShouldNot(HaveOccurred())

				req.Header.Set("Authorization", "Bearer "+clientToken)

				resp, err := httpClient.Do(req)
				Expect(err).ShouldNot(HaveOccurred())

				defer resp.Body.Close()

				By("Content-type should be CSV")
				Expect(resp.Header.Get("Content-Type")).Should(Equal("text/csv"))
				Expect(resp.TransferEncoding).Should(ContainElement("chunked"))

				csvReader := csv.NewReader(resp.Body)

				records, err := csvReader.ReadAll()
				Expect(err).ShouldNot(HaveOccurred())

				Expect(len(records)).Should(BeNumerically(">", 10))

				By("First line should be the titles")
				Expect(records[0]).Should(ContainElements([]string{
					"compliance_events_id",
					"compliance_events_compliance",
					"compliance_events_message",
					"compliance_events_metadata",
					"compliance_events_reported_by",
					"compliance_events_timestamp",
					"clusters_cluster_id",
					"clusters_name",
					"parent_policies_id",
					"parent_policies_name",
					"parent_policies_namespace",
					"parent_policies_categories",
					"parent_policies_controls",
					"parent_policies_standards",
					"policies_id",
					"policies_api_group",
					"policies_kind",
					"policies_name",
					"policies_namespace",
					"policies_severity",
				}))

				By("All line should have 20 columns")
				for _, r := range records {
					Expect(r).Should(HaveLen(20))
				}
			})
			It("Should return only header when SA does not have any GET verb to managedCluster",
				func(ctx context.Context) {
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, csvEndpoint, nil)
					Expect(err).ShouldNot(HaveOccurred())

					req.Header.Set("Content-Type", "application/json")
					// Set auth token
					req.Header.Set("Authorization", "Bearer "+wrongSAToken)

					resp, err := httpClient.Do(req)
					Expect(err).ShouldNot(HaveOccurred())

					defer resp.Body.Close()

					By("Content-type should be CSV")
					Expect(resp.Header.Get("Content-Type")).Should(Equal("text/csv"))

					csvReader := csv.NewReader(resp.Body)

					records, err := csvReader.ReadAll()
					Expect(err).ShouldNot(HaveOccurred())

					By("Should return only header")
					Expect(records).Should(HaveLen(1))

					Expect(records[0]).Should(ContainElements([]string{
						"compliance_events_id",
						"compliance_events_compliance",
						"compliance_events_message",
						"compliance_events_metadata",
						"compliance_events_reported_by",
						"compliance_events_timestamp",
						"clusters_cluster_id",
						"clusters_name",
						"parent_policies_id",
						"parent_policies_name",
						"parent_policies_namespace",
						"parent_policies_categories",
						"parent_policies_controls",
						"parent_policies_standards",
						"policies_id",
						"policies_api_group",
						"policies_kind",
						"policies_name",
						"policies_namespace",
						"policies_severity",
					}))
				})

			DescribeTable("Should filter CSV file",
				func(ctx context.Context, queryArgs []string, expectedLine int) {
					endpoints := csvEndpoint

					endpoints += "?" + strings.Join(queryArgs, "&")

					req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoints, nil)
					Expect(err).ShouldNot(HaveOccurred())

					// Set auth token
					req.Header.Set("Authorization", "Bearer "+clientToken)

					resp, err := httpClient.Do(req)
					Expect(err).ShouldNot(HaveOccurred())

					defer resp.Body.Close()

					csvReader := csv.NewReader(resp.Body)
					records, err := csvReader.ReadAll()
					Expect(err).ShouldNot(HaveOccurred())

					// The first element is title
					Expect(records).Should(HaveLen(expectedLine))
				},
				Entry(
					"Filter by cluster.cluster_id",
					[]string{"cluster.cluster_id=test1-managed1-fake-uuid-1,test6-managed6-fake-uuid-6"},
					// titles + actual data
					4,
				),
				Entry(
					"Filter by cluster.name",
					[]string{"cluster.name=managed1,managed6"},
					4,
				),
				Entry(
					"Filter by event.compliance",
					[]string{"event.compliance=Compliant"},
					7,
				),
				Entry(
					"Filter by event.message",
					[]string{"event.message=configmaps%20%5Bcommon%5D%20not%20found%20in%20namespace%20default"},
					4,
				),
				Entry(
					"Filter by event.message_includes",
					[]string{"event.message_includes=etcd"},
					4,
				),
				Entry(
					"Filter by event.message_like",
					[]string{"event.message_like=configmaps%20%5B%25common%25%5D%25"},
					9,
				),
				Entry(
					"Filter by event.timestamp",
					[]string{"event.timestamp=2023-01-01T01:01:01.111Z"},
					2,
				),
				Entry(
					"Filter by event.timestamp_after",
					[]string{"event.timestamp_after=2023-04-01T01:01:01.111Z"},
					6,
				),
				Entry(
					"Filter by event.timestamp_before",
					[]string{"event.timestamp_before=2023-04-01T01:01:01.111Z"},
					7,
				),
				Entry(
					"Filter by event.timestamp_after and event.timestamp_before",
					[]string{
						"event.timestamp_after=2023-01-01T01:01:01.111Z",
						"event.timestamp_before=2023-04-01T01:01:01.111Z",
					},
					6,
				),
				Entry(
					"Filter by parent_policy.categories",
					[]string{"parent_policy.categories=cat-1,cat-3"},
					5,
				),
				Entry(
					"Filter by parent_policy.controls",
					[]string{"parent_policy.controls=ctrl-2"},
					4,
				),
				Entry(
					"Filter by parent_policy.id",
					[]string{"parent_policy.id=2"},
					4,
				),
				Entry(
					"Filter by parent_policy.name",
					[]string{"parent_policy.name=etcd-encryption1"},
					2,
				),
				Entry(
					"Filter by parent_policy.namespace",
					[]string{"parent_policy.namespace=policies"},
					10,
				),
				Entry(
					"Filter by parent_policy.standards",
					[]string{"parent_policy.standards=stand-2"},
					4,
				),
				Entry(
					"Filter by policy.apiGroup",
					[]string{"policy.apiGroup=policy.open-cluster-management.io"},
					12,
				),
				Entry(
					"Filter by policy.apiGroup no results",
					[]string{"policy.apiGroup=does-not-exist"},
					1,
				),
				Entry(
					"Filter by policy.id",
					[]string{"policy.id=4"},
					4,
				),
				Entry(
					"Filter by policy.kind",
					[]string{"policy.kind=ConfigurationPolicy"},
					12,
				),
				Entry(
					"Filter by policy.kind no results",
					[]string{"policy.kind=something-else"},
					1,
				),
				Entry(
					"Filter by policy.name",
					[]string{"policy.name=common-b"},
					3,
				),
				Entry(
					"Filter by policy.namespace",
					[]string{"policy.namespace=default"},
					2,
				),
				Entry(
					"Filter by policy.severity",
					[]string{"policy.severity=low"},
					10,
				),
				Entry(
					"Filter by policy.severity is null",
					[]string{"policy.severity"},
					3,
				),
			)
		})
	})

	Describe("Duplicate compliance event", func() {
		payload1 := []byte(`{
			"cluster": {
				"name": "managed2",
				"cluster_id": "test2-managed2-fake-uuid-2"
			},
			"policy": {
				"apiGroup": "policy.open-cluster-management.io",
				"kind": "ConfigurationPolicy",
				"name": "duplicate-test",
				"spec": {"test": "two"}
			},
			"event": {
				"compliance": "NonCompliant",
				"message": "configmaps [etcd] not found in namespace default",
				"timestamp": "2023-02-02T02:02:02.222Z"
			}
		}`)

		BeforeAll(func(ctx context.Context) {
			By("POST the initial event")
			Eventually(postEvent(ctx, payload1, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
		})

		It("Should fail when posting the same compliance event", func(ctx context.Context) {
			err := postEvent(ctx, payload1, clientToken)
			Expect(err).To(MatchError(ContainSubstring("The compliance event already exists")))
		})
	})

	Describe("Large values", func() {
		It("Should allow a large spec and message", func(ctx context.Context) {
			longString := ""
			charset := []rune{
				'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm', 'n', 'o', 'p', 'q', 'r', 's', 't',
			}

			for i := 0; i < 100000; i++ {
				c := charset[rand.Intn(len(charset))]
				longString += string(c)
			}

			payload := []byte(fmt.Sprintf(`{
			"cluster": {
				"name": "managed2",
				"cluster_id": "test2-managed2-fake-uuid-2"
			},
			"policy": {
				"apiGroup": "policy.open-cluster-management.io",
				"kind": "ConfigurationPolicy",
				"name": "duplicate-test",
				"spec": {"test": "%s"}
			},
			"event": {
				"compliance": "NonCompliant",
				"message": "%s",
				"timestamp": "2023-02-02T02:02:02.222Z"
			}
		}`, longString, longString))

			By("POST the event")
			Eventually(postEvent(ctx, payload, clientToken), "5s", "1s").ShouldNot(HaveOccurred())
		})
	})

	Describe("Test authorization", func() {
		Describe("Test method Get", func() {
			It("Should return unauthorized when it is empty token", func(ctx context.Context) {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsEndpoint+"/1", nil)
				Expect(err).ToNot(HaveOccurred())

				res, err := httpClient.Do(req)
				Expect(res.StatusCode).Should(Equal(http.StatusUnauthorized))
				Expect(err).ShouldNot(HaveOccurred())

				req, err = http.NewRequestWithContext(ctx, http.MethodGet, eventsEndpoint, nil)
				Expect(err).ToNot(HaveOccurred())

				res, err = httpClient.Do(req)
				Expect(res.StatusCode).Should(Equal(http.StatusUnauthorized))
				Expect(err).ShouldNot(HaveOccurred())

				req, err = http.NewRequestWithContext(ctx, http.MethodGet, csvEndpoint, nil)
				Expect(err).ToNot(HaveOccurred())

				res, err = httpClient.Do(req)
				Expect(res.StatusCode).Should(Equal(http.StatusUnauthorized))
				Expect(err).ShouldNot(HaveOccurred())
			})
			It("Should return empty data when SA does not have any GET verb to managedCluster",
				func(ctx context.Context) {
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsEndpoint, nil)
					Expect(err).ShouldNot(HaveOccurred())

					req.Header.Set("Content-Type", "application/json")
					// Set auth token
					req.Header.Set("Authorization", "Bearer "+wrongSAToken)

					resp, err := httpClient.Do(req)
					Expect(err).ShouldNot(HaveOccurred())

					defer resp.Body.Close()

					body, err := io.ReadAll(resp.Body)
					Expect(err).ShouldNot(HaveOccurred())

					respJSON := map[string]any{}

					err = json.Unmarshal(body, &respJSON)
					Expect(err).ShouldNot(HaveOccurred())

					rows, ok := respJSON["data"].([]interface{})
					Expect(ok).To(BeTrue())

					By("Should return 0 rows")
					Expect(rows).Should(BeEmpty())
				})

			It("Should return empty data when only unknown cluster IDs are provided",
				func(ctx context.Context) {
					endpoint := eventsEndpoint + "?cluster.cluster_id=does-not-exist,does-also-not-exist"
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
					Expect(err).ShouldNot(HaveOccurred())
					req.Header.Set("Authorization", "Bearer "+clientToken)

					resp, err := httpClient.Do(req)
					Expect(err).ShouldNot(HaveOccurred())

					defer resp.Body.Close()

					body, err := io.ReadAll(resp.Body)
					Expect(err).ShouldNot(HaveOccurred())

					respJSON := map[string]any{}

					err = json.Unmarshal(body, &respJSON)
					Expect(err).ShouldNot(HaveOccurred())

					rows, ok := respJSON["data"].([]interface{})
					Expect(ok).To(BeTrue())

					By("Should return 0 rows")
					Expect(rows).Should(BeEmpty())
				},
			)

			It("Should return a forbidden error when SA has only managed1 auth",
				func(ctx context.Context) {
					argument := "cluster.name=managed1,managed2,managed3"

					By("governance-policy-propagator SA Should be able to access all")

					respJSON, err := listEvents(ctx, clientToken, argument)
					Expect(err).ShouldNot(HaveOccurred())

					data, ok := respJSON["data"].([]any)
					Expect(ok).Should(BeTrue())

					By("Should include at least managed2 or managed3")
					hasVariousClusters := false
					for _, d := range data {
						complianceEvent, ok := d.(map[string]interface{})
						Expect(ok).To(BeTrue())

						name, ok := complianceEvent["cluster"].(map[string]interface{})["name"].(string)
						Expect(ok).To(BeTrue())

						if name == "managed2" || name == "managed3" {
							hasVariousClusters = true

							break
						}
					}
					Expect(hasVariousClusters).Should(BeTrue())

					req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsEndpoint+"?"+argument, nil)
					Expect(err).ShouldNot(HaveOccurred())

					req.Header.Set("Content-Type", "application/json")
					// Set auth token
					req.Header.Set("Authorization", "Bearer "+subsetSAToken)

					resp, err := httpClient.Do(req)
					Expect(err).ShouldNot(HaveOccurred())

					defer resp.Body.Close()

					body, err := io.ReadAll(resp.Body)
					Expect(err).ShouldNot(HaveOccurred())

					respJSON = map[string]any{}

					err = json.Unmarshal(body, &respJSON)
					Expect(err).ShouldNot(HaveOccurred())

					message, ok := respJSON["message"].(string)
					Expect(ok).To(BeTrue())

					Expect(message).
						Should(Equal("the request is not allowed: the following cluster filters are not authorized: " +
							"managed2, managed3"))

					Expect(resp.StatusCode).Should(Equal(http.StatusForbidden))
					Expect(err).ShouldNot(HaveOccurred())
				})
			It("Should return a forbidden error when only unauthorized ID are passed as id",
				func(ctx context.Context) {
					argument := "cluster.cluster_id=wrong-id,test1-managed1-fake-uuid-1,test2-managed2-fake-uuid-2"

					req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsEndpoint+"?"+argument, nil)
					Expect(err).ShouldNot(HaveOccurred())

					req.Header.Set("Content-Type", "application/json")
					// Set auth token
					req.Header.Set("Authorization", "Bearer "+subsetSAToken)

					resp, err := httpClient.Do(req)
					Expect(err).ShouldNot(HaveOccurred())

					defer resp.Body.Close()

					body, err := io.ReadAll(resp.Body)
					Expect(err).ShouldNot(HaveOccurred())

					respJSON := map[string]any{}

					err = json.Unmarshal(body, &respJSON)
					Expect(err).ShouldNot(HaveOccurred())

					message, ok := respJSON["message"].(string)
					Expect(ok).To(BeTrue())

					By("The error message should include test2-managed2-fake-uuid-2 except managed1")
					Expect(message).
						Should(Equal(
							"the request is not allowed: the following cluster filters are not authorized: " +
								"test2-managed2-fake-uuid-2"))

					Expect(resp.StatusCode).Should(Equal(http.StatusForbidden))
					Expect(err).ShouldNot(HaveOccurred())
				})
			It("Should return managed1 with subset SA when the query is empty",
				func(ctx context.Context) {
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsEndpoint, nil)
					Expect(err).ShouldNot(HaveOccurred())

					req.Header.Set("Content-Type", "application/json")
					// Set auth token
					req.Header.Set("Authorization", "Bearer "+subsetSAToken)

					resp, err := httpClient.Do(req)
					Expect(err).ShouldNot(HaveOccurred())

					defer resp.Body.Close()

					body, err := io.ReadAll(resp.Body)
					Expect(err).ShouldNot(HaveOccurred())

					respJSON := map[string]any{}

					err = json.Unmarshal(body, &respJSON)
					Expect(err).ShouldNot(HaveOccurred())

					rows, ok := respJSON["data"].([]interface{})
					Expect(ok).To(BeTrue())

					By("Should return only managed1")
					Expect(rows).Should(HaveLen(1))

					id, ok := rows[0].(map[string]interface{})["id"].(float64)
					Expect(ok).To(BeTrue())

					Expect(int(id)).Should(Equal(1))
				})
		})
	})
})

var _ = Describe("Test query generation", Label("compliance-events-api"), func() {
	It("Tests the select query for a cluster", func() {
		cluster := complianceeventsapi.Cluster{
			ClusterID: "my-cluster-id",
			Name:      "my-cluster",
		}
		sql, vals := cluster.SelectQuery("id", "spec")
		Expect(sql).To(Equal("SELECT id, spec FROM clusters WHERE cluster_id=$1 AND name=$2"))
		Expect(vals).To(HaveLen(2))
	})

	It("Tests the select query for a minimum parent policy", func() {
		parent := complianceeventsapi.ParentPolicy{
			Name:      "parent-a",
			Namespace: "policies",
		}
		sql, vals := parent.SelectQuery("id", "spec")
		Expect(sql).To(Equal(
			"SELECT id, spec FROM parent_policies WHERE name=$1 AND namespace=$2 AND categories IS NULL AND " +
				"controls IS NULL AND standards IS NULL",
		))
		Expect(vals).To(HaveLen(2))
	})

	It("Tests the select query for a parent policy with all options", func() {
		parent := complianceeventsapi.ParentPolicy{
			Name:       "parent-a",
			Namespace:  "policies",
			Categories: pq.StringArray{"cat-1"},
			Controls:   pq.StringArray{"control-1", "control-2"},
			Standards:  pq.StringArray{"standard-1"},
		}
		sql, vals := parent.SelectQuery("id")
		Expect(sql).To(Equal(
			"SELECT id FROM parent_policies WHERE name=$1 AND namespace=$2 AND categories=$3 AND controls=$4 " +
				"AND standards=$5",
		))
		Expect(vals).To(HaveLen(5))
	})

	It("Tests the select query for a minimum policy", func() {
		policy := complianceeventsapi.Policy{
			Name:     "parent-a",
			Kind:     "ConfigurationPolicy",
			APIGroup: "policy.open-cluster-management.io",
			Spec:     complianceeventsapi.JSONMap{"spec": "this-out"},
		}
		sql, vals := policy.SelectQuery("id")
		Expect(sql).To(Equal(
			"SELECT policies.id FROM policies LEFT JOIN specs ON policies.spec_id=specs.id WHERE api_group=$1 " +
				"AND kind=$2 AND name=$3 AND spec=$4 AND namespace is NULL AND severity is NULL",
		))
		Expect(vals).To(HaveLen(4))
	})

	It("Tests the select query for a policy with all options", func() {
		ns := "policies"
		severity := "critical"

		policy := complianceeventsapi.Policy{
			Name:      "parent-a",
			Namespace: &ns,
			Kind:      "ConfigurationPolicy",
			APIGroup:  "policy.open-cluster-management.io",
			Spec:      complianceeventsapi.JSONMap{"spec": "this-out"},
			Severity:  &severity,
		}
		sql, vals := policy.SelectQuery("id")
		Expect(sql).To(Equal(
			"SELECT policies.id FROM policies LEFT JOIN specs ON policies.spec_id=specs.id WHERE api_group=$1 " +
				"AND kind=$2 AND name=$3 AND spec=$4 AND namespace=$5 AND severity=$6",
		))
		Expect(vals).To(HaveLen(6))
	})
})

func postEvent(ctx context.Context, payload []byte, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventsEndpoint, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	errs := make([]error, 0)

	resp, err := httpClient.Do(req)
	if err != nil {
		errs = append(errs, err)
	}

	if resp != nil {
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			errs = append(errs, err)
		}

		if resp.StatusCode != http.StatusCreated {
			errs = append(errs, fmt.Errorf("Got non-201 status code %v; response: %q", resp.StatusCode, string(body)))
		}
	}

	return errors.Join(errs...)
}

func listEvents(ctx context.Context, token string, queryArgs ...string) (map[string]any, error) {
	url := eventsEndpoint

	if len(queryArgs) > 0 {
		url += "?" + strings.Join(queryArgs, "&")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	// Set auth token
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	respJSON := map[string]any{}

	err = json.Unmarshal(body, &respJSON)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return respJSON, fmt.Errorf("Got non-200 status code %v; response: %q", resp.StatusCode, string(body))
	}

	return respJSON, nil
}

func getToken(ctx context.Context, ns, saName string) string {
	secret := &v1.Secret{}
	var err error

	Eventually(func(g Gomega) error {
		secret, err = clientHub.CoreV1().Secrets(ns).
			Get(ctx, saName, metav1.GetOptions{})

		_, ok := secret.Data["token"]
		g.Expect(ok).Should(BeTrue())

		return err
	}).ShouldNot(HaveOccurred())

	_, ok := secret.Data["token"]
	Expect(ok).Should(BeTrue())

	return string(secret.Data["token"])
}
