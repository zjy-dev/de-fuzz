# Color definitions
COLOR_RESET := \033[0m
COLOR_BOLD := \033[1m
COLOR_RED := \033[31m
COLOR_GREEN := \033[32m
COLOR_YELLOW := \033[33m
COLOR_BLUE := \033[34m
COLOR_MAGENTA := \033[35m
COLOR_CYAN := \033[36m

# Test report directory
TEST_REPORT_DIR := ./test-report

# System information
COMMIT_ID := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
COMMIT_SHORT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
TEST_TIME := $(shell date '+%Y-%m-%d %H:%M:%S %Z')
CPU_INFO := $(shell lscpu | grep "Model name" | sed 's/Model name:[[:space:]]*//' || echo "unknown")
CPU_CORES := $(shell nproc 2>/dev/null || echo "unknown")
MEM_TOTAL := $(shell free -h | awk '/^Mem:/ {print $$2}' || echo "unknown")

.DEFAULT_GOAL := help

.PHONY: help
help: ## 显示帮助信息
	@echo "$(COLOR_BOLD)$(COLOR_CYAN)De-Fuzz Makefile 帮助$(COLOR_RESET)"
	@echo "$(COLOR_BLUE)═══════════════════════════════════════════════════$(COLOR_RESET)"
	@echo ""
	@echo "$(COLOR_BOLD)测试相关命令:$(COLOR_RESET)"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  $(COLOR_GREEN)%-25s$(COLOR_RESET) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(COLOR_BOLD)示例:$(COLOR_RESET)"
	@echo "  make test-unit          - 运行单元测试"
	@echo "  make test-bench         - 运行性能测试并生成报告"
	@echo "  make test-integrate     - 运行集成测试并生成报告"
	@echo "  make test-all           - 运行所有测试"
	@echo "  make test-report-show   - 查看生成的测试报告"
	@echo ""

.PHONY: test-unit
test-unit: ## 运行单元测试
	@echo "$(COLOR_BOLD)🧪 运行单元测试...$(COLOR_RESET)"
	@echo "$(COLOR_BLUE)===============================================$(COLOR_RESET)"
	@go test -v -short ./internal/...
	@echo ""
	@echo "$(COLOR_GREEN)✅ 单元测试完成！$(COLOR_RESET)"

.PHONY: test-bench
test-bench: ## 运行性能测试并生成报告
	@echo "$(COLOR_BOLD)⚡ 运行性能测试...$(COLOR_RESET)"
	@echo "$(COLOR_BLUE)===============================================$(COLOR_RESET)"
	@mkdir -p $(TEST_REPORT_DIR)
	@echo "生成性能测试报告..."
	@{ \
		echo "# Benchmark Test Report"; \
		echo ""; \
		echo "## Test Environment"; \
		echo "- **Commit ID**: $(COMMIT_ID)"; \
		echo "- **Commit Short**: $(COMMIT_SHORT)"; \
		echo "- **Test Time**: $(TEST_TIME)"; \
		echo "- **CPU**: $(CPU_INFO)"; \
		echo "- **CPU Cores**: $(CPU_CORES)"; \
		echo "- **Memory**: $(MEM_TOTAL)"; \
		echo ""; \
		echo "## Benchmark Results"; \
		echo ""; \
		echo '```'; \
	} > $(TEST_REPORT_DIR)/benchmark.md
	@go test ./internal/coverage/... -bench=. -benchmem -benchtime=3s -run=^$$ 2>&1 | tee -a $(TEST_REPORT_DIR)/benchmark.md
	@echo '```' >> $(TEST_REPORT_DIR)/benchmark.md
	@echo ""
	@echo "$(COLOR_GREEN)✅ 性能测试完成！报告保存至: $(TEST_REPORT_DIR)/benchmark.md$(COLOR_RESET)"

.PHONY: test-integrate
test-integrate: ## 运行集成测试并生成报告
	@echo "$(COLOR_BOLD)🔗 运行集成测试...$(COLOR_RESET)"
	@echo "$(COLOR_BLUE)===============================================$(COLOR_RESET)"
	@mkdir -p $(TEST_REPORT_DIR)
	@echo "生成集成测试报告..."
	@{ \
		echo "# Integration Test Report"; \
		echo ""; \
		echo "## Test Environment"; \
		echo "- **Commit ID**: $(COMMIT_ID)"; \
		echo "- **Commit Short**: $(COMMIT_SHORT)"; \
		echo "- **Test Time**: $(TEST_TIME)"; \
		echo "- **CPU**: $(CPU_INFO)"; \
		echo "- **CPU Cores**: $(CPU_CORES)"; \
		echo "- **Memory**: $(MEM_TOTAL)"; \
		echo ""; \
		echo "## Integration Test Results"; \
		echo ""; \
		echo '```'; \
	} > $(TEST_REPORT_DIR)/integration.md
	@go test -v -tags=integration -run "Integration" -timeout 10m ./internal/... 2>&1 | tee -a $(TEST_REPORT_DIR)/integration.md
	@echo '```' >> $(TEST_REPORT_DIR)/integration.md
	@echo ""
	@echo "$(COLOR_GREEN)✅ 集成测试完成！报告保存至: $(TEST_REPORT_DIR)/integration.md$(COLOR_RESET)"

.PHONY: test-integration
test-integration: test-integrate ## 运行集成测试的别名

.PHONY: test-all
test-all: test-unit test-bench test-integrate ## 运行所有测试（单元测试、性能测试、集成测试）
	@echo ""
	@echo "$(COLOR_BOLD)$(COLOR_GREEN)🎉 所有测试完成！$(COLOR_RESET)"
	@echo "$(COLOR_CYAN)测试报告位置: $(TEST_REPORT_DIR)/$(COLOR_RESET)"
	@ls -lh $(TEST_REPORT_DIR)/

.PHONY: test-report-clean
test-report-clean: ## 清理测试报告目录
	@echo "$(COLOR_YELLOW)🧹 清理测试报告...$(COLOR_RESET)"
	@rm -rf $(TEST_REPORT_DIR)
	@echo "$(COLOR_GREEN)✅ 测试报告已清理$(COLOR_RESET)"

.PHONY: test-report-show
test-report-show: ## 显示测试报告列表
	@echo "$(COLOR_CYAN)📊 测试报告:$(COLOR_RESET)"
	@if [ -d "$(TEST_REPORT_DIR)" ]; then \
		ls -lh $(TEST_REPORT_DIR)/ 2>/dev/null || echo "报告目录为空"; \
	else \
		echo "报告目录不存在，请先运行测试"; \
	fi