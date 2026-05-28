---
title: Invariants Survey Index
description: 编译器与运行时防御机制的不变量调研档案 —— 分类索引与筛选方法论
last_updated: 2026-05-28
---

# Invariants Survey Index

本目录是一份纯**研究档案**: 把 GCC / LLVM / glibc / 各 ISA ABI 文档中关于防御机制的"安全不变量"按机制系统抽样, 形成 oracle 设计的形式化依据.

> "不变量"指的是: 防御机制要发挥作用所**必须**满足的属性. 违反它意味着该机制在编译产物或运行时已被静默削弱, 无论代码本身有没有 bug.

每份 markdown 是一个独立的机制 survey, 引用一手资料 (源码 / 手册 / Bugzilla / CVE / paper), 不假设任何下游消费者。下游 (oracle、静态分析、CI) 决定如何用这些不变量, 是另一个层面的事.

主入口: [`gcc-llvm-defense-invariant-source-survey.md`](./gcc-llvm-defense-invariant-source-survey.md) —— 跨机制总表与一手信息源分类.

## 1. 按机制分类索引

### 栈相关 (Stack Protection / Hardening)

| 机制 | Survey |
| --- | --- |
| Stack canary (SP) | [`stack-canary.md`](./stack-canary.md) |
| `_FORTIFY_SOURCE` (FORT) | [`fortify-source.md`](./fortify-source.md) |
| `-fstack-clash-protection` (SCP) | [`stack-clash-protection.md`](./stack-clash-protection.md) |
| `-fstack-check` (SCK) | [`stack-check.md`](./stack-check.md) |
| SafeStack (SS) | [`safestack.md`](./safestack.md) |
| ShadowCallStack (SCS, AArch64 `x18` / RISC-V `gp`) | [`shadow-call-stack.md`](./shadow-call-stack.md) |
| Shadow Stack (Intel CET-SHSTK / x86) | [`shadow-stack.md`](./shadow-stack.md) |
| GCS (AArch64 Guarded Control Stack) | [`gcs.md`](./gcs.md) |

### 控制流完整性 (CFI / IBT / PAC)

| 机制 | Survey |
| --- | --- |
| Clang CFI | [`cfi.md`](./cfi.md) |
| Kernel CFI (`-fsanitize=kcfi`) | [`kcfi.md`](./kcfi.md) |
| Hardened CFR (`-fharden-control-flow-redundancy`) | [`hcfr.md`](./hcfr.md) |
| Intel CET-IBT (`endbr*`) | [`endbr-ibt.md`](./endbr-ibt.md) |
| AArch64 BTI | [`bti.md`](./bti.md) |
| AArch64 PAC | [`pointer-authentication.md`](./pointer-authentication.md) |
| RISC-V CFI (Zicfilp/Zicfiss) | [`riscv-cfi.md`](./riscv-cfi.md) |

### 内存安全与边界

| 机制 | Survey |
| --- | --- |
| Bounds Safety (`__counted_by`, ISO N2778) | [`bounds-safety.md`](./bounds-safety.md) |
| Sanitizers (ASan/HWASan/MSan/TSan/UBSan/DFSan) | [`sanitizers.md`](./sanitizers.md) |
| SanitizerCoverage | [`sancov.md`](./sancov.md) |
| Structure Protection (vtable / typed alloc) | [`structure-protection.md`](./structure-protection.md) |

### 编译器代码硬化

| 机制 | Survey |
| --- | --- |
| `-fhardened` (HARD, 元 flag) | [`hardened.md`](./hardened.md) |
| `-fzero-call-used-regs` (ZCUR) | [`zero-call-used-regs.md`](./zero-call-used-regs.md) |
| `-fstrub` (STRUB, 栈擦除) | [`strub.md`](./strub.md) |
| `-ftrivial-auto-var-init` (AVI) | [`auto-var-init.md`](./auto-var-init.md) |

## 2. Survey 字段约定

每条不变量按下表字段记录, 字段集来自 [`gcc-llvm-defense-invariant-source-survey.md`](./gcc-llvm-defense-invariant-source-survey.md):

| 字段 | 含义 |
| --- | --- |
| `ID` | 机制内唯一标识, 命名 `INV-<MECH>-<CATEGORY><NN>` (如 `INV-SP-L01`) |
| `statement` | 不变量的可机器验证陈述 |
| `compiler` | GCC / LLVM / 共用 ABI |
| `version` | 适用编译器版本范围 |
| `target` | 适用 ISA / OS |
| `source_kind` | user-doc / source / internals / ABI-spec / runtime / bug-disclosure / paper |
| `source_url_or_path` | 一手证据链接 |
| `evidence_snippet` | 摘录的证据原文 (可选) |
| `version_sensitivity` | stable / target-specific / likely-to-drift |
| `observation` | 违反时**可被外部观测的现象** (二进制特征 / 运行时行为). 是后续设计 checker 的入口, 但**不写具体实现** |

> `observation` 字段刻意只描述"现象", 不描述"如何检测". 例如: "二进制中 `__stack_chk_fail` 符号缺失" 是现象; "在 ELF `.dynsym` 中查 `__stack_chk_fail`" 是检测手段, 后者应留给 oracle 实现文档.

## 3. 可程序化筛选方法论

> 本节是项目级公共规范. 每份 survey 在自己的「## 可程序化 invariants」章下复用同一套标准, 不再重复方法论.

### 3.1 为何要筛选

研究档案追求**完备覆盖**: 一手资料里能找到的不变量都列上; 但程序化检测的资源有限, 也并非所有不变量都值得做成自动化 checker. 两者目标不同, 不强求一一映射.

下面三类不变量通常**不适合**做成 checker:

- **编译器内部状态**: 例如 GCC `cfgexpand.cc` 的启发式分类位、SSA 上的冲突图, 在编译产物里没有可观测痕迹.
- **配置级前提 / 链接器契约**: 例如缺失 `__stack_chk_fail_local` 直接导致链接失败, 编译流水线已抓, 不是 oracle 的"静默失效"领域.
- **作者侧最佳实践 (hardening-ideal)**: 例如"参数副本应优先放在 callee-saved 寄存器", 文档原文都标注是 ideal 而非硬契约, 自动化判定会引入大量误报.

筛选就是把研究档案里的不变量切成"应当做 checker"和"应当显式排除"两类, 让后续工程投入有依据.

### 3.2 评估维度

每条不变量按下面四个维度评估. 维度是**判定工具**, 不是评分卡; 任一维度严重不达标即可作为排除理由.

| 维度 | 含义 |
| --- | --- |
| **可观测性 (Observability)** | 验证违反/满足该不变量所需的信号, 能否从编译产物或运行时行为中获取. 不达标的典型: 信号只存在于编译器中间表示 (GIMPLE / LLVM IR), 二进制里完全消失. |
| **判定确定性 (Decisiveness)** | 是否存在明确的真值条件 ("条件 X ⇒ 必定违反"或"条件 X ⇒ 必定满足"), 避免启发式误报. 不达标的典型: 触发条件依赖编译器版本特定启发式, 或阈值随优化等级飘移. |
| **实现成本 (Cost)** | 验证该不变量需要多少额外基础设施. 不达标的典型: 必须新写跨 ISA 的反汇编器、多版本编译器对照构建、复杂源代码插桩. |
| **静态 vs 动态归属** | 见 §3.3, 决定 checker 在哪个阶段运行. 这是分类维度, 不是排除维度. |

> 维度间不正交但**够用**: 实现成本与可观测性相关, 但显式拆开能让排除理由更精准.

### 3.3 静态 / 动态归属判定

对应 [`docs/story_line.md`](../../story_line.md) §4 "可由机器验证的静态属性 (看汇编/二进制特征) 与动态属性 (看运行时行为)" 的二分法:

1. **若违反/满足条件可以仅通过检查未执行的二进制 (符号表、节内容、反汇编、字符串) 得出**, 归 **静态**. 例: `__stack_chk_fail` 符号是否被链入、`endbr64` 是否落在间接调用目标的首字节.
2. **若必须运行二进制**, 通过观察退出状态、输出标志、二分搜索行为、运行时寄存器/内存快照得出, 归 **动态**. 例: 越界写入是否被 canary 拦截、guard 残留是否泄露到调用者寄存器.
3. 同一条不变量可能"静态部分可观, 动态部分更准": 此时拆成两条不同归属的 checker 比模糊归一类好.
4. "机制未启用 / 不适用"**不是**第三类, 而应在 checker 内表达为"不适用"verdict, 由聚合层忽略.

### 3.4 通用记录格式

每份 survey 在自己的「## 可程序化 invariants」章下**只**列筛选结果, 格式如下:

#### 通过筛选

```
- INV-XX-Y0N — <一句话简述>
  - 类别: 静态 / 动态
  - 通过理由: <一两句话, 说明四个维度上为什么 OK>
```

#### 未通过筛选

```
- INV-XX-Y0N — <一句话简述>
  - 排除理由: <一两句话, 必须显式指向四维度之一, 例如「可观测性不足: 信号只存在于 cfgexpand.cc 的编译期分类位」>
```

要求:

- 不在该章重复方法论, 通过相对链接回引本节: `参见 [筛选方法论](./README.md#3-可程序化筛选方法论)`.
- 不写实现细节; 实现细节归 oracle 实现文档.
- 不写优先级; 优先级是工程节奏问题, 与不变量是否可观测无关.
- 排除理由必须**精确指向某一维度**, 模糊表述 (如"实现复杂") 应被替换为具体维度 ("实现成本: 需要为 7 种长尾 ISA 各写反汇编器").

## 4. 写新 survey 的流程

1. 复制一份现有 survey 当模板 (推荐 [`stack-canary.md`](./stack-canary.md), 字段最齐全).
2. 替换机制简写、给所有不变量分配新 ID.
3. 在 §1 "按机制分类索引"表里追加一行.
4. (可选) 在 [`gcc-llvm-defense-invariant-source-survey.md`](./gcc-llvm-defense-invariant-source-survey.md) "已知锚点"表追加几条标志性不变量, 用来交叉校验.
