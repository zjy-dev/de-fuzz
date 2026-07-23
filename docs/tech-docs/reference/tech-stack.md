---
title: Tech Stack
description: DeFuzz 的语言、依赖、外部二进制工具与并发模型（Go core + Python orchestrator 双语栈）
priority: MEDIUM
last_updated: 2026-06-26
status: IMPLEMENTED
related_docs:
  - ../architecture/overview.md
  - ../architecture/agentic-loop-redesign.md
---

# Tech Stack

DeFuzz 现在是两段式：一个 Go 写的确定性 core（gRPC + MCP 双适配器），加一个 Python 写的编排层（LangGraph）。语言、依赖、外部工具按这两段分别列。版本以 `core/go.mod` 与 `orchestrator/pyproject.toml` 为权威源；本文只讲"为什么用它、用在哪里"。

## 1. 语言与编译

| 项 | 值 | 备注 |
| --- | --- | --- |
| Go core | Go 1.25.5 | `core/go.mod:3` |
| Go module path | `github.com/zjy-dev/de-fuzz` | |
| Go 构建入口 | `core/cmd/defuzz-core/main.go` | Makefile target: `make build-core` |
| Python orchestrator | requires-python >= 3.12，uv 管理 | `orchestrator/pyproject.toml` |
| Python 包 / 入口 | 包 `defuzz_loop`，console script `defuzz-loop = "defuzz_loop.cli:main"` | `uv run defuzz-loop ...` |

Go core 一个进程开两个 listener：gRPC（默认 `127.0.0.1:50051`，确定性节点）+ MCP（默认 `127.0.0.1:50052`，Streamable HTTP `/mcp`，agent 只读 tool）。装配见 `core/cmd/defuzz-core/main.go`。

## 2. Go core 直接依赖（`require` 块）

| 库 | 版本 | 用途 | 主要消费者 |
| --- | --- | --- | --- |
| `google.golang.org/grpc` | v1.81.1 | 确定性节点的 gRPC server（build/coverage/oracle/checker-metadata） | `core/internal/service/grpc_server.go` |
| `google.golang.org/protobuf` | v1.36.11 | proto 运行时；桩由 `oracle.proto` 生成 | `core/internal/service/pb/` |
| `github.com/modelcontextprotocol/go-sdk` | v1.6.1 | agent 只读 tool 的 MCP server | `core/internal/service/mcp_server.go` |
| `golang.org/x/arch` | v0.27.0 | 反汇编 ISA 指令（checker 静态分析二进制） | `core/internal/oracle/disasm/` |
| `github.com/zjy-dev/gcovr-json-util/v2` | v2.2.0 | gcovr JSON 报告解析 + 累积/增量 diff | `core/internal/service/mcp_server.go`（`coverage_diff` tool） |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML 配置解析（ISA→toolchain 映射） | `core/internal/service/toolchains.go` |
| `github.com/stretchr/testify` | v1.10.0 | 单元测试断言 + suite | 全部 `*_test.go` |

> LLM 客户端不在 Go core 里；已迁至 Python orchestrator，见 `orchestrator/defuzz_loop/llm.py` 与 `orchestrator/configs/llm.yaml`。
>
> 本项目**不**使用 zap/logrus 等第三方 logger；`core/internal/logger/` 是自研的薄包装，研究脚手架对结构化日志没需求。

## 3. Python orchestrator 直接依赖

权威源 `orchestrator/pyproject.toml`，关键项：

| 库 | 用途 | 主要消费者 |
| --- | --- | --- |
| `langgraph` | 显式编排的有向图 + checkpointer（共享状态版本化） | `defuzz_loop/graph.py`、`defuzz_loop/audit.py` |
| `langgraph-checkpoint-sqlite` | 每个 run 一个 `checkpoints.sqlite`，支撑 inspect/replay/trace-bug | `defuzz_loop/audit.py` |
| `langchain-core` | LLM 抽象（chat model） | `defuzz_loop/llm.py` |
| `grpcio` | 调 Go core 的确定性 gRPC 节点 | `defuzz_loop/clients/grpc_client.py` |
| `mcp` | 调 Go core 的 MCP 只读 tool | `defuzz_loop/clients/mcp_client.py` |
| `pydantic` | Blackboard 共享状态 schema | `defuzz_loop/state.py` |

## 4. Go 标准库的关键使用

| 包 | 用途 |
| --- | --- |
| `debug/elf` | `BinaryInspector` 直接解析 ELF dynsym / 导入符号；不 shell out 到 `nm`/`objdump`（`core/internal/oracle/inspector.go`）|
| `os/exec` | 编译器调用、QEMU 调用；通过 `core/internal/exec.CommandExecutor` 抽象，便于测试替身 |
| `net` / `net/http` | gRPC listener + MCP 的 Streamable HTTP handler（`core/cmd/defuzz-core/main.go`）|
| `encoding/json` | coverage / checker 结果序列化 |
| `text/template` | seed 模板合并 (`core/internal/seed/template.go`) |

## 5. 外部二进制工具（运行期依赖）

| 工具 | 用途 | 何时调用 | 配置入口 |
| --- | --- | --- | --- |
| `xgcc` / 交叉 gcc (插桩 GCC) | 按 (checker→ISA) 矩阵编译每颗 seed | build 节点（`BuildService.Build`） | `configs/toolchains.yaml` 每 ISA 的 `gcc_path` |
| `gcovr` | 把 `.gcda` 转成 JSON 覆盖率报告 | coverage 节点（`CoverageService.Measure`，经注入的 measurer） | measurer 装配层 |
| `gcov-14` | 被 `gcovr` 内部调用解 `.gcno`/`.gcda` | 同上 | gcovr 的 `--gcov-executable` |
| `qemu-aarch64` / `qemu-riscv64` 等 | 跨架构 user-mode 执行 | oracle 的动态 checker | `configs/toolchains.yaml` 每 ISA 的 `qemu_path` + `qemu_sysroot` |
| `creduce` | 违反时缩小 PoC | 最小化 agent 的 MCP tool `creduce_run` | — |
| `python3 + uv` | 编排层运行时（LangGraph + 三个 agent） | 整个 run | `orchestrator/configs/llm.yaml` |

ISA → toolchain 的映射集中在 `configs/toolchains.yaml`；某 ISA 缺工具链时 build 节点产出 error cell（R8）而非崩溃，loop 优雅降级。外部二进制的存在性由用户负责保证。

## 6. LLM Provider

LLM 已迁至 Python orchestrator，不再在 Go core 中实现。Provider 抽象与多后端（OpenAI 兼容、Anthropic）由 `orchestrator/defuzz_loop/llm.py` 的 `LLMConfig` + `build_chat_model` 统一，配置见 `orchestrator/configs/llm.yaml`。API key 通过 `.env` 注入，不硬编码。三个 agent（Generator / Feedback / Minimizer）全经这一层取 chat model。

## 7. 测试 + 集成测试

| 类型 | 命令 | 备注 |
| --- | --- | --- |
| Go unit | `make test` (`go test -short -race ./...`) | core 全包 |
| Go integration | `make test-integration` (`-tags=integration -run "Integration"`) | 需要外部依赖（gcovr/QEMU） |
| Go coverage report | `make test-cover` | 输出 `test-report/coverage.html` |
| Python | `make test-py` (`uv run pytest tests/ -q` + `uv run ruff check`) | orchestrator 测试套 + lint |
| proto 重生成 | `make proto` | 由 uv-managed `grpc_tools.protoc` 从 `core/proto/oracle.proto` 生成 Go + Python 双语桩 |

## 8. 并发模型

**当前显式串行**：

- 编排图按固定边序单线程推进（`defuzz_loop/graph.py`）；每轮 generator → routing → build → coverage → oracle 顺序走，可复现。
- Go core 的 `MechanismOracle.Analyze` 顺序执行 checker，不开 goroutine。

理由（ADR-003 §3.5）：研究阶段优先 reproducibility，并行会引入 cache key 竞争 + LLM rate-limit 抖动。后续若要并行，天然边界是不同 target（不同 invariant）之间，而非单 target 内的不同 retry。

## 9. 持久化格式

| 文件 | 格式 | 写入者 | 读取者 |
| --- | --- | --- | --- |
| `orchestrator/runs/<exp>_<mech>_<UTC>/checkpoints.sqlite` | LangGraph SQLite checkpoint 链 | 编排图每个节点出口 | `inspect` / `replay` / `trace-bug` |
| `orchestrator/runs/<exp>_<mech>_<UTC>/manifest.json` | JSON：git_sha / toolchains / checker_catalog / llm / ablation / disabled_agents | run init（`audit.build_manifest`） | 三个只读子命令解析 thread |
| `corpus/seed_<NNN>.{c,json}` | C 源 + 元数据 JSON | seed 持久化 | 调试 / 复现 |

格式说明见 `core/internal/seed/metadata.go` 与 `orchestrator/defuzz_loop/audit.py`。
