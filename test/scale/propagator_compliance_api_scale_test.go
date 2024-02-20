package scale

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	_ "github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var httpClient = http.Client{Timeout: 30 * time.Second}

const (
	eventsEndpoint = "http://localhost:5480/api/v1/compliance-events"
	csvEndpoints   = "http://localhost:5480/api/v1/reports/compliance-events"
)

var _ = Describe("Scale Test propagation compliance API", Ordered, func() {
	var db *sql.DB

	template := `{
		"cluster": {
			"name": "%s",
			"cluster_id": "test1-%s-fake-uuid-1"
		},
		"parent_policy": {
			"name": "%s",
			"namespace": "%s",
			"categories": ["cat-1", "cat-2"],
			"controls": ["ctrl-1"],
			"standards": ["stand-1"]
		},
		"policy": {
			"apiGroup": "policy.open-cluster-management.io",
			"kind": "ConfigurationPolicy",
			"name": "%s",
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
	}`

	BeforeAll(func(ctx context.Context) {
		var err error

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

		By("Clean table content")
		tableNameRows, err := db.Query("SELECT tablename FROM pg_tables WHERE schemaname = current_schema()")
		Expect(err).ToNot(HaveOccurred())

		defer tableNameRows.Close()

		tableNames, err := getTableNames(db)
		Expect(err).ToNot(HaveOccurred())

		for _, tableName := range tableNames {
			_, err := db.ExecContext(ctx, "TRUNCATE "+tableName+" CASCADE")
			Expect(err).ToNot(HaveOccurred())
		}

		By("Post 4 compliance events to each policy")
		for i := 0; i < policyNum; i++ {
			for k := 1; k < managedClusterNum+1; k++ {
				for j := 1; j < eventNum+1; j++ {
					managedClusterName := "managed" + strconv.Itoa(k)
					policyName := "etcd-encryption-" + managedClusterName + "-" + strconv.Itoa(j)

					By("posting parent policyName: " + policyNamePrefix + strconv.Itoa(i) +
						" policy name: " + policyName)
					payload := fmt.Sprintf(template, managedClusterName, managedClusterName,
						// ParentPolicy Name
						policyNamePrefix+strconv.Itoa(i),
						// ParentPolicy Namespace
						testNamespace,
						// Policy Name
						policyName,
					)
					Eventually(postEvent(ctx, []byte(payload)), "10s", "1s").ShouldNot(HaveOccurred())
				}
			}
		}
	})

	It("CSV Test", func(ctx context.Context) {
		start := time.Now()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, csvEndpoints, nil)
		Expect(err).ShouldNot(HaveOccurred())

		resp, err := httpClient.Do(req)
		Expect(err).ShouldNot(HaveOccurred())

		elapsed := time.Since(start)

		GinkgoWriter.Println(fmt.Sprintf("CSV API call took %.2f seconds", elapsed.Seconds()))

		Expect(elapsed.Seconds()).Should(BeNumerically("<", 30.0))

		csvReader := csv.NewReader(resp.Body)

		records, err := csvReader.ReadAll()
		Expect(err).ShouldNot(HaveOccurred())

		By("Should include header and records")
		Expect(len(records)).Should(BeNumerically(">", managedClusterNum*policyNum*eventNum))

		GinkgoWriter.Println(fmt.Sprintf("Records Num: %d", len(records)))

		By("Should have 21 header")
		Expect(records[0]).Should(HaveLen(21))
	})
})

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

func postEvent(ctx context.Context, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventsEndpoint, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

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
