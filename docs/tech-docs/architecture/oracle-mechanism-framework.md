---
title: Oracle Mechanism Framework (Implemented Reference)
description: MechanismOracle / InvariantChecker / BinaryInspector / Polarizer 的实现态参考，承接 ADR-003
priority: HIGH
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ./decisions/003-oracle-multi-invariant-redesign.md
  - ../features/canary-oracle.md
  - ../guides/adding-a-defense-mechanism.md
---

# Oracle Mechanism Framework (Implemented Reference)

> 本文是 ADR-003 落地之后的 **实现态参考**。设计动因 / trade-off / 备选方案见 `@/home/yall/project/de-fuzz/docs/tech-docs/architecture/decisions/003-oracle-multi-invariant-redesign.md`，本文只描述"现在已经在 main 上跑的代码长什么样、怎么用、怎么扩"。

## 1. 类型分层

```
oracle.Oracle                                       (内部 facade，引擎只看到这个)
  └─ MechanismOracle                                (per-mechanism 聚合器)
        ├─ Polarizer                                (per-seed polarity 决策)
        └─ []InvariantChecker
              ├─ Category() ∈ {Static, Dynamic}     # 故事线 §4：静态属性 / 动态属性
              ├─ ID()      e.g. "INV-SP-L01"
              └─ Check(*CheckContext) InvariantResult
                                      └─ uses BinaryInspector / Executor / Cache
```

代码定位：

- `@/home/yall/project/de-fuzz/internal/oracle/oracle.go:39-45` —— `Oracle` 接口（保持向引擎稳定）
- `@/home/yall/project/de-fuzz/internal/oracle/mechanism.go:33-114` —— `MechanismOracle` + `Analyze` 主调度
- `@/home/yall/project/de-fuzz/internal/oracle/invariant.go:97-192` —— `InvariantResult` / `InvariantChecker` / `CheckContext`
- `@/home/yall/project/de-fuzz/internal/oracle/inspector.go:18-191` —— `BinaryInspector` (ELF, stdlib)
- `@/home/yall/project/de-fuzz/internal/oracle/checker_*.go` —— 各 invariant checker

## 2. Verdict 与 Category

### 2.1 Verdict 三态再加错误

```go
const (
    VerdictPass         InvariantVerdict = iota  // 不变量成立
    VerdictFail                                  // bug 候选
    VerdictNotApplicable                         // 不可断言（条件不满足、负控降级、binary 缺失等）
    VerdictError                                 // 基础设施失败（ELF parse/Executor 出错）
)
```

`Pass` 是零值，未设置字段不会意外翻成 `Fail`。聚合器把 NA / Error 计入诊断但**不**报为 bug；高 NA 比例是文档化的"该 invariant 在当前 seed 形态下未被覆盖"的可见信号（见 ADR-003 §2.3.D）。

### 2.2 Category 对齐故事线：静态 / 动态二分

故事线（`@/home/yall/project/de-fuzz/docs/story_line.md` §4）把每条安全不变量明确分成两种可机器验证的形态：**静态属性（看汇编 / 二进制特征）** 与 **动态属性（看运行时行为）**。代码里的 `InvariantCategory` 直接对齐这两类，并兼任调度阶段——便宜的静态检查先跑，昂贵的动态检查后跑。

| Category | 现状 | 何时跑 | 备注 |
| --- | --- | --- | --- |
| `CategoryStatic` | `StackChkSymbolsChecker`、`MainNoCanaryChecker` | Phase 1 | 纯 ELF / 反汇编断言；任何 `Fail` 进入聚合 |
| `CategoryDynamic` | `DynamicBufferSearchChecker`、`EpilogueCanaryScrubChecker` | Phase 2 | 需要执行二进制；通过 `CheckContext.Cache` 复用 binary search 结果 |

**关于"机制未启用"**：这不是一个独立调度阶段，也不应被建模成 `Fail`。机制是否启用是 checker 在 Static phase 自然观察到的事实——例如 `StackChkSymbolsChecker` 发现 binary 没有 `__stack_chk_fail` 导入时，应返回 `VerdictNotApplicable + Reason="mechanism not active"`。NA 永远不被聚合器视为 bug，所以"配置错误"不会变成假阳性，同时诊断仍会出现在 `Bug.Description` 的 `Not applicable` 段。

调度顺序在 `mechanism.go` 内的 `Analyze`，目前 **顺序串行**——并行作为 follow-up 显式排除（ADR-003 §3.5）。

## 3. Polarity 模型

`Polarity` 表示"对当前 seed 来说，机制是被启用的（positive）还是被刻意关闭的（inverted）"。它由 `Polarizer` 接口提供：

```go
type Polarizer interface { Polarity(s *seed.Seed) Polarity }
```

`CanaryOracle.polarityFor`（`canary_oracle.go:161-194`）实现：seed 标记 `IsNegativeControl` 或携带 `negative_cflags`（默认 `-fno-stack-protector`）→ `PolarityInverted`，否则 `PolarityPositive`。

### 3.1 Per-checker polarity-sensitive 标记

`applyPolarity`（`mechanism.go:152-184`）默认把 checker 视为 **polarity-insensitive**（即 polarity 翻转不影响其 verdict）；只有在 `InvariantResult.Detail["polarity_sensitive"] = true` 时才生效翻转规则：

| Verdict (positive) | 翻转后 |
| --- | --- |
| `VerdictPass` | 降级为 `VerdictNotApplicable`（"机制本应失败却仍成立"——不视为 bug，可作为后续 negative control 验证） |
| `VerdictFail` | 升级为 `VerdictPass`（负控下崩溃是预期） |
| `VerdictNotApplicable` / `VerdictError` | 透传 |

设计理由：大多数 invariant 是绝对断言（如 INV-SP-A01 "main 没 canary 槽"，无论是否启用 SP 都应成立）；只有少数（如 INV-SP-L01 二分搜索的退码语义）会随 polarity 翻转。把 polarity-insensitive 设为默认，新增 checker 不容易"忘记 polarity"。

### 3.2 检查清单

| Checker | polarity-sensitive |
| --- | --- |
| `StackChkSymbolsChecker` (INV-SP-G01) | ❌ |
| `MainNoCanaryChecker` (INV-SP-A01) | ❌ |
| `DynamicBufferSearchChecker` (INV-SP-L01) | ✅（在 `Check` 内显式设置 Detail）|
| `EpilogueCanaryScrubChecker` (INV-SP-R03) | ❌ |

## 4. CheckContext 与跨 checker 缓存

```go
type CheckContext struct {
    Seed       *seed.Seed       // nil-safe: 单元测试可不传
    BinaryPath string
    Executor   Executor
    Inspector  BinaryInspector  // 由 mechanism.Analyze 在 BinaryPath != "" 时构造
    Polarity   Polarity
    Cache      map[string]any   // per-Analyze 共享，跨 checker memoization
}
```

约定：

- **Cache 是 per-Analyze 的**，每次 `MechanismOracle.Analyze` 启动时新建（`mechanism.go:71-77`），不跨 seed。
- Cache key 应使用包私有命名常量，命名空间为 `<file>.<purpose>`。当前已用：
  - `dynamicSearchCacheKey = "oracle.dynamic_buffer_search.result"` —— `DynamicBufferSearchChecker` 与未来共用 binary search 的 dynamic checker 复用同一份 `(MinCrashSize, CrashExitCode, HasSentinel, Probes)` 结果。
- BinaryInspector 是 lazy + per-call cached：第一次访问 `Symbols()` / `ImportedFunctions()` 时才打开 ELF；ELF 不可读时所有方法返回 `ErrBinaryMissing` / `ErrNotELF`，被 checker `naOrError` helper 翻译成 `VerdictNotApplicable`。

## 5. 聚合策略

`MechanismOracle.Analyze` 实施 **OR 聚合**：

1. Phase 1 (Static) + Phase 2 (Dynamic) 顺序执行，收集所有 verdict；
2. **任一 `VerdictFail`** → 返回 `*Bug`，描述由 `formatDescription` 渲染；否则返回 `nil`。

`VerdictNotApplicable` 与 `VerdictError` 永远不会触发 bug，仅作为诊断写入 `Bug.Description`。当机制未启用时，相关 checker 通过返回 NA 自然把整次分析降级为"无 bug"，无需额外的 enablement gating 阶段。

### 5.1 报告格式

`Bug.Description` 是稳定的多行文本（解析友好）：

```
[stack canary] 1 invariant violation(s) detected (polarity=positive).

Violations:
  - INV-SP-L01 (dynamic)
      Evidence: Stack canary bypass detected: fill_size=256 caused SIGSEGV (exit 139) after seed() returned; mechanism failed to trap before return
      Detail: {crash_exit_code=139, default_buf_size=64, has_sentinel=true, max_fill_size=4096, min_crash_size=256, polarity_sensitive=true, probes=14}
      Source: https://gcc.gnu.org/onlinedocs/gccint/Stack-Smashing-Protection.html
      Sensitivity: stable

Passed: INV-SP-A01, INV-SP-G01

Not applicable:
  - INV-SP-R03: scrub probe declined: CANARY_SCRUB_NA reason=arch-not-supported
```

字段语义：`Evidence`（人读）/ `Detail`（机读，按 key 字典序排）/ `SourceURL`（survey 反链）/ `Sensitivity`（stable / likely-to-drift）。

### 5.2 落地路径

`Bug.Description` → `Engine.runOracle` → `seed.Meta.OracleVerdict = OracleVerdictBug`、`seed.Meta.BugDescription = bug.Description`；持久化路径见 `@/home/yall/project/de-fuzz/internal/seed/metadata.go:54-57`。

## 6. 已注册 checker 速查

| ID | Mechanism | Category | File:Line | Polarity-sensitive | Cache key |
| --- | --- | --- | --- | --- | --- |
| `INV-SP-G01` | canary | Static | `checker_static_canary.go:30-86` | ❌ | (none, 直接走 Inspector) |
| `INV-SP-A01` | canary | Static | `checker_static_canary.go:118-191` | ❌ | (none) |
| `INV-SP-L01` | canary | Dynamic | `checker_dynamic_buffer.go:22-241` | ✅ | `oracle.dynamic_buffer_search.result` |
| `INV-SP-R03` | canary | Dynamic | `checker_dynamic_scrub.go:46-167` | ❌ | (none, 单次 argv `scrub` 探测) |

## 7. 添加 checker 的最小步骤

1. 在 `@/home/yall/project/de-fuzz/docs/tech-docs/invariants/<mechanism>.md` 中确定 invariant ID 与 oracle_mapping。
2. 新建 `internal/oracle/checker_<...>.go`，实现 `InvariantChecker`：返回完整 `InvariantResult`，包括 `ID / Category / Verdict / Evidence / Detail / SourceURL / Sensitivity`。`Reason` 只在 NA / Error 时填。
3. 在对应 `<mechanism>_oracle.go` 的 `mechanism()` 函数 Checkers 列表里追加该 checker；保留按 Category 顺序声明的习惯（Static → Dynamic）。
4. 写 `checker_<...>_test.go`：至少覆盖 Pass / Fail / NA 三条路径；`fake_test_helper.go` 模式为 mock executor / inspector 提供参考（参见 `mechanism_test.go`）。
5. （可选）如果 Verdict 翻转该随 polarity 走，在 `Detail` 里设 `polarity_sensitive: true`。

整套端到端流程（含 prompt / config 改动）见 `@/home/yall/project/de-fuzz/docs/tech-docs/guides/adding-a-defense-mechanism.md`。
