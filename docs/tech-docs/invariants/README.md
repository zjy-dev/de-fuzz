---
title: Invariants Survey Index
description: 24 份防御机制 invariant 调研的分类索引，及与 oracle InvariantChecker 实现的对照
priority: HIGH
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ../architecture/oracle-mechanism-framework.md
  - ../features/canary-oracle.md
  - ../guides/adding-a-defense-mechanism.md
---

# Invariants Survey Index

本目录是 DeFuzz 的"研究档案"层：把 GCC / LLVM / glibc 等开源编译器与运行时里的防御机制按 invariant 维度系统抽样。每份 markdown 是一个独立的 survey artifact，文件内部使用约定字段（`oracle_mapping` / `source_url_or_path` / `version_sensitivity`），与本目录之外的代码文档解耦。

> **本目录是只读的研究产物**：survey 文件本身不带 frontmatter（保留原貌），只通过本 README 提供分类导航。

主入口：[`gcc-llvm-defense-invariant-source-survey.md`](./gcc-llvm-defense-invariant-source-survey.md) —— 跨机制总表、信息源分类、给 DeFuzz 的抽样建议。

## 1. 按机制分类索引

### 栈相关 (Stack Protection / Hardening)

| 机制 | Survey | Oracle 实现状态 |
| --- | --- | --- |
| Stack canary (SP) | [`stack-canary.md`](./stack-canary.md) | ✅ `CanaryOracle` (4 个 checker, 见 `features/canary-oracle.md`) |
| `_FORTIFY_SOURCE` (FORT) | [`fortify-source.md`](./fortify-source.md) | ⚠ DEPRECATED (`_archive/oracles/fortify-oracle.md`) |
| `-fstack-clash-protection` (SCP) | [`stack-clash-protection.md`](./stack-clash-protection.md) | ❌ 未实现 |
| `-fstack-check` (SCK) | [`stack-check.md`](./stack-check.md) | ❌ 未实现 |
| SafeStack (SS) | [`safestack.md`](./safestack.md) | ❌ 未实现 |
| ShadowCallStack (SCS, AArch64 `x18` / RISC-V `gp`) | [`shadow-call-stack.md`](./shadow-call-stack.md) | ❌ 未实现 |
| Shadow Stack (Intel CET-SHSTK / x86) | [`shadow-stack.md`](./shadow-stack.md) | ❌ 未实现 |
| GCS (AArch64 Guarded Control Stack) | [`gcs.md`](./gcs.md) | ❌ 未实现 |

### 控制流完整性 (CFI / IBT / PAC)

| 机制 | Survey | Oracle 实现状态 |
| --- | --- | --- |
| Clang CFI | [`cfi.md`](./cfi.md) | ❌ 未实现 |
| Kernel CFI (`-fsanitize=kcfi`) | [`kcfi.md`](./kcfi.md) | ❌ 未实现 |
| Hardened CFR (`-fharden-control-flow-redundancy`) | [`hcfr.md`](./hcfr.md) | ❌ 未实现 |
| Intel CET-IBT (`endbr*`) | [`endbr-ibt.md`](./endbr-ibt.md) | ❌ 未实现 |
| AArch64 BTI | [`bti.md`](./bti.md) | ❌ 未实现 |
| AArch64 PAC | [`pointer-authentication.md`](./pointer-authentication.md) | ❌ 未实现 |
| RISC-V CFI (Zicfilp/Zicfiss) | [`riscv-cfi.md`](./riscv-cfi.md) | ❌ 未实现 |

### 内存安全与边界

| 机制 | Survey | Oracle 实现状态 |
| --- | --- | --- |
| Bounds Safety (`__counted_by`, ISO N2778) | [`bounds-safety.md`](./bounds-safety.md) | ❌ 未实现 |
| Sanitizers (ASan/HWASan/MSan/TSan/UBSan/DFSan) | [`sanitizers.md`](./sanitizers.md) | ❌ 未实现 |
| SanitizerCoverage | [`sancov.md`](./sancov.md) | ❌ 未实现 |
| Structure Protection (vtable / typed alloc) | [`structure-protection.md`](./structure-protection.md) | ❌ 未实现 |

### 编译器代码硬化

| 机制 | Survey | Oracle 实现状态 |
| --- | --- | --- |
| `-fhardened` (HARD, 元 flag) | [`hardened.md`](./hardened.md) | ❌ 未实现 |
| `-fzero-call-used-regs` (ZCUR) | [`zero-call-used-regs.md`](./zero-call-used-regs.md) | ❌ 未实现 |
| `-fstrub` (STRUB, 栈擦除) | [`strub.md`](./strub.md) | ❌ 未实现 |
| `-ftrivial-auto-var-init` (AVI) | [`auto-var-init.md`](./auto-var-init.md) | ❌ 未实现 |

## 2. 已实现 invariant ↔ checker 对照（仅 canary）

| Invariant ID | Survey 行 | Checker 实现 | 文档 |
| --- | --- | --- | --- |
| `INV-SP-G01` | `stack-canary.md` § Global symbol | `internal/oracle/checker_static_canary.go:30-86` `StackChkSymbolsChecker` | [`features/canary-oracle.md`](../features/canary-oracle.md) |
| `INV-SP-A01` | `stack-canary.md` § main no-canary | `internal/oracle/checker_static_canary.go:118-191` `MainNoCanaryChecker` | 同上 |
| `INV-SP-L01` | `stack-canary.md` § runtime-bypass | `internal/oracle/checker_dynamic_buffer.go` `DynamicBufferSearchChecker` | 同上 |
| `INV-SP-R03` | `stack-canary.md` § epilogue scrub | `internal/oracle/checker_dynamic_scrub.go` `EpilogueCanaryScrubChecker` | 同上 |

其它机制的 invariant 已经在 survey 里枚举但**尚未**有对应 `InvariantChecker` 实现；扩展路径见 [`guides/adding-a-defense-mechanism.md`](../guides/adding-a-defense-mechanism.md)。

## 3. Survey 字段约定（不强制 frontmatter）

各 survey 文件在表格里采用以下约定列（出处：`gcc-llvm-defense-invariant-source-survey.md` § "每条 invariant 推荐保存的字段"）：

| 字段 | 含义 |
| --- | --- |
| `compiler` | GCC / LLVM / 共用 ABI |
| `version` | 例：GCC 17.x trunk |
| `mechanism` | SP / FORT / SCP / SCK / CET / BTI / PAC / SCS / CFI / KCFI / SS / ASan / ... |
| `statement` | invariant 的可执行陈述 |
| `source_url_or_path` | 一手证据链接（GCC mirror / LLVM source / RFC / Bugzilla / CVE） |
| `version_sensitivity` | stable / target-specific / likely-to-drift |
| `oracle_mapping` | 在 DeFuzz 里若违反，应被哪个 oracle 抓到 |

**给 oracle 实现者的反链**：每个 `InvariantResult.SourceURL` 字段应当指向 survey 文件中对应行的 `source_url_or_path`，让 bug 报告一路追溯回一手证据。

## 4. 可程序化筛选方法论

> 本节是项目级公共规范, 各机制的 invariant 文档 (如 [`stack-canary.md`](./stack-canary.md) 的「可程序化 invariants」章) 复用同一套标准, 只在该机制内列出筛选结果, 不再重复方法论本身.

### 4.1 筛选目的

invariant 文档面向"研究档案", 力求覆盖一手信息源里**所有**与机制相关的不变量; 但 oracle 实现面向"工程交付", 资源有限. 两者目标不同, 不能一一映射:

- 部分不变量描述的是**编译器内部状态** (例如启发式分类位、冲突图), 在二进制层面没有可观测痕迹;
- 部分不变量是**配置级前提**或**链接器契约**, 违反它会直接编译/链接失败, 不需要 oracle;
- 部分不变量是**约束作者的最佳实践** (hardening-ideal), 而非可被攻击者绕过的硬安全契约, 易产生大量 false-positive.

因此先做"可程序化筛选", 把研究档案里的 invariants 切成两类: **应当实现 checker 的** 与 **应当显式排除的**, 让后续工程投入有依据.

### 4.2 评估维度

每条 invariant 在筛选阶段按以下四个维度评估. 维度是**判定工具**, 不是评分卡; 任一维度严重不达标即可作为排除理由.

| 维度 | 含义 | 典型不达标信号 |
| --- | --- | --- |
| **可观测性 (Observability)** | 验证该不变量所需的信号能否通过现有 oracle 基础设施获取 — `BinaryInspector` (ELF 符号 / 节 / 反汇编)、`Executor` (退出码 / stdout / stderr)、seed 模板插桩、ISA-aware 反汇编 | 信号只存在于编译器中间表示 (GIMPLE / LLVM IR), 在二进制中无残留; 或需要 cross-arch 反汇编但 Capstone / `golang.org/x/arch` 覆盖不全 |
| **判定确定性 (Decisiveness)** | 是否存在明确的真值条件, 能避免启发式误报. "条件 X 成立 ⇒ 必定违反"或"条件 X 成立 ⇒ 必定满足" | 触发条件依赖编译器版本特定启发式微调 (likely-to-drift 中的 *impl-level* drift); 或需要数值阈值且阈值因优化等级而变 |
| **实现成本 (Cost)** | 能否复用现有 checker 框架 (静态: `BinaryInspector`; 动态: `Executor` + 退出码协议; seed 模板的 sentinel / scrub 通道) | 必须新写一个跨 ISA 的反汇编器; 或需要在 seed 模板里维护逐 ISA 的 inline asm; 或需要构造对照编译器版本 (历史 GCC ≤13.2) 才能验证 |
| **静态 vs 动态归属** | 见 §4.3 的判定准则, 决定 checker 进入哪一调度阶段 | — (此维度不是排除维度, 而是分类维度) |

> 维度间不正交但**够用**: 实现成本与可观测性高度相关 (反汇编缺失既是观测障碍也是成本); 但显式拆开能让排除理由更精准.

### 4.3 静态 / 动态归属判定准则

与 [`docs/story_line.md`](../../story_line.md) §4 "可由机器验证的静态属性 (看汇编/二进制特征) 与动态属性 (看运行时行为)" 一致, 也与 `internal/oracle/invariant.go` 中的 `CategoryStatic` / `CategoryDynamic` 一一对应.

判定步骤:

1. **若违反/满足条件可以仅通过检查未执行的二进制 (符号表 / 节内容 / 反汇编 / 字符串) 得出**, 归 **静态 (Static)**. 典型: `__stack_chk_fail` 是否被链入、`endbr64` 是否落在函数首字节.
2. **若必须运行二进制**, 通过观察退出码、stdout 标志位、二分搜索行为、运行时寄存器/内存快照得出, 归 **动态 (Dynamic)**. 典型: 越界写入是否被 canary 拦截、guard 残留是否泄露.
3. 同一条 invariant 可能"静态部分可观, 动态部分更准"; 此时拆成两个 checker 分别归属, 而不是模糊归一类.
4. "机制未启用 / 不适用"**不是**第三类, 应在对应 checker 内返回 `VerdictNotApplicable`, 由聚合器忽略.

### 4.4 通用记录格式约定

各机制的 invariant 文档应在自己的「## 可程序化 invariants」章下**只**列出筛选结果, 格式如下 (字段紧凑, 不重复方法论):

#### 通过筛选

```
- INV-XX-Y0N — <一句话简述>
  - 类别: 静态 / 动态
  - 通过理由: <一两句话, 引用本节维度>
```

#### 未通过筛选

```
- INV-XX-Y0N — <一句话简述>
  - 排除理由: <一两句话, 必须显式指向本节某一维度, 例如「可观测性不足: 信号仅存在于 GCC GIMPLE 层」>
```

要求:

- **不在该章重复方法论**, 通过相对链接回引本节: `参见 [筛选方法论](../README.md#4-可程序化筛选方法论)`.
- **不写实现细节**, 实现细节归 `features/<mechanism>-oracle.md`.
- **不写优先级**, 优先级是工程节奏问题, 与筛选无关; 若需汇报留在 PR / 沟通渠道.
- 排除理由必须**精确指向四维度之一**, 模糊表述 (如"实现复杂") 应被替换为具体维度 ("实现成本: 需要为 7 种长尾 ISA 各写反汇编器").

## 5. 写新 survey 的流程

1. 复制一份现有 survey 当模板（推荐 `stack-canary.md`，结构最齐全）。
2. 替换机制简写、给所有 invariant 新 ID（命名 `INV-<MECH>-<CATEGORY><NN>`，例如 `INV-FORT-L01`）。
3. 在本 README 的"按机制分类索引"表里追加一行；标记 oracle 实现状态为"❌ 未实现"。
4. （可选）在 `gcc-llvm-defense-invariant-source-survey.md` 末尾"已知锚点"表里追加几条标志性 invariant，用来交叉校验。

实现 oracle 的步骤独立进行：[`guides/adding-a-defense-mechanism.md`](../guides/adding-a-defense-mechanism.md)。
