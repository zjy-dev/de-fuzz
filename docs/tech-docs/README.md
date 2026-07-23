---
title: DeFuzz Technical Documentation
description: 项目技术文档总入口，按 priority 排序
priority: CRITICAL
last_updated: 2026-05-08
status: IMPLEMENTED
---

# DeFuzz Technical Documentation

DeFuzz 是一个 LLM-driven 的约束求解 fuzzer，用来测试 C 编译器自身的防御实现（stack canary、fortify、CFI、shadow stack 等）。本目录是项目所有技术文档的入口，按"使用频率 + 重要性"分组。

> **从哪里开始？**
> - 第一次接触这个项目 → 读 `architecture/overview.md`，再按需展开。
> - 想跑起来 → `guides/building-instrumented-gcc.md` 搭被测目标；编排层跑法见 `architecture/agentic-loop-redesign.md` §7（代码落点）与仓库根 `Makefile`（`make build-core` + `orchestrator/` 下 `uv run`）。
> - 想加防御机制 → `guides/adding-a-defense-mechanism.md`。
> - 想理解 oracle 怎么工作 → `architecture/oracle-mechanism-framework.md`。

## Architecture (HIGH priority)

| 文档 | 主题 |
| --- | --- |
| [architecture/agentic-loop-redesign.md](./architecture/agentic-loop-redesign.md) | 当前架构权威文档：Python (LangGraph) 编排 + 三个 agent + Go core 双适配器（gRPC + MCP） |
| [architecture/overview.md](./architecture/overview.md) | 系统总览：组件分层、数据流、近期改造时间线 |
| [architecture/oracle-mechanism-framework.md](./architecture/oracle-mechanism-framework.md) | `MechanismOracle` / `InvariantChecker` / `BinaryInspector` / `Polarizer` 实现态参考 |

### Architecture Decision Records

| ADR | 状态 | 主题 |
| --- | --- | --- |
| [decisions/002-multi-cfg-orchestration.md](./architecture/decisions/002-multi-cfg-orchestration.md) | Superseded | 多 CFG 解析与 PR `b907c01` 评估 |
| [decisions/003-oracle-multi-invariant-redesign.md](./architecture/decisions/003-oracle-multi-invariant-redesign.md) | Accepted, Implemented | Oracle 从单 verdict 演化为多 invariant 聚合 |

ADR 索引：[decisions/README.md](./architecture/decisions/README.md)。

## Features (HIGH priority)

| 文档 | 实现状态 |
| --- | --- |
| [features/canary-oracle.md](./features/canary-oracle.md) | ✅ 已实现 (4 个 invariant checker) |
| [features/ibt-oracle.md](./features/ibt-oracle.md) | ✅ 已实现 (10 个 invariant checker; DREV-2026-004) |
| [features/mechanism-contract.md](./features/mechanism-contract.md) | ✅ 已实现 (canary 已注册) |
| [_archive/oracles/fortify-oracle.md](./_archive/oracles/fortify-oracle.md) | ⚠ DEPRECATED — Go 实现在 a7307b6 移除 |

## Guides (MEDIUM priority)

| 文档 | 何时读 |
| --- | --- |
| [guides/adding-a-defense-mechanism.md](./guides/adding-a-defense-mechanism.md) | 想加新防御机制（端到端 8 步 checklist） |
| [guides/building-instrumented-gcc.md](./guides/building-instrumented-gcc.md) | 想搭起被测目标（GCC 插桩） |
| [guides/cflags-configuration.md](./guides/cflags-configuration.md) | 配 `cflags`、理解 LLM 动态 cflags 边界 |
| [guides/oracle-e2e-testing.md](./guides/oracle-e2e-testing.md) | 绕过 fuzz-loop 直达测试 Oracle（通用模板 + 验证清单） |

## Reference (MEDIUM priority)

| 文档 | 用途 |
| --- | --- |
| [reference/tech-stack.md](./reference/tech-stack.md) | 语言、依赖、外部二进制工具、并发模型、持久化格式 |
| [reference/open-source-c-compilers.md](./reference/open-source-c-compilers.md) | 选型调研：为什么选 GCC 当被测目标 |

## Invariants Survey

| 入口 | 内容 |
| --- | --- |
| [invariants/README.md](./invariants/README.md) | 24 份机制 invariant survey 的分类索引（按 mechanism） |

主入口：[invariants/gcc-llvm-defense-invariant-source-survey.md](./invariants/gcc-llvm-defense-invariant-source-survey.md) —— 跨机制总表。

## GCC Instrumentation

[gcc-instrumentation/README.md](./gcc-instrumentation/README.md) 子目录含各 ISA 的 BUILD-GUIDE 与构建脚本。

## Audit Campaigns

| 报告 | 范围 | 结论 |
| --- | --- | --- |
| [audits/fortify-source-gcc16-glibc239.md](./audits/fortify-source-gcc16-glibc239.md) | FORTIFY_SOURCE 静态审计：GCC 16.1.0 + glibc 2.39 / aarch64 | 1 个新 finding（DREV-2026-025，INV-FORT-O02 signed-count 32 位截断 silent bypass）；O01/O03/W01/R01/R02/C01 HOLDS；C02 为已知 sourceware #24987 carry-forward |

## Recent Changes (本次同步覆盖范围)

| Commit | 主题 | 受影响文档 |
| --- | --- | --- |
| `268464c` | refactor(oracle): add invariant-based mechanism checks | architecture/decisions/003 + architecture/oracle-mechanism-framework + features/canary-oracle |
| `ee04f48` | feat(oracle): detect epilogue canary leaks (`INV-SP-R03`) | features/canary-oracle |
| — | docs(guides): add oracle end-to-end testing guide | guides/oracle-e2e-testing + features/canary-oracle + guides/adding-a-defense-mechanism |
| `a7307b6` | refactor(prompt): bind strategies to mechanism contracts；删除 fortify oracle | features/mechanism-contract + architecture/prompt-architecture + _archive/oracles/fortify-oracle |
| `7bed9d9` | docs: remove generated presentation workspace | (无影响) |
| `489ed60` | docs: move oracle references under docs/oracles | 进一步迁到 features/ |

## 状态约定

文档头部 frontmatter 的 `status` 字段：

| Status | 含义 |
| --- | --- |
| `IMPLEMENTED` | 文档描述与代码现状一致 |
| `PLANNED` | 文档描述未来工作；代码尚未存在 |
| `DEPRECATED` | 文档描述已移除/不再适用；保留作为历史 |
| `Accepted` / `Superseded` | 仅 ADR 使用 |
