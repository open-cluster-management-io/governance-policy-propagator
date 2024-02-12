//go:build e2e
// +build e2e

// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package main

import (
	"os"
	"testing"
)

// TestRunMain wraps the main() function in order to build a test binary and collection coverage for
// E2E/Integration tests. Controller CLI flags are also passed in here.
func TestRunMain(t *testing.T) {
	os.Args = append(os.Args,
		"--leader-elect=false",
		"--enable-webhooks=false",
		"--compliance-history-api-port=8385",
		"--compliance-history-api-cert=dev-tls.crt",
		"--compliance-history-api-key=dev-tls.key",
	)

	main()
}
