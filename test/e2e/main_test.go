// Copyright (c) 2020 Red Hat, Inc.
// +build integration

package e2e

import (
	"testing"

	f "github.com/operator-framework/operator-sdk/pkg/test"
)

func TestMain(m *testing.M) {
	f.MainEntry(m)
}
