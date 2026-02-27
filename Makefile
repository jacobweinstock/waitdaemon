

help: ## show this help message
	@grep -E '^[a-zA-Z_-]+.*:.*?## .*$$' Makefile | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[32m%-30s\033[0m %s\n", $$1, $$2}'

########### Tools variables ###########
# Tool versions
GORELEASER_VERSION     := v2.14.1
GOLANGCI_LINT_VERSION  := v2.10.1

# Tool fully qualified paths (FQP)
TOOLS_DIR := $(PWD)/out/tools
GORELEASER_FQP := $(TOOLS_DIR)/goreleaser-$(GORELEASER_VERSION)

$(GORELEASER_FQP):
	GOBIN=$(TOOLS_DIR) go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)
	@mv $(TOOLS_DIR)/goreleaser $(GORELEASER_FQP)

.PHONY: clean-tools
clean-tools: ## Remove all tools
	rm -rf $(TOOLS_DIR)

tools: $(GORELEASER_FQP) ## install tools

.PHONY: build
build: bin/waitdaemon ## build the binary

bin/waitdaemon:
	CGO_ENABLED=0 go build -o bin/waitdaemon main.go

.PHONY: build-image
build-image: ## build the docker image
	docker build -t ghcr.io/jacobweinstock/waitdaemon:latest .

.PHONY: release-local
release-local: tools ## Build and release all binaries and docker images locally
	$(GORELEASER_FQP) release --snapshot --clean --skip=publish,announce

.PHONY: release
release: tools ## Build and release all binaries
	$(GORELEASER_FQP) release --clean


############## Linting ##############
.PHONY: lint
lint: _lint  ## Run linting

LINT_ARCH := $(shell uname -m)
LINT_OS := $(shell uname)
LINT_OS_LOWER := $(shell echo $(LINT_OS) | tr '[:upper:]' '[:lower:]')
LINT_ROOT := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))

# shellcheck and hadolint lack arm64 native binaries: rely on x86-64 emulation
ifeq ($(LINT_OS),Darwin)
	ifeq ($(LINT_ARCH),arm64)
		LINT_ARCH=x86_64
	endif
endif

LINTERS :=
FIXERS :=

GOLANGCI_LINT_CONFIG := $(LINT_ROOT)/.golangci.yml
GOLANGCI_LINT_BIN := $(LINT_ROOT)/out/tools/golangci-lint-$(GOLANGCI_LINT_VERSION)-$(LINT_ARCH)
$(GOLANGCI_LINT_BIN):
	mkdir -p $(LINT_ROOT)/out/tools
	rm -rf $(LINT_ROOT)/out/tools/golangci-lint-*
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(LINT_ROOT)/out/tools $(GOLANGCI_LINT_VERSION)
	mv $(LINT_ROOT)/out/tools/golangci-lint $@

LINTERS += golangci-lint-lint
golangci-lint-lint: $(GOLANGCI_LINT_BIN)
	find . -name go.mod -not -path "./out/*" -execdir sh -c '"$(GOLANGCI_LINT_BIN)" run --timeout 10m -c "$(GOLANGCI_LINT_CONFIG)"' '{}' '+'

FIXERS += golangci-lint-fix
golangci-lint-fix: $(GOLANGCI_LINT_BIN)
	find . -name go.mod -not -path "./out/*" -execdir "$(GOLANGCI_LINT_BIN)" run -c "$(GOLANGCI_LINT_CONFIG)" --fix \;

.PHONY: _lint $(LINTERS)
_lint: $(LINTERS)

.PHONY: fix $(FIXERS)
fix: $(FIXERS)
