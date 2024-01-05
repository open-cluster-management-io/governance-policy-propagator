package complianceeventsapi

import (
	"fmt"
	"testing"

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
