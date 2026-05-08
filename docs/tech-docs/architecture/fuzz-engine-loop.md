---
title: Fuzz Engine Main Loop
description: 约束求解主循环、重试与发散分析、覆盖率/Oracle/Corpus 三方决策、RandomMutationPhase 接入
priority: HIGH
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ./gcc-pipeline.md
  - ./oracle-mechanism-framework.md
  - ../features/flag-scheduler.md
  - ../features/random-mutation-phase.md
---

# Fuzz Engine Main Loop

`internal/fuzz/engine.go` 的 `Engine` 是把 compiler / coverage / analyzer / oracle / prompt 五个子系统粘起来的指挥棒。本文按"主流程 → 子流程 → 决策表"的顺序拆解它。

## 1. 顶层流程

```
Engine.Run                                       // engine.go:126-196
 ├─ processInitialSeeds                          // engine.go:198-286
 │    每颗初始 seed: compile → measureSeed → RecordCoverage → runOracle
 ├─ for iter < MaxIterations (-1 = unlimited):
 │     analyzer.SelectTarget                     // CFG-guided targeting
 │     solveConstraint(target)                   // engine.go:288-480
 │     每 10 轮: saveState
 ├─ (target 全覆盖 + EnableRandomPhase)
 │     RandomMutationPhase.Run                   // phase_random.go:38-79
 └─ finalizeState + printSummary
```

## 2. 单次约束求解 (`solveConstraint`)

```
solveConstraint(target):
  1. 加载 base seed (从 target.BaseSeed 解析 ID, 走 corpus.Get)
  2. BuildTargetContextFromCFG → ctx
  3. 第一次尝试:
        attachPromptProfile(target, ctx)         # 注入 FlagScheduler 选出的 profile
        generateMutatedSeed(ctx)                 # constraint prompt → LLM
        tryMutatedSeed(seed, target)
  4. 命中 → 返回 (true, 0)
  5. 未命中 → MaxRetries 次循环:
        if 上次编译失败 → 编译错误 prompt
        else → divergence 分析 + refined prompt
        重新 LLM 调用、重新 tryMutatedSeed
        命中 → 返回 (true, retry+1)
        覆盖到新行 (CoveredNew) → 返回 (false, retry+1)  # 视为局部进展
  6. 全部用尽 → DecayBBWeight + 返回 (false, MaxRetries)
```

代码位置：`@/home/yall/project/de-fuzz/internal/fuzz/engine.go:288-480`。

### 2.1 `seedTryResult` 字段语义

`engine.go:84-95`：

| 字段 | 含义 |
| --- | --- |
| `HitTarget` | 是否命中 target BB（按 file:line 判定） |
| `CoveredNew` | 是否使覆盖率有增量（不含 target） |
| `CompileFailed` / `CompileError` | 编译失败状态与 stderr，供下一轮编译错误反馈 prompt |
| `SeedCode` | 本次尝试的 seed 源码，附在编译错误 prompt 里 |
| `OracleVerdict` / `BugType` / `BugDescription` | oracle 结果 |

## 3. 覆盖率 / Oracle / Corpus 三方决策表

`tryMutatedSeed`（`engine.go:520-678`）每次执行都要回答三个问题：要不要把这条 seed 写进 coverage mapping？要不要写进 corpus？要不要把它的覆盖合并进 `total.json`？决策表：

| 条件 | RecordCoverage | corpus.Add | coverage.Merge |
| --- | --- | --- | --- |
| `CoveredNew == true` | ✅ | ✅ (reason=`coverage`) | 当 `HasIncreased` 时 ✅ |
| `HitTarget == true` | ✅ | ✅ (reason=`target`) | 当 `HasIncreased` 时 ✅ |
| `foundBug == true` | ✅ | ✅ (reason=`bug`) | 当 `HasIncreased` 时 ✅ |
| 三者皆假 | ❌ | ❌ | ❌ |
| **negative-control profile** | 视同三者皆假；`CoveredNew` 强制设为 false (`engine.go:603-606`) | ❌ | ❌ |

**negative control 不进 corpus**：因为 polarity 翻转后这些 seed 是用来验证"机制确实关掉"的，不是种群繁殖材料；进 corpus 会污染后续目标选择。

## 4. 重试分支详解

### 4.1 编译错误反馈 (`prompt.CompileErrorInfo`)

`engine.go:351-369`：当 `lastResult.CompileFailed` 时切到 `GetCompileErrorPrompt`，把上次的 `SeedCode` + `CompilerOutput` + `RetryAttempt` 一起塞给 LLM，让它产出"修过的"版本。

### 4.2 发散分析 (`uftrace`)

`engine.go:370-432`：

1. 对 base seed 与 mutated seed 各跑一次 `uftrace record xgcc -c <seed.c>`；
2. `UftraceAnalyzer.Analyze` 从 `task.txt` 拿到 `cc1` PID，replay 调用序列，找首个发散函数；
3. 调用 `analyzer.GetFunction(divergentFunc)` 反查源码行号区间，用 `coverage.ReadSourceLines` 切出函数源码；
4. `GetRefinedPrompt` 拼成"错题解析"prompt：base seed + mutated seed + divergent function source code。

`uftrace` 不可用 / replay 失败时退化为"用 target.Function 当 divergentFunc"，仍能继续重试。

## 5. FlagProfile 接入

每次 `solveConstraint` 在第一次 LLM 调用前调用：

- `assignDefaultProfile(seed)` —— 给 seed 一个 baseline profile (`engine.go:680-685`)；
- `attachPromptProfile(target, ctx, source)` —— 把 `FlagScheduler.NextProfileForTarget` 选出的 profile 写到 `TargetContext.ActiveFlagProfile*` 系列字段，供 prompt builder 使用。

profile 还会 clone 进 `seed.FlagProfile`（`clonePromptProfile`），下游 oracle 的 `Polarizer` 据此判定 `IsNegativeControl`。

详见 `@/home/yall/project/de-fuzz/docs/tech-docs/features/flag-scheduler.md`。

## 6. 终态：覆盖饱和后的 Random Phase

`Engine.Run` 在 `analyzer.SelectTarget()` 返回 `nil` 时认为"目标全覆盖"，若配置 `EnableRandomPhase=true` 就把控制权交给 `RandomMutationPhase`：

```go
if target == nil {
    if e.cfg.EnableRandomPhase {
        randomPhase := NewRandomMutationPhase(e, e.cfg.MaxRandomIterations)
        randomPhase.Run()
    }
    break
}
```

随机阶段与主循环最大的差异是 **持久化条件**：只有 oracle 报 bug 才把 seed 写进 corpus；新覆盖率不算。详见 `@/home/yall/project/de-fuzz/docs/tech-docs/features/random-mutation-phase.md`。

## 7. 状态保存与可恢复性

| 时机 | 保存内容 | 入口 |
| --- | --- | --- |
| 处理完所有 initial seeds 后 | coverage_mapping.json + state.json | `processInitialSeeds` 末尾 `e.saveState()` |
| 每 10 轮 constraint solving | 同上 | `Run` 主循环 |
| 主循环退出 | `finalizeState`：再次保存 + `corpus.Finalize` (清 pool_size / current_fuzzing_id) | `Run` 末尾 |

`coverage.HasIncreased` + `coverage.Merge` 保证 `total.json` 累积覆盖；恢复时 `[Fuzz] Found existing coverage data` 即从该文件继续。

## 8. 故障域速查

| 现象 | 排查入口 |
| --- | --- |
| `runOracle` 报 `oracle requires AnalyzeContext with Executor and BinaryPath` | 检查 `OracleExecutor` 是否注入；`useQEMU` 与 `cfg.Compiler.Fuzz.QEMUPath` 是否一致 |
| 主循环每轮重试都触发"divergence found" | `uftrace` 输出未被 `findParserStart` 跳过初始化噪音 — 通常 prompt 质量差异，不是 bug |
| 大量 seed 命中 oracle 但 `corpus.Add` 失败 | 看 logger.Warn `Failed to add seed to corpus`；多半是磁盘 / 命名冲突 (`internal/seed/naming.go`) |
| 覆盖率不再增长但 oracle 不停报 bug | 进入 RandomMutationPhase 是设计行为；可调小 `MaxRandomIterations` |

代码引用：`@/home/yall/project/de-fuzz/internal/fuzz/engine.go`、`@/home/yall/project/de-fuzz/internal/fuzz/phase_random.go`、`@/home/yall/project/de-fuzz/internal/fuzz/flag_strategy.go`。
