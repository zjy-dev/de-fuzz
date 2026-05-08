---
title: docs/ shell index
description: docs/ 顶层导航壳；技术文档主体在 docs/tech-docs/
priority: LOW
last_updated: 2026-05-08
status: IMPLEMENTED
---

# docs/

技术文档主体已迁移到 [`tech-docs/`](./tech-docs/README.md)（自 2026-05-08 起）。

> **快速跳转**：[`docs/tech-docs/README.md`](./tech-docs/README.md)。

## 旧路径 → 新路径映射

外部链接、博客文章、PR 描述里如果还引用了旧路径，按下表替换：

| 旧路径 (≤ 2026-05-08) | 新路径 |
| --- | --- |
| `docs/architecture/gcc-pipeline.md` | `docs/tech-docs/architecture/gcc-pipeline.md` |
| `docs/architecture/multi-cfg-evaluation.md` | `docs/tech-docs/architecture/decisions/002-multi-cfg-orchestration.md` |
| `docs/architecture/oracle-multi-invariant-redesign.md` | `docs/tech-docs/architecture/decisions/003-oracle-multi-invariant-redesign.md` |
| `docs/oracles/canary-oracle.md` | `docs/tech-docs/features/canary-oracle.md` |
| `docs/oracles/fortify-oracle.md` | `docs/tech-docs/_archive/oracles/fortify-oracle.md` (DEPRECATED) |
| `docs/cflags-configuration.md` | `docs/tech-docs/guides/cflags-configuration.md` |
| `docs/open-source-c-compilers.md` | `docs/tech-docs/reference/open-source-c-compilers.md` |
| `docs/prompt-architecture.md` | `docs/tech-docs/architecture/prompt-architecture.md` |
| `docs/invariants/<mechanism>.md` | `docs/tech-docs/invariants/<mechanism>.md` (内容不变) |
| `docs/gcc-instrumentation/` | `docs/tech-docs/gcc-instrumentation/` |

迁移原因与新结构说明：见 `tech-docs/README.md` 顶部"Recent Changes"。
