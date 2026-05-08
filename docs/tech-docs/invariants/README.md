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

## 4. 写新 survey 的流程

1. 复制一份现有 survey 当模板（推荐 `stack-canary.md`，结构最齐全）。
2. 替换机制简写、给所有 invariant 新 ID（命名 `INV-<MECH>-<CATEGORY><NN>`，例如 `INV-FORT-L01`）。
3. 在本 README 的"按机制分类索引"表里追加一行；标记 oracle 实现状态为"❌ 未实现"。
4. （可选）在 `gcc-llvm-defense-invariant-source-survey.md` 末尾"已知锚点"表里追加几条标志性 invariant，用来交叉校验。

实现 oracle 的步骤独立进行：[`guides/adding-a-defense-mechanism.md`](../guides/adding-a-defense-mechanism.md)。
