.PHONY: test-unit
test-unit: ## 运行单元测试
	@echo "$(COLOR_BOLD)🧪 运行单元测试...$(COLOR_RESET)"
	@echo "$(COLOR_BLUE)===============================================$(COLOR_RESET)"
	@go test -v -short ./internal/...
	@echo ""
	@echo "$(COLOR_GREEN)✅ 单元测试完成！$(COLOR_RESET)"

.PHONY: test-integration
test-integration: ## 运行集成测试
	@echo "$(COLOR_BOLD)🔗 运行集成测试...$(COLOR_RESET)"
	@echo "$(COLOR_BLUE)===============================================$(COLOR_RESET)"
	@go test -v -tags=integration -run "Integration" ./internal/...
	@echo ""
	@echo "$(COLOR_GREEN)✅ 集成测试完成！$(COLOR_RESET)"