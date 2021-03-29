// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package utils

import (
	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/api/equality"

	"fmt"
)

func SemanticEqual(expected interface{}) types.GomegaMatcher {
	return &semanticMatcher{
		expected: expected,
	}
}

type semanticMatcher struct {
	expected interface{}
}

func (matcher *semanticMatcher) Match(actual interface{}) (success bool, err error) {
	return equality.Semantic.DeepEqual(actual, matcher.expected), nil
}

func (matcher *semanticMatcher) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected\n\t%#v\nto equal\n\t%#v", actual, matcher.expected)
}

func (matcher *semanticMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected\n\t%#v\nnot to equal\n\t%#v", actual, matcher.expected)
}
