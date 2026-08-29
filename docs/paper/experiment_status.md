---
title: DeFuzz 论文实验结构与进度
description: 按 DeFuzz 主流程组织的三个主实验 Part 与独立消融实验
last_updated: 2026-08-29
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
├── pipeline
├── invariant-generation
├── checker-authoring
├── agent-audit
└── ablation
    ├── without-rag
    ├── without-oracle
    └── bare-agent
```

每一级命令均提供独立的 `--help`。`pipeline` 从一份 typed YAML 运行完整三阶段证据链，并在同一 campaign 中运行 `full`、`without-rag`、`without-oracle` 和 `bare-agent` 四个 variant。`without-rag` 仍完整运行 Part I -> Part II -> Part III，只把 Part I 固定为 Segmented CoT-only；后两项复用 Full 的冻结 Part I/II artifact，只改变 Part III 暴露给 worker 的信息。每个 repetition 都生成独立 artifact 目录、stage result、Token 明细/汇总和 manifest，顶层 manifest 汇总全部 lane 的最终状态。`--show-plan` 保持无副作用，并校验 HTTP 配置、凭据环境变量与正式输入。

工程闭环已经跑通，但这不等于正式论文实验结果已经产生。当前已有三层既有验证：无模型四臂 smoke 验证 pipeline 与 resume；跨语言 E2E 验证 Python orchestrator -> Part II bundle -> Go dispatcher -> Clang -> online feedback -> offline verification；有界真实 HTTP pilot 验证 DeFuzz 直连 Responses API、严格结构化输出、tool loop、grounding、checker authoring 与 Token 采集。正式重复实验仍需在冻结、干净的 compiler/reference checkout 与完整工具链上执行。
默认 reference root 是 `/Users/bytedance/projects/research/defend-reviewer/main`，也可通过 `DEFUZZ_REFERENCE_ROOT` 或 `--reference-root` 覆盖。CLI 会在创建 run 或启动 Agent 前执行输入检查：Part I 要求显式提供且存在的 `--corpus-root`，Part II 要求 `--inputs`/`--from-run`，Part III 要求 `--target-tree` 与完整 reference docs；配置错误返回 exit code 2。
同一 `run-id` 默认拒绝覆盖；`--resume` 会比对冻结的输入快照和跨阶段 hash chain，并跳过已成功 lane。`--from-run` 会核验上游 manifest、stage result、checker bundle 及其 artifact SHA-256。Part I/II/III 都把 demo `findings/` 置于强制 deny-read 边界：HTTP backend 只暴露受 cwd、deny-list 与 symlink 检查约束的文件工具，CLI backend 则使用宿主 sandbox；Part III 还先复制 sanitized、只读源码 workspace。demo `findings/` 只在 worker 退出后的 `--demo-parity` 阶段由 evaluator 读取。若 backend 无法提供该隔离能力，formal run 会 fail closed。
正式实验不得设置 `DEFUZZ_FAST_PLAN=1`；该开关仅用于对超大未跟踪源码树快速查看 plan，会跳过递归输入快照。

## Formal 与 Fixture / Pilot 的边界

- `configs/experiments/example.yaml` 仅用于 fixture smoke。它运行 `mode: fixture` 的内建无模型 runner，验证 typed YAML、三阶段 handoff、四臂 lane 编排、hash chain 与 resume。该路径不产生论文实验数据。
- `configs/experiments/formal.example.yaml` 是 formal campaign 模板。它显式固定 `mode: formal` 与四个 variant，并使用 `backend.kind: http`、`config_path` 指向本地 HTTP 配置。HTTP 配置固定模型、推理档位和凭据环境变量名；密钥值不得写入文件。clean Git 输入目录、完整 reference 文档和绝对 toolchain driver 路径不满足时会直接失败，不会静默退回 fixture。
- `configs/experiments/http-agent.example.yaml` 是无密钥的本地配置示例，当前固定 `coconut-gpt-5-6-terra-max` 与 `reasoning_effort: medium`。`base_url` 和配置路径可按环境替换。DeFuzz 自己调用 `/v1/responses`；OpenCode 配置只可参考 endpoint/model，不是 runner，也不进入实验依赖链。
- 当前 HTTP 证据仍是有界 pilot 与工程 E2E，只能表述为“直连 HTTP 工程链路已验证”，不能写成完整语料、正式 finding 统计或最终四臂对比结论。
- partial range / shard / `max_segments` 仅可用于 pilot；当前 formal YAML 只接受单个完整、未分片的 Part I corpus（`shard_count: 1`、`max_segments: null`）。在实现可验证的 shard-union manifest 之前，分布式分片不能写成正式 full-corpus 结果。
- `demo-parity` 是工程 parity / coverage 对照，不是正式论文结果；`poc-verified` profile 证据更强，但同样不等于正式主实验统计。

正式 compiler baseline 已更正为 GCC `17.0.0 experimental 20260531`，commit `f20bc4c2fe00928013c533e241b89ae3a6724ca1`。Part I corpus 与 Part III audit source 必须来自该冻结 checkout；当前历史 GCC 16.1 RAG 数字仅是先前探索记录，不属于这次正式 baseline。

## 正式实验前的代码前置：统一 Token 统计

统一 Token 统计模块 [`token_usage.py`](../../orchestrator/defuzz_loop/token_usage.py) 已接入统一实验入口：每次 repetition 使用独立 sink，内部 LLM 调用通过 ambient context 记录，HTTP backend 则对每个收到的 Responses 轮次记录 provider usage，并在 tool/schema-repair 轮次之间累计。三个主实验 Part 和四个 variant 因此可从同一统计入口取数，进行等预算对比。

统计范围包括：

- **Part I**：Segmented CoT 和 RAG 中的 distillation、analogy、specialization、entailment 等模型调用；embedding 请求单独统计请求次数与输入规模，不混入 chat token。
- **Part II**：Checker 生成、修复和测试反馈过程中发生的全部模型调用。
- **Part III**：Agent 审计、反馈和 PoC 最小化过程中的全部模型调用。
- **四个 variant**：完整 DeFuzz、w/o RAG、w/o Oracle 和裸 Agent 使用同一统计口径。

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
正式汇报时，Token 可比性必须按 repetition 判定，而不是按单个成功 finding 选取样本。只有 `usage_missing_count == 0` 且未超预算的 repetition 才能进入 Full / ablation 间的 Token 或成本比较。

## 主实验

| Part | 输入 | 实验要回答的问题 | 实验内容 | 主要输出与指标 | 当前进度 |
| --- | --- | --- | --- | --- | --- |
| **Part I：不变量生成实验** | 编译器源码、规格与 ABI 文档、历史漏洞及补丁 | DeFuzz 能否从大规模语料中生成正确、有安全意义、可证伪且不重复的安全不变量？Segmented CoT 与 RAG 是否互补？ | 运行两条互补路径：① Segmented CoT 对完整语料分段审阅，保证广度；② RAG 以历史漏洞根因为 probe，检索并迁移高价值同构模式。两路候选进入相同的 grounding 与 novelty 过滤。 | CoT/RAG 各自产出数、交集与增量；候选接受率；专家判定的有效率与 Cohen's kappa；novelty；每条有效不变量的 Token、时间和人工成本。 | **正式数据未运行。** 先前 GCC 16.1 RAG 探索产生过 24 probes、4,496 chunks、BM25 9 条、dense 7 条、去重并集 11 条，但不可作为 GCC 17 正式 baseline 的结果。当前需要先完成 HTTP pilot，再基于冻结 GCC 17 corpus 运行两路生成与专家盲评。 |
| **Part II：Checker 编写实验** | Part I 通过验证的不变量，以及对应 statement、observation、evidence、target/ISA 信息 | 生成的不变量能否稳定转化为可执行、可复用且判定准确的 static/dynamic checker？编写 checker 需要多少自动化与人工修正？ | 逐条在同一个累计 workspace 中将 accepted invariant 转为 checker；每项失败回滚，成功项保留；最后全树验证并构建 content-addressed checker bundle。 | checker 转换成功率；一次编译/测试通过率；最终通过率；static/dynamic 分布；TP/FN/FP/TN 与 Pass/Fail/NA/Error 分布；人工修改次数与时间；每个 checker 的 Token/运行成本。 | **工程实现完成，正式实验未运行。** 已输出 `results.jsonl`、累计 patch、runtime catalog、可执行 dispatcher 和 manifest，并校验文件 ownership、依赖闭包、tree hash 与 artifact hash。 |
| **Part III：Agent 审计实验** | 目标编译器与防御机制、Part I 的不变量、Part II 的 executable checkers | 在不变量与 checker 的指导下，Agent 能否系统审计编译器防御并发现真实静默失效？ | Agent 按机制与 ISA 审计目标源码、构造或迭代触发样例；Full 使用 checker 在线反馈；所有 variant 在 worker 结束后由同一冻结 dispatcher 独立验证。 | 审计的 compiler/mechanism/ISA 覆盖；候选数；checker-confirmed 数；PoC verified 数；去重后的真实缺陷数；假阳性率；上游 reported/confirmed/fixed/CVE 数；time-to-first-finding、Token 与总审计成本。 | **链路已在真实 GCC17 上端到端验证，产出 1 个确定性 verified finding；正式多重复 campaign 统计未完成。** 真实模型 Agent（`coconut-gpt-5-6-terra-max`，reasoning medium）在冻结 GCC 17.0.0-experimental 20260531 上独立提出 IBT immediate-masking 缺陷候选；冻结 dispatcher 的独立 canonical checker `INV-IBT-B01` 确定性复现 `FAIL`（`f+0x8@0x8[.text]`），在线引导 checker `INVGEN-…` 与最终 checker 分离。详见下方“GCC17 端到端 verified finding”。该结果证明真实后端链路可发现真实缺陷，但仍是单机制、单重复结果，不等于完整 campaign 的缺陷计数与消融对比。 |

三个 Part 必须按同一条证据链衔接：Part I 的输出是 accepted invariants；Part II 只统计由这些 invariants 转换并验证的 checkers；Part III 的五项 gate 仅表示候选结构完整。最终 finding 还必须通过独立的源码 excerpt 核验和由 orchestrator 冻结命令驱动的离线 checker/PoC 验证；Agent 自报的 `poc_verified` 不构成确认。
Formal clean-room 要求同样适用于“发现集”边界：历史 bug 文档与 reference docs 可用于 Invariant Generation / Checker Writing / Agent Audit，但 evaluator-only `findings/` 不得暴露给 worker。这样 Part III 的有效 finding 只能来自当前 campaign 的审计与独立复核，不能从既有 findings 库中直接复用答案。

## GCC17 端到端 verified finding（真实后端，单重复）

这是当前唯一一条跑通“真实模型 Agent 提出候选 → 冻结 dispatcher 独立确定性验证”的完整链路结果，用于证明后端链路能在真实 GCC17 上发现真实缺陷；**它是单机制、单重复结果，不是正式 campaign 的缺陷计数或消融结论。**

- **目标**：GCC `17.0.0 experimental 20260531`，commit `f20bc4c2fe00928013c533e241b89ae3a6724ca1`，`x86_64-linux-gnu`，机制 IBT（`-fcf-protection=branch`）。
- **缺陷**：`ix86_endbr_immediate_operand`（`gcc/config/i386/predicates.md`）用移位后的整数值与 32 位 ENDBR 编码比较，而不是对四字节窗口取掩码；高位非零、低四字节恰为 `F3 0F 1E FA`（ENDBR64）的 64 位立即数被放行，可在函数体内形成非预期 landing pad，削弱 IBT 前向边 CFI。
- **Agent 侧**：`coconut-gpt-5-6-terra-max`，reasoning `medium`，DeFuzz 直连本地 Coconut `/v1/responses`；候选通过结构准入，指纹 `567b8940…b8998e`，agent-audit 阶段 9 次调用共 1,380,909 tokens（`usage_missing_count=0`）。
- **双 oracle 验证**（同一冻结 dispatcher、同一候选、同一 toolchain）：

  | 模式 | 路由 checker | verdict | 含义 |
  | --- | --- | --- | --- |
  | `-mode online` | bundle `checker_ids` → `INVGEN-0DD8B9A56F5624D8`（函数入口 ENDBR 属性） | `PASS` | 在线引导 checker 只确认间接目标入口有 ENDBR，用于引导 Agent，不能证明函数体内无 stray ENDBR。 |
  | `-mode verify` | `related_invariants` → 编译期 canonical `INV-IBT-B01` | `FAIL`（exit 0） | 独立 canonical checker 确定性检出 `found 1 unintended ENDBR opcode(s) inside function bodies: f+0x8@0x8[.text]`。 |

- **关键设计结论**：在线引导 checker 与最终独立 checker 必须分离。在线 `PASS` 不能推翻另一条 `related_invariant`；最终验证由编译进冻结 dispatcher 的 canonical checker（`INV-IBT-B01`，不在 bundle catalog 中）独立、fail-closed 地执行。Agent 自报 `poc_verified` 不构成确认。
- **证据目录**：`de-fuzz-experiment-runs/stages/gcc17-http-terra-r5-finalverify-evidence-20260829/`，含 `r5-candidate.json`、`online.stdout.json`、`verify.stdout.json`、各自 stderr 日志与 `provenance.json`（记录候选指纹、bundle/dispatcher/catalog SHA-256、toolchain、命令与双模式 verdict）。
- **可信 bundle**：`de-fuzz-experiment-runs/stages/gcc17-http-terra-scoped-part2-r5-finalverify-20260829/rep-001/artifacts/`，`bundle_id=f1e74449…e2c2`，dispatcher `47d50a3a…b891`，catalog `ef57e21c…06254`，含 15 个 Part II checker；`INV-IBT-B01` 为 dispatcher 内编译 canonical checker。

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

当前状态：**工程闭环和旧 TraeX 有界 pilot 已验证；HTTP 正式 backend 已接入但尚未产生正式实验数据。** Part III 显式、哈希绑定地消费 Part I accepted invariants；Full 和 `w/o Oracle` 对 worker 暴露同一份裁剪后的不变量，裸 Agent 不接收该输入。Full 同时消费 Part II bundle，并自动用同一个 dispatcher 执行 online 与 offline 模式；`w/o Oracle` 完全跳过在线反馈，但保留相同的隔离离线验证；裸 Agent 是单个中性 worker，不接收 DeFuzz doctrine、机制分片、历史 RAG、不变量或 checker 指引。Typed pipeline 的 `variants` 一次运行四个 variant，并把 variant 写入 lane 身份和 hash chain。Standalone 消融仍要求 `--baseline-run`，并冻结模型、推理档位、预算、源码/reference 内容快照、机制/ISA、bundle 和 toolchains。

## 数据产物

统一 pipeline 除每个 lane 的 stage artifact 外，还会在 campaign 根目录生成四个聚合文件：

- `campaign-results.json`
- `campaign-results.csv`
- `campaign-comparison.json`
- `campaign-comparison.csv`

前两者保留每个 `target × variant × repetition × part` 的 long-form 记录，包括失败、跳过和 `usage_missing_count`；后两者只汇总 complete、valid 且对应指标非缺失的 repetition。正式作图或论文表格应从 comparison 文件取均值/方差，从 results 文件追溯异常 repetition 与 provenance。

## 工程验收记录（非论文结果）

| 验证层 | 2026-08-27 结果 | 说明 |
| --- | --- | --- |
| 四臂 pipeline smoke | 通过 | `full`、`without-rag`、`without-oracle`、`bare-agent` 四条 lane 均跑完 Part III，resume 通过；fixture 只验证编排，不计入论文数据。 |
| 跨语言 E2E | 通过 | 真实 Part II bundle 构建、Go dispatcher、Clang x86_64 ELF、online/verify 双模式和 Part III `verified-findings` 全链路通过。 |
| 旧 TraeX 真实 Agent 有界 pilot | 通过 | `real-agent-cleanroom-v5` 使用 `GPT-5.6-Terra`，在隔离的单 segment Segmented CoT 中生成 1 条 accepted invariant，并经独立 entailment grounding；2 次调用共记录 16,824 tokens，`usage_missing_count=0`。该旧 backend pilot 只证明当时的链路，不代表 HTTP backend 或全语料结果。 |
| HTTP Responses backend | 有界 pilot 通过 | Part I 单 segment pilot 记录 8,381 tokens；用户入口 Part I pilot 记录 13,028 tokens；Part II 单 invariant pilot 产出 ready bundle 并记录 355,254 tokens。三者 `usage_missing_count=0`，但均不作为正式 campaign 结果。 |
| Demo parity corpus | 解析通过 | 当前只读 demo corpus 共 30 条；`demo-workset` 为 27 条，排除 retracted `DREV-2026-015` 以及 schema-invalid `DREV-2026-021`、`DREV-2026-030`；`poc-verified` 为 20 条。draft 条目仍纳入工程 workset，但正式报告必须披露 profile 与 status 分布。 |
| 发布回归 | 通过 | Python 全量 `496 passed`、Go test/vet、Ruff、mypy 和 wheel/sdist 构建均通过；GitHub CI 结果以对应提交为准。 |

## 当前进度

| 实验 | 状态 | 下一关键动作 |
| --- | --- | --- |
| 共同前置：统一 CLI、HTTP Responses backend、Token 统计与数据隔离 | **工程实现完成，HTTP pilot 通过** | 冻结 YAML、GCC revision、模型与预算，启动正式 campaign。 |
| Part I：不变量生成 | **HTTP pilot 通过；GCC 17 正式数据未运行** | 对完整冻结语料运行 Segmented CoT + RAG 并进行专家盲评。 |
| Part II：Checker 编写 | **累计 bundle 工程与跨语言 E2E 通过；正式数据未完成** | 使用 Part I 正式输出执行，统计首次/最终通过率和人工复核成本。 |
| Part III：Agent 审计 | **真实 GCC17 后端链路已端到端验证并产出 1 个确定性 verified finding（IBT）；正式多重复 campaign 未完成** | 在冻结 toolchain/source 上按机制/ISA 进行重复审计，汇总 verified findings、假阳性率与上游状态。 |
| 四个 variant：Full、w/o RAG、w/o Oracle、裸 Agent | **四臂 fixture smoke 通过；正式数据未运行** | 在同一 HTTP backend、GCC 17 baseline 与预算下运行固定重复次数，并报告均值、方差或置信区间。 |
