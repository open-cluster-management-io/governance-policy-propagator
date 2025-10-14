# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

The governance-policy-propagator is a Kubernetes controller that watches `Policies`, `PlacementBindings`, and `PlacementRules`/`Placements`. It manages replicated Policies in cluster namespaces based on placement decisions and updates root policy status with aggregated cluster compliance results.

This is part of the Open Cluster Management (OCM) governance framework, specifically the policy propagation system that distributes policies from the hub cluster to managed clusters.

## Build and Test Commands

### Building
```bash
# Build the controller binary (CGO is required)
make build

# Build container images
make build-images
```

### Testing
```bash
# Run unit tests
make test-dependencies  # Install test dependencies first
make test

# Run unit tests with coverage
make test-coverage      # Generates coverage_unit.out

# Run e2e tests (requires Kind cluster)
make e2e-dependencies
make e2e-test

# Run specific e2e test suites
make e2e-test-webhook           # Webhook tests only
make e2e-test-policyautomation  # PolicyAutomation tests only
```

### Running a Single Test
```bash
# Unit tests - use standard go test with package path
KUBEBUILDER_ASSETS="$(./bin/setup-envtest use 1.26.x -p path)" go test -v ./controllers/propagator -run TestSpecificTest

# E2E tests - use ginkgo with focus
./bin/ginkgo -v --focus="specific test name" test/e2e
```

### Linting and Formatting
```bash
# Format code (runs gofmt, gofumpt, and gci)
make fmt

# Lint code
make lint               # Runs both YAML and Go linting
make lint-go           # Go linting only
make lint-yaml         # YAML linting only

# Security scanning
make gosec-scan
```

### Local Development

**Option 1: Run controller locally (recommended for development)**
```bash
# Create Kind cluster and install CRDs
make kind-bootstrap-cluster-dev

# Run controller locally (webhooks disabled by default)
make run
# Or with custom settings:
WATCH_NAMESPACE="" go run main.go --leader-elect=false --enable-webhooks=false --log-level=2
```

**Option 2: Run in Kind cluster**
```bash
# Create cluster, build image, and deploy
make kind-bootstrap-cluster-dev
make build-images
make kind-deploy-controller-dev
```

**Clean up**
```bash
make kind-delete-cluster
```

### Generating Manifests
```bash
# Generate CRDs and RBAC from code annotations
make manifests

# Generate DeepCopy methods
make generate

# Generate complete operator.yaml deployment
make generate-operator-yaml
```

**Important**: The deployment YAML files in `deploy/` are auto-generated. After code changes affecting CRDs or RBAC, run `make generate-operator-yaml`. Never manually edit `deploy/operator.yaml`.

## Architecture

### Controller Reconciliation Flow

The propagator watches for changes and triggers reconciles:
1. Changes to root Policies (non-cluster namespaces) → RootPolicyReconciler
2. Changes to replicated Policies (cluster namespaces) → ReplicatedPolicyReconciler
3. Changes to PlacementBindings → reconciles on bound Policies
4. Changes to Placements/PlacementRules → reconciles on bound Policies

Each reconcile:
1. Creates/updates/deletes replicated policies in cluster namespaces based on placement decisions
2. Updates policy status with aggregated cluster compliance results

### Core Controllers

Located in `controllers/` with each subdirectory containing a focused controller:

**propagator/** - Core policy replication logic
- `RootPolicyReconciler`: Handles root policies in hub namespaces, determines placement, sends events to replicated policy reconciler
- `ReplicatedPolicyReconciler`: Handles replicated policies in cluster namespaces (e.g., `managed1`, `managed2`), processes hub templates, manages encryption
- Key files:
  - `rootpolicy_controller.go`: Root policy reconciliation entrypoint at main.go:335
  - `replicatedpolicy_controller.go`: Replicated policy reconciliation
  - `propagation.go`: Template processing with `{{hub ... hub}}` delimiters
  - `encryption.go`: Policy template encryption/decryption using AES
  - `replication.go`: Policy copying and override logic

**rootpolicystatus/** - Aggregates compliance status from managed clusters back to root policies

**policyset/** - Manages PolicySet resources that group policies together with dependency ordering

**automation/** - PolicyAutomation controller for triggering Ansible jobs based on policy violations

**encryptionkeys/** - Manages encryption key rotation (default 30 days) for encrypted policy templates

**policymetrics/** - Exposes Prometheus metrics for policy compliance

**common/** - Shared utilities for all controllers, including namespace detection (cluster vs non-cluster)

### Hub Templates

Policies support hub-side templating with `{{hub ... hub}}` delimiters (defined in propagator/propagation.go:33-34). Templates are resolved before replication to managed clusters using the `go-template-utils` library.

Template resolution:
- Default: Limited to same namespace and the target ManagedCluster resource
- With `spec.hubTemplateOptions.serviceAccountName`: Uses specified ServiceAccount permissions for cluster-wide access
- Template context includes: `ManagedClusterName`, `ManagedClusterLabels`, `PolicyMetadata`

### Namespace Model

**Cluster namespaces**: Named after managed clusters (e.g., `managed1`, `managed2`). Contain replicated policies created by the propagator. Detection: namespace name matches a ManagedCluster resource name (see common/common.go).

**Non-cluster namespaces**: Hub namespaces containing root policies that get propagated.

### Policy Locking

Uses `sync.Map` in `main.go:319` to store per-policy mutexes (`RootPolicyLocks`). Each root policy reconcile acquires its lock to prevent concurrent modifications (see rootpolicy_controller.go:37-40).

### Custom Resource Definitions

**api/v1/** - Core policy types:
- `Policy`: Wrapper for policy engine resources with `spec.policy-templates` array
- `PlacementBinding`: Binds policies to Placement/PlacementRule resources
- Key fields: `Policy.Spec.PolicyTemplates`, `Policy.Spec.Dependencies`, `Policy.Spec.HubTemplateOptions`

**api/v1beta1/**:
- `PolicySet`: Groups policies with dependency ordering
- `PolicyAutomation`: Ansible automation trigger configuration

### Configuration

Environment variables (see main.go):
- `WATCH_NAMESPACE`: Required. Comma-separated namespaces to watch, or empty for cluster-wide
- `CONTROLLER_CONFIG_QPS`: Override default QPS (200.0)
- `CONTROLLER_CONFIG_BURST`: Override default burst (400)
- `DISABLE_REPORT_METRICS`: Set to "true" to disable metrics reporting

CLI flags:
- `--enable-webhooks`: Enable policy validation webhook (default: true)
- `--disable-placementrule`: Disable PlacementRule watches (auto-detected if CRD missing)
- `--encryption-key-rotation`: Days until key rotation (default: 30)
- `--root-policy-max-concurrency`: Root policy reconciler concurrency (default: 2)
- `--replicated-policy-max-concurrency`: Replicated policy reconciler concurrency (default: 10)
- `--log-level`: Logging verbosity level

## Testing Notes

### E2E Test Structure

Tests are in `test/e2e/case*_test.go` files organized by feature:
- case1: Basic policy propagation
- case2: Status aggregation
- case5: PolicyAutomation
- case6: Placement (non-PlacementRule) propagation
- case8: Metrics
- case11-13: PolicySet functionality
- case17: Policy webhook validation

E2E tests use Ginkgo/Gomega and expect a running Kind cluster with the propagator deployed.

### Dependencies

The project uses Go 1.24 with controller-runtime v0.19.0, kubernetes v0.31.x, and OCM api/subscription libraries.

Key dependencies:
- `github.com/stolostron/go-template-utils/v7`: Hub template resolution
- `github.com/stolostron/kubernetes-dependency-watches`: Dynamic watches for template dependencies
- `sigs.k8s.io/controller-runtime`: Kubernetes controller framework

### PlacementRule Deprecation

PlacementRule (from `apps.open-cluster-management.io`) is deprecated in favor of Placement (from `cluster.open-cluster-management.io`). The controller auto-detects CRD availability and disables PlacementRule watches if the CRD is not found.
