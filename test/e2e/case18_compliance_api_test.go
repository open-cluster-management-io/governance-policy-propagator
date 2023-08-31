// Copyright Contributors to the Open Cluster Management project

package e2e

import (
	"context"
	"database/sql"

	_ "github.com/lib/pq"
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
	var testCtx context.Context
	var testCtxCancel context.CancelFunc

	BeforeAll(func() {
		var err error

		k8sConfig, err = LoadConfig("", "", "")
		Expect(err).ToNot(HaveOccurred())

		db, err = sql.Open("postgres", "postgresql://grc:grc@localhost:5432/ocm-compliance-history?sslmode=disable")
		Expect(err).ToNot(HaveOccurred())

		Expect(db.Ping()).To(Succeed())
	})

	BeforeEach(func() {
		testCtx, testCtxCancel = context.WithCancel(context.Background())
		// Drop all tables to start fresh
		tableNameRows, err := db.Query("SELECT tablename FROM pg_tables WHERE schemaname = current_schema()")
		Expect(err).ToNot(HaveOccurred())

		defer tableNameRows.Close()

		tableNames, err := getTableNames(db)
		Expect(err).ToNot(HaveOccurred())

		for _, tableName := range tableNames {
			_, err := db.ExecContext(testCtx, "DROP TABLE IF EXISTS "+tableName+" CASCADE")
			Expect(err).ToNot(HaveOccurred())
		}
	})

	AfterEach(func() {
		if testCtx != nil {
			defer testCtxCancel()
		}
	})

	AfterAll(func() {
		if db == nil {
			return
		}

		Expect(db.Close()).To(Succeed())
	})

	Describe("Test the database migrations", func() {
		It("Migrates from a clean database", func() {
			defer testCtxCancel()

			err := complianceeventsapi.StartManager(testCtx, k8sConfig, false)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				tableNames, err := getTableNames(db)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(tableNames).ToNot(BeEmpty())

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
})
