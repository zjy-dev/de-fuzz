# DeFuzz Makefile
# Go project best practices

# ==============================================================================
# Variables
# ==============================================================================

# Binary name and paths
BINARY_NAME := defuzz
CMD_PATH := ./cmd/defuzz
CORE_BINARY_NAME := defuzz-core
CORE_CMD_PATH := ./cmd/defuzz-core
BUILD_DIR := .

# Proto
PROTO_DIR := ./specs/002-agentic-loop-redesign/contracts
PROTO_FILE := oracle.proto
GO_PB_OUT := ./internal/service/pb
PY_PB_OUT := ./orchestrator/defuzz_loop/clients/pb

# Go commands
GO := go
GOTEST := $(GO) test
GOBUILD := $(GO) build
GOMOD := $(GO) mod
GOFMT := gofmt
GOLINT := golangci-lint

# Build info (injected via ldflags if needed)
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)"

# Test
TEST_REPORT_DIR := ./test-report
TEST_TIMEOUT := 10m

# Default target
.DEFAULT_GOAL := help

# ==============================================================================
# Build
# ==============================================================================

.PHONY: build
build: ## Build the binary
	@echo "🔨 Building $(BINARY_NAME)..."
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_PATH)
	@echo "✅ Built: $(BUILD_DIR)/$(BINARY_NAME)"

.PHONY: build-debug
build-debug: ## Build with debug info (no stripping)
	@echo "🔨 Building $(BINARY_NAME) (debug)..."
	$(GOBUILD) -gcflags="all=-N -l" -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_PATH)
	@echo "✅ Built: $(BUILD_DIR)/$(BINARY_NAME) (debug)"

.PHONY: install
install: ## Install binary to $GOPATH/bin
	@echo "📦 Installing $(BINARY_NAME)..."
	$(GO) install $(LDFLAGS) $(CMD_PATH)
	@echo "✅ Installed to $(shell go env GOPATH)/bin/$(BINARY_NAME)"

.PHONY: build-core
build-core: ## Build the deterministic gRPC+MCP Go core (cmd/defuzz-core)
	@echo "🔨 Building $(CORE_BINARY_NAME)..."
	$(GOBUILD) -o $(BUILD_DIR)/$(CORE_BINARY_NAME) $(CORE_CMD_PATH)
	@echo "✅ Built: $(BUILD_DIR)/$(CORE_BINARY_NAME)"

.PHONY: proto
proto: ## Generate Go + Python gRPC stubs from oracle.proto (uses uv-managed grpc_tools.protoc)
	@echo "🧬 Generating protobuf stubs..."
	@mkdir -p $(GO_PB_OUT) $(PY_PB_OUT)
	cd orchestrator && PATH="$$PATH:$(shell go env GOPATH)/bin" uv run python -m grpc_tools.protoc \
		-I ../$(PROTO_DIR) \
		--go_out=../$(GO_PB_OUT) --go_opt=paths=source_relative \
		--go-grpc_out=../$(GO_PB_OUT) --go-grpc_opt=paths=source_relative \
		--python_out=defuzz_loop/clients/pb \
		--grpc_python_out=defuzz_loop/clients/pb \
		--pyi_out=defuzz_loop/clients/pb \
		$(PROTO_FILE)
	@echo "⚠️  Then fix the Python grpc import to package-relative ('from . import oracle_pb2')."
	@echo "✅ Stubs generated"

# ==============================================================================
# Development
# ==============================================================================

.PHONY: run
run: build ## Build and run
	./$(BINARY_NAME)

.PHONY: fmt
fmt: ## Format code
	@echo "🎨 Formatting code..."
	$(GOFMT) -s -w ./cmd ./internal
	@echo "✅ Done"

.PHONY: lint
lint: ## Run linter (requires golangci-lint)
	@echo "🔍 Running linter..."
	$(GOLINT) run ./cmd/... ./internal/...

.PHONY: vet
vet: ## Run go vet
	@echo "🔍 Running go vet..."
	$(GO) vet ./cmd/... ./internal/...

.PHONY: tidy
tidy: ## Tidy and verify dependencies
	@echo "📦 Tidying modules..."
	$(GOMOD) tidy -e
	$(GOMOD) verify
	@echo "✅ Done"

# ==============================================================================
# Testing
# ==============================================================================

.PHONY: test
test: ## Run all unit tests
	@echo "🧪 Running unit tests..."
	$(GOTEST) -short -race ./internal/...

.PHONY: test-v
test-v: ## Run unit tests with verbose output
	@echo "🧪 Running unit tests (verbose)..."
	$(GOTEST) -v -short -race ./internal/...

.PHONY: test-cover
test-cover: ## Run tests with coverage report
	@echo "🧪 Running tests with coverage..."
	@mkdir -p $(TEST_REPORT_DIR)
	$(GOTEST) -short -race -coverprofile=$(TEST_REPORT_DIR)/coverage.out ./internal/...
	$(GO) tool cover -html=$(TEST_REPORT_DIR)/coverage.out -o $(TEST_REPORT_DIR)/coverage.html
	@echo "✅ Coverage report: $(TEST_REPORT_DIR)/coverage.html"

.PHONY: test-integration
test-integration: ## Run integration tests (requires external deps)
	@echo "🔗 Running integration tests..."
	$(GOTEST) -v -tags=integration -run "Integration" -timeout $(TEST_TIMEOUT) ./internal/...

.PHONY: test-bench
test-bench: ## Run benchmark tests
	@echo "⚡ Running benchmarks..."
	$(GOTEST) ./internal/coverage/... -bench=. -benchmem -benchtime=3s -run=^$$

.PHONY: test-all
test-all: test test-integration test-bench ## Run all tests
	@echo "🎉 All tests completed!"

# ==============================================================================
# Cleanup
# ==============================================================================

.PHONY: clean
clean: ## Remove build artifacts
	@echo "🧹 Cleaning..."
	rm -f $(BUILD_DIR)/$(BINARY_NAME)
	rm -rf $(TEST_REPORT_DIR)
	@echo "✅ Clean"

.PHONY: clean-all
clean-all: clean ## Deep clean (including Go cache)
	$(GO) clean -cache -testcache

# ==============================================================================
# Help
# ==============================================================================

.PHONY: help
help: ## Show this help
	@echo ""
	@echo "DeFuzz - LLM-driven constraint solving fuzzer"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo ""