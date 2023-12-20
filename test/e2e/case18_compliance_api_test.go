// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"

	"open-cluster-management.io/governance-policy-propagator/controllers/complianceeventsapi"
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
var _ = Describe("Test policy webhook", Label("compliance-events-api"), Ordered, func() {
	var k8sConfig *rest.Config
	var db *sql.DB

	BeforeAll(func(ctx context.Context) {
		var err error

		k8sConfig, err = LoadConfig("", "", "")
		Expect(err).ToNot(HaveOccurred())

		db, err = sql.Open("postgres", "postgresql://grc:grc@localhost:5432/ocm-compliance-history?sslmode=disable")
		DeferCleanup(func() {
			if db == nil {
				return
			}

			Expect(db.Close()).To(Succeed())
		})

		Expect(err).ToNot(HaveOccurred())

		Expect(db.Ping()).To(Succeed())

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

		mgrCtx, mgrCancel := context.WithCancel(context.Background())

		err = complianceeventsapi.StartManager(mgrCtx, k8sConfig, false, "localhost:5480")
		DeferCleanup(func() {
			mgrCancel()
		})

		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Test the database migrations", func() {
		It("Migrates from a clean database", func(ctx context.Context) {
			Eventually(func(g Gomega) {
				tableNames, err := getTableNames(db)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(tableNames).To(ContainElements("clusters", "parent_policies", "policies", "compliance_events"))

				migrationVersionRows := db.QueryRow("SELECT version, dirty FROM schema_migrations")
				var version int
				var dirty bool
				err = migrationVersionRows.Scan(&version, &dirty)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(version).To(Equal(1))
				g.Expect(dirty).To(BeFalse())
			}, defaultTimeoutSeconds, 1).Should(Succeed())
		})
	})

	Describe("Test POSTing Events", func() {
		Describe("POST one valid event with including all the optional fields", func() {
			payload := []byte(`{
				"cluster": {
					"name": "cluster1",
					"cluster_id": "test1-cluster1-fake-uuid-1"
				},
				"parent_policy": {
					"name": "policies.etcd-encryption1",
					"categories": ["cat-1", "cat-2"],
					"controls": ["ctrl-1"],
					"standards": ["stand-1"]
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "etcd-encryption1",
					"namespace": "local-cluster",
					"spec": "{\"test\":\"one\",\"severity\":\"low\"}",
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
				Eventually(postEvent(ctx, payload), "5s", "1s").ShouldNot(HaveOccurred())
			})

			It("Should have created the cluster in a table", func() {
				rows, err := db.Query("SELECT * FROM clusters WHERE cluster_id = $1", "test1-cluster1-fake-uuid-1")
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
					Expect(name).To(Equal("cluster1"))
					count++
				}

				Expect(count).To(Equal(1))
			})

			It("Should have created the parent policy in a table", func() {
				rows, err := db.Query("SELECT * FROM parent_policies WHERE name = $1", "policies.etcd-encryption1")
				Expect(err).ToNot(HaveOccurred())

				count := 0
				for rows.Next() {
					var (
						id     int
						name   string
						cats   pq.StringArray
						ctrls  pq.StringArray
						stands pq.StringArray
					)

					err := rows.Scan(&id, &name, &cats, &ctrls, &stands)
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
				rows, err := db.Query("SELECT * FROM policies WHERE name = $1", "etcd-encryption1")
				Expect(err).ToNot(HaveOccurred())

				count := 0
				for rows.Next() {
					var (
						id       int
						kind     string
						apiGroup string
						name     string
						ns       *string
						pid      *int
						spec     *string
						specHash *string
						severity *string
					)

					err := rows.Scan(&id, &kind, &apiGroup, &name, &ns, &pid, &spec, &specHash, &severity)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					Expect(kind).To(Equal("ConfigurationPolicy"))
					Expect(apiGroup).To(Equal("policy.open-cluster-management.io"))
					Expect(ns).ToNot(BeNil())
					Expect(*ns).To(Equal("local-cluster"))
					Expect(pid).ToNot(BeNil())
					Expect(*pid).ToNot(Equal(0))
					Expect(spec).ToNot(BeNil())
					Expect(*spec).To(Equal(`{"test":"one","severity":"low"}`))
					Expect(specHash).ToNot(BeNil())
					Expect(*specHash).To(Equal("cb84fe29e44202e3aeb46d39ba46993f60cdc6af"))
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
						id         int
						clusterID  int
						policyID   int
						compliance string
						message    string
						timestamp  string
						metadata   complianceeventsapi.JSONMap
						reportedBy *string
					)

					err := rows.Scan(&id, &clusterID, &policyID, &compliance, &message, &timestamp,
						&metadata, &reportedBy)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					Expect(clusterID).NotTo(Equal(0))
					Expect(policyID).NotTo(Equal(0))
					Expect(compliance).To(Equal("NonCompliant"))
					Expect(message).To(Equal("configmaps [etcd] not found in namespace default"))
					Expect(timestamp).To(Equal("2023-01-01T01:01:01.111Z"))
					Expect(metadata).To(HaveKeyWithValue("test", true))
					Expect(reportedBy).ToNot(BeNil())
					Expect(*reportedBy).To(Equal("optional-test"))
					count++
				}

				Expect(count).To(Equal(1))
			})
		})

		Describe("POST two minimally-valid events on different clusters and policies", func() {
			payload1 := []byte(`{
				"cluster": {
					"name": "cluster2",
					"cluster_id": "test2-cluster2-fake-uuid-2"
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "etcd-encryption2",
					"spec": "{\"test\":\"two\"}"
				},
				"event": {
					"compliance": "NonCompliant",
					"message": "configmaps [etcd] not found in namespace default",
					"timestamp": "2023-02-02T02:02:02.222Z"
				}
			}`)

			payload2 := []byte(`{
				"cluster": {
					"name": "cluster3",
					"cluster_id": "test2-cluster3-fake-uuid-3"
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "etcd-encryption2",
					"spec": "{\"different-spec-test\":\"two-and-a-half\"}"
				},
				"event": {
					"compliance": "Compliant",
					"message": "configmaps [etcd] found in namespace default",
					"timestamp": "2023-02-02T02:02:02.222Z"
				}
			}`)

			BeforeAll(func(ctx context.Context) {
				By("POST the events")
				Eventually(postEvent(ctx, payload1), "5s", "1s").ShouldNot(HaveOccurred())
				Eventually(postEvent(ctx, payload2), "5s", "1s").ShouldNot(HaveOccurred())
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

					Expect(id).NotTo(Equal(0))
					clusternames = append(clusternames, name)
				}

				Expect(clusternames).To(ContainElements("cluster2", "cluster3"))
			})

			It("Should have created two policies in a table despite having the same name", func() {
				rows, err := db.Query("SELECT * FROM policies WHERE name = $1", "etcd-encryption2")
				Expect(err).ToNot(HaveOccurred())

				hashes := make([]string, 0)
				for rows.Next() {
					var (
						id       int
						kind     string
						apiGroup string
						name     string
						ns       *string
						pid      *int
						spec     *string
						specHash *string
						severity *string
					)

					err := rows.Scan(&id, &kind, &apiGroup, &name, &ns, &pid, &spec, &specHash, &severity)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					Expect(specHash).ToNot(BeNil())
					hashes = append(hashes, *specHash)
				}

				Expect(hashes).To(ConsistOf(
					"8cfd1ee0a4b10aadaa4e4f3b2b9ec15e6616c1e5",
					"2c6c7170351bfaa98eb45453b93766c18d24fa04",
				))
			})

			It("Should have created both events in a table", func() {
				rows, err := db.Query("SELECT * FROM compliance_events WHERE timestamp = $1",
					"2023-02-02T02:02:02.222Z")
				Expect(err).ToNot(HaveOccurred())

				messages := make([]string, 0)
				for rows.Next() {
					var (
						id         int
						clusterID  int
						policyID   int
						compliance string
						message    string
						timestamp  string
						metadata   *string
						reportedBy *string
					)

					err := rows.Scan(&id, &clusterID, &policyID, &compliance, &message, &timestamp,
						&metadata, &reportedBy)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					Expect(clusterID).NotTo(Equal(0))
					Expect(policyID).NotTo(Equal(0))

					messages = append(messages, message)
				}

				Expect(messages).To(ConsistOf(
					"configmaps [etcd] found in namespace default",
					"configmaps [etcd] not found in namespace default",
				))
			})
		})

		Describe("POST two events on the same cluster and policy", func() {
			// payload1 defines most things, and should cause the cluster, parent, and policy to be created.
			payload1 := []byte(`{
				"cluster": {
					"name": "cluster4",
					"cluster_id": "test3-cluster4-fake-uuid-4"
				},
				"parent_policy": {
					"name": "policies.common-parent",
					"categories": ["cat-3", "cat-4"],
					"controls": ["ctrl-2"],
					"standards": ["stand-2"]
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common",
					"spec": "{\"test\":\"three\",\"severity\":\"low\"}",
					"severity": "low"
				},
				"event": {
					"compliance": "NonCompliant",
					"message": "configmaps [common] not found in namespace default",
					"timestamp": "2023-03-03T03:03:03.333Z"
				}
			}`)

			// payload2 just uses the specHash for the policy.
			payload2 := []byte(`{
				"cluster": {
					"name": "cluster4",
					"cluster_id": "test3-cluster4-fake-uuid-4"
				},
				"parent_policy": {
					"name": "policies.common-parent",
					"categories": ["cat-3", "cat-4"],
					"controls": ["ctrl-2"],
					"standards": ["stand-2"]
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common",
					"specHash": "5382228c69c6017d4efbd6e42717930cb2020da0",
					"severity": "low"
				},
				"event": {
					"compliance": "NonCompliant",
					"message": "configmaps [common] not found in namespace default",
					"timestamp": "2023-04-04T04:04:04.444Z"
				}
			}`)

			BeforeAll(func(ctx context.Context) {
				By("POST the events")
				Eventually(postEvent(ctx, payload1), "5s", "1s").ShouldNot(HaveOccurred())
				Eventually(postEvent(ctx, payload2), "5s", "1s").ShouldNot(HaveOccurred())
			})

			It("Should have only created one cluster in the table", func() {
				rows, err := db.Query("SELECT * FROM clusters WHERE name = $1", "cluster4")
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
				rows, err := db.Query("SELECT * FROM parent_policies WHERE name = $1", "policies.common-parent")
				Expect(err).ToNot(HaveOccurred())

				count := 0
				for rows.Next() {
					var (
						id     int
						name   string
						cats   pq.StringArray
						ctrls  pq.StringArray
						stands pq.StringArray
					)

					err := rows.Scan(&id, &name, &cats, &ctrls, &stands)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					count++
				}

				Expect(count).To(Equal(1))
			})

			It("Should have only created one policy in a table", func() {
				rows, err := db.Query("SELECT * FROM policies WHERE name = $1", "common")
				Expect(err).ToNot(HaveOccurred())

				hashes := make([]string, 0)
				for rows.Next() {
					var (
						id       int
						kind     string
						apiGroup string
						name     string
						ns       *string
						pid      *int
						spec     *string
						specHash *string
						severity *string
					)

					err := rows.Scan(&id, &kind, &apiGroup, &name, &ns, &pid, &spec, &specHash, &severity)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					Expect(specHash).ToNot(BeNil())
					hashes = append(hashes, *specHash)
				}

				Expect(hashes).To(ConsistOf(
					"5382228c69c6017d4efbd6e42717930cb2020da0",
				))
			})

			It("Should have created both events in a table", func() {
				rows, err := db.Query("SELECT * FROM compliance_events WHERE message = $1",
					"configmaps [common] not found in namespace default")
				Expect(err).ToNot(HaveOccurred())

				timestamps := make([]string, 0)
				for rows.Next() {
					var (
						id         int
						clusterID  int
						policyID   int
						compliance string
						message    string
						timestamp  string
						metadata   *string
						reportedBy *string
					)

					err := rows.Scan(&id, &clusterID, &policyID, &compliance, &message, &timestamp,
						&metadata, &reportedBy)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					Expect(clusterID).NotTo(Equal(0))
					Expect(policyID).NotTo(Equal(0))

					timestamps = append(timestamps, timestamp)
				}

				Expect(timestamps).To(ConsistOf(
					"2023-03-03T03:03:03.333Z",
					"2023-04-04T04:04:04.444Z",
				))
			})
		})

		Describe("POST events to check parent policy matching", func() {
			// payload1 defines most things, and should cause the cluster, parent, and policy to be created.
			payload1 := []byte(`{
				"cluster": {
					"name": "cluster5",
					"cluster_id": "test5-cluster5-fake-uuid-5"
				},
				"parent_policy": {
					"name": "policies.parent-a",
					"standards": ["stand-3"]
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common-a",
					"spec": "{\"test\":\"four\",\"severity\":\"low\"}",
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
					"name": "cluster5",
					"cluster_id": "test5-cluster5-fake-uuid-5"
				},
				"parent_policy": {
					"name": "policies.parent-a"
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common-a",
					"spec": "{\"test\":\"four\",\"severity\":\"low\"}",
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
					"name": "cluster5",
					"cluster_id": "test5-cluster5-fake-uuid-5"
				},
				"parent_policy": {
					"name": "policies.parent-a",
					"standards": []
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common-a",
					"spec": "{\"test\":\"four\",\"severity\":\"low\"}",
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
				Eventually(postEvent(ctx, payload1), "5s", "1s").ShouldNot(HaveOccurred())
				Eventually(postEvent(ctx, payload2), "5s", "1s").ShouldNot(HaveOccurred())
				Eventually(postEvent(ctx, payload3), "5s", "1s").ShouldNot(HaveOccurred())
			})

			It("Should have created two parent policies", func() {
				rows, err := db.Query("SELECT * FROM parent_policies WHERE name = $1", "policies.parent-a")
				Expect(err).ToNot(HaveOccurred())

				standardArrays := make([]pq.StringArray, 0)
				for rows.Next() {
					var (
						id     int
						name   string
						cats   pq.StringArray
						ctrls  pq.StringArray
						stands pq.StringArray
					)

					err := rows.Scan(&id, &name, &cats, &ctrls, &stands)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					standardArrays = append(standardArrays, stands)
				}

				Expect(standardArrays).To(ConsistOf(
					pq.StringArray{"stand-3"},
					nil,
				))
			})

			It("Should have created two policies in a table, with different parents", func() {
				rows, err := db.Query("SELECT * FROM policies WHERE name = $1", "common-a")
				Expect(err).ToNot(HaveOccurred())

				ids := make([]int, 0)
				names := make([]string, 0)
				pids := make([]int, 0)
				hashes := make([]string, 0)
				for rows.Next() {
					var (
						id       int
						kind     string
						apiGroup string
						name     string
						ns       *string
						pid      *int
						spec     *string
						specHash string
						severity *string
					)

					err := rows.Scan(&id, &kind, &apiGroup, &name, &ns, &pid, &spec, &specHash, &severity)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					Expect(specHash).ToNot(BeNil())
					ids = append(ids, id)
					names = append(names, name)
					pids = append(pids, *pid)
					hashes = append(hashes, specHash)
				}

				Expect(ids).To(HaveLen(2))
				Expect(ids[0]).ToNot(Equal(ids[1]))
				Expect(names[0]).To(Equal(names[1]))
				Expect(pids[0]).ToNot(Equal(pids[1]))
				Expect(hashes[0]).To(Equal(hashes[1]))
			})
		})

		Describe("POST events to check policy namespace matching", func() {
			// payload1 should cause the cluster, parent, and policy to be created.
			payload1 := []byte(`{
				"cluster": {
					"name": "cluster6",
					"cluster_id": "test6-cluster6-fake-uuid-6"
				},
				"parent_policy": {
					"name": "policies.parent-b"
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common-b",
					"spec": "{\"test\":\"four\",\"severity\":\"low\"}",
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
					"name": "cluster6",
					"cluster_id": "test6-cluster6-fake-uuid-6"
				},
				"parent_policy": {
					"name": "policies.parent-b"
				},
				"policy": {
					"apiGroup": "policy.open-cluster-management.io",
					"kind": "ConfigurationPolicy",
					"name": "common-b",
					"spec": "{\"test\":\"four\",\"severity\":\"low\"}",
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
				Eventually(postEvent(ctx, payload1), "5s", "1s").ShouldNot(HaveOccurred())
				Eventually(postEvent(ctx, payload2), "5s", "1s").ShouldNot(HaveOccurred())
			})

			It("Should have created one parent policy", func() {
				rows, err := db.Query("SELECT * FROM parent_policies WHERE name = $1", "policies.parent-b")
				Expect(err).ToNot(HaveOccurred())

				count := 0
				for rows.Next() {
					var (
						id     int
						name   string
						cats   pq.StringArray
						ctrls  pq.StringArray
						stands pq.StringArray
					)

					err := rows.Scan(&id, &name, &cats, &ctrls, &stands)
					Expect(err).ToNot(HaveOccurred())
					Expect(id).NotTo(Equal(0))
					count++
				}

				Expect(count).To(Equal(1))
			})

			It("Should have created two policies in the table, with different namespaces", func() {
				rows, err := db.Query("SELECT * FROM policies WHERE name = $1", "common-b")
				Expect(err).ToNot(HaveOccurred())

				ids := make([]int, 0)
				names := make([]string, 0)
				namespaces := make([]string, 0)
				pids := make([]int, 0)
				hashes := make([]string, 0)
				for rows.Next() {
					var (
						id       int
						kind     string
						apiGroup string
						name     string
						ns       *string
						pid      *int
						spec     *string
						specHash string
						severity *string
					)

					err := rows.Scan(&id, &kind, &apiGroup, &name, &ns, &pid, &spec, &specHash, &severity)
					Expect(err).ToNot(HaveOccurred())

					Expect(id).NotTo(Equal(0))
					Expect(specHash).ToNot(BeNil())
					ids = append(ids, id)
					names = append(names, name)
					pids = append(pids, *pid)
					hashes = append(hashes, specHash)

					if ns != nil {
						namespaces = append(namespaces, *ns)
					}
				}

				Expect(ids).To(HaveLen(2))
				Expect(ids[0]).ToNot(Equal(ids[1]))
				Expect(names[0]).To(Equal(names[1]))
				Expect(namespaces).To(ConsistOf("default"))
				Expect(pids[0]).To(Equal(pids[1]))
				Expect(hashes[0]).To(Equal(hashes[1]))
			})
		})

		Describe("POST invalid events", func() {
			It("should require the cluster to be specified", func(ctx context.Context) {
				Eventually(postEvent(ctx, []byte(`{
					"parent_policy": {
						"name": "validity-parent"
					},
					"policy": {
						"apiGroup": "policy.open-cluster-management.io",
						"kind": "ConfigurationPolicy",
						"name": "validity",
						"spec": "{\"test\":\"validity\",\"severity\":\"low\"}",
						"severity": "low"
					},
					"event": {
						"compliance": "Compliant",
						"message": "configmaps [valid] valid in namespace valid",
						"timestamp": "2023-09-09T09:09:09.999Z"
					}
				}`)), "5s", "1s").Should(MatchError(ContainSubstring("Got non-201 status code 400")))
			})

			It("should require the event time to be specified", func(ctx context.Context) {
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
						"spec": "{\"test\":\"validity\",\"severity\":\"low\"}",
						"severity": "low"
					},
					"event": {
						"compliance": "Compliant",
						"message": "configmaps [valid] valid in namespace valid"
					}
				}`)), "5s", "1s").Should(MatchError(ContainSubstring("Got non-201 status code 400")))
			})

			It("should require the policy spec and hash to match", func(ctx context.Context) {
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
						"spec": "{\"test\":\"validity\",\"severity\":\"low\"}",
						"severity": "low",
						"specHash": "foobar"
					},
					"event": {
						"compliance": "Compliant",
						"message": "configmaps [valid] valid in namespace valid",
						"timestamp": "2023-09-09T09:09:09.999Z"
					}
				}`)), "5s", "1s").Should(MatchError(ContainSubstring("Got non-201 status code 400")))
			})

			It("should require the input to be valid JSON", func(ctx context.Context) {
				Eventually(postEvent(ctx, []byte(`{
					foo: bar: baz
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
						"spec": "{\"test\":\"validity\",\"severity\":\"low\"}",
						"severity": "low",
						"specHash": "foobar"
					},
					"event": {
						"compliance": "Compliant",
						"message": "configmaps [valid] valid in namespace valid",
						"timestamp": "2023-09-09T09:09:09.999Z"
					}
				}`)), "5s", "1s").Should(MatchError(ContainSubstring("Got non-201 status code 400")))
			})

			It("should require the spec when inputting a new policy", func(ctx context.Context) {
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
						"specHash": "0123456789abcdefzzzzzzzzzzzzzzzzzzzzzzzz"
					},
					"event": {
						"compliance": "Compliant",
						"message": "configmaps [valid] valid in namespace valid",
						"timestamp": "2023-09-09T09:09:09.999Z"
					}
				}`)), "5s", "1s").Should(MatchError(ContainSubstring("policy.spec is not optional for new policies")))
			})
		})
	})
})

func postEvent(ctx context.Context, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://localhost:5480/api/v1/compliance-events", bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	errs := make([]error, 0)

	client := &http.Client{}

	resp, err := client.Do(req)
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
