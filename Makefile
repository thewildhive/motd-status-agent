SHELL := /bin/bash

BINARY_NAME := motd-status-agent
BIN_DIR := bin
GO := go
VERSION ?= dev
GO_BUILD_FLAGS := -buildvcs=false -trimpath
LDFLAGS := -ldflags="-s -w -X main.VERSION=$(VERSION)"

.PHONY: all build build-optimized clean test test-status-agent-integration check check-workflows cross-compile package help

all: build-optimized

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GO_BUILD_FLAGS) -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/motd-status-agent

build-optimized:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GO_BUILD_FLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/motd-status-agent

clean:
	rm -rf $(BIN_DIR) dist

test:
	$(GO) test -count=1 ./...

test-status-agent-integration:
	bash scripts/test-status-agent-integration.sh

check: check-workflows
	@files="$$(gofmt -l .)"; test -z "$$files" || { echo "Run gofmt -w .:"; echo "$$files"; exit 1; }
	@before="$$(sha256sum go.mod go.sum 2>/dev/null || sha256sum go.mod)"; $(GO) mod tidy; after="$$(sha256sum go.mod go.sum 2>/dev/null || sha256sum go.mod)"; test "$$before" = "$$after" || { echo "go mod tidy changed module files"; exit 1; }
	$(GO) mod verify
	$(GO) vet ./...
	$(GO) test -count=1 ./...
	$(GO) test -race -count=1 ./...
	$(GO) build $(GO_BUILD_FLAGS) ./cmd/...
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) -o /tmp/motd-status-agent-amd64-check ./cmd/motd-status-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GO_BUILD_FLAGS) -o /tmp/motd-status-agent-arm64-check ./cmd/motd-status-agent
	$(GO) build $(GO_BUILD_FLAGS) -o /tmp/motd-status-agent-vulncheck ./cmd/motd-status-agent
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.6.0 -mode=binary /tmp/motd-status-agent-vulncheck

check-workflows:
	$(GO) run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
	bash -n .github/scripts/*.sh scripts/*.sh

cross-compile:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/motd-status-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GO_BUILD_FLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/motd-status-agent

package:
	SIGNING_KEY_FILE="$(SIGNING_KEY_FILE)" .github/scripts/package-release.sh "$(VERSION)" dist

help:
	@echo "motd-status-agent targets:"
	@echo "  make check                         Run the authoritative local gate"
	@echo "  make test-status-agent-integration Test against the pinned go-motd consumer"
	@echo "  make cross-compile                 Build Linux amd64 and arm64 binaries"
	@echo "  make package VERSION=X.Y.Z SIGNING_KEY_FILE=/path/to/key"
