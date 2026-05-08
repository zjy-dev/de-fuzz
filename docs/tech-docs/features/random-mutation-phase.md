---
title: Random Mutation Phase
description: 覆盖饱和后的随机变异阶段：触发条件、流程与持久化策略
priority: MEDIUM
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ../architecture/fuzz-engine-loop.md
  - ./canary-oracle.md
---

# Random Mutation Phase

`internal/fuzz/phase_random.go` 在主循环的 CFG-guided 约束求解阶段结束之后接管控制流。它是一个 **bug-only** 的辅助阶段：不再追求覆盖增量，只在已有 corpus 基础上做随机变异，仅当 oracle 报 bug 时把新 seed 持久化。

## 1. 触发条件

```go
if e.cfg.MaxIterations > 0 && e.iterationCount >= e.cfg.MaxIterations { break }
target := e.cfg.Analyzer.SelectTarget()
if target == nil {
    if e.cfg.EnableRandomPhase {
        randomPhase := NewRandomMutationPhase(e, e.cfg.MaxRandomIterations)
        randomPhase.Run()
    }
    break
}
```

两个先决条件：

1. `Analyzer.SelectTarget()` 返回 `nil` —— 候选 BB 已全部覆盖或不可达。
2. `Engine.Config.EnableRandomPhase = true` —— 配置开关；默认关闭。

`MaxRandomIterations` 控制随机阶段最长跑多少轮（`0` = 无限）。

## 2. 流程

```
RandomMutationPhase.Run                        phase_random.go:38-79
 │
 ├─ getProcessedSeeds (从 corpus 读所有 ProcessedCount 内的 seed)
 │
 └─ for iter < maxIterations:
      baseSeed = processedSeeds[rng.Intn(len)]
      mutateAndCheck(baseSeed)                 phase_random.go:114-175
        ├─ MutationContext{TotalCoveragePercentage}
        ├─ PromptService.GetMutatePrompt(...)
        ├─ LLM.GetCompletionWithSystem
        ├─ ParseLLMResponse → mutatedSeed
        ├─ assignDefaultProfile
        ├─ compiler.Compile (失败即静默丢弃)
        └─ runOracle:
              bug != nil → corpus.Add + persistCompilationRecord
              bug == nil → 丢弃 (与主循环不同：没有覆盖增量也不保留)
```

## 3. 与主循环的差异

| 维度 | 主循环 (`solveConstraint`) | RandomMutationPhase |
| --- | --- | --- |
| 目标驱动 | `SelectTarget()` 选 CFG 中的 target BB | 无目标，只是变异 |
| Prompt 类型 | constraint / refined / compile-error | mutate prompt |
| 持久化条件 | covered_new ∨ hit_target ∨ found_bug | **only** found_bug |
| FlagProfile | 走 FlagScheduler 矩阵 + 1/20 负控 | 用 `assignDefaultProfile` 给 baseline；不走轮转 |
| Coverage 记录 | 始终 RecordCoverage（合格 seed） | 不再 RecordCoverage |
| 失败处理 | 进入重试循环 + 发散分析 | 静默丢弃，进入下一次随机抽样 |

## 4. 配置接入

`fuzz.Config` 中相关字段：

```go
EnableRandomPhase   bool // 开关
MaxRandomIterations int  // 0 = unlimited
```

CLI 当前未暴露这两项；配置走代码注入，常见用法是研究脚本里 `cfgEngine := fuzz.NewEngine(fuzz.Config{ ..., EnableRandomPhase: true, MaxRandomIterations: 1000 })`。后续如需 YAML 化，配置位于 `compiler.fuzz.random_phase.{enabled,max_iterations}` 是合理选择，但本次未在 `internal/config/config.go` 中预留字段。

## 5. 与 Oracle 的耦合

随机阶段的 `runOracle` 仍走 `MechanismOracle`，因此所有 invariant checker 行为与主循环完全一致。区别只在 **何时记录**：

- `OracleVerdictBug` → 把 `BugDescription` 写到 `Seed.Meta.BugDescription`，corpus.Add；
- 其它 verdict（包括 `Normal` / `Skipped`）→ 直接丢，不进 corpus，不记 coverage。

这意味着随机阶段**不会**把"无 bug 但触发了新覆盖"的 seed 持久化；其设计意图是 stress-test 已有 corpus 在变异下的稳定性，而不是再次扩展覆盖面。

## 6. 故障域速查

| 现象 | 排查 |
| --- | --- |
| 进入随机阶段但 `processedSeeds` 为空 | corpus 没有 processed seeds；通常是 `processInitialSeeds` 把所有 initial seeds 都标了 `SeedStateProcessed` 但 `state.json` 未保存 |
| 随机阶段每轮都 compile 失败 | mutate prompt 让 LLM 偏离了模板；检查 `prompts/base/mutate.md` 与 understanding 是否对齐 |
| 报 bug 后 corpus.Add 失败 | 看 `phase_random.go:167` 的 logger.Warn；多半是 ID 冲突或磁盘错误 |

代码：`@/home/yall/project/de-fuzz/internal/fuzz/phase_random.go`、注入位置：`@/home/yall/project/de-fuzz/internal/fuzz/engine.go:158-167`。
