# GCC 16.2/AArch64 零候选审计：根因与改进建议

日期：2026-10-13。性质：evaluator-only 事后诊断，不是新的盲审或正式实验。历史 findings、本文及配套诊断数据不得进入后续盲审 worker 的输入。未修改原始 run、冻结输入或系统实现。

**结论**

本轮零候选包含实际漏检，不能仅归因于旧 findings 数量虚高或目标范围缩小。对同一 GCC 16.2.0/AArch64 工具链进行有限编译对照，重新观察到了 ASan 高对齐局部变量缺少 redzone、C++ strict-flex 导致 object size 未知及 checked call 消失，以及 signed counted_by 尺寸窄化。它们分别对应旧报告的缺陷机制，原审计没有输出候选。[编译证据](compiler-observations.json)

主要工程问题是：生成的不变量与审计 family 分类不兼容；worker 输入被完整 checker catalog 中重复的溯源材料占据；审计只有短暂源码读取，没有执行证伪闭环；现有实现将没有候选、没有验证的运行视为有效完成。不变量从当前代码提取时还会把当前实现的例外写进契约，漏掉对例外本身的质疑。

证据能够证明这些执行事实及具体漏检，尚不能量化各因素各自造成了多少召回损失。尤其没有旧端到端会话的完整模型、推理设置、预算和工具轨迹，不能声称 Pi SDK 或某个模型本身能力下降。

**1. 比较口径**

读取 `defend-reviewer/main/findings/DREV-*/README.md`，共 44 份记录：

| 范围 | 记录数 | 解释 |
|---|---:|---|
| GCC | 27 | 混合 GCC 15.2、16.1/RC、17 快照 |
| LLVM | 13 | 不属于本次 GCC 目标 |
| lld | 1 | 不属于本次 GCC 源码目标 |
| compiler-rt | 3 | 不属于本次 GCC 源码目标 |
| GCC 且标记 generic 或包含 AArch64 | 14 | 只是待复核集合，不是 14 个当前版本已确认 bug |

27 份 GCC 记录中，没有一份 front matter 的 `toolchain_version` 直接标记 GCC 16.2。该事实不表示缺陷在 16.2 消失。`002/011` 是跨版本的 strcat bound 根因，`003/051` 是跨版本的 C++ strict-flex 根因；按根因去重时不能各算两个。旧语料还混有静态推断、编译层确认、跨版本动态佐证与上游报告，`poc_verified: true` 不是统一验证等级。[完整清单](analysis-evidence.json)

本次是 GCC 16.2/AArch64 单次工程运行，旧 findings 是多次、多个目标的累计档案。因此“旧档案 27 对本轮 0”不是受控的召回率比较；但下面的同版本复核说明，范围差异也解释不了全部零发现。

**2. 对旧案例的有限复核**

使用原实验的 immutable image ID `693d9c9094719f16a87575f1415a18c9efd0563f172358232575e850fae6b2f7`，driver 为 `/opt/gcc16/bin/aarch64-linux-gnu-gcc`，实际 `-dumpfullversion` 返回 `16.2.0`。源码绑定 revision 为 `78d4ac73dd391005b895a6148cd9831e28e1208b`。

本诊断只编译和反汇编自包含测试程序，没有运行测试程序，没有 LLM 实验调用，也没有向原候选统计追加结果。完整输入、argv、返回码、汇编、IR 和哈希保存在 [compiler-observations.json](compiler-observations.json)。

| 旧记录 | GCC 16.2/AArch64 观察 | 本轮遗漏及结论边界 |
|---|---|---|
| DREV-2026-048，ASan 高对齐 fixed-size 局部变量 | `-O1 -fsanitize=address`：普通 `char[64]` 有 frame descriptor、shadow poison stores；`aligned(64)` 版本有 access check，却没有对应 descriptor 和 poison stores | E 读取 `cfgexpand.cc:1490–1594`，没有继续读取约 `1603–1651` 的 large-alignment 分支。最终将 high-alignment 留在 coverage gap。编译层遗漏已重现，未在本诊断运行 ASan 程序 |
| DREV-2026-003/051，C++ strict-flex | 相同代码和 `-O2 -fstrict-flex-arrays=3`：C 返回 object size `1`，保留 `__strcpy_chk`；C++ 返回 `-1`，变成 `strcpy` | B 只对 C++ 源码执行 grep，没有继续读取 C/C++ producer-consumer 链。本诊断用显式 builtin 隔离中端行为；未重新声称完成 glibc 头文件与运行期验证 |
| DREV-2026-025，signed counted_by | 优化 IR 包含 `long → MAX(...,0) → int → sizetype`。加入 `-fno-ipa-icf` 的对照后，unsigned 版本保留原宽度 | B 的 broad grep 被 50 条结果截断，未读取 `tree-object-size.cc:869–902`。确认尺寸窄化，不复用旧报告全部影响表述 |
| DREV-2026-024，BTI 常量 | 最小常量例子产生入口的合法 `bti c`，常量本身分解成 `mov`/`movk`，未产生声称的额外 landing pad | 这份旧报告不能直接当作当前版本阳性真值。本次反驳的是该最小例子，不是证明所有常量池/布局路径均安全 |

`025` 的旧运行示例还有独立的解释问题：它为只有 4096 字节 backing storage 的对象声明 `counted_by` count 为 `0x80000000`，然后把 5000 字节写入成功解释为本次窄化造成的 bypass。即使 count 没有窄化，声明的 2 GiB bound 仍大于 5000；该示例不能单独证明窄化的因果影响。尺寸计算缺陷与这个影响演示应分别评估。

首次 counted_by 双函数编译出现优化后同形结果，因此增加 `-fno-ipa-icf` 对照来隔离 signed/unsigned 行为；没有将这一额外现象自动记成新 bug。一次容器挂载使用 `/tmp` 失败，改用解析后的 `/private/tmp` 成功，失败也保存在证据中。

**3. 根因：43 条完成映射，只有 13 条获得审计归属**

实际 `visible_mappings_by_family`：

| Family | A | B | C | D | E | 合计 |
|---|---:|---:|---:|---:|---:|---:|
| 映射条数 | 7 | 1 | 2 | 1 | 2 | 13 |
| 模型请求数 | 6 | 5 | 6 | 5 | 5 | 27 |
| `read` 调用数 | 8 | 3 | 12 | 4 | 9 | 36 |

`normalize_mechanism()` 的别名表和 `CANONICAL_AUDIT_FAMILIES` 只覆盖固定分类；Part I 接受任意形状正确的 mechanism slug。`_generated_invariant_document()` 与 `bind_mapping_handoff()` 按机制精确过滤，没有断言每条已选不变量至少有一个 owner。

因此 30/43 条没有进入 family 的 generated-invariants 和 source-mapping 专用视图。例如：

- `address-sanitizer` 没归一到 `asan`。
- `object-size-check` 没归入 FORTIFY/memory-bounds。
- `gcs`、`hwasan`、`strub` 等没有 family。
- `bounds-warning` 等其他标签也未分配。

完整 catalog 仍把所有 43 条提供给每个 worker，所以不能说这 30 条“完全不可见”。事实上 E 从 catalog 中提到了 `INVGEN-C862E337607FE69D`。但上游耗费成本生成的专门映射和清晰审计任务没有交付，`43/43 mapping completed` 不能代表 `43/43 audited`。

代码证据：[机制分类](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/audit_schema.py#L36-L168)、[不变量过滤](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/prompt_bundle.py#L467-L518)、[映射过滤](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/experiment_engine/mapping_handoff.py#L52-L82)。运行证据见 [analysis-evidence.json](analysis-evidence.json) 的 `invariants` 和 `mapping_handoff`。

**4. 根因：输入体积很大，实际审计很浅**

每个 worker 的原始 prompt 是 1.05–1.26 MB，其中相同的完整 oracle catalog 占 1,003,895 bytes，约为各 prompt 的 80%–96%。catalog 中 `lineage` 与 `lineages` 两个字段的序列化内容合计约 940 KB，主要是重复的源片段和溯源材料；这些应留在审计存档中，不必完整传给每次模型请求。

本轮最终审计共 10,125,151 tokens：

- input：10,108,952，其中 cached input 为 8,177,906。
- output：16,199。
- provider 报告的 reasoning tokens：1,551，属于输出统计，不应再次相加。
- 所有 family 的实际 `thinking_level` 均为 `minimal`，上限为每 worker 32 次请求，但只用了 5–6 次。
- 五个 worker 的事件区间依次覆盖 `12:37:50Z–12:43:50Z`，约六分钟；这是 worker 审计区间，不是整轮流水线耗时。

大量 tokens 在重复处理既有上下文，不代表等量的新证伪工作，也不能按统一单价推算缓存请求费用。已保留的最终 worker 均正常结束，没有触及 32-call 上限。不能把这个零候选结果归因于最后一轮又被 token ceiling 截断。

B/E 对应最明显的执行证据：B 仅三次 `read`，都没有读 C++ strict-flex 或 counted_by 的关键实现；E 在 ASan 正常分支附近停下。七次工具错误全部是 workspace 相对路径使用问题，后续调用恢复；它们消耗了少量探索机会，但不能解释为持续环境不可用。

代码证据：[完整 catalog 注入](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/agent_audit.py#L1148-L1168)、[默认 minimal](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/experiment_engine/pi_config.py#L49-L52)。真实调用、文件范围、token 和配置摘录均在 [analysis-evidence.json](analysis-evidence.json)。

**5. 根因：发现与验证之间存在循环依赖**

当前执行关系是：

```text
只读 worker → 先写出完整 candidate → online oracle → 修订报告
                     │
                     └─ candidates 为空 → 无 feedback → break → completed
```

[audit-family 定义](file:///Users/bytedance/projects/research/de-fuzz/main/agents/definitions/audit-family.md) 只提供文件查询和 CallGraph，没有编译、IR/汇编检查或受控运行工具。`agent_audit.py:1393–1396` 只遍历 `report.candidates` 执行 oracle；`1421–1422` 在没有 feedback 时退出，`coverage_gaps` 和 `next_steps` 没有进入任务队列。本轮五份报告留下 32 个 gap，却没有任何验证任务被执行。

同时，online oracle 要求候选必须选择现有 bundle 的 checker ID；没有合适 checker 时无法直接请求验证。checker 更新又依赖已经发生的 oracle feedback。这对探索新机制或 checker 尚未覆盖的边界形成障碍。

必须区分两个事实：

1. **本轮没有任何候选被机械 gate 删掉。** `candidates=0`、`candidate_rejected=0`；不能解释成“找到了二十条，都被严格验证过滤掉”。
2. **严格 doctrine 也不是新旧之间已经证实的唯一差异。** 旧 reviewer 本身也只读，且两边 doctrine 都明确允许 `poc_verified=false` 的源码候选。旧 `048`、`051` 的 timeline 保存了源码分析之后的编译验证；本轮编排没有完成对应的探索与实验任务。没有旧会话的完整轨迹，无法逐步还原其调度，但现有证据支持修复循环，不支持简单删除精度要求。

代码证据：[candidate 驱动 oracle 和提前退出](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/agent_audit.py#L1380-L1424)、[checker ID 门槛](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/online_oracle.py#L318-L334)。

**6. 根因：把当前实现行为写成预期契约**

Part I 的分段提示词要求 `Return only candidates entailed by this exact segment`。这有助于防止幻觉，但也倾向于把代码已有的条件当成正确契约。

具体例子 `INVGEN-2BEB3971E70676E4` 的 statement 是：

> Address Sanitizer stack layout keeps differently sized supported-alignment partition representatives separate so shorter variables receive adequate protection.

它只要求 **supported-alignment** 范围内的行为；源片段却已经包含 “large alignment … those aren't protected anyway” 的注释。高对齐对象为什么不保护，本应是待证伪对象，结果被排除在该生成契约的定义域之外。E 随后确认正常分支并把 large alignment 放进 gap，与这一收窄一致。这里是明确的任务表述偏差；其独立因果效果仍需消融验证。

B 唯一被路由的生成不变量 `INVGEN-B3AA41472B6BB64B` 检查的是 producer command-line 字符串保留 `_FORTIFY_SOURCE`，并非运行时 bound 或 `_chk` 调用完整性。baseline surveys 虽然仍在 prompt 中，生成 checker 数量却并不意味着对这些关键防御契约提供了验证能力。

代码证据：[分段生成提示词](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/experiment_engine/segmented.py#L587-L614)；不变量原文和完整归属清单见 [analysis-evidence.json](analysis-evidence.json)。

**7. 完成条件和插桩交接的缺口**

`result_valid` 主要取决于 worker 结构合法、没有 oracle/update 错误，以及已有候选的结果状态。没有候选时这些条件可全部满足；它没有检查每个审计目标是否形成了证伪结论，也没有检查是否执行过测试。[完成判断](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/agent_audit.py#L1630-L1651)

日志插桩只生成补丁和 catalog；`run-all` 后续 audit argv 仍接原 target tree 和 toolchains config，没有消费插桩构建结果。[交接代码](file:///Users/bytedance/projects/research/de-fuzz/main/orchestrator/defuzz_loop/experiments_cli.py#L1375-L1425)

因此，本轮 `completed` 应理解为“阶段程序正常结束、报告结构有效”，不是“所有防御目标完成证伪”。插桩缺失不能单独解释源码层面零候选，但意味着设计中的覆盖反馈没有产生作用。此前把阶段完成当作完整审计效果验收，口径不成立。

**8. 改进顺序与验收标准**

| 优先级 | 改动 | 具体落点 | 验收条件 |
|---|---|---|---|
| P0 | 统一机制分类和任务归属 | `segmented.py`、`audit_schema.py`、`prompt_bundle.py`、`mapping_handoff.py` | 每条 selected invariant 至少有一个 owner；未知机制显式报错或进入待分派队列；进入审计的 ID 并集等于适用输入集。拒绝静默丢失 |
| P0 | 给模型只传紧凑 checker 能力摘要 | `agent_audit.py`、`prompt_bundle.py` | 仅含 ID、机制、适用条件、可观测结果、调用能力；移除 `lineage/lineages`、全量源片段和无关 family 内容。原 catalog 和哈希保留给 evaluator；校验与原 bundle 一致 |
| P0 | 增加 hypothesis / test-request 中间产物 | `audit_schema.py`、`agent_audit.py`、受控执行接口 | 没有 candidate、但存在可执行测试请求时仍会编译/检查；无匹配 checker 标为 `oracle_gap` 并可请求新增 checker；精度 gate 留在 confirmed finding 出口 |
| P0 | 用审计义务而非空 JSON 决定何时停止 | `agent_audit.py` 及阶段结果 schema | 每个机制/不变量记录 tested、source-refuted、candidate、unverified、blocked、out-of-scope；零执行且有未完成目标时不得标为 audit-complete。不强迫产出 bug |
| P1 | 恢复有预算约束的多轮证伪 | 审计 worker 和任务调度 | 以 hypothesis 为单元读取源码、提交 seed/flags、消费 compiler/checker 结果并决定下一步；支持边界值、语言、优化和相邻分支对照，避免以 tool-call 数充当深度 |
| P1 | 分离规范契约与实现观察 | Part I 产物和 reviewer | 分别记录规范出处、适用前提、实现条件、待验证例外；实现条件不能自行缩小规范定义域。ASan alignment 等例外必须给出文档依据或进入测试队列 |
| P1 | 完成插桩工具链交付 | instrumentation、toolchain manifests、audit runner | 补丁应用、GCC 构建、binary hash、功能 smoke、日志开关、实际命中、audit 使用的 driver hash 全部连通；base 与 instrumented 产物关系可追溯，不能简单改坏原 CallGraph 绑定 |
| P1 | 单独校准推理等级与工作粒度 | Pi config、机制/假设任务划分 | 同模型和相同预算，比较 minimal 与更高推理等级；采用按需源读取，记录真实 tokens 和已验证召回。不要只提高 token ceiling |
| P2 | 补齐跨层和默认 hook 检索 | source mapper、审计覆盖清单 | 链接器/运行库缺失应成为明确依赖；无 AArch64 专属 hook 时继续定位 generic default；grep 截断应触发缩小范围检索 |

建议的执行关系：

```text
机制契约 + 源码映射
  → hypothesis（可以尚未满足 finding admission）
  → 明确测试请求 / checker-gap 请求
  → 编排器编译、检查 IR/汇编/ELF、按需要受控执行
  → 证据反馈 → 修订或反驳 hypothesis
  → 独立验证 → confirmed finding / refuted / unverified
```

这个改动保留显式编排、冻结输入和独立判定。无需赋予 worker 任意 shell 权限；可以让它提交声明式测试请求，由已有的受控执行层完成操作。

**9. 下一轮怎样验证改进有效**

先复用已冻结的上游产物，只诊断和校准审计阶段；P0 修复验收前不再重跑昂贵的整条流水线。

评估分为两类，禁止混用结论：

- **已知案例回放**：evaluator 读取 findings，按 GCC16.2/AArch64 与根因去重，重新验证 positive/control；`048`、`003/051`、`025` 可以作为本轮编译层阳性起点，`024` 的最小例子可作为不应误报的对照。测执行器、checker 和判定是否正确，不能称为盲发现召回。
- **隔离盲审**：worker 只读源码和许可的规范/历史库；旧 DREV、reproducer、本文与目标位置均留在 evaluator。冻结模型、推理、预算、源码、工具链和测试范围，用 evaluator 在任务结束后做根因匹配。经过这些案例调参的数据属于开发集，正式效果还需独立 holdout。

工程校准建议先保持相同模型和推理设置，只修正路由、catalog 和工作循环，观察覆盖、候选、验证执行量是否改善；再单独比较更高推理设置。若同时改所有因素，可以检验整体修复效果，但不能宣称某一项带来了全部提升。

至少记录：适用且在当前版本仍存在的独立根因数、被定位根因数、提出的可执行 hypotheses、执行测试数、检出/反驳/未验证数、precision、recall、time-to-first-confirmed、每个确认根因的 tokens、所有失败重试和缺失 usage。不要把历史总报告数、生成 checker 数或 MCP 查询数当成 bug 发现效果。

**证据索引**

- [结构化诊断数据与原始证据哈希](analysis-evidence.json)：44 份旧记录元数据、43 条不变量归属、五个 worker 的实际工具调用、token、gap、源码哈希。
- [本次有限编译对照](compiler-observations.json)：版本探测、完整源程序、命令、原始汇编/IR、返回码及挂载失败记录。
- [原审计汇总](file:///Users/bytedance/projects/research/defuzz-experiment-runs/gcc16-aarch64-20261013/gcc16-aarch64-full-20261013-agent-audit/rep-001/artifacts/agent-audit-summary.json)：零候选、零 online oracle、零独立 PoC 执行。
- [旧审计 doctrine](file:///Users/bytedance/projects/research/defend-reviewer/main/.claude/agents/defend-reviewer.md) 与 [旧端到端模板](file:///Users/bytedance/projects/research/defend-reviewer/main/docs/prompts/full-review.md)：旧流程同样要求证据与 findings 隔离，允许明确标记的静态候选。
- [当前架构](file:///Users/bytedance/projects/research/de-fuzz/main/docs/tech-docs/architecture/agentic-loop-redesign.md) 与 [实验规程](file:///Users/bytedance/projects/research/de-fuzz/main/docs/paper/experiment_runbook.md)：设计要求受证据驱动的循环，工程结果与 formal 证据必须分开。
