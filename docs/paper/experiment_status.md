---
title: DeFuzz 论文实验结构与进度
description: 按 DeFuzz 主流程组织的三个主实验 Part 与独立消融实验
last_updated: 2026-08-26
status: DRAFT
---

# DeFuzz 论文实验结构与进度

## 总体结构

论文实验按照 DeFuzz 的主流程组织，而不是按照结果类型拆分：

```text
Part I                     Part II                  Part III
不变量生成                 Checker 编写             Agent 审计
Segmented CoT + RAG  --->  Invariant -> Checker  --->  Audit -> Finding
```

因此，最终实验分为：

1. **主实验**：包含三个前后衔接的 Part。
2. **消融实验**：独立成节，其中必做对照是 **完整 DeFuzz vs. 裸 Agent**。

Experimental Setup 是所有实验共享的设置，不单独算一个实验。真实缺陷数、机制/ISA 分布和上游确认情况是 Part III 的结果，不再单独划为主实验 Part。

本期统一实验入口为 [`defuzz-experiment`](../../orchestrator/defuzz_loop/experiments_cli.py)：

```text
defuzz-experiment
├── invariant-generation
├── checker-authoring
├── agent-audit
└── ablation
    ├── without-rag
    ├── without-oracle
    └── bare-agent
```

每一级命令均提供独立的 `--help`。统一入口现已接线到 Part I、Part II 和 Part III 的 stage runner；`without-rag` 调度 Part I 的 Segmented CoT-only 路径，`without-oracle` 与 `bare-agent` 调度 Part III 的对应策略。每个 repetition 都生成独立 artifact 目录、stage result、Token 明细/汇总和 manifest，顶层 manifest 汇总全部 repetition 的最终状态。`--show-plan` 保持无副作用，并显示所选 agent binary 在当前环境是否可用。

接线完成不等于正式实验已经运行。真实执行仍依赖当前环境中可用的 `traex`/`codex` 二进制、模型凭据、reference/source tree 和编译器工具链；本轮验证仅使用 fake backend/stage，不调用昂贵模型。
默认 reference root 是 `/Users/bytedance/projects/research/defend-reviewer/main`，也可通过 `DEFUZZ_REFERENCE_ROOT` 或 `--reference-root` 覆盖。CLI 会在创建 run 或启动 Agent 前执行输入检查：Part I 要求显式提供且存在的 `--corpus-root`，Part II 要求 `--inputs`/`--from-run`，Part III 要求 `--target-tree` 与完整 reference docs；配置错误返回 exit code 2。
同一 `run-id` 默认拒绝覆盖；`--resume` 会比对冻结的输入快照并跳过已成功 repetition。`--from-run` 会核验上游 manifest、stage result 与 artifact SHA-256。Part III 先复制 sanitized、只读源码 workspace；在支持的 macOS 主机上，真实外部 backend 还通过 OS sandbox 拒绝读取 reference checkout。demo `findings/` 只在 worker 退出后的 `--demo-parity` 阶段由 orchestrator 读取。
正式实验不得设置 `DEFUZZ_FAST_PLAN=1`；该开关仅用于对超大未跟踪源码树快速查看 plan，会跳过递归输入快照。

## 正式实验前的代码前置：统一 Token 统计

统一 Token 统计模块 [`token_usage.py`](../../orchestrator/defuzz_loop/token_usage.py) 已接入统一实验入口：每次 repetition 使用独立 sink，内部 LLM 调用通过 ambient context 记录，外部 agent backend 的 usage 也写入同一份 run 级统计。三个主实验 Part 和三组消融因此可从同一统计入口取数，进行等预算对比。

统计范围包括：

- **Part I**：Segmented CoT 和 RAG 中的 distillation、analogy、specialization、entailment 等模型调用；embedding 请求单独统计请求次数与输入规模，不混入 chat token。
- **Part II**：Checker 生成、修复和测试反馈过程中发生的全部模型调用。
- **Part III**：Agent 审计、反馈和 PoC 最小化过程中的全部模型调用。
- **消融实验**：完整 DeFuzz、w/o RAG、w/o Oracle 和裸 Agent 使用同一统计口径。

每次调用至少记录：

| 字段 | 含义 |
| --- | --- |
| `schema_version` / `call_id` / `timestamp` | 记录格式版本、唯一调用 ID 与 UTC 时间 |
| `run_id` / `experiment` / `variant` | 所属实验、重复轮次与消融组 |
| `part` / `stage` / `agent` | Part I/II/III，以及具体阶段或 Agent |
| `provider` / `model` | 实际模型配置 |
| `input_tokens` / `output_tokens` / `total_tokens` | 单次调用用量 |
| `cached_input_tokens` / `cache_creation_input_tokens` / `reasoning_tokens` | Provider 返回时记录，否则为空 |
| `latency_ms` / `success` / `error_type` | 延迟、成功状态与失败类型 |
| `estimated_cost` | 调用方按冻结价格表提供的可选估算成本；Token 原始值始终保留 |

模块支持逐调用 JSONL，以及按 `run × part × stage × model` 聚合的 JSON/CSV；统一 CLI 会把汇总路径写入每次 repetition 的 manifest。若 Provider 不返回 usage，该次调用必须标记为 `usage_missing`，不能静默记为 0。
如果 Provider 已返回响应、但结构化解析或 schema 校验失败，该次调用仍按 raw response 中的实际 Token 计费，并记录 `success=false`；只有在收到响应前失败时 usage 才保持缺失。缺失 usage、没有 usage 记录或最终预算超限都会使 repetition 标记为不可比较并失败，避免失败重试绕过预算。

## 主实验

| Part | 输入 | 实验要回答的问题 | 实验内容 | 主要输出与指标 | 当前进度 |
| --- | --- | --- | --- | --- | --- |
| **Part I：不变量生成实验** | 编译器源码、规格与 ABI 文档、历史漏洞及补丁 | DeFuzz 能否从大规模语料中生成正确、有安全意义、可证伪且不重复的安全不变量？Segmented CoT 与 RAG 是否互补？ | 运行两条互补路径：① Segmented CoT 对完整语料分段审阅，保证广度；② RAG 以历史漏洞根因为 probe，检索并迁移高价值同构模式。两路候选进入相同的 grounding 与 novelty 过滤。 | CoT/RAG 各自产出数、交集与增量；候选接受率；专家判定的有效率与 Cohen's kappa；novelty；每条有效不变量的 Token、时间和人工成本。 | **部分完成。** RAG 已有 24 probes、4,496 个 GCC 16.1 chunks，BM25 9 条、dense 7 条、去重并集 11 条的历史结果；Segmented CoT 的统一结果表、两路最终合并结果和专家盲评尚未完成。 |
| **Part II：Checker 编写实验** | Part I 通过验证的不变量，以及对应 statement、observation、evidence、target/ISA 信息 | 生成的不变量能否稳定转化为可执行、可复用且判定准确的 static/dynamic checker？编写 checker 需要多少自动化与人工修正？ | 逐条将 accepted invariant 转为 checker，并补齐注册信息、ISA metadata、正例、负例和 vulnerable/fixed 回归样本；在统一 checker contract 下执行编译、测试和语义验证。 | checker 转换成功率；一次编译/测试通过率；最终通过率；static/dynamic 分布；TP/FN/FP/TN 与 Pass/Fail/NA/Error 分布；人工修改次数与时间；每个 checker 的 Token/运行成本。 | **实现素材已有，正式实验未做。** 当前 Canary、IBT、FORTIFY 已有 28 个 checker metadata 和较多测试，但没有记录“哪个 invariant 如何生成 checker”、一次通过率、人工修改量和统一准确率结果。 |
| **Part III：Agent 审计实验** | 目标编译器与防御机制、Part I 的不变量、Part II 的 executable checkers | 在不变量与 checker 的指导下，Agent 能否系统审计编译器防御并发现真实静默失效？ | Agent 按机制与 ISA 审计目标源码、构造或迭代触发样例；checker 提供确定性裁决；对 Fail 结果进行最小化、去重、人工复核和上游报告。 | 审计的 compiler/mechanism/ISA 覆盖；候选数；checker-confirmed 数；PoC verified 数；去重后的真实缺陷数；假阳性率；上游 reported/confirmed/fixed/CVE 数；time-to-first-finding、Token 与总审计成本。 | **已有 finding 与复现素材，最终统计未冻结。** DREV corpus、独立 repro 和部分上游材料已存在，但还需统一“审计 run -> checker 证据 -> PoC -> finding -> upstream 状态”的 provenance，才能形成论文结果。 |

三个 Part 必须按同一条证据链衔接：Part I 的输出是 accepted invariants；Part II 只统计由这些 invariants 转换并验证的 checkers；Part III 的五项 gate 仅表示候选结构完整。最终 finding 还必须通过独立的源码 excerpt 核验和由 orchestrator 冻结命令驱动的离线 checker/PoC 验证；Agent 自报的 `poc_verified` 不构成确认。

## 独立消融实验

消融只保留以下三组，不再扩展其他开关：

| 变体 | 相对完整 DeFuzz 移除什么 | 主要验证结论 |
| --- | --- | --- |
| **w/o RAG** | Part I 不运行历史漏洞驱动的 RAG 路径，只保留 Segmented CoT 生成的不变量；后续 checker 编写与 Agent 审计流程不变。 | RAG 是否带来 CoT 无法覆盖的有效不变量和最终 finding。 |
| **w/o Oracle（Checker）** | Part III 仍使用相同 invariants 和审计流程，但不向 Agent 提供专用 checker 的在线确定性反馈；Agent 仅输出候选。实验结束后，再由隔离的统一验证流程离线复核两组候选。 | Checker 是否降低假阳性、减少无效探索并提高有效 finding 的产出效率。 |
| **裸 Agent** | 不提供预生成 invariants、RAG 结果、专用 checkers 或 DeFuzz 结构化审计流程；Agent 使用通用工具自由审计。 | 完整 DeFuzz 相比直接使用通用 Agent 的总体收益。 |

公平性要求：

- Full、w/o RAG、w/o Oracle 和裸 Agent 使用相同的 compiler 版本、机制/ISA 范围、模型与模型参数。
- 各组使用相同的 Token、wall-clock 和并发预算；预算由统一 Token 统计模块执行和核验。
- 三个消融组都可以自由提出候选，但最终结果必须经过同一套隔离的准入标准和确定性复现，不能直接采用 Agent 的自报结论。
- 报告去重后的有效 finding 数、候选到有效 finding 的转化率、假阳性率、PoC verified 数、time-to-first-finding、Token 和总耗时。

当前状态：**代码入口与变体策略已实现，正式实验尚未运行。** `w/o RAG` 已固定为 Segmented CoT-only；Full Part III 要求配置 candidate-bound `--online-oracle-command` 并运行“候选 → checker → 反馈 → 复审”回合，缺少命令或任一 Oracle 调用返回 `ERROR` 时整个 repetition fail closed；`w/o Oracle` 完全跳过该在线反馈回路。裸 Agent 已改为单个中性审计 worker，不接收 DeFuzz doctrine、机制分片、历史 RAG、不变量或专用 checker。所有消融必须提供 `--baseline-run`，冻结完整组的 backend、模型、预算、源码、机制/ISA、版本、并发与离线验证命令。

## 当前进度

| 实验 | 状态 | 下一关键动作 |
| --- | --- | --- |
| 共同前置：统一 CLI 与 Token 统计 | 已接线，待真实环境运行 | 在具备 agent binary、模型凭据、source/reference tree 与编译工具链的环境中冻结配置并执行正式重复实验。 |
| Part I：不变量生成 | 部分完成 | 汇总 Segmented CoT 结果，与 RAG 合并后进行专家盲评。 |
| Part II：Checker 编写 | Runner 已实现，正式实验未运行 | 用 Part I 的冻结输出运行独立 workspace authoring，采集一次通过率、最终通过率与人工复核成本。 |
| Part III：Agent 审计 | Runner、结构准入、在线 Oracle 闭环与 demo parity 已实现 | 在隔离实验机上验证 OS 级文件读取隔离，配置真实 candidate-bound checker/PoC 命令并执行正式重复实验。 |
| 消融：w/o RAG、w/o Oracle、裸 Agent | 三个入口与策略已实现，正式实验未运行 | 绑定同一 full-arm baseline 后执行重复实验并汇总置信区间。 |
