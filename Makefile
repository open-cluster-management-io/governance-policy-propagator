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


# This repo is build in Travis-ci by default;
# Override this variable in local env.
TRAVIS_BUILD ?= 1

# Image URL to use all building/pushing image targets;
# Use your own docker registry and image name for dev/test by overridding the IMG and REGISTRY environment variable.
IMG ?= $(shell cat COMPONENT_NAME 2> /dev/null)
REGISTRY ?= quay.io/open-cluster-management
TAG ?= edge

# Github host to use for checking the source tree;
# Override this variable ue with your own value if you're working on forked repo.
GIT_HOST ?= github.com/open-cluster-management

PWD := $(shell pwd)
BASE_DIR := $(shell basename $(PWD))

# Keep an existing GOPATH, make a private one if it is undefined
GOPATH_DEFAULT := $(PWD)/.go
export GOPATH ?= $(GOPATH_DEFAULT)
GOBIN_DEFAULT := $(GOPATH)/bin
export GOBIN ?= $(GOBIN_DEFAULT)
export PATH := $(PATH):$(GOBIN)
GOARCH = $(shell go env GOARCH)
GOOS = $(shell go env GOOS)
TESTARGS_DEFAULT := "-v"
export TESTARGS ?= $(TESTARGS_DEFAULT)
DEST ?= $(GOPATH)/src/$(GIT_HOST)/$(BASE_DIR)
VERSION ?= $(shell cat COMPONENT_VERSION 2> /dev/null)
KBVERSION := 2.3.0
IMAGE_NAME_AND_VERSION ?= $(REGISTRY)/$(IMG)
# Handle KinD configuration
KIND_NAME ?= test-hub
KIND_NAMESPACE ?= open-cluster-management
KIND_VERSION ?= latest
ifneq ($(KIND_VERSION), latest)
	KIND_ARGS = --image kindest/node:$(KIND_VERSION)
else
	KIND_ARGS =
endif

LOCAL_OS := $(shell uname)
ifeq ($(LOCAL_OS),Linux)
    TARGET_OS ?= linux
    XARGS_FLAGS="-r"
else ifeq ($(LOCAL_OS),Darwin)
    TARGET_OS ?= darwin
    XARGS_FLAGS=
else
    $(error "This system's OS $(LOCAL_OS) isn't recognized/supported")
endif

.PHONY: fmt lint test coverage build build-images

USE_VENDORIZED_BUILD_HARNESS ?=

ifndef USE_VENDORIZED_BUILD_HARNESS
	ifeq ($(TRAVIS_BUILD),1)
		ifeq (,$(wildcard ./.build-harness-bootstrap))
			-include $(shell curl -H 'Accept: application/vnd.github.v4.raw' -L https://api.github.com/repos/open-cluster-management/build-harness-extensions/contents/templates/Makefile.build-harness-bootstrap -o .build-harness-bootstrap; echo .build-harness-bootstrap)
		endif
	endif
else
-include vbh/.build-harness-vendorized
endif

default::
	@echo "Build Harness Bootstrapped"

include build/common/Makefile.common.mk

############################################################
# work section
############################################################
$(GOBIN):
	@echo "create gobin"
	@mkdir -p $(GOBIN)

work: $(GOBIN)

############################################################
# format section
############################################################

# All available format: format-go format-protos format-python
# Default value will run all formats, override these make target with your requirements:
#    eg: fmt: format-go format-protos
fmt: # format-go format-protos format-python
	go fmt ./...

############################################################
# check section
############################################################

check: lint

# All available linters: lint-dockerfiles lint-scripts lint-yaml lint-copyright-banner lint-go lint-python lint-helm lint-markdown lint-sass lint-typescript lint-protos
# Default value will run all linters, override these make target with your requirements:
#    eg: lint: lint-go lint-yaml
lint: lint-all

############################################################
# test section
############################################################

test:
	go test ${TESTARGS} `go list ./... | grep -v test/e2e`

test-dependencies:
	curl -L https://github.com/kubernetes-sigs/kubebuilder/releases/download/v$(KBVERSION)/kubebuilder_$(KBVERSION)_$(GOOS)_$(GOARCH).tar.gz | tar -xz -C /tmp/
	sudo mv -n /tmp/kubebuilder_$(KBVERSION)_$(GOOS)_$(GOARCH) /usr/local/kubebuilder
	export PATH=$PATH:/usr/local/kubebuilder/bin

############################################################
# build section
############################################################

build:
	@build/common/scripts/gobuild.sh build/_output/bin/$(IMG) ./cmd/manager

local:
	@GOOS=darwin build/common/scripts/gobuild.sh build/_output/bin/$(IMG) ./cmd/manager

############################################################
# images section
############################################################

build-images:
	@docker build -t ${IMAGE_NAME_AND_VERSION} -f build/Dockerfile .
	@docker tag ${IMAGE_NAME_AND_VERSION} $(REGISTRY)/$(IMG):$(TAG)

############################################################
# clean section
############################################################
clean::
	rm -f build/_output/bin/$(IMG)

############################################################
# check copyright section
############################################################
copyright-check:
	./build/copyright-check.sh $(TRAVIS_BRANCH)


############################################################
# e2e test section
############################################################
.PHONY: kind-bootstrap-cluster
kind-bootstrap-cluster: kind-create-cluster install-crds kind-deploy-controller install-resources

.PHONY: kind-bootstrap-cluster-dev
kind-bootstrap-cluster-dev: kind-create-cluster install-crds install-resources

kind-deploy-controller:
	@echo installing policy-propagator
	kubectl get ns $(KIND_NAMESPACE) ; if [ $$? -ne 0 ] ; then kubectl create ns $(KIND_NAMESPACE) ; fi
	kubectl apply -f deploy/ -n $(KIND_NAMESPACE)

kind-deploy-controller-dev:
	@echo Pushing image to KinD cluster
	kind load docker-image $(REGISTRY)/$(IMG):$(TAG) --name $(KIND_NAME)
	@echo Installing $(IMG)
	kubectl get ns $(KIND_NAMESPACE) ; if [ $$? -ne 0 ] ; then kubectl create ns $(KIND_NAMESPACE) ; fi
	kubectl apply -f deploy/ -n $(KIND_NAMESPACE)
	@echo "Patch deployment image"
	kubectl patch deployment $(IMG) -n $(KIND_NAMESPACE) -p "{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"$(IMG)\",\"imagePullPolicy\":\"Never\"}]}}}}"
	kubectl patch deployment $(IMG) -n $(KIND_NAMESPACE) -p "{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"$(IMG)\",\"image\":\"$(REGISTRY)/$(IMG):$(TAG)\"}]}}}}"
	kubectl rollout status -n $(KIND_NAMESPACE) deployment $(IMG) --timeout=180s

# Specify KIND_VERSION to indicate the version tag of the KinD image
kind-create-cluster:
	@echo "creating cluster"
	kind create cluster --name $(KIND_NAME) $(KIND_ARGS)

kind-delete-cluster:
	kind delete cluster --name $(KIND_NAME)

install-crds:
	@echo installing crds
	kubectl apply -f deploy/crds/apps.open-cluster-management.io_placementrules_crd.yaml
	kubectl apply -f deploy/crds/policy.open-cluster-management.io_placementbindings_crd.yaml
	kubectl apply -f deploy/crds/policy.open-cluster-management.io_policies_crd.yaml
	kubectl apply -f deploy/crds/policy.open-cluster-management.io_policyautomations_crd.yaml
	kubectl apply -f test/crds/cluster.open-cluster-management.io_managedclusters.yaml
	kubectl apply -f test/crds/cluster.open-cluster-management.io_placementdecisions_crd.yaml
	kubectl apply -f test/crds/cluster.open-cluster-management.io_placements_crd.yaml
	kubectl apply -f test/crds/tower.ansible.com_joblaunch_crd.yaml

install-resources:
	@echo creating namespaces
	kubectl create ns policy-propagator-test
	kubectl create ns managed1
	kubectl create ns managed2
	@echo creating cluster resources
	kubectl apply -f test/resources/managed1-cluster.yaml
	kubectl apply -f test/resources/managed2-cluster.yaml

e2e-test:
	ginkgo -v --slowSpecThreshold=10 test/e2e

e2e-dependencies:
	go get github.com/onsi/ginkgo/ginkgo@v1.14.1
	go get github.com/onsi/gomega/...@v1.10.1

e2e-debug:
	kubectl get all -n $(KIND_NAMESPACE)
	kubectl get Policy.policy.open-cluster-management.io --all-namespaces
	kubectl describe pods -n $(KIND_NAMESPACE)
	kubectl logs $$(kubectl get pods -n $(KIND_NAMESPACE) -o name | grep $(IMG)) -n $(KIND_NAMESPACE)

############################################################
# e2e test coverage
############################################################
build-instrumented:
	go test -covermode=atomic -coverpkg=github.com/open-cluster-management/$(IMG)... -c -tags e2e ./cmd/manager -o build/_output/bin/$(IMG)-instrumented

run-instrumented:
	WATCH_NAMESPACE="" ./build/_output/bin/$(IMG)-instrumented -test.run "^TestRunMain$$" -test.coverprofile=coverage.out &>/dev/null &

stop-instrumented:
	ps -ef | grep 'govern' | grep -v grep | awk '{print $$2}' | xargs kill
