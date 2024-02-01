package complianceeventsapi

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestSplitQueryValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		queryVal string
		expected []string
	}{
		{"cluster1", []string{"cluster1"}},
		{"cluster1,", []string{"cluster1"}},
		{",cluster1", []string{"cluster1"}},
		{"cluster1,cluster2", []string{"cluster1", "cluster2"}},
		{`cluster\,monkey,not-monkey`, []string{`cluster,monkey`, "not-monkey"}},
	}

	for _, test := range tests {
		test := test

		t.Run(
			fmt.Sprintf("?cluster.name=%s", test.queryVal),
			func(t *testing.T) {
				t.Parallel()

				g := NewWithT(t)
				g.Expect(splitQueryValue(test.queryVal)).To(Equal(test.expected))
			},
		)
	}
}

func TestConvertToCsvLine(t *testing.T) {
	t.Parallel()

	theTime := time.Date(2021, 8, 15, 14, 30, 45, 100, time.UTC)

	reportBy := "cat1"

	ce := ComplianceEvent{
		EventID: 1,
		Event: EventDetails{
			Compliance: "cp1",
			Message:    "event1 message",
			Metadata:   nil,
			ReportedBy: &reportBy,
			Timestamp:  theTime,
		},
		Cluster: Cluster{
			ClusterID: "1111",
			Name:      "cluster1",
		},
		Policy: Policy{
			KeyID:    0,
			Kind:     "",
			APIGroup: "v1",
			Name:     "",
			Spec: map[string]interface{}{
				"name":      "hi",
				"namespace": "cat-1",
			},
		},
	}

	values := convertToCsvLine(&ce, true)

	g := NewWithT(t)
	g.Expect(values).Should(HaveLen(21))
	// Should follow this order
	// 	"compliance_events_id",
	// "compliance_events_compliance",
	// "compliance_events_message",
	// "compliance_events_metadata",
	// "compliance_events_reported_by",
	// "compliance_events_timestamp",
	// "clusters_cluster_id",
	// "clusters_name",
	// "parent_policies_id",
	// "parent_policies_name",
	// "parent_policies_namespace",
	// "parent_policies_categories",
	// "parent_policies_controls",
	// "parent_policies_standards",
	// "policies_id",
	// "policies_api_group",
	// "policies_kind",
	// "policies_name",
	// "policies_namespace",
	// "policies_severity",
	// "policies_spec",
	g.Expect(values).Should(Equal([]string{
		"1", "cp1", "event1 message",
		"", "cat1", "2021-08-15 14:30:45.0000001 +0000 UTC",
		"1111", "cluster1", "", "", "", "", "", "", "", "v1", "", "", "", "",
		"{\n  \"name\": \"hi\",\n  \"namespace\": \"cat-1\"\n}",
	}))

	// Test includeSpec = false
	values = convertToCsvLine(&ce, false)
	g.Expect(values).Should(HaveLen(20), "Test Some fields set")

	parentPolicy := &ParentPolicy{
		KeyID:      11,
		Name:       "parent-my-name",
		Namespace:  "ns-pp",
		Categories: []string{"cate-1", "cate-2"},
		Controls:   []string{"control-1", "control-2"},
		Standards:  []string{"stand-1", "stand-2"},
	}

	// Test All fields set
	ce = ComplianceEvent{
		EventID:      1,
		ParentPolicy: parentPolicy,
		Event: EventDetails{
			Compliance: "cp1",
			Message:    "event1 message",
			Metadata: JSONMap{
				"pet":    "cat1",
				"flower": []string{"rose", "sunflower"},
				"number": 1,
			},
			ReportedBy: &reportBy,
			Timestamp:  theTime,
		},
		Cluster: Cluster{
			ClusterID: "22",
			Name:      "cluster1",
		},
		Policy: Policy{
			KeyID:    0,
			Kind:     "configuration",
			APIGroup: "v1",
			Name:     "policy-name",
			Spec: JSONMap{
				"name":      "hi",
				"namespace": "cat-1",
			},
		},
	}

	values = convertToCsvLine(&ce, true)
	g.Expect(values).Should(Equal([]string{
		"1", "cp1", "event1 message",
		"{\n  \"flower\": [\n    \"rose\",\n    \"sunflower\"\n  ],\n  \"number\": 1,\n  \"pet\": \"cat1\"\n}",
		"cat1", "2021-08-15 14:30:45.0000001 +0000 UTC", "22", "cluster1",
		"11", "parent-my-name", "ns-pp", "cate-1, cate-2",
		"control-1, control-2", "stand-1, stand-2", "",
		"v1", "configuration", "policy-name", "", "",
		"{\n  \"name\": \"hi\",\n  \"namespace\": \"cat-1\"\n}",
	}), "Test All fields set")
}

func TestGetCsvHeader(t *testing.T) {
	g := NewWithT(t)

	result := getCsvHeader(true)
	g.Expect(result).Should(HaveLen(21))
	g.Expect(result).Should(Equal([]string{
		"compliance_events_id",
		"compliance_events_compliance",
		"compliance_events_message", "compliance_events_metadata",
		"compliance_events_reported_by", "compliance_events_timestamp", "clusters_cluster_id",
		"clusters_name", "parent_policies_id", "parent_policies_name",
		"parent_policies_namespace", "parent_policies_categories", "parent_policies_controls",
		"parent_policies_standards", "policies_id", "policies_api_group", "policies_kind", "policies_name",
		"policies_namespace", "policies_severity", "policies_spec",
	}))

	result = getCsvHeader(false)
	g.Expect(result).Should(HaveLen(20))
}
