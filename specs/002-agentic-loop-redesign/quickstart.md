# Quickstart: Agentic Loop Redesign

面向开发者的最短上手路径。验证 plan 的 MVP（SC-001）：Generator + 三个确定性节点跑通单轮闭环，不接反馈/最小化 agent。

## 前置

- Go 1.25.5（仓库现有），Python 3.12
- instrumented `xgcc` + `gcovr`（见 `docs/tech-docs/guides/building-instrumented-gcc.md`）
- QEMU user-mode（跨 ISA 执行）
- LLM provider（`configs/remixer.yaml`）

## 1. 起 Go core（gRPC + MCP 双适配器）

```bash
go build -o bin/defuzz-core ./cmd/defuzz-core
./bin/defuzz-core --grpc-addr unix:///tmp/defuzz-core.grpc --mcp-addr unix:///tmp/defuzz-core.mcp
```

一个进程同时暴露：
- gRPC：BuildService / CoverageService / OracleService / CheckerMetadataService
- MCP：search_source / query_invariants / coverage_diff / creduce_run / compile_exec

两者共读 `internal/oracle/metadata.go` 的 checker 元数据（SSOT）。

## 2. 装 Python 编排

```bash
cd orchestrator
python -m venv .venv && source .venv/bin/activate
pip install -e .                       # langgraph, grpcio, mcp, pydantic ...
python -m grpc_tools.protoc -I ../specs/002-agentic-loop-redesign/contracts \
  --python_out=defuzz_loop/clients/pb --grpc_python_out=defuzz_loop/clients/pb \
  ../specs/002-agentic-loop-redesign/contracts/oracle.proto
```

## 3. 跑 MVP 单轮（仅 Generator + 确定性节点）

```bash
python -m defuzz_loop.graph run \
  --initial-seeds ../initial_seeds \
  --grpc unix:///tmp/defuzz-core.grpc \
  --mcp  unix:///tmp/defuzz-core.mcp \
  --max-rounds 1 \
  --disable-agent feedback --disable-agent minimizer   # MVP：只跑骨架 + Generator
```

期望：日志按 `generate → routing → build → coverage → oracle → 路由` 固定顺序推进；每步落一个 checkpoint。

## 4. 验证关键不变量

```bash
cd orchestrator && pytest
```

- `test_graph_loop.py`：节点顺序固定；not_violated 回开头、violated 终止（SC-001）。
- `test_checker_routing.py`：Generator 输出中**不含 ISA 维度**；differential checker 的 ISA 全跑（SC-002 / SC-006）。
- `test_blackboard.py`：锁定 checkpoint 重放输入一致（SC-003）；ablation flag 单独开关（SC-004）；写权限矩阵越权抛错（FR-022）。

## 5. 检视 blackboard / replay

```bash
python -m defuzz_loop.graph inspect --thread <id>              # 看某 run 的 checkpoint 链
python -m defuzz_loop.graph replay  --thread <id> --checkpoint <cid>  # 锁版本重放
python -m defuzz_loop.graph trace-bug --thread <id> --bug <bid>      # bug→确定性证据回溯（SC-005）
```

## 接下来

- 接入反馈 agent：去掉 `--disable-agent feedback`，验证 guidance 写回 + 下一轮 Generator 读取（FR-024）。
- 接入最小化 agent：制造一个 violated 种子，去掉 `--disable-agent minimizer`，验证 PoC 仍触发原 bug（FR-026）。
- 做 ablation 对照：`--ablation checker_routing=off` 跑全笛卡尔积，对比 token 开销与命中率（SC-004/SC-007）。
