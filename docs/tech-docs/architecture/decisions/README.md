---
title: Architecture Decision Records (ADR Log)
description: DeFuzz 架构决策记录索引
priority: MEDIUM
last_updated: 2026-05-08
status: IMPLEMENTED
---

# Architecture Decision Records

按编号顺序记录 DeFuzz 的关键架构决策。每条 ADR 都用 [Michael Nygard 风格](https://github.com/joelparkerhenderson/architecture-decision-record/tree/main/locales/en/templates/decision-record-template-by-michael-nygard) 的精简变体：**Status / Context / Decision / Consequences**。

## 索引

| ADR | Status | 主题 | 关键代码位置 |
| --- | --- | --- | --- |
| 001 | (隐式 — 见 README) | LLM-driven constraint solving as the fuzzing engine | `internal/fuzz/engine.go`, `internal/llm/` |
| [002](./002-multi-cfg-orchestration.md) | Superseded | Multi-CFG orchestration（PR `b907c01` 评估） | `cmd/defuzz/app/fuzz.go:330-387`, `internal/coverage/analyzer.go` |
| [003](./003-oracle-multi-invariant-redesign.md) | Accepted, Implemented | Oracle multi-invariant redesign | `internal/oracle/{mechanism,invariant,inspector}.go`, `checker_*.go` |

## 编号规则

- 编号自 002 开始，因为 001 是项目本身的奠基设计，已散落在 `README.md` 与 `architecture/overview.md` 里，没有单独 ADR。
- 新增 ADR 取下一个未用编号，命名 `NNN-<short-slug>.md`。
- 状态约定：`Proposed` / `Accepted` / `Accepted, Implemented` / `Superseded` / `Deprecated`。被 supersede 时在新 ADR 里反链旧 ADR、不删除旧文件。

## 何时写 ADR

满足任一即写：

- 改动跨 ≥ 2 个 internal/ 包，且影响数据流；
- 引入或废弃一个核心抽象（接口、协议、配置维度）；
- 决定与一个外部依赖绑定（语言版本、第三方服务）；
- 暂停某条已落地特性、保留代码不删（如 fortify oracle 的归档处理）。

## 模板

```md
---
title: "ADR-NNN: <short title>"
description: <one sentence>
priority: HIGH | MEDIUM
last_updated: YYYY-MM-DD
status: Proposed | Accepted | Accepted, Implemented | Superseded | Deprecated
related_docs:
  - <relative path>
---

# ADR-NNN: <short title>

## Status
<status>

## Context
<触发该决策的背景与约束>

## Decision
<采取的方案>

## Consequences
<正面 + 负面后果，包括需要 follow-up 的事>

## Implementation Pointers (Implemented 状态时填)
- `internal/<pkg>/<file>.go:<line-range>` — <要点>
```
