# DeFuzz Makefile
# Go core lives in core/ (its own module); Python orchestrator in orchestrator/.

# ==============================================================================
# Variables
# ==============================================================================

CORE_DIR := core
CORE_BINARY_NAME := defuzz-core
CORE_CMD_PATH := ./cmd/defuzz-core
BUILD_DIR := bin

# Proto (gRPC contract SSOT lives with the Go core, under core/proto/)
PROTO_DIR := core/proto
PROTO_FILE := oracle.proto
GO_PB_OUT := core/internal/service/pb
PY_PB_OUT := orchestrator/defuzz_loop/clients/pb

# Go commands (run inside core/)
GO := go
GOFMT := gofmt
GOLINT := golangci-lint

TEST_REPORT_DIR := test-report
TEST_TIMEOUT := 10m

.DEFAULT_GOAL := help

# ==============================================================================
# Build
# ==============================================================================

.PHONY: build-core
build-core: ## Build the deterministic gRPC+MCP Go core (core/cmd/defuzz-core)
	@echo "🔨 Building $(CORE_BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	cd $(CORE_DIR) && $(GO) build -o ../$(BUILD_DIR)/$(CORE_BINARY_NAME) $(CORE_CMD_PATH)
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

.PHONY: fmt
fmt: ## Format Go code
	@echo "🎨 Formatting code..."
	cd $(CORE_DIR) && $(GOFMT) -s -w ./cmd ./internal
	@echo "✅ Done"

.PHONY: lint
lint: ## Run Go linter (requires golangci-lint)
	@echo "🔍 Running linter..."
	cd $(CORE_DIR) && $(GOLINT) run ./...

.PHONY: vet
vet: ## Run go vet
	@echo "🔍 Running go vet..."
	cd $(CORE_DIR) && $(GO) vet ./...

.PHONY: tidy
tidy: ## Tidy and verify dependencies
	@echo "📦 Tidying modules..."
	cd $(CORE_DIR) && $(GO) mod tidy -e && $(GO) mod verify
	@echo "✅ Done"

# ==============================================================================
# Testing
# ==============================================================================

.PHONY: test
test: ## Run all Go unit tests
	@echo "🧪 Running unit tests..."
	cd $(CORE_DIR) && $(GO) test -short -race ./...

.PHONY: test-v
test-v: ## Run unit tests with verbose output
	@echo "🧪 Running unit tests (verbose)..."
	cd $(CORE_DIR) && $(GO) test -v -short -race ./...

.PHONY: test-cover
test-cover: ## Run tests with coverage report
	@echo "🧪 Running tests with coverage..."
	@mkdir -p $(TEST_REPORT_DIR)
	cd $(CORE_DIR) && $(GO) test -short -race -coverprofile=../$(TEST_REPORT_DIR)/coverage.out ./...
	cd $(CORE_DIR) && $(GO) tool cover -html=../$(TEST_REPORT_DIR)/coverage.out -o ../$(TEST_REPORT_DIR)/coverage.html
	@echo "✅ Coverage report: $(TEST_REPORT_DIR)/coverage.html"

.PHONY: test-integration
test-integration: ## Run integration tests (requires external deps)
	@echo "🔗 Running integration tests..."
	cd $(CORE_DIR) && $(GO) test -v -tags=integration -run "Integration" -timeout $(TEST_TIMEOUT) ./...

.PHONY: test-py
test-py: ## Run the Python orchestrator test suite
	@echo "🐍 Running orchestrator tests..."
	cd orchestrator && uv run pytest tests/ -q && uv run ruff check defuzz_loop/ tests/

# ==============================================================================
# Cleanup
# ==============================================================================

.PHONY: clean
clean: ## Remove build artifacts
	@echo "🧹 Cleaning..."
	rm -rf $(BUILD_DIR) $(TEST_REPORT_DIR)
	@echo "✅ Clean"

.PHONY: clean-all
clean-all: clean ## Deep clean (including Go cache)
	cd $(CORE_DIR) && $(GO) clean -cache -testcache

# ==============================================================================
# Help
# ==============================================================================

.PHONY: help
help: ## Show this help
	@echo ""
	@echo "DeFuzz - agentic loop for compiler-defense fuzzing (Go core + Python orchestrator)"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo ""
