# GCC/AArch64 Checker 与 Source Mapping 失败根因分析

分析日期：2026-10-12。对象：`.work/aarch64-full-20261012/` 保留的真实实验产物、当前执行代码、安装的 OpenCode 1.18.31。此次诊断没有调用外部模型、没有启动全量重跑、没有修改执行代码。

本报告纠正此前 `gcc-aarch64-full-20261012-report.html` 及 `statistics.json` 中的三项解释：不能将 Checker 失败归因于多数 Go 质量检查不通过；不能将 mapping 失败归因于 32K 模型输出上限；不能根据现有 `usage_missing=0` 认定 token 完整计量。原始实验仍然是未完成的工程运行，没有主审计或独立验证结论。

## 1. 结论与实际分母

两个阶段的首要阻断共享同一个工程故障：**OpenCode 子会话通过 stdout 管道导出时发生截断，DeFuzz 在解析会话 JSON 时失败。** 这不是 mapping 最终回答的 JSON 解析失败。Checker 另有空产物误通过、重试丢失任务上下文和详细证据被清理的问题。[执行代码](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/experiment_engine/opencode_backend.py#L210-L269)

| 指标 | Checker 编写 | Source Mapping |
|---|---:|---:|
| 输入不变量 | 42 | 35（AArch64 范围） |
| Agent attempts | 124 | 103 |
| 会话 JSON 解析失败 attempts | 123 | 102 |
| 失败不变量 | 41 | 34 |
| 记录上的成功 | 1 条局部验收通过，但未生成 checker | 1 条正式写入 mapping |
| 额外核验 | 124 次均无文件变更 | 103 份 task result 均可解析并通过当前 schema、路径与行范围检查 |

Checker 的 123 次失败验证项都是 `type=agent-error`；没有进入逐条 `gofmt / go test / go vet`。因此这些记录无法测量模型的 checker 编码正确率。mapping 的离线重解析只是诊断证据，不补写原实验成功状态，也不证明源码锚点的语义正确性。

证据：[逐条 Checker 结果](file:///Users/bytedance/projects/research/de-fuzz/main/.work/aarch64-full-20261012/gcc-aarch64-full-20261012-checker-authoring/rep-001/artifacts/results.jsonl)、[mapping 汇总](file:///Users/bytedance/projects/research/de-fuzz/main/.work/aarch64-full-20261012/gcc-aarch64-full-20261012-source-mapping/rep-001/artifacts/source-mapping-summary.json)、[本次机器可读核验](file:///Users/bytedance/projects/research/de-fuzz/main/docs/reviews/gcc-aarch64-20261012-root-cause-evidence.json)。

## 2. 共同根因：子会话导出被截断

### 2.1 故障位置

OpenCode `run --format json` 返回父会话事件，包括 task 的最终结果。DeFuzz 再执行 `opencode export <child-session-id>`，补取子代理的读文件、CallGraph、输出及 token 记录。当前 `_collect_events()` 用 `stdout=asyncio.subprocess.PIPE` 捕获导出，随后在第 244 行调用 `json.loads(output)`。

当前安装二进制内的导出实现使用 `process.stdout.write(JSON.stringify(...))`，未等待 write callback 或 drain；CLI 外层最终执行 `process.exit()`。结合下述同一会话对照，证据支持其在 stdout 管道尚未排空时退出。退出码 0 并不代表 JSON 已完整传输。Python 的 `communicate()` 本身不是固定 64KiB 截断器。

解析出错后，DeFuzz 追加通用 `error` 事件；`_read_final()` 遇到任意此类事件就返回失败。因此，即使模型已返回合法最终结果，也会因为证据收集失败而被判成 agent failure。

### 2.2 无模型调用的真实会话复现

从本次唯一保留的成功 mapping 子会话 JSON 导入临时、隔离的 OpenCode 数据目录，仅执行 import/export。

| 读取方式 | 观察结果 |
|---|---|
| 初次 subprocess 管道，5 次 | 65,536、65,536、65,536、98,304、98,304 字节；全部 JSON 无效，退出码全部为 0 |
| 同次普通文件导出 | 119,601 字节；JSON 完整 |
| 使用与 backend 相同的 asyncio 管道复核 | 65,536 字节；JSON 无效，退出码 0 |
| 同次 asyncio 普通文件导出 | 119,605 字节；JSON 完整，session ID 一致 |
| 同次字节比较 | 管道结果是完整文件的精确前缀 |

两轮 import/export 间完整长度略有差异，比较使用各轮自身的完整结果；第二轮的精确前缀比较进一步排除了最终模型内容生成错误。二进制和输入会话 SHA-256 已写入核验 JSON。

### 2.3 为什么不像输出 token 不够

103 个父会话 task result 均可通过 `SourceMappingResult` schema；解析后 JSON 对象序列化大小仅约 1.7–5.5KB。错误位置则来自包含读文件结果、工具消息等内容的完整会话导出，常接近 64KiB 边界。增大模型输出 token 限制不会修复这一传输问题。

示例：[第一个 mapping 的完整 task result](file:///Users/bytedance/projects/research/de-fuzz/main/.work/aarch64-full-20261012/gcc-aarch64-full-20261012-source-mapping/rep-001/artifacts/agents/mapping-00001/events.jsonl)。该结果已经包含 GCC pass、函数、条件等结构化锚点，而对应完整子会话导出仍解析失败。

### 2.4 修复

优先将 **export 子进程的 stdout 直接绑定普通文件描述符**，在同一隔离边界下等进程退出，再从文件读 JSON。使用同一临时运行环境和 session ID，保留 stderr、退出码、字节数、哈希及解析结果。解析失败也保留原始导出，不要像当前实现一样只有成功解析后才持久化。

长期修复可升级到经大会话导出验证的 OpenCode 版本，或修复其 stdout flush/退出行为。本次没有验证其他版本，不能把“升级”当作已经证明有效的方案。

不可简单忽略 export 错误、直接采用父会话答案：虽然答案可读，完整子会话的用量、工具执行、身份和错误证据仍然缺失。正式执行仍应失败关闭。

## 3. Checker 独立问题：空产物通过局部验收

唯一 `final_status=passed` 的 `INVGEN-B90310C8136D589C`：

- `changed_files=[]`，first/final patch 均为 0 字节。
- parent/result tree SHA-256 相同。
- 原有 Go 包通过格式、测试和 vet。
- 最终 dispatcher catalog 没有此 ID，报 `dispatcher catalog is missing checker 'INVGEN-B90310C8136D589C'`。

当前验收条件是 agent 成功、通用 Go 检查通过、无禁止的文件修改；它没有要求本条不变量的实现实际存在。没有做事也能满足上述条件。最终 catalog 门禁正确地挡住了不完整 bundle，但检查发生太晚，该条已被计为成功，也没有把缺失 ID 反馈给它的修复循环。[逐条验收逻辑](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/experiment_engine/checker_authoring.py#L1472-L1546)

修复应把逐条成功定义改为可核验的产物契约：

1. 新生成条目必须有有效实现变更；显式复用应走独立路径，核验既有 checker ID 与语义映射，不能伪装为新生成成功。
2. 编译 dispatcher，确认当前 ID 同时出现在 metadata 和运行时注册 catalog 中，校验机制、ISA、依赖及路由。
3. 执行针对该 ID 的 Pass、Fail、NotApplicable、Error、nil/missing context 用例；通用包测试通过不能代替新条目的测试。应覆盖 checker 自身语义，避免仅用一个新符号或空测试充数。
4. 将缺失实现、缺失注册、catalog 不一致作为当前条目的反馈，允许有界修复；只有上述步骤全部通过才计入 `generated_checker_passed`。

**仍未查明的部分**：为什么真实模型在这 124 次尝试中均未留下文件变更。export 失败解释返回状态，却不会自动解释“没有落盘”。没有保留的原始 checker 对话可用于判定模型仅输出文字、调用工具失败还是遇到其他限制。不能把这部分直接归因于模型质量或权限。

现有 `test_native_checker_author_can_edit_only_workspace` 本次运行通过（1 passed，2.22s），证明当前 backend 在本地 openai-compatible fixture 下可以写文件；它没有覆盖本次生产 Responses adapter 和模型，因此仅能排除“该 backend 在任何情况下都不能写”。

## 4. Checker 重试与证据管理放大了问题

### 4.1 新会话只收到 ID 和错误

`_subprocess_environment()` 每次建立新的 OpenCode 数据目录；后续 attempt 没有延续前次会话。初次提示含完整不变量，但 `_repair_prompt()` 仅带 ID、一般约束和失败反馈。41 个失败条目各额外重试两次，共 82 次重试；在工作树无实现时，这种提示缺少显式的原任务语义。[重试提示](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/experiment_engine/checker_authoring.py#L662-L677)

修复：每次新会话都重发完整规范化不变量、实现契约、已有变更摘要和确定性诊断。基础设施错误应先在同一会话上重试本地 export，而不是要求模型“修复 JSON”或“修复 checker”。连续相同基础设施错误应停止阶段并保存现场，避免遍历全部输入重复消耗。

### 4.2 会话记录跟着临时源码一起被删除

Checker 的 `agent_output` 位于临时工作树的 `.defuzz-agent/...`，最终清理整个 lease；永久产物只有 results 和 patch。实际检查发现 checker artifacts 下没有 measured-events 或子会话 JSON。这使写入失败原因无法追溯。

修复：把逐 attempt 的 prompt、父/子会话、工具结果、最终回答、stderr、失败分类和哈希写到阶段的永久 artifact 目录。仍按既有阶段隔离规则控制可读路径；该目录不可误纳入 checker 源码 patch。清理前必须确认失败证据已持久化。

### 4.3 用量统计产生盲区

导出 JSON 解析失败发生在子会话事件提取之前。父会话 usage 存在，现有 sink 不知道未提取的子会话调用，因此可能保持 `usage_missing=0`。

Checker 的 127 条 usage 记录与 124 次 attempts 相比，仅多 3 条；mapping 的 109 条与 103 次 attempts 相比，仅多 6 条，后者对应唯一成功导出的子会话。失败导出的子调用用量没有进入统计。此前的 4,676,834 tokens 是保留 usage 记录的合计，不能作为完整成本，也不能据此认定真实预算未触顶。

修复：分别记录 `usage_missing` 与 `session_evidence_complete`，把子会话导出失败标为用量完整性未知、`token_comparable=false`。无原始证据不得补 0 或估算成实测。用 (session ID, message/step ID) 去重，避免重新 export 导致重复计量。

## 5. 修复传输后还需处理的输入与语义问题

这些问题是真实存在的后续风险，但不是本次 225 次解析失败的直接原因。

- **范围不同**：Checker 的 `requested_isas=[]`，处理全部 42 条；mapping 筛为 AArch64 35 条。新执行应让两阶段共享一份冻结 scope，明确记录另外 7 条的排除理由，同时保留适用 checker 的跨 ISA 差分依赖。
- **机制缺少下游路由**：按当前规范化规则，35 条 AArch64 输入中有 25 条的 mechanism 不在 canonical audit families 中，包括 `gcs`、`hwasan`、`strub`、若干分析/诊断类标签。部分可能应增加经确认的别名或机制支持，部分应重新审查是否属于软件防御不变量；不能静默丢弃，也不能全部塞进一个通用族。
- **输入混合了不同观测层级**：例如 `backend-multi` 条目描述 SME/ZA 活跃状态内部转换，另有编译期 warning、别名分析等条目。应明确每条的激活条件、可观察证据、编译器版本及如何落到既有 `CheckContext`，不要只凭“有源码依据”认定可用现有二进制 checker 验证。上游筛选或 schema 变更必须单独记录为方法变更。
- **版本来源需澄清**：本次目标是 GCC17，而两个 RAG 不变量仍带 `version=gcc-16.1.0`；CallGraph 数据是 GCC16.2.0。历史版本可以作为检索线索，但源码锚点必须以冻结 GCC17 直接验证，图结果标为跨版本辅助证据。
- **mapping 验收较浅**：`validate_anchors()` 当前只检查路径存在、未越界及行号范围；103 次通过不代表函数名、条件和不变量语义均正确。应补符号/源码片段与内容哈希验证，并允许明确表达 target hook 等非独立 pass 的实现，避免强迫填一个 pass 名。

机制清单来自 [audit_schema.py](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/audit_schema.py#L38-L96)；mapping 路由按机制筛选，见 [mapping_handoff.py](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/experiment_engine/mapping_handoff.py)。

## 6. 推荐落地顺序与验收

| 优先级 | 改动 | 验收依据 |
|---|---|---|
| P0 | export 使用文件接收；失败原文持久化 | 同一真实大 session 管道问题可复现，修复方式获得完整 JSON、身份及全部 step |
| P0 | token/工具证据完整性与错误分类 | export 失败不能仍报告 token 可比；不再消耗模型 repair attempt |
| P0 | checker 日志从临时目录移到永久 artifacts | 成败尝试在源码清理后均可追溯 |
| P0 | 逐条实现存在性、注册和 catalog 验收 | 不改代码的 agent 必须失败；缺失 ID 进入该条修复循环 |
| P1 | 每次 repair 重发完整不变量 | 全新会话也能获得原任务语义和具体反馈 |
| P1 | AArch64 scope、机制和版本对齐 | 两阶段输入集合一致；每条有明确后续处理路径 |
| P1 | 有界真实 checker 验证 | 先验证一个简单静态项和一个依赖现有机制的项，保存完整工具与产物证据 |
| P2 | 加强 mapping 语义核验及图覆盖 | 行号正确之外，符号、条件、版本和机制关联可复核 |

有界真实验证属于工程诊断，不能作为全量结果。只有 P0/P1 验收通过后，才以新 run ID 和冻结配置运行完整适用输入。保留本次失败产物，不覆盖、不拼接成已完成正式 repetition，也不放宽 ready bundle 门禁。

## 7. 假设与证据状态

| 假设 | 结论 | 证据与边界 |
|---|---|---|
| 子会话导出管道截断导致两个阶段失败 | CONFIRMED | 同一真实 session、文件/管道对照、精确前缀、backend 解析点与历史错误一致；历史失败原始 export 未保存 |
| mapping 最终 JSON 普遍不合法或撞到 32K 上限 | REJECTED | 103 份 task result 均通过 JSON/schema；错误来自完整会话 export |
| 大多数 checker 未通过 Go 编译/测试/vet | REJECTED | 123 次失败均未进入逐条 Go 验证 |
| checker 空产物可以被计为局部通过 | CONFIRMED | 无改动、空 patch、相同 tree hash、最终 catalog 缺失 ID 与代码条件吻合 |
| 全局写权限缺失导致本次所有 checker 无变更 | INCONCLUSIVE | 本地写契约测试通过，但历史真实会话已删除，不能确定真实模型未落盘原因 |
| CallGraph 版本缺口直接造成本次 mapping 解析失败 | REJECTED | 所有失败仍是 export 解析错误；版本缺口保留为语义质量风险 |

`progress_status: diagnosis_complete_with_explicit_evidence_gaps`。本次交付为根因分析与修复设计；生产代码修复及真实模型重跑尚未执行。
