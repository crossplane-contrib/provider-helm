# Project Setup
PROJECT_NAME := provider-helm
PROJECT_REPO := github.com/crossplane-contrib/$(PROJECT_NAME)

PLATFORMS ?= linux_amd64 linux_arm64
include build/makelib/common.mk

# ====================================================================================
# Setup Output

-include build/makelib/output.mk

# ====================================================================================
# Setup Go

# Set a sane default so that the nprocs calculation below is less noisy on the initial
# loading of this file
NPROCS ?= 1

# each of our test suites starts a kube-apiserver and running many test suites in
# parallel can lead to high CPU utilization. by default we reduce the parallelism
# to half the number of CPU cores.
GO_TEST_PARALLEL := $(shell echo $$(( $(NPROCS) / 2 )))

GO_STATIC_PACKAGES = $(GO_PROJECT)/cmd/provider
GO_LDFLAGS += -X $(GO_PROJECT)/pkg/version.Version=$(VERSION)
GO_SUBDIRS += cmd pkg apis
GO111MODULE = on
-include build/makelib/golang.mk

# ====================================================================================
# Setup Kubernetes tools

-include build/makelib/k8s_tools.mk

# ====================================================================================
# Setup Package

PACKAGE=package
export PACKAGE
PACKAGE_REGISTRY=$(PACKAGE)/.registry
PACKAGE_REGISTRY_SOURCE=config/package/manifests

DOCKER_REGISTRY = crossplane
IMAGES = provider-helm
-include build/makelib/image.mk

# ====================================================================================
# Targets

# run `make help` to see the targets and options

# We want submodules to be set up the first time `make` is run.
# We manage the build/ folder and its Makefiles as a submodule.
# The first time `make` is run, the includes of build/*.mk files will
# all fail, and this target will be run. The next time, the default as defined
# by the includes will be run instead.
fallthrough: submodules
	@echo Initial setup complete. Running make again . . .
	@make


# Generate a coverage report for cobertura applying exclusions on
# - generated file
cobertura:
	@cat $(GO_TEST_OUTPUT)/coverage.txt | \
		grep -v zz_generated.deepcopy | \
		$(GOCOVER_COBERTURA) > $(GO_TEST_OUTPUT)/cobertura-coverage.xml

# Ensure a PR is ready for review.
reviewable: generate lint
	@go mod tidy

# Ensure branch is clean.
check-diff: reviewable
	@$(INFO) checking that branch is clean
	@test -z "$$(git status --porcelain)" || $(FAIL)
	@$(OK) branch is clean

# integration tests
e2e.run: test-integration

# Run integration tests.
test-integration: $(KIND) $(KUBECTL)
	@$(INFO) running integration tests using kind $(KIND_VERSION)
	@$(ROOT_DIR)/cluster/local/integration_tests.sh || $(FAIL)
	@$(OK) integration tests passed

# Update the submodules, such as the common build scripts.
submodules:
	@git submodule sync
	@git submodule update --init --recursive

# This is for running out-of-cluster locally, and is for convenience. Running
# this make target will print out the command which was used. For more control,
# try running the binary directly with different arguments.
run: $(KUBECTL) generate
	@$(INFO) Running Crossplane locally out-of-cluster . . .
	@$(KUBECTL) apply -f config/crd/ -R
	go run cmd/provider/main.go -d

dev: $(KIND) $(KUBECTL)
	@$(INFO) Creating kind cluster
	@$(KIND) create cluster --name=provider-gcp-dev
	@$(KUBECTL) cluster-info --context kind-provider-gcp-dev
	@$(INFO) Installing Crossplane CRDs
	@$(KUBECTL) apply -k https://github.com/crossplane/crossplane//cluster?ref=master
	@$(INFO) Installing Provider GCP CRDs
	@$(KUBECTL) apply -f $(CRD_DIR) -R
	@$(INFO) Starting Provider GCP controllers
	@$(GO) run cmd/provider/main.go --debug

dev-clean: $(KIND) $(KUBECTL)
	@$(INFO) Deleting kind cluster
	@$(KIND) delete cluster --name=provider-gcp-dev

# ====================================================================================
# Package related targets

# Initialize the package folder
$(PACKAGE_REGISTRY):
	@mkdir -p $(PACKAGE_REGISTRY)/resources
	@touch $(PACKAGE_REGISTRY)/app.yaml $(PACKAGE_REGISTRY)/install.yaml

build.artifacts: build-package

CRD_DIR=config/crd
build-package: $(PACKAGE_REGISTRY)
# Copy CRDs over
#
# The reason this looks complicated is because it is
# preserving the original crd filenames and changing
# *.yaml to *.crd.yaml.
#
# An alternate and simpler-looking approach would
# be to cat all of the files into a single crd.yaml,
# but then we couldn't use per CRD metadata files.
	@$(INFO) building package in $(PACKAGE)
	@find $(CRD_DIR) -type f -name '*.yaml' | \
		while read filename ; do mkdir -p $(PACKAGE_REGISTRY)/resources/$$(basename $${filename%_*});\
		concise=$${filename#*_}; \
		cat $$filename > \
		$(PACKAGE_REGISTRY)/resources/$$( basename $${filename%_*} )/$$( basename $${concise/.yaml/.crd.yaml} ) \
		; done
	@cp -r $(PACKAGE_REGISTRY_SOURCE)/* $(PACKAGE_REGISTRY)

clean: clean-package

clean-package:
	@rm -rf $(PACKAGE)

manifests:
	@$(INFO) Deprecated. Run make generate instead.

.PHONY: cobertura reviewable submodules fallthrough test-integration run clean-package build-package manifests dev dev-clean