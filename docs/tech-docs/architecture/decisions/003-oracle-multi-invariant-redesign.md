---
title: "ADR-003: Oracle Multi-Invariant Redesign"
description: 把 oracle 从"每机制单一 verdict"演化为"机制聚合器 + 并行 invariant checker"的设计决策
priority: HIGH
last_updated: 2026-05-08
status: Accepted, Implemented
related_docs:
  - ../oracle-mechanism-framework.md
  - ../../features/canary-oracle.md
  - ../../invariants/stack-canary.md
---

# ADR-003: Oracle Multi-Invariant Redesign

> **Addendum (2026-05-21)**：本 ADR §3.2 中提出的 "Phase 1: Enablement (BLOCKING)" 已在 2026-05-21 后被回收。`InvariantCategory` 简化为 `Static / Dynamic` 两类，对齐 `@/home/yall/project/de-fuzz/docs/story_line.md` §4 的"静态属性 / 动态属性"二分类，原因：
> - Enablement 自项目落地以来从未注册过 checker，是事实上的死代码；
> - 它把"机制是否启用"建模为独立调度阶段，与"以何种证据验证不变量"这条 Category 主轴正交，分类不纯；
> - "机制未启用"的语义现在统一由 checker 层返回 `VerdictNotApplicable + Reason` 表达——NA 不会被聚合器视为 bug，因此配置错误不再可能造成假阳性，原 BLOCKING gating 的目标自然达成。
>
> 本节后文（包括 §3.2 的 Phase 1 描述、§5 末段对应的 BLOCKING 阐述）保留为历史记录，不要按它去修代码；以 `@/home/yall/project/de-fuzz/docs/tech-docs/architecture/oracle-mechanism-framework.md` 为现行实现态参考。

> **实施现状 (2026-05-08)**：本 ADR 提出的方向已落地于 commit `268464c refactor(oracle): add invariant-based mechanism checks` 与 follow-up `ee04f48 feat(oracle): detect epilogue canary leaks`。代码位置：
> - `@/home/yall/project/de-fuzz/internal/oracle/mechanism.go` —— `MechanismOracle` 聚合器；
> - `@/home/yall/project/de-fuzz/internal/oracle/invariant.go` —— `InvariantChecker` / `InvariantResult` / `Polarity` / `CheckContext`；
> - `@/home/yall/project/de-fuzz/internal/oracle/inspector.go` —— `BinaryInspector` (基于 `debug/elf` 的纯 Go 实现，未走 nm/objdump shell-out)；
> - `@/home/yall/project/de-fuzz/internal/oracle/checker_dynamic_buffer.go`、`checker_dynamic_scrub.go`、`checker_static_canary.go` —— 当前已注册的 4 个 checker。
>
> 实现态参考请阅 `@/home/yall/project/de-fuzz/docs/tech-docs/architecture/oracle-mechanism-framework.md`；本文保留作为决策动因与设计 trade-off 的历史记录。

# Oracle 多不变量改造的思考

> **配套阅读**：`@/home/yall/project/de-fuzz/docs/tech-docs/features/canary-oracle.md`、`@/home/yall/project/de-fuzz/docs/tech-docs/invariants/stack-canary.md`、`@/home/yall/project/de-fuzz/docs/tech-docs/architecture/decisions/002-multi-cfg-orchestration.md`、`@/home/yall/project/de-fuzz/docs/tech-docs/architecture/gcc-pipeline.md`。
>
> **目标**：在不打散现有 fuzzer 主循环的前提下，把 oracle 从"每个防御机制一个 oracle、给单一 verdict"演化为"每个防御机制一个聚合器、内部并行执行多条不变量断言、汇总成结构化报告"。本文先梳理动机，再评估代价，最后给出推荐架构与迁移路径。

## 1. 现状回顾

### 1.1 当前 Oracle 抽象

`@/home/yall/project/de-fuzz/internal/oracle/oracle.go:39-45` 定义的 `Oracle` 接口非常薄：

```go
type Oracle interface {
    Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error)
}
```

- **输入**：一颗 seed、可选执行上下文（`BinaryPath` + `Executor`）、fuzz 引擎跑出的若干 `Result`。
- **输出**：要么一个 `*Bug`（含 `Description`），要么 `nil`。
- **注册**：`@/home/yall/project/de-fuzz/internal/oracle/registry.go:20-31` 用 `Register(name, factory)` 把字符串映射到工厂；YAML 里 `compiler.oracle.type: "canary"` 决定加载哪一个（`@/home/yall/project/de-fuzz/cmd/defuzz/app/fuzz.go:266-276`）。
- **每个机制一个文件**：`canary_oracle.go`、`fortify_oracle.go`、`crash_oracle.go`、`llm_oracle.go`，互不复用 helper，主体逻辑都是"二分 fill_size + 哨兵 + exit code 判定"的复刻（参见 `@/home/yall/project/de-fuzz/internal/oracle/canary_oracle.go:228-276` 与 `@/home/yall/project/de-fuzz/internal/oracle/fortify_oracle.go:158-206`，几乎是同一段代码）。

### 1.2 Canary oracle 的具体行为

`@/home/yall/project/de-fuzz/docs/oracles/canary-oracle.md` 把整个 stack canary 防御压缩成一个判定流程：

1. 在 `[0, MaxBufferSize]` 上对 `fill_size` 做二分搜索，找最小的崩溃尺寸；
2. 看 exit code 是 134 (SIGABRT) 还是 139 (SIGSEGV) / 135 (SIGBUS)；
3. 配合 `SEED_RETURNED` sentinel 区分"`seed()` 返回后崩"vs"`seed()` 内部崩"；
4. 输出二选一：报 bug 或不报。

**这条单一信号链能直接覆盖的不变量**只有 `INV-SP-L01`（canary slot 必须在 vulnerable locals 与 saved registers 之间）和 `INV-SP-F01`（`__stack_chk_fail` 退码 134 的运行时契约）两条；它顺带间接覆盖 `INV-SP-L02 / L03 / L05 / CVE-2023-4039`，但是是通过"恰好以同一个失败模式表现"的方式覆盖的，**不能区分**到底是哪一条 invariant 失效。

### 1.3 Invariants 调研显示的覆盖缺口

`@/home/yall/project/de-fuzz/docs/invariants/stack-canary.md` 单独为 SP 列出了 8 大类、30+ 条 invariants，按可观察方式分布如下：

| 类别 | 代表 invariants | 当前 oracle 是否检 |
| --- | --- | --- |
| **启用条件 (Enablement)** | INV-SP-E01/E02/E03 | ❌ 完全靠人审 yaml + cflags |
| **启发式 (Heuristic)** | INV-SP-H01/H02/H03（含 8 字节边界） | ❌ 二分搜索看不到分类位翻转 |
| **栈帧布局 (Layout)** | INV-SP-L01/L02/L03/L04/L05 | ⚠️ 仅覆盖到"末端表现一致"的 L01；L04 (spill 污染) 当前是已知假阳性来源 |
| **寄存器/调用约定** | INV-SP-R01/R02 | ❌ 需反汇编 prologue/epilogue |
| **Guard 来源** | INV-SP-G01/G02/G03/G04 | ❌ 需 `nm` / 反汇编识别 `__stack_chk_guard` 与 `%fs:0x28` |
| **运行时契约** | INV-SP-F01/F02/F03/F04 | ⚠️ 仅 F01 间接（看到 134 即默认 F01 成立） |
| **属性/局部禁用** | INV-SP-A01/A02/A03 | ❌ 需对函数粒度反汇编 |
| **链接/DSO** | INV-SP-X01/X02 | ❌ |

**结论**：现有 canary oracle 用一个 boolean 报告 30+ 个 invariants 的并集。这意味着：

- **报喜不报忧**：任何 invariant 的违反，只要不"恰好制造出 SIGSEGV-with-sentinel"就被静默吞掉；INV-SP-L04 这类 hardening 缺陷会以"没崩"的形式漏检。
- **报忧不分喜**：报 bug 时只能附带"bypass detected at fill_size = N"，不能告知"是 INV-SP-L02 (AArch64 layout) 还是 CVE-2023-4039 还是 L04 spill 污染"。
- **不可扩展**：往 oracle 里继续塞新检查就是无止境往 `analyzePositiveCase` 加 switch 分支。
- **跨机制重复**：fortify、未来 CFI / shadow stack / IBT 都会遇到完全相同的"启用条件 / 静态特征 / 运行时崩溃 / 退化"四象限，复制粘贴 oracle 不可持续。

### 1.4 Fuzz 引擎对 oracle 输出的耦合

`@/home/yall/project/de-fuzz/internal/fuzz/engine.go:608-622` / `:811-836` 表明引擎对 oracle 的依赖只有两件事：

- 调一次 `Analyze`，拿 `*Bug` 或 `nil`；
- 把 `bug.Description` 写进 `OracleVerdict` (`OracleVerdictNormal | OracleVerdictBug | OracleVerdictSkipped`) 与 `BugDescription` (`@/home/yall/project/de-fuzz/internal/seed/metadata.go:54-57`)。

也就是说，**改造 oracle 内部实现不需要碰引擎**；只要保证最后能返回一个聚合后的 `*Bug` 即可。这给"内部多不变量并行 + 外部单一 Bug"的渐进重构留出了足够空间。

## 2. 评估"多不变量预言机"提议

### 2.1 提议陈述

> 每个防御机制一个 oracle，oracle 内部实现若干 invariant assertion。所有 invariant 并行执行，结果汇总后再给出整体判定。

把它分解成两个独立设计决策：

1. **是否把"机制 → 多 invariant"显式建模？**（即引入 invariant checker 为一等公民）
2. **是否需要并行执行 invariant？**（即 checker 之间的并发模型）

两者其实可以解耦：先把结构拆出来，稳定后再考虑并行。

### 2.2 收益（按重要性递减）

1. **可追溯性 = 报告里能写出"哪条 invariant 失败、对应 survey 哪条引证"。**
   - 直接对接 `@/home/yall/project/de-fuzz/docs/invariants/*.md` 里每条 invariant 的 `oracle_mapping` 字段。一份 bug 报告可以引用 `INV-SP-L01` + 提供 evidence_snippet + version_sensitivity，研究价值远高于今天的 "Buffer overflow at size 256 caused SIGSEGV"。
2. **检查正交性 = 加一条 invariant 不动其它 checker。**
   - 例如想新增 INV-SP-G02 ("x86_64 prologue 必须 `mov %fs:0x28, %rax`")，只要写一个新 checker 文件、注册到 SP 聚合器即可，与 binary search checker 完全独立。
3. **验证不同信号源能覆盖的子集**：static (objdump/nm) 检查 INV-SP-G* / E* / A*；dynamic (binary search) 检查 INV-SP-L* / F01；diff (跨 ISA / 跨编译器版本) 检查 INV-SP-CVE-2023-4039。一份机制的"覆盖度"可量化为"启用的 invariant 数 / survey 总数"。
4. **跨机制复用 checker 模板**：
   - "二分 fill_size + sentinel + exit code"模板被 canary 与 fortify 共用 (`@/home/yall/project/de-fuzz/internal/oracle/canary_oracle.go:228-276` 几乎是 `fortify_oracle.go:158-206` 的复制)；
   - "扫 ELF 导入符号 / 反汇编 prologue 匹配指令模式"模板会被 SP / FORT / CFI / SCS / IBT 都用到；
   - 一旦 invariant checker 是一等公民，这些模板能落到独立的 helper 包，避免每加一个机制就重写一遍。
5. **diagnostics**：每条 invariant 单独报"已检 / 跳过 / 通过 / 违反 / 不适用"，启动期就能像 `multi-cfg-evaluation.md` §4.1.3 那样输出高信噪比日志，比当前"binary search 没找到崩溃 → 静默 nil"友好很多。
6. **支持 negative control**：`isNegativeCase` 现在只能整体反转判定 (`@/home/yall/project/de-fuzz/internal/oracle/canary_oracle.go:121-142`)；invariant 维度上更精细——例如 `-fno-stack-protector` 应让 INV-SP-L01 期望"不触发 abort"、但 INV-SP-A01 仍然期望"`main` 没 canary 槽"。

### 2.3 代价（按严重度递减）

#### A. 输入异质，统一接口设计有难度

不变量需要的输入差别很大：

| Invariant | 需要的输入 | 当前可获得性 |
| --- | --- | --- |
| INV-SP-L01 (binary search) | `BinaryPath` + 可控 stdin/argv 的 executor | ✅ 已有 |
| INV-SP-G01 (`__stack_chk_guard` 引用) | ELF 文件 + `nm`/objdump | 🟡 工具未集成 |
| INV-SP-G02 (prologue 含 `mov %fs:0x28`) | objdump 反汇编 | 🟡 |
| INV-SP-A01 (`main` 不插 canary) | objdump 函数粒度反汇编 + 符号表 | 🟡 |
| INV-SP-E01 (启发式触发面) | 编译命令 + cflags + 函数特征 (8 字节边界) | 🟡 需要 fuzz engine 或 prompt service 把 cflags 落到 ctx |
| INV-SP-X02 (LTO 不改变插桩决策) | 同源代码两次编译产物 diff | ❌ 跨 seed 历史 |
| INV-SP-CVE-2023-4039 | 跨编译器版本 (GCC ≤13.2 vs ≥14) 同 seed 对比 | ❌ 当前 fuzz 一次只用一个编译器 |

**结论**：`AnalyzeContext` 需要扩展，至少包含：

- `BinaryPath` (现有)
- `Executor` (现有)
- `CFlags []string` (从 `seed.Seed.AppliedLLMCFlags` + profile 已可得，但当前 oracle 不接)
- `CompilerVersion` / `Target` / `ISA` (config 已有，未传到 oracle)
- 一个轻量级 `BinaryInspector` 接口：`Symbols() / Disasm(symbol) / ReadSection(name)`，封装 `nm`/`objdump`/`readelf` 调用与缓存

但要避免把 `AnalyzeContext` 变成"无所不包的 god-context"，否则 invariant checker 的可测试性会被毁。建议：

- ctx 只放"现成数据 + 现成接口"；
- 每个 checker 在内部决定要不要 invoke `BinaryInspector`，没用就不调；
- 共享 `BinaryInspector` 实例做缓存，避免 100 个 checker 各 fork 100 次 `objdump`。

#### B. 并行的真实收益与代价不对称

- **大头时间**在 `binarySearchCrash` 的 ~log₂(N) 次 QEMU 执行（每次几十 ms ~ 几百 ms，N=1024 时约 10 次 = 数秒）。
- 静态 invariant (objdump/nm) 是毫秒级。
- 把 30 个 invariant 并行起来，总耗时被那一条 binary-search 约束死，并发收益 << 复杂度增加。

**建议**：

- **先序后并行**：先跑全部静态 invariant（毫秒级，串行也无所谓），再跑动态 invariant；动态 invariant 内部如果有"独立 fill_size 区间"才考虑并行（例如二分 + 跨 ISA 探测可独立执行）。
- **不要为并行而并行**。`Executor` 当前是 QEMU pipe，多并发会触发 QEMU 实例 fork 风暴；非要并行需要 executor 池 (`sync.Pool` of QEMU) 或并发上限，本身又是一个子项目。
- 真正能并行的天然边界是 **跨机制的 oracle 同跑**（canary + fortify 同时分析同一 seed），但目前 fuzz engine 一次只挂一个 oracle (`@/home/yall/project/de-fuzz/internal/fuzz/engine.go:26`)，需要先把 `Oracle` 改成 `[]Oracle` 或引入 composite oracle。

#### C. 聚合策略需要明确的 spec

多个 invariant 各自给 verdict 后，最终 `*Bug` 怎么写？这不是工程问题而是产品决策：

| 聚合策略 | 行为 | 适用场景 |
| --- | --- | --- |
| **OR** (任一违反 → bug) | 报告所有违反的 invariant | 默认；研究价值最高 |
| **WEIGHTED** | 每条 invariant 带置信度，超过阈值才报 | 当 dynamic 信号本身有假阳性 (INV-SP-L04) 时 |
| **CONJUNCTIVE** (n/m 同时违反才报) | 多个 invariant 互证 | 想把 CVE-2023-4039 这种 "L01 violation && AArch64 && GCC ≤13.2" 三条命中才报 |
| **BLOCKING** (某一条失败则阻断后续) | 节省时间 | INV-SP-E01 (没启用 SP) → 直接跳过 L*/G*/A* |

实践中 BLOCKING + OR 是最自然的组合：先用 enablement invariant 当 gating；通过后用 OR 合并所有 violation。

#### D. 不变量到 seed 模板的反向耦合

每条 invariant 的 `oracle_mapping` 字段隐含一个"为了检测它，seed 要长什么样"的要求。例如 INV-SP-H01 (8 字节边界) 要求 seed 有"恰好 7 字节字符数组"或"恰好 8 字节字符数组"两种变体。当前 fuzzer 的 seed 是 LLM 自由生成的，并不保证 invariant 触发条件能被覆盖。

**含义**：oracle 不能完全离开 seed 模板独立改造。要么在 prompt 端为 invariant 设计模板（接近 fortify oracle 已有做法 `@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md`），要么 oracle 自带"用同一个 binary，但用不同 argv 触发不同 invariant"的能力（INV-SP-L01 已经在用，扩展是可行的）。

**风险**：假如 invariant checker 静默"我跑了但 seed 不满足触发条件"，会出现"oracle 全绿 → 实际没测"的盲区。每条 invariant 必须返回三态：`Pass | Fail | NotApplicable(reason)`，aggregator 必须把 "NotApplicable 比例 > 阈值" 当作可见 warning。

#### E. 与 negative control / flag profile 的交互

`isNegativeCase` 现在判 `IsNegativeControl` 或匹配 `negative_cflags` 后整体反转 (`@/home/yall/project/de-fuzz/internal/oracle/canary_oracle.go:121-142`)。多 invariant 模式下必须改成"每条 invariant 自己声明 'expected polarity'"：

- 例如 INV-SP-A01 (`main` 没 canary)：positive = "main 没 canary 槽"；负控组 (`-fno-stack-protector`) 时仍然 positive = "main 没 canary 槽"，invariant 不变。
- 例如 INV-SP-L01 (canary 阻挡 ret 改写)：positive = "fill_size 越界 → SIGABRT 134"；负控组时 positive 翻转为 "fill_size 越界 → SIGSEGV 139 (没 canary 时这是预期)"。

这要求 invariant 元数据里多一个字段：`expected_polarity_negative: keep | invert | n/a`，让 aggregator 在判定前根据 seed flag profile 应用极性。

#### F. 增量复杂度对 LLM-driven oracle 的冲击

`@/home/yall/project/de-fuzz/internal/oracle/llm_oracle.go` 走的是另一条路（feedback → LLM 分析）。如果把"按机制聚合"作为统一抽象，LLM oracle 在新框架下要么变成"自带一个统一的 invariant: 异常输出特征"、要么独立成另一组 oracle。设计时不要让 LLM oracle 退化成只能跑在 invariant aggregator 之外。

## 3. 推荐架构

下面给出一个**最小破坏性、可分阶段落地**的设计。所有命名只是占位，落地时可调整。

### 3.1 类型分层

```
oracle.Oracle               (现有接口, 不动)
  └─ MechanismOracle        (新增, 各防御机制 oracle 实现它)
       └─ []InvariantChecker (新增, 每条 invariant 一个文件)
            └─ uses BinaryInspector / Executor / SeedFacts
```

- **`Oracle`**：保持 `Analyze(seed, ctx, results) (*Bug, error)` 不变，引擎完全无感。
- **`MechanismOracle`**（接口）：内部组合一组 `InvariantChecker`，负责按 enablement → static → dynamic 顺序调度，并把每条 verdict 汇总成 `*Bug`。一个 MechanismOracle 对应一份 invariants 文档（SP / FORT / CFI / SCS / ...）。
- **`InvariantChecker`**：

    ```go
    type InvariantVerdict int
    const (
        VerdictPass InvariantVerdict = iota
        VerdictFail
        VerdictNotApplicable
        VerdictError
    )

    type InvariantResult struct {
        ID            string            // 例如 "INV-SP-L01"
        Verdict       InvariantVerdict
        Evidence      string            // 一行描述
        Detail        map[string]any    // 例如 {"min_crash_size": 256, "exit_code": 139}
        SourceURL     string            // 直接抄 invariants/*.md 的 source_url_or_path
        Sensitivity   string            // stable / likely-to-drift
        Polarity      string            // positive / inverted (经 negative-control 翻转后)
    }

    type InvariantChecker interface {
        ID() string                                // "INV-SP-L01"
        Category() string                          // "layout" / "static" / "runtime"
        Check(ctx CheckContext) InvariantResult
    }
    ```

- **`CheckContext`**：扩展自 `AnalyzeContext`，加 `CFlags`、`CompilerVersion`、`Target`、`Inspector BinaryInspector`、`Seed *seed.Seed`。
- **`BinaryInspector`**：把 `nm`/`objdump`/`readelf`/`strings` 包成 lazy + cached 接口，所有 checker 共享一个实例。

### 3.2 调度流程

每个 `MechanismOracle.Analyze` 内部：

1. **Phase 1: Enablement (BLOCKING)**：跑所有 `Category == "enablement"` 的 checker。任一返回 `Fail` → 视配置：
   - 若是 negative control 期望失败 → 继续；
   - 否则 short-circuit：聚合成"机制未启用"verdict，但**不报 bug**（这是配置问题不是漏洞）。
2. **Phase 2: Static (并行)**：跑所有 `Category == "static"` 的 checker。这一阶段没有 IO 之外的副作用，可以放到 goroutine pool。
3. **Phase 3: Dynamic (顺序)**：binary search、sentinel、跨 fill_size 探测；放在最后是因为最贵。**注意**：这一阶段 checker 之间可能共享 binary-search 中间结果（例如 L01 与 L04 共用同一组 (fill_size, exit_code, sentinel) 三元组），需要在 ctx 里挂一个 dynamic-result cache，避免 N 个 checker 各跑一次二分。
4. **Phase 4: Aggregation**：按 §2.3.C 的策略汇总。默认 OR；若没有任何 `Fail`，返回 `nil`；否则把所有 `Fail` 的 invariant 拼成一份结构化 `Bug.Description`。

### 3.3 报告结构

`*Bug.Description` 从今天的散文升级为分块文本（仍是字符串，便于落 metadata，不影响下游）：

```
[CanaryMechanismOracle] 1 violation found.

Violations:
  - INV-SP-L01 (layout):
      Evidence: fill_size=256 → exit_code=139 with sentinel SEED_RETURNED present.
      Detail: {min_crash_size: 256, exit_code: 139, sentinel: true, fill_strategy: "binary"}
      Source: gcc/cfgexpand.cc add_stack_protection_conflicts; aarch64.cc save-regs comment
      Sensitivity: stable

Passed (12):
  - INV-SP-E01, INV-SP-E03, INV-SP-G01, INV-SP-G02, INV-SP-A01, INV-SP-F01,
    INV-SP-F02, INV-SP-X01, INV-SP-H01, INV-SP-L02, INV-SP-L03, INV-SP-L05

Not applicable (3):
  - INV-SP-CVE-2023-4039: requires GCC ≤13.2, current is 15.2
  - INV-SP-G03: requires aarch64 sysreg mode, current is global
  - INV-SP-X02: requires LTO comparison run

Errors (0)
```

这样 metadata 里 `BugDescription` 一行就具备 reproduce + 引证 + 可信度三要素。

### 3.4 与现有代码的契合点

- **保留 `oracle.Oracle` + `Register`**：把 `canary_oracle.go` 改为 `canary_mechanism.go`，内部实例化一组 `InvariantChecker`，对外仍以 `"canary"` 这个名字注册。引擎、配置、metadata 路径全部不动。
- **二分 + sentinel checker** 直接抽出到 `oracle/checkers/dynamic_buffer_search.go`，参数化 (max size、sentinel marker、exit code 解释表)，让 fortify 也复用同一个 checker（消除 `@/home/yall/project/de-fuzz/internal/oracle/canary_oracle.go:228-276` 与 `fortify_oracle.go:158-206` 的重复）。
- **新增 `oracle/checkers/static_*.go`**：先做最容易的两条——`INV-SP-G01` (`__stack_chk_guard` 符号引用) 与 `INV-SP-A01` (`main` 不插 canary)，验证 BinaryInspector 与 InvariantChecker 模型在真实工作量下不漏抽象。
- **测试**：`canary_oracle_test.go` 与 `cve_2023_4039_integration_test.go` 不能动语义；改造后这两套用例应该作为 INV-SP-L01 / INV-SP-CVE-2023-4039 的 reference test，既验证 mechanism oracle 的最终 `*Bug`，也验证个体 checker 的 `InvariantResult`。

### 3.5 显式的非目标

为了控制范围，**第一阶段不做**：

1. **跨机制 oracle 并发**：保持 `engine.cfg.Oracle` 单实例。多机制并发是另一个工程，与本改造正交。
2. **跨编译器版本 diff oracle**：INV-SP-X02 / CVE-2023-4039 的"跨版本对比"留作 follow-up，因为这要求 fuzz 引擎支持 multi-compiler runs。
3. **改 prompt 模板**：seed 形态先沿用现状，invariant checker 在 "不适用 (NotApplicable)" 上诚实标注；待 §3.1-3.4 稳定后再去拉 prompt service 配合 invariant 设计 seed 变体。

## 4. 风险评估

| 风险 | 严重度 | 缓解 |
| --- | --- | --- |
| invariant checker 数量爆炸，但 seed 形态不能触发大多数 checker → 大量 "NotApplicable" 看起来像绿色但其实没测 | 高 | aggregator 必须报告 NotApplicable 比例；CI 对每个机制设阈值（例如 NA ≤ 50%）；负控组定期跑全 invariant 矩阵作为校准 |
| `BinaryInspector` 把 oracle 与外部工具耦合（`nm`/`objdump` 版本差异） | 中 | 通过 toolchain 配置注入工具路径；inspector 内部记录每个工具的版本 / 输出 hash，作为 evidence 的一部分 |
| dynamic-result cache 出错 → 多个 checker 看到不一致的 binary-search 结果 | 中 | cache 键包含 (binary 路径、argv、env、cflags hash)；调试模式下关闭 cache 重跑对照 |
| 聚合策略变更引入回归（OR ↔ WEIGHTED） | 中 | aggregator 策略走配置开关 (`oracle.options.aggregation`)，默认 OR；切换策略时跑 `cve_2023_4039_integration_test.go` 全集 |
| 二分搜索在 CI 上的执行时间膨胀（多 checker 共享 cache 但首跑仍贵） | 低 | dynamic phase 仍然只跑一次二分；新增的 invariant 都从同一份 dynamic 结果派生 |

## 5. 与同期改造的耦合

- **多 CFG (`@/home/yall/project/de-fuzz/docs/architecture/multi-cfg-evaluation.md`)**：正交。多 CFG 决定 `SelectTarget` 的候选空间，oracle 改造决定如何评估每次执行。两者唯一交集是"日志诊断"：都建议把"被跳过 / 不适用 的项"显式 warn，CI 可以共用一套日志收敛工具。
- **GCC pipeline (`@/home/yall/project/de-fuzz/docs/architecture/gcc-pipeline.md`)**：oracle 改造不影响构建期插桩；运行期 invariant checker 可以反过来读 `.gcda` 衍生信息（例如某条 SP 路径是否被覆盖），但这是后期增强，非必需。
- **Fortify oracle (`@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md` + `@/home/yall/project/de-fuzz/docs/invariants/fortify-source.md`)**：是这次改造的第二验证场——fortify 与 canary 共享"二分 + sentinel + exit code"模板，第一阶段把这两个机制接入新框架，能马上证伪/证实"机制 → invariant"抽象的复用价值。

## 6. 结论

- 把 oracle 重构为"每防御机制一个聚合器、内部 N 条 invariant checker"是对的方向，因为它正好对齐 `docs/invariants/*.md` 的研究产物，让 oracle 的覆盖度可量化、可增量扩展。
- "并行执行"作为目标过于强：真实瓶颈在动态执行而非 invariant 之间的串行；先把结构拆出来，并行作为 follow-up。
- 对外接口 (`Oracle.Analyze`) 保持不动；改造完全在 `internal/oracle/` 内部进行，引擎、配置、metadata 三处零改动。
- 最小可落地里程碑：把现 canary oracle 的二分逻辑抽成 `dynamic_buffer_search` checker（INV-SP-L01），加 1-2 条 static checker（INV-SP-G01 / A01）走通 BinaryInspector 闭环；然后让 fortify oracle 复用同一个 dynamic checker，回收当前重复的 80 行代码。这一步走通后再批量补 invariant。

## 7. 参考

- 现有 oracle 接口：`@/home/yall/project/de-fuzz/internal/oracle/oracle.go:1-60`
- 现有 canary oracle 实现：`@/home/yall/project/de-fuzz/internal/oracle/canary_oracle.go:1-277`
- 现有 fortify oracle 实现：`@/home/yall/project/de-fuzz/internal/oracle/fortify_oracle.go:1-207`
- 引擎对 oracle 的依赖点：`@/home/yall/project/de-fuzz/internal/fuzz/engine.go:608-622`、`@/home/yall/project/de-fuzz/internal/fuzz/engine.go:811-836`
- 配置入口：`@/home/yall/project/de-fuzz/cmd/defuzz/app/fuzz.go:266-276`
- 完整 SP invariant 列表：`@/home/yall/project/de-fuzz/docs/invariants/stack-canary.md`
- Canary oracle 设计文档：`@/home/yall/project/de-fuzz/docs/oracles/canary-oracle.md`
- Fortify oracle 设计文档：`@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md`
- 架构文档风格参考：`@/home/yall/project/de-fuzz/docs/architecture/multi-cfg-evaluation.md`
