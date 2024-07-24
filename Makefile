# Copyright 2019 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
# Copyright Contributors to the Open Cluster Management project

PWD := $(shell pwd)
LOCAL_BIN ?= $(PWD)/bin

export PATH := $(LOCAL_BIN):$(PATH)
GOARCH = $(shell go env GOARCH)
GOOS = $(shell go env GOOS)
TESTARGS_DEFAULT := "-v"
export TESTARGS ?= $(TESTARGS_DEFAULT)
VERSION ?= $(shell cat COMPONENT_VERSION 2> /dev/null)
IMAGE_NAME_AND_VERSION ?= $(REGISTRY)/$(IMG)
CONTROLLER_NAME = $(shell cat COMPONENT_NAME 2> /dev/null)
CONTROLLER_NAMESPACE ?= open-cluster-management
# Handle KinD configuration
CLUSTER_NAME ?= hub
KIND_NAMESPACE ?= $(CONTROLLER_NAMESPACE)
POSTGRES_HOST ?= localhost

# Test coverage threshold
export COVERAGE_MIN ?= 75

# Image URL to use all building/pushing image targets;
# Use your own docker registry and image name for dev/test by overridding the IMG and REGISTRY environment variable.
IMG ?= $(shell cat COMPONENT_NAME 2> /dev/null)
REGISTRY ?= quay.io/open-cluster-management
TAG ?= latest

# Fix sed issues on mac by using GSED
SED = sed
ifeq ($(GOOS), darwin)
  SED = gsed
endif

include build/common/Makefile.common.mk

############################################################
# work section
############################################################
$(GOBIN):
	@echo "create gobin"
	@mkdir -p $(GOBIN)

############################################################
# clean section
############################################################

.PHONY: clean
clean:
	-rm bin/*
	-rm build/_output/bin/*
	-rm coverage*.out
	-rm report*.json
	-rm kubeconfig_*
	-rm -r vendor/

############################################################
# lint section
############################################################

.PHONY: fmt
fmt:

.PHONY: lint
lint:

############################################################
# test section
############################################################
KBVERSION = 3.12.0
ENVTEST_K8S_VERSION = 1.26.x

.PHONY: test
test: test-dependencies
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" go test $(TESTARGS) `go list ./... | grep -v test/e2e`

.PHONY: test-coverage
test-coverage: TESTARGS = -json -cover -covermode=atomic -coverprofile=coverage_unit.out
test-coverage: test

.PHONY: test-dependencies
test-dependencies: envtest kubebuilder

.PHONY: gosec-scan
gosec-scan: GOSEC_ARGS=-exclude G201

############################################################
# build section
############################################################

.PHONY: build
build:
	CGO_ENABLED=1 go build -o build/_output/bin/$(IMG) main.go

############################################################
# images section
############################################################

.PHONY: build-images
build-images:
	@docker build -t ${IMAGE_NAME_AND_VERSION} -f build/Dockerfile .
	@docker tag ${IMAGE_NAME_AND_VERSION} $(REGISTRY)/$(IMG):$(TAG)

.PHONY: run
run:
	WATCH_NAMESPACE="$(WATCH_NAMESPACE)" go run main.go --leader-elect=false --enable-webhooks=false --log-level=2

############################################################
# Generate manifests
############################################################

.PHONY: manifests
manifests: kustomize controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) crd rbac:roleName=governance-policy-propagator paths="./..." output:crd:artifacts:config=deploy/crds output:rbac:artifacts:config=deploy/rbac
	mv deploy/crds/policy.open-cluster-management.io_policies.yaml deploy/crds/kustomize/policy.open-cluster-management.io_policies.yaml
	@printf -- "---\n" > deploy/crds/policy.open-cluster-management.io_policies.yaml
	$(KUSTOMIZE) build deploy/crds/kustomize >> deploy/crds/policy.open-cluster-management.io_policies.yaml
	$(SED) -i 's/ description: |-/ description: >-/g' deploy/crds/policy.open-cluster-management.io_policies.yaml

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: generate-operator-yaml
generate-operator-yaml: kustomize manifests
	$(KUSTOMIZE) build deploy/manager > deploy/operator.yaml

############################################################
# e2e test section
############################################################

.PHONY: kind-bootstrap-cluster
kind-bootstrap-cluster: POSTGRES_HOST=postgres
kind-bootstrap-cluster: kind-bootstrap-cluster-dev webhook kind-deploy-controller install-resources

.PHONY: kind-bootstrap-cluster-dev
kind-bootstrap-cluster-dev: kind-create-cluster install-crds kind-controller-kubeconfig postgres

cert-manager:
	@echo Installing cert-manager
	kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.12.0/cert-manager.yaml
	@echo "Waiting until the pods are up"
	kubectl wait deployment -n cert-manager cert-manager --for condition=Available=True --timeout=180s
	kubectl wait --for=condition=Ready pod -l app.kubernetes.io/instance=cert-manager -n cert-manager --timeout=180s 

postgres: cert-manager
	@echo "Installing Postgres"
	-kubectl create ns $(KIND_NAMESPACE)
	sed 's/open-cluster-management/$(KIND_NAMESPACE)/g' build/kind/postgres.yaml | kubectl apply --timeout=180s -f-

	@echo "Waiting until the pods are up"
	@sleep 3
	kubectl -n $(KIND_NAMESPACE) wait --for=condition=Ready pod -l app=postgres

	@echo "Creating the governance-policy-database secret"
	@kubectl -n $(KIND_NAMESPACE) get secret governance-policy-database || \
	kubectl -n $(KIND_NAMESPACE) create secret generic governance-policy-database \
		--from-literal="user=grc" \
		--from-literal="password=grc" \
		--from-literal="host=$(POSTGRES_HOST)" \
		--from-literal="dbname=ocm-compliance-history" \
		--from-literal="ca=$$(kubectl -n $(KIND_NAMESPACE) get secret postgres-cert -o json | jq -r '.data["ca.crt"]' | base64 -d)"
	
	@echo "Copying the compliance API certificates locally"
	kubectl -n $(KIND_NAMESPACE) get secret compliance-api-cert -o json | jq -r '.data["tls.crt"]' | base64 -d > dev-tls.crt
	kubectl -n $(KIND_NAMESPACE) get secret compliance-api-cert -o json | jq -r '.data["ca.crt"]' | base64 -d >> dev-ca.crt
	kubectl -n $(KIND_NAMESPACE) get secret compliance-api-cert -o json | jq -r '.data["tls.key"]' | base64 -d > dev-tls.key

webhook: cert-manager
	-kubectl create ns $(KIND_NAMESPACE)
	sed -E 's,open-cluster-management(.svc|/|$$),$(KIND_NAMESPACE)\1,g' deploy/webhook.yaml | kubectl apply -f -

HUB_ONLY ?= none

.PHONY: kind-deploy-controller
kind-deploy-controller: manifests
	if [ "$(HUB_ONLY)" = "true" ]; then\
		$(MAKE) webhook;\
		kubectl delete deployment governance-policy-propagator -n $(KIND_NAMESPACE) ;\
		kubectl wait --for=delete pod -l name=governance-policy-propagator --timeout=60s -n $(KIND_NAMESPACE);\
	fi
	@echo installing $(IMG)
	-kubectl create ns $(KIND_NAMESPACE)
	sed 's/namespace: open-cluster-management/namespace: $(KIND_NAMESPACE)/g' deploy/operator.yaml | kubectl apply -f - -n $(KIND_NAMESPACE)

.PHONY: kind-deploy-controller-dev
kind-deploy-controller-dev: kind-deploy-controller
	@echo "Patch deployment image"
	kubectl patch deployment $(IMG) -n $(KIND_NAMESPACE) -p "{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"$(IMG)\",\"imagePullPolicy\":\"Never\"}]}}}}"
	kubectl patch deployment $(IMG) -n $(KIND_NAMESPACE) -p "{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"$(IMG)\",\"image\":\"$(REGISTRY)/$(IMG):$(TAG)\"}]}}}}"

	@echo Pushing image to KinD cluster
	kind load docker-image $(REGISTRY)/$(IMG):$(TAG) --name $(KIND_NAME)
	kubectl rollout restart deployment/$(IMG) -n $(KIND_NAMESPACE)
	kubectl rollout status -n $(KIND_NAMESPACE) deployment $(IMG) --timeout=180s

# Specify KIND_VERSION to indicate the version tag of the KinD image
.PHONY: kind-create-cluster
kind-create-cluster: KIND_ARGS += --config build/kind/kind-config.yaml

.PHONY: kind-delete-cluster
kind-delete-cluster:
	kind delete cluster --name $(KIND_NAME)

.PHONY: install-crds
install-crds: manifests
	@echo installing crds
	kubectl apply -f deploy/crds/policy.open-cluster-management.io_placementbindings.yaml
	kubectl apply -f deploy/crds/policy.open-cluster-management.io_policies.yaml
	kubectl apply -f deploy/crds/policy.open-cluster-management.io_policyautomations.yaml
	kubectl apply -f deploy/crds/policy.open-cluster-management.io_policysets.yaml
	kubectl apply -f https://raw.githubusercontent.com/open-cluster-management-io/multicloud-operators-subscription/main/deploy/hub-common/apps.open-cluster-management.io_placementrules_crd.yaml
	kubectl apply -f https://raw.githubusercontent.com/open-cluster-management-io/api/main/cluster/v1/0000_00_clusters.open-cluster-management.io_managedclusters.crd.yaml
	kubectl apply -f https://raw.githubusercontent.com/open-cluster-management-io/api/main/cluster/v1beta1/0000_02_clusters.open-cluster-management.io_placements.crd.yaml --validate=false
	kubectl apply -f https://raw.githubusercontent.com/open-cluster-management-io/api/main/cluster/v1beta1/0000_03_clusters.open-cluster-management.io_placementdecisions.crd.yaml --validate=false
	kubectl apply -f deploy/crds/external/tower.ansible.com_joblaunch_crd.yaml
	kubectl apply -f test/resources/case5_policy_automation/dns-crd.yaml

.PHONY: install-resources
install-resources:
	@echo creating namespaces
	kubectl create ns policy-propagator-test
	kubectl create ns $(KIND_NAMESPACE)
	kubectl create ns local-cluster
	kubectl create ns managed1
	kubectl create ns managed2
	kubectl create ns managed3
	kubectl create ns managed4
	kubectl create ns managed5
	kubectl create ns managed6
	@echo deploying roles and service account
	kubectl apply -k deploy/rbac -n $(KIND_NAMESPACE)
	sed 's/namespace: open-cluster-management/namespace: $(KIND_NAMESPACE)/' deploy/manager/service-account.yaml | \
	  kubectl apply -f -
	@echo creating cluster resources
	kubectl apply -f test/resources/local-cluster.yaml
	kubectl apply -f test/resources/managed1-cluster.yaml
	kubectl apply -f test/resources/managed2-cluster.yaml
	kubectl apply -f test/resources/managed3-cluster.yaml
	kubectl apply -f test/resources/managed4-cluster.yaml
	kubectl apply -f test/resources/managed5-cluster.yaml
	kubectl apply -f test/resources/managed6-cluster.yaml
	@echo setting a Hub cluster DNS name
	kubectl apply -f test/resources/case5_policy_automation/cluster-dns.yaml

E2E_LABEL_FILTER = --label-filter="!webhook && !compliance-events-api && !policyautomation"
.PHONY: e2e-test
e2e-test: e2e-dependencies
	$(GINKGO) -v --fail-fast $(E2E_TEST_ARGS) $(E2E_LABEL_FILTER) test/e2e -- $(E2E_TEST_CODE_ARGS)

.PHONY: e2e-test-webhook
e2e-test-webhook: E2E_LABEL_FILTER = --label-filter="webhook"
e2e-test-webhook: e2e-test

.PHONY: e2e-test-compliance-events-api
e2e-test-compliance-events-api: E2E_LABEL_FILTER = --label-filter="compliance-events-api"
e2e-test-compliance-events-api: e2e-test

.PHONY: e2e-test-coverage-compliance-events-api
e2e-test-coverage-compliance-events-api: E2E_TEST_ARGS = --json-report=report_e2e_compliance_events_api.json --covermode=atomic --coverpkg=open-cluster-management.io/governance-policy-propagator/controllers/complianceeventsapi --coverprofile=coverage_e2e_compliance_events_api.out --output-dir=.
e2e-test-coverage-compliance-events-api: e2e-test-compliance-events-api

.PHONY: e2e-test-policyautomation
e2e-test-policyautomation: E2E_LABEL_FILTER = --label-filter="policyautomation"
e2e-test-policyautomation: e2e-test

.PHONY: e2e-test-non-placement-rule
e2e-test-non-placement-rule: E2E_LABEL_FILTER = --label-filter="non-placement-rule"
e2e-test-non-placement-rule: e2e-test

.PHONY: e2e-build-instrumented
e2e-build-instrumented:
	go test -covermode=atomic -coverpkg=$(shell cat go.mod | head -1 | cut -d ' ' -f 2)/... -c -tags e2e ./ -o build/_output/bin/$(IMG)-instrumented

TEST_COVERAGE_OUT = coverage_e2e.out
.PHONY: e2e-run-instrumented
e2e-run-instrumented: e2e-build-instrumented
	WATCH_NAMESPACE="$(WATCH_NAMESPACE)" ./build/_output/bin/$(IMG)-instrumented -test.run "^TestRunMain$$" -test.coverprofile=$(TEST_COVERAGE_OUT) 2>&1 | tee ./build/_output/controller.log &

.PHONY: e2e-stop-instrumented
e2e-stop-instrumented:
	ps -ef | grep '$(IMG)' | grep -v grep | awk '{print $$2}' | xargs kill

.PHONY: e2e-test-coverage
e2e-test-coverage: E2E_TEST_ARGS = --json-report=report_e2e.json --output-dir=.
e2e-test-coverage: E2E_TEST_CODE_ARGS = --compliance-api-port=8385
e2e-test-coverage: e2e-run-instrumented e2e-test e2e-stop-instrumented

.PHONY: e2e-test-coverage-policyautomation
e2e-test-coverage-policyautomation: E2E_TEST_ARGS = --json-report=report_e2e_policyautomation.json --output-dir=.
e2e-test-coverage-policyautomation: E2E_LABEL_FILTER = --label-filter="policyautomation"
e2e-test-coverage-policyautomation: TEST_COVERAGE_OUT = coverage_e2e_policyautomation.out
e2e-test-coverage-policyautomation: e2e-test-coverage

.PHONY: e2e-debug
e2e-debug:
	@echo local controller log:
	-cat build/_output/controller.log
	@echo remote controller log:
	-kubectl logs $$(kubectl get pods -n $(KIND_NAMESPACE) -o name | grep $(IMG)) -n $(KIND_NAMESPACE) -c governance-policy-propagator

############################################################
# test coverage
############################################################
COVERAGE_FILE = coverage.out

.PHONY: coverage-merge
coverage-merge: coverage-dependencies
	@echo Merging the coverage reports into $(COVERAGE_FILE)
	$(GOCOVMERGE) $(PWD)/coverage_* > $(COVERAGE_FILE)

.PHONY: coverage-verify
coverage-verify:
	./build/common/scripts/coverage_calc.sh
