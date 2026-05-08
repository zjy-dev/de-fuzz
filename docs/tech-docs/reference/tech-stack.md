---
title: Tech Stack
description: DeFuzz 的语言、依赖、外部二进制工具与并发模型
priority: MEDIUM
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ./scripts-commands.md
  - ./config-schema.md
  - ../architecture/overview.md
---

# Tech Stack

DeFuzz 是 Go 1.25 写的命令行工具，关键依赖与外部组件如下。版本信息以 `@/home/yall/project/de-fuzz/go.mod` 为权威源；本文只讲"为什么用它、用在哪里"。

## 1. 语言与编译

| 项 | 值 | 备注 |
| --- | --- | --- |
| Language | Go 1.25.5 | `go.mod:3` |
| Module path | `github.com/zjy-dev/de-fuzz` | |
| 构建入口 | `cmd/defuzz/main.go` → `cmd/defuzz/app/{root,fuzz,generate}.go` | Makefile target: `make build` |

## 2. 直接依赖（`require` 块）

| 库 | 版本 | 用途 | 主要消费者 |
| --- | --- | --- | --- |
| `github.com/spf13/cobra` | v1.10.1 | CLI 子命令路由 | `cmd/defuzz/app/root.go` |
| `github.com/spf13/viper` | v1.19.0 | YAML 配置加载 + 环境变量解析 | `internal/config/config.go` |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML 序列化（远程外部 schema） | `internal/coverage/...` |
| `github.com/zjy-dev/gcovr-json-util/v2` | v2.2.0 | gcovr JSON 报告解析 + 累积合并 | `internal/coverage/gcc.go` |
| `github.com/sashabaranov/go-openai` | v1.41.2 | OpenAI-compatible LLM 客户端 | `internal/llm/openai_client.go` |
| `github.com/anthropics/anthropic-sdk-go` | v1.26.0 | Claude 客户端（备选 provider） | `internal/llm/anthropic_client.go` |
| `github.com/stretchr/testify` | v1.10.0 | 单元测试断言 + suite | 全部 `*_test.go` |

> 本项目**不**使用 zap/logrus 等第三方 logger；`internal/logger/` 是自研的薄包装，原因是研究脚手架对结构化日志没需求。

## 3. 标准库的关键使用

| 包 | 用途 |
| --- | --- |
| `debug/elf` | `BinaryInspector` 直接解析 ELF dynsym / 导入符号；不 shell out 到 `nm`/`objdump`（`internal/oracle/inspector.go`）|
| `os/exec` | 编译器调用、QEMU 调用、gcovr 调用；通过 `internal/exec.CommandExecutor` 抽象，便于测试替身 |
| `encoding/json` | corpus / coverage_mapping / metrics 持久化 |
| `text/template` | seed 模板合并 (`internal/seed/template.go`) |
| `flag` (cobra 内部) + `time` + `sync` | 主循环计时、子流程超时 |

## 4. 外部二进制工具（运行期依赖）

| 工具 | 用途 | 何时调用 | 配置入口 |
| --- | --- | --- | --- |
| `xgcc` (插桩 GCC) | 编译每颗 seed；副作用是产生 `.gcda` | 每一轮 `compile_func` | `compiler.path` |
| `gcovr` | 把 `.gcda` 转成 JSON 覆盖率报告 | 每一轮 `coverage.MeasureCompiled` | `compiler.gcovr_command` + `gcovr_exec_path` |
| `gcov-14` | 被 `gcovr` 内部调用解 `.gcno`/`.gcda` | 同上 | gcovr_command 中 `--gcov-executable` |
| `qemu-aarch64` / `qemu-loongarch64` 等 | 跨架构 user-mode 执行 | 每一轮 oracle dynamic checker | `compiler.fuzz.qemu_path` + `qemu_sysroot` |
| `uftrace` | 发散分析：record + replay seed 编译过程 | 每次约束求解 retry | 系统 PATH 自动发现 (`internal/coverage/divergence.go`) |
| `python3 + uv` | 可视化脚本 `scripts/plot_coverage.py` | 离线分析 | n/a |

外部二进制的存在性由用户负责保证；fuzzer 启动期不做工具体检（这是 follow-up 项）。

## 5. LLM Provider

| Provider | 客户端 | 配置 |
| --- | --- | --- |
| OpenAI / 兼容 (DeepSeek, MiniMax) | `internal/llm/openai_client.go` (`go-openai`) | `configs/remixer.yaml` 的 `default_temperature` + remixer endpoint |
| Anthropic Claude | `internal/llm/anthropic_client.go` (`anthropic-sdk-go`) | 同上，由 remixer config 路由 |
| Remixer 路由 | `internal/llm/llm.go` | 顶层 `remixer_config` 字段 |

API key 通过 `.env` 文件 + viper 的 `${VAR}` 语法注入到 YAML，不硬编码（`config.go:resolveEnvVars`）。

## 6. 测试 + 集成测试

| 类型 | 命令 | 备注 |
| --- | --- | --- |
| Unit | `make test` (`go test -short -race ./internal/...`) | 当前覆盖大部分包 |
| Integration | `make test-integration` (`-tags=integration -run "Integration"`) | 需要外部依赖（gcovr/uftrace/QEMU） |
| Benchmark | `make test-bench` | 仅 `internal/coverage/` 有 benchmark |
| Coverage report | `make test-cover` | 输出 `test-reports/coverage.html` |

## 7. 并发模型

**当前显式串行**：

- 主循环单线程；`solveConstraint` 内部 LLM / compile / oracle 都是串行。
- `MechanismOracle.Analyze` 顺序执行 checker（`mechanism.go:97-101`），不开 goroutine。

理由（ADR-003 §3.5）：研究阶段优先要 reproducibility，并行会引入 cache key 竞争 + LLM rate-limit 抖动。后续如果要并行：以 target 为粒度（不同 target 的 `solveConstraint` 之间），不是单 target 内的不同 retry。

## 8. 持久化格式

| 文件 | 格式 | 写入者 | 读取者 |
| --- | --- | --- | --- |
| `corpus/seed_<NNN>.{c,json}` | C 源 + 元数据 JSON | `corpus.FileManager.Add` | `Recover` / `phase_random.go` |
| `state/coverage_mapping.json` | JSON: line → seed IDs | `coverage.Analyzer.Save` | `Recover` |
| `state/total.json` | gcovr JSON | `coverage.GCCCoverage.Merge` | `LoadCoverage` |
| `state/state.json` | metrics + 检查点 | `state.FileMetricsManager.Save` | `Load` |
| `state/compile_command.json` | per-seed 编译命令 | `engine.persistCompilationRecord` | 调试时人读 |
| `cflags.json` (per-seed) | LLM 给的 cflags | 同上 | 同上 |

格式说明：`@/home/yall/project/de-fuzz/internal/seed/metadata.go`、`internal/coverage/`。
