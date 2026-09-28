# ====================================================================================
# Setup Project

PROJECT_NAME ?= provider-talos
PROJECT_REPO ?= github.com/inc4-ops/$(PROJECT_NAME)

# Terraform is used at development time only, to dump the provider schema into
# config/schema.json. It is not shipped: every resource runs in-process through
# the plugin-framework (no-fork) client, so the image carries no terraform binary
# and no provider plugin.
export TERRAFORM_VERSION ?= 1.5.7

# Do not allow a version of terraform greater than 1.5.x, due to versions 1.6+ being
# licensed under BSL, which is not permitted.
TERRAFORM_VERSION_VALID := $(shell [ "$(TERRAFORM_VERSION)" = "`printf "$(TERRAFORM_VERSION)\n1.6" | sort -V | head -n1`" ] && echo 1 || echo 0)

export TERRAFORM_PROVIDER_SOURCE ?= siderolabs/talos
export TERRAFORM_PROVIDER_REPO ?= https://github.com/siderolabs/terraform-provider-talos
# Must equal the github.com/siderolabs/terraform-provider-talos version in go.mod (make check-tf-provider-version).
export TERRAFORM_PROVIDER_VERSION ?= 0.12.0
export TERRAFORM_DOCS_PATH ?= docs/resources

PLATFORMS ?= linux_amd64 linux_arm64

# -include will silently skip missing files, which allows us
# to load those files with a target in the Makefile. If only
# "include" was used, the make command would fail and refuse
# to run a target until the include commands succeeded.
-include build/makelib/common.mk

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

GO_REQUIRED_VERSION ?= 1.26
GOLANGCILINT_VERSION ?= 2.13.0
GO_STATIC_PACKAGES = $(GO_PROJECT)/cmd/provider $(GO_PROJECT)/cmd/generator
GO_LDFLAGS += -X $(GO_PROJECT)/internal/version.Version=$(VERSION)
# config holds the invariants test (config/provider_test.go) that guards the node-reset and
# CA-key rules; `make test` only runs the packages under $(GO_SUBDIRS).
GO_SUBDIRS += cmd internal apis config
-include build/makelib/golang.mk

# ====================================================================================
# Setup Kubernetes tools

KIND_VERSION = v0.31.0
UPTEST_VERSION = v2.2.0
CRDDIFF_VERSION = v0.12.1
CROSSPLANE_CLI_VERSION = v2.4.0
# for e2e testing
CROSSPLANE_VERSION = 2.4.0
-include build/makelib/k8s_tools.mk

# ====================================================================================
# Setup Images

REGISTRY_ORGS ?= ghcr.io/inc4-ops
IMAGES = $(PROJECT_NAME)
-include build/makelib/imagelight.mk

# ====================================================================================
# Setup XPKG

XPKG_REG_ORGS ?= ghcr.io/inc4-ops
# NOTE(hasheddan): skip promoting on xpkg.crossplane.io as channel tags are
# inferred.
XPKG_REG_ORGS_NO_PROMOTE ?= ghcr.io/inc4-ops
XPKGS = $(PROJECT_NAME)
-include build/makelib/xpkg.mk

# ====================================================================================
# Fallthrough

# run `make help` to see the targets and options

# We want submodules to be set up the first time `make` is run.
# We manage the build/ folder and its Makefiles as a submodule.
# The first time `make` is run, the includes of build/*.mk files will
# all fail, and this target will be run. The next time, the default as defined
# by the includes will be run instead.
fallthrough: submodules
	@echo Initial setup complete. Running make again . . .
	@make

# NOTE(hasheddan): we force image building to happen prior to xpkg build so that
# we ensure image is present in daemon.
xpkg.build.provider-talos: do.build.images

# NOTE(hasheddan): we ensure up is installed prior to running platform-specific
# build steps in parallel to avoid encountering an installation race condition.
build.init: $(UP) $(CROSSPLANE_CLI) check-terraform-version

# ====================================================================================
# Setup Terraform for fetching provider schema
TERRAFORM := $(TOOLS_HOST_DIR)/terraform-$(TERRAFORM_VERSION)
TERRAFORM_WORKDIR := $(WORK_DIR)/terraform
TERRAFORM_PROVIDER_SCHEMA := config/schema.json

check-terraform-version:
ifneq ($(TERRAFORM_VERSION_VALID),1)
	$(error invalid TERRAFORM_VERSION $(TERRAFORM_VERSION), must be less than 1.6.0 since that version introduced a not permitted BSL license))
endif

$(TERRAFORM): check-terraform-version
	@$(INFO) installing terraform $(HOSTOS)-$(HOSTARCH)
	@mkdir -p $(TOOLS_HOST_DIR)/tmp-terraform
	@curl -fsSL https://releases.hashicorp.com/terraform/$(TERRAFORM_VERSION)/terraform_$(TERRAFORM_VERSION)_$(SAFEHOST_PLATFORM).zip -o $(TOOLS_HOST_DIR)/tmp-terraform/terraform.zip
	@unzip $(TOOLS_HOST_DIR)/tmp-terraform/terraform.zip -d $(TOOLS_HOST_DIR)/tmp-terraform
	@mv $(TOOLS_HOST_DIR)/tmp-terraform/terraform $(TERRAFORM)
	@rm -fr $(TOOLS_HOST_DIR)/tmp-terraform
	@$(OK) installing terraform $(HOSTOS)-$(HOSTARCH)

$(TERRAFORM_PROVIDER_SCHEMA): $(TERRAFORM)
	@$(INFO) generating provider schema for $(TERRAFORM_PROVIDER_SOURCE) $(TERRAFORM_PROVIDER_VERSION)
	@mkdir -p $(TERRAFORM_WORKDIR)
	@echo '{"terraform":[{"required_providers":[{"provider":{"source":"'"$(TERRAFORM_PROVIDER_SOURCE)"'","version":"'"$(TERRAFORM_PROVIDER_VERSION)"'"}}],"required_version":"'"$(TERRAFORM_VERSION)"'"}]}' > $(TERRAFORM_WORKDIR)/main.tf.json
	@$(TERRAFORM) -chdir=$(TERRAFORM_WORKDIR) init > $(TERRAFORM_WORKDIR)/terraform-logs.txt 2>&1
	@$(TERRAFORM) -chdir=$(TERRAFORM_WORKDIR) providers schema -json=true > $(TERRAFORM_PROVIDER_SCHEMA) 2>> $(TERRAFORM_WORKDIR)/terraform-logs.txt
	@$(OK) generating provider schema for $(TERRAFORM_PROVIDER_SOURCE) $(TERRAFORM_PROVIDER_VERSION)

pull-docs:
	@if [ ! -d "$(WORK_DIR)/$(TERRAFORM_PROVIDER_SOURCE)" ]; then \
  		mkdir -p "$(WORK_DIR)/$(TERRAFORM_PROVIDER_SOURCE)" && \
		git clone -c advice.detachedHead=false --depth 1 --filter=blob:none --branch "v$(TERRAFORM_PROVIDER_VERSION)" --sparse "$(TERRAFORM_PROVIDER_REPO)" "$(WORK_DIR)/$(TERRAFORM_PROVIDER_SOURCE)"; \
	fi
	@git -C "$(WORK_DIR)/$(TERRAFORM_PROVIDER_SOURCE)" sparse-checkout set "$(TERRAFORM_DOCS_PATH)"

generate.init: check-tf-provider-version $(TERRAFORM_PROVIDER_SCHEMA) pull-docs

# go.mod embeds the provider; the schema and docs come from the Makefile pin. Keep them equal.
check-tf-provider-version:
	@gomod=$$(go list -m -f '{{.Version}}' github.com/siderolabs/terraform-provider-talos); \
	if [ "$$gomod" != "v$(TERRAFORM_PROVIDER_VERSION)" ]; then \
		echo "go.mod has terraform-provider-talos $$gomod, Makefile has v$(TERRAFORM_PROVIDER_VERSION)"; exit 1; fi

.PHONY: $(TERRAFORM_PROVIDER_SCHEMA) pull-docs check-terraform-version check-tf-provider-version

# NOTICE must list every MPL-2.0 module compiled into the shipped binaries. Builds the
# provider for each of $(PLATFORMS) and compares `go version -m` with NOTICE (hack/notice.py).
notice.check:
	@$(INFO) checking NOTICE against the modules in the provider binaries
	@$(foreach p,$(PLATFORMS),CGO_ENABLED=0 GOOS=$(word 1,$(subst _, ,$(p))) GOARCH=$(word 2,$(subst _, ,$(p))) $(GO) build -trimpath -o $(WORK_DIR)/notice/$(p)/provider ./cmd/provider || $(FAIL);)
	@python3 hack/notice.py check NOTICE $(foreach p,$(PLATFORMS),$(WORK_DIR)/notice/$(p)/provider) || $(FAIL)
	@$(OK) checking NOTICE against the modules in the provider binaries

.PHONY: notice.check

# ====================================================================================
# Targets

# NOTE: the build submodule currently overrides XDG_CACHE_HOME in order to
# force the Helm 3 to use the .work/helm directory. This causes Go on Linux
# machines to use that directory as the build cache as well. We should adjust
# this behavior in the build submodule because it is also causing Linux users
# to duplicate their build cache, but for now we just make it easier to identify
# its location in CI so that we cache between builds.
go.cachedir:
	@go env GOCACHE

go.mod.cachedir:
	@go env GOMODCACHE

# Generate a coverage report for cobertura applying exclusions on
# - generated file
cobertura:
	@cat $(GO_TEST_OUTPUT)/coverage.txt | \
		grep -v zz_ | \
		$(GOCOVER_COBERTURA) > $(GO_TEST_OUTPUT)/cobertura-coverage.xml

# Update the submodules, such as the common build scripts.
submodules:
	@git submodule sync
	@git submodule update --init --recursive

# This is for running out-of-cluster locally, and is for convenience. Running
# this make target will print out the command which was used. For more control,
# try running the binary directly with different arguments.
run: go.build
	@$(INFO) Running Crossplane locally out-of-cluster . . .
	@# To see other arguments that can be provided, run the command with --help instead
	$(GO_OUT_DIR)/provider --debug

# ====================================================================================
# End to End Testing
CROSSPLANE_NAMESPACE = crossplane-system
# The build submodule defaults to local-dev, a name every crossplane repo shares; a
# repo-unique name keeps `make local-deploy` and `make e2e` off other repos' clusters.
KIND_CLUSTER_NAME ?= $(PROJECT_NAME)
-include build/makelib/local.xpkg.mk
-include build/makelib/controlplane.mk

local-deploy: build controlplane.up local.xpkg.deploy.provider.$(PROJECT_NAME)
	@$(INFO) running locally built provider
	@$(KUBECTL) wait provider.pkg $(PROJECT_NAME) --for condition=Healthy --timeout 5m
	@$(KUBECTL) -n crossplane-system wait --for=condition=Available deployment --all --timeout=5m
	@$(OK) running locally built provider

# Live e2e against a real Talos node: a Talos container in maintenance mode on the
# kind docker network (cluster/test/talos-e2e.sh). `make e2e` builds and deploys the
# provider into a fresh kind cluster of its own and runs the script; `make e2e-talos`
# runs the script against a running `make local-deploy`. The host needs docker and jq;
# kubectl, talosctl and yq are pinned downloads. The script only ever talks to the
# kind-$(KIND_CLUSTER_NAME) context. Budget about 4 GB of docker memory.
#
# The Talos v1.14 container passes overlayfs layers to the kernel as file descriptors
# (lowerdir+ through FSCONFIG_SET_FD), so it only boots on a Docker host kernel with
# that support. colima's default Ubuntu 24.04 VM (kernel 6.8) lacks it: Talos aborts
# with "failed to set up /etc overlay" and apid never listens. The ubuntu-24.04 GitHub
# runners (kernel 6.17) are where the e2e is expected to run.
TALOS_VERSION ?= v1.14.1
K8S_VERSION ?= 1.36.5
TALOSCTL := $(TOOLS_HOST_DIR)/talosctl-$(TALOS_VERSION)

$(TALOSCTL):
	@$(INFO) installing talosctl $(TALOS_VERSION)
	@mkdir -p $(TOOLS_HOST_DIR) || $(FAIL)
	@curl -fsSLo $(TALOSCTL).tmp https://github.com/siderolabs/talos/releases/download/$(TALOS_VERSION)/talosctl-$(HOSTOS)-$(SAFEHOSTARCH) || $(FAIL)
	@chmod +x $(TALOSCTL).tmp && mv $(TALOSCTL).tmp $(TALOSCTL) || $(FAIL)
	@$(OK) installing talosctl $(TALOS_VERSION)

e2e-talos: $(KUBECTL) $(TALOSCTL) $(YQ)
	@$(INFO) running Talos e2e
	@KUBECTL=$(KUBECTL) TALOSCTL=$(TALOSCTL) YQ=$(YQ) \
		TALOS_VERSION=$(TALOS_VERSION) K8S_VERSION=$(K8S_VERSION) \
		KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) CROSSPLANE_NAMESPACE=$(CROSSPLANE_NAMESPACE) \
		./cluster/test/talos-e2e.sh || $(FAIL)
	@$(OK) running Talos e2e

# `make e2e` runs in a kind cluster and a kubeconfig of its own: it deletes and recreates
# kind-$(E2E_KIND_CLUSTER_NAME), and kind, helm and kubectl read and write only
# $(E2E_KUBECONFIG), so it never switches the current context of ~/.kube/config.
E2E_KIND_CLUSTER_NAME ?= $(PROJECT_NAME)-e2e
E2E_KUBECONFIG ?= $(WORK_DIR)/e2e/kubeconfig
E2E_ENV = KUBECONFIG=$(E2E_KUBECONFIG)
E2E_VARS = KIND_CLUSTER_NAME=$(E2E_KIND_CLUSTER_NAME)

# build/makelib runs `make e2e` as the sub-makes e2e.init, e2e.run and e2e.done in
# order, so the provider is deployed before the e2e starts, even under make -j.
e2e.init: e2e.deploy
e2e.run: e2e.talos

# A fresh kind cluster, as build/makelib/uptest.mk does for its e2e: local.xpkg.deploy
# reuses a fixed package digest and image tag, so a provider already installed in a
# reused cluster keeps its old CRDs and keeps running its old image.
e2e.deploy:
	@mkdir -p $(dir $(E2E_KUBECONFIG)) || $(FAIL)
	@$(E2E_ENV) $(MAKE) $(E2E_VARS) controlplane.down
	@$(E2E_ENV) $(MAKE) $(E2E_VARS) local-deploy

e2e.talos:
	@$(E2E_ENV) $(MAKE) $(E2E_VARS) e2e-talos

# Dumps the `make e2e` control plane to $(OUTPUT_DIR)/controlplane-dump (CI runs it when
# the e2e fails).
e2e.dump:
	@$(E2E_ENV) $(MAKE) $(E2E_VARS) controlplane.dump

.PHONY: e2e-talos e2e.deploy e2e.talos e2e.dump

# Reports only; it never fails. crossplane/uptest has no cmd/crddiff (checked v0.12.0 to
# v2.2.0) and no v0.12.1 tag, so run it from the upbound/uptest module, as the
# crossplane-contrib providers do.
crddiff:
	@$(INFO) Checking breaking CRD schema changes
	@for crd in $${MODIFIED_CRD_LIST}; do \
		if ! git cat-file -e "$${GITHUB_BASE_REF}:$${crd}" 2>/dev/null; then \
			echo "CRD $${crd} does not exist in the $${GITHUB_BASE_REF} branch. Skipping..." ; \
			continue ; \
		fi ; \
		echo "Checking $${crd} for breaking API changes..." ; \
		changes_detected=$$(go run github.com/upbound/uptest/cmd/crddiff@$(CRDDIFF_VERSION) revision --enable-upjet-extensions <(git cat-file -p "$${GITHUB_BASE_REF}:$${crd}") "$${crd}" 2>&1) ; \
		if [[ $$? != 0 ]] ; then \
			printf "\033[31m"; echo "Breaking change detected!"; printf "\033[0m" ; \
			echo "$${changes_detected}" ; \
			echo ; \
		fi ; \
	done
	@$(OK) Checking breaking CRD schema changes

# Compares the native schema version of every resource in config/schema.json with the base
# branch; upjet v2 writes no config/generated.lst, so the list comes from the schema itself.
schema-version-diff:
	@$(INFO) Checking for native state schema version changes
	@export PREV_PROVIDER_VERSION=$$(git cat-file -p "${GITHUB_BASE_REF}:Makefile" | sed -nr 's/^export[[:space:]]*TERRAFORM_PROVIDER_VERSION[[:space:]]*[?:]?=[[:space:]]*(.+)/\1/p'); \
	echo Detected previous Terraform provider version: $${PREV_PROVIDER_VERSION}; \
	echo Current Terraform provider version: $${TERRAFORM_PROVIDER_VERSION}; \
	mkdir -p $(WORK_DIR); \
	git cat-file -p "$${GITHUB_BASE_REF}:config/schema.json" > "$(WORK_DIR)/schema.json.$${PREV_PROVIDER_VERSION}"; \
	jq -c '[.provider_schemas[].resource_schemas | keys[]]' config/schema.json > "$(WORK_DIR)/generated.lst"; \
	python3 scripts/version_diff.py "$(WORK_DIR)/generated.lst" "$(WORK_DIR)/schema.json.$${PREV_PROVIDER_VERSION}" config/schema.json
	@$(OK) Checking for native state schema version changes

.PHONY: cobertura submodules fallthrough run crds.clean

# ====================================================================================
# Special Targets

define CROSSPLANE_MAKE_HELP
Crossplane Targets:
    cobertura             Generate a coverage report for cobertura applying exclusions on generated files.
    submodules            Update the submodules, such as the common build scripts.
    run                   Run crossplane locally, out-of-cluster. Useful for development.

endef
# The reason CROSSPLANE_MAKE_HELP is used instead of CROSSPLANE_HELP is because the crossplane
# binary will try to use CROSSPLANE_HELP if it is set, and this is for something different.
export CROSSPLANE_MAKE_HELP

crossplane.help:
	@echo "$$CROSSPLANE_MAKE_HELP"

help-special: crossplane.help

.PHONY: crossplane.help help-special

# TODO(negz): Update CI to use these targets.
vendor: modules.download
vendor.check: modules.check
