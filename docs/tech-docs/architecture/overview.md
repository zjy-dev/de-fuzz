---
title: System Overview
description: DeFuzz 整体架构、组件依赖、数据流与近期改造时间线
priority: CRITICAL
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ./gcc-pipeline.md
  - ./oracle-mechanism-framework.md
  - ./fuzz-engine-loop.md
  - ./prompt-architecture.md
---

# System Overview

DeFuzz 是一个 Go 语言写的、面向编译器自身防御实现的 LLM-driven 约束求解 fuzzer。它把"测程序"换成"测编译器"——被插桩的 `xgcc` 在编译每颗 LLM 生成的 C seed 时把覆盖率写进自己的 `.gcda`，fuzzer 再据此选下一个未覆盖的目标 BB、用 LLM 求解"什么样的 seed 能让编译器走到这个 BB"。

## 1. 一句话定位

> 给定一份带覆盖率 + CFG dump 的 GCC，循环挑高分支基本块作为目标，用 LLM 把"已覆盖的相邻 seed"改写成能命中目标的新 seed，编译产物再被 oracle 跑出 invariant verdict，命中 bug 的 seed 持久化。

## 2. 组件分层

```
                   defuzz CLI (cobra)
                    │
                    ▼
   ┌──────────────────────────────────────────┐
   │      cmd/defuzz/app                      │  装配 + 校验 (mechanism contract,
   │   - root.go / fuzz.go / generate.go      │   strategy ↔ oracle 一致性)
   └──────────────────────────────────────────┘
                    │ injects
                    ▼
   ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────────┐
   │ internal/fuzz   │  │ internal/oracle │  │ internal/prompt     │
   │ ──────────────  │  │ ──────────────  │  │ ──────────────────  │
   │ Engine          │◀─│ MechanismOracle │  │ PromptService       │
   │ FlagScheduler   │  │  └ Checkers     │  │  └ mechanism.Contract│
   │ RandomPhase     │  │ BinaryInspector │  │ Builder             │
   └─────────────────┘  └─────────────────┘  └─────────────────────┘
        │      ▲                ▲                       ▲
        │      │                │                       │
        ▼      │                │                       │
   ┌─────────────────┐  ┌──────────────────┐  ┌────────────────────┐
   │ internal/coverage│  │ internal/seed   │  │ internal/llm       │
   │  Analyzer       │  │  Seed/Storage   │  │  remixer client    │
   │  GCCCoverage    │  │  FlagProfile    │  │                    │
   │  Uftrace        │  │  template merge │  │                    │
   └─────────────────┘  └──────────────────┘  └────────────────────┘
        │                       │
        ▼                       ▼
   ┌─────────────────┐  ┌──────────────────┐
   │ internal/compiler│  │ internal/corpus  │
   │  GCCCompiler     │  │  FileManager     │
   └─────────────────┘  └──────────────────┘
        │
        ▼
   ┌─────────────────┐  ┌──────────────────┐
   │ internal/exec    │  │ internal/seed_executor │
   │  CommandExecutor │  │  OracleExecutorAdapter │
   │                  │  │  QEMUOracleExecutorAdapter │
   └─────────────────┘  └──────────────────┘
```

详细数据流见 `@/home/yall/project/de-fuzz/docs/tech-docs/architecture/gcc-pipeline.md`。

## 3. 主循环数据流（一次成功的迭代）

```
Engine.Run
  └─ processInitialSeeds (一次)
        ↓
  ┌─ for iter < MaxIterations:
  │     1. analyzer.SelectTarget()                  # CFG-guided
  │     2. PromptService.GetConstraintPrompt(ctx)   # base + understanding + contract block
  │     3. LLM.GetCompletionWithSystem(...)
  │     4. PromptService.ParseLLMResponse           # 校验 RequiredMarkers
  │     5. compiler.Compile(seed)                   # 写 .gcda
  │     6. coverage.MeasureCompiled                 # gcovr → JSON
  │     7. analyzer.RecordCoverage / CheckNewCoverage
  │     8. oracle.Analyze(seed, ctx, results)       # MechanismOracle 调度
  │           └─ Enablement → Static → Dynamic
  │     9. corpus.Add (qualified: covered_new || hit_target || found_bug)
  │     10. (非命中) divergence.Analyze + GetRefinedPrompt → goto 5
  └─ (target 全覆盖时, 可选)
       RandomMutationPhase.Run                       # 见 features/random-mutation-phase.md
```

## 4. 与外部世界的接口

| 边界 | 由谁负责 | 关键接口 |
| --- | --- | --- |
| 测试目标 (`xgcc`) | 项目外构建，见 `tech-docs/guides/building-instrumented-gcc.md` | 命令行参数 + `.gcda` 副作用 |
| 覆盖率工具 (`gcovr`) | 系统安装；`compiler.gcovr_command` 配置 | JSON 报告 |
| 发散分析 (`uftrace`) | 系统安装（可选）；`coverage/divergence.go` 调用 | replay 输出文本 |
| LLM Provider | `internal/llm/remixer*` + `configs/remixer.yaml` | OpenAI-compatible API |
| ELF 解析 | `debug/elf` (stdlib) | `BinaryInspector` |
| 跨架构执行 | QEMU user-mode；`internal/seed_executor/QEMUOracleExecutorAdapter` | exec.Cmd |

## 5. 近期改造时间线 (本次文档同步覆盖范围)

| Commit | 主题 | 覆盖文档 |
| --- | --- | --- |
| `268464c` | refactor(oracle): add invariant-based mechanism checks | `architecture/decisions/003-...md`, `architecture/oracle-mechanism-framework.md`, `features/canary-oracle.md` |
| `ee04f48` | feat(oracle): detect epilogue canary leaks (`INV-SP-R03`) | `features/canary-oracle.md` (实现现状), 模板 marker 在 `features/mechanism-contract.md` |
| `7bed9d9` | docs: remove generated presentation workspace | (无影响) |
| `489ed60` | docs: move oracle references under docs/oracles | 本次同步进一步迁到 `tech-docs/features/` |
| `a7307b6` | refactor(prompt): bind strategies to mechanism contracts；删除 fortify oracle | `features/mechanism-contract.md`, `_archive/oracles/fortify-oracle.md`, `architecture/prompt-architecture.md` |

## 6. 进一步阅读

- 构建期与运行期的端到端数据流：`@/home/yall/project/de-fuzz/docs/tech-docs/architecture/gcc-pipeline.md`
- Oracle 多 invariant 框架的实现态参考：`@/home/yall/project/de-fuzz/docs/tech-docs/architecture/oracle-mechanism-framework.md`
- Fuzz 主循环与 RandomMutationPhase：`@/home/yall/project/de-fuzz/docs/tech-docs/architecture/fuzz-engine-loop.md`
- Prompt 流水线与 mechanism contract：`@/home/yall/project/de-fuzz/docs/tech-docs/architecture/prompt-architecture.md` + `tech-docs/features/mechanism-contract.md`
- 添加新防御机制的端到端 checklist：`@/home/yall/project/de-fuzz/docs/tech-docs/guides/adding-a-defense-mechanism.md`
