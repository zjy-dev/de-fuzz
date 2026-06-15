# Feature Specification: Agentic Loop Redesign

**Feature Branch**: `docs/agentic-loop-redesign`

**Created**: 2026-06-14

**Status**: Draft

**Input**: 在本分支实现 `docs/tech-docs/architecture/agentic-loop-redesign.md` 的方案——用显式编排的确定性流水线替代覆盖率驱动的 fuzz 主循环，外加三个各带 tools 的 agent，checker 绑定 ISA 做种子路由。

## Overview

把现有"覆盖率驱动的 fuzz 主循环"里"生成种子"那一环替换为 agent，oracle 原样复用做 ground truth，ISA 调度从"机制 × ISA"笛卡尔积收进 agent 的语义判断里。系统由一个**显式编排器**驱动：build、coverage、oracle 等有确定答案的步骤写死在一条固定顺序的流水线里；agent 只在固定的三个位置（开头生成、未违反反馈、违反最小化）被调用，在那里自由 ReAct，但不掌管流程往哪走。agent 之间不直接对话，只通过一块由流水线持有并版本化的**共享状态（blackboard）**间接联动。

核心约束：正确性始终由确定性证据守住——agent 负责提议，oracle 负责裁决；agent 选 checker 是性能优化而非正确性把关。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - 显式编排的单轮迭代闭环 (Priority: P1)

研究者启动一次运行，系统按固定顺序推进单轮迭代：读共享状态 → 调用 Generator 生成新种子并选中 checker 集 → build（按 checker→ISA 编译）→ coverage（强制测量并写回共享状态）→ oracle（跑 checker 得 verdict）→ 根据 verdict 路由（未违反走反馈 agent、违反走最小化 agent）→ 写回共享状态、进入下一轮。

**Why this priority**: 这是整个方案的骨架。没有这条确定性流水线，三个 agent 无处挂载，护城河（可复现/可 ablation/可审计）也无从谈起。它本身就是一个可运行、可演示的 MVP。

**Independent Test**: 在不接入反馈 agent / 最小化 agent 的前提下，仅用 Generator + 三个确定性节点跑通若干轮，验证每轮按固定顺序推进、共享状态被正确读写、产物可被人工检视。

**Acceptance Scenarios**:

1. **Given** 一块初始化好的共享状态（含初始语料、空覆盖率、空 verdict 历史），**When** 编排器启动一轮迭代，**Then** 系统严格按 generate → build → coverage → oracle → 路由的顺序执行，且每个节点的输入只来自共享状态、输出只写回共享状态。
2. **Given** oracle 对某颗种子返回非违反（Pass/NotApplicable），**When** 该轮结束，**Then** 编排器把控制权交给反馈分支并回到开头，进入下一轮。
3. **Given** oracle 对某颗种子返回违反（Fail），**When** 该轮结束，**Then** 编排器把控制权交给违反分支（最小化），该支为终点不再回环。
4. **Given** 锁定共享状态的某个版本，**When** 重放该轮，**Then** 任一 agent 的输入完全确定、轨迹可复现。

---

### User Story 2 - 种子按 checker 路由、checker 绑定 ISA (Priority: P1)

Generator 只回答语义问题——"这颗种子的结构能触发哪些 checker？"——选中一个 checker 就自动认领该 checker 静态绑定的全部 ISA。ISA 这个裸维度 agent 从头到尾不碰；ISA 调度由流水线按 checker 元数据查表得出。

**Why this priority**: 这是相对"让 agent 直接挑 ISA"的关键改进，直接治"维度爆炸"痛点，并守住跨 ISA 静默失效这一最值钱的 bug 信号。与 US1 同为 P1，因为流水线的 build 节点必须依赖这套路由才能确定编译矩阵。

**Independent Test**: 给定一组带 ISA 绑定元数据的 checker，让 Generator 对若干种子选出 checker 集，验证流水线据此查表展开出正确的 (checker, ISA) 编译/执行矩阵，且 agent 输出中不含任何 ISA 维度。

**Acceptance Scenarios**:

1. **Given** 每个 checker 静态声明 `applicable_isas`、`mode`(single/differential)、`cost`(cheap/expensive)，**When** Generator 选中某 checker，**Then** 流水线自动展开该 checker 绑定的全部 ISA，无需 agent 指定 ISA。
2. **Given** Generator 选中一个 differential 类 checker，**When** 流水线编排执行，**Then** 该 checker 绑定的多个 ISA 被强制全跑、不可被剪枝，差分判定由元数据保证。
3. **Given** 一组廉价静态 checker，**When** 任一轮迭代执行，**Then** 这些 checker 默认全开、不进 agent 决策；只有昂贵 checker 才由 Generator 路由。
4. **Given** Generator 路由判断错误（漏选了某昂贵 checker），**When** oracle 聚合，**Then** 正确性仍由"静态 checker 全开 + checker 自带 NotApplicable"兜住，不产生假阴性误报为正确。

---

### User Story 3 - 共享状态（blackboard）联动通道 (Priority: P1)

三个 agent 不互发 prompt，所有联动都走一块由流水线持有并版本化的共享状态：反馈 agent 写回的引导落到共享状态，下一轮开头被 Generator 读出；oracle verdict 历史、覆盖率累积、种子语料与家系也都记在这里。

**Why this priority**: 这块共享状态是系统跟"自由发挥型"系统拉开差距的地方，是护城河真正落地的位置。它是 US1 闭环和 US4/US5 联动的共同载体，缺它则可复现/可 ablation/可审计都不成立。

**Independent Test**: 构造一个带历史版本的共享状态，验证每条联动边（如"反馈 agent → Generator"）都能单独开关，且锁定某版本后任一 agent 输入完全确定。

**Acceptance Scenarios**:

1. **Given** 系统运行多轮，**When** 检视共享状态，**Then** 它至少持有：种子语料与家系、覆盖率累积、oracle verdict 历史、反馈 agent 写回的引导。
2. **Given** 反馈 agent 产出一条引导，**When** 该轮结束，**Then** 引导先落到共享状态，下一轮开头再被 Generator 读出，绝不在 agent 间直接传递。
3. **Given** 任一条联动边被关闭（ablation），**When** 系统运行，**Then** 其余流程仍能推进，该边的影响可被单独度量。
4. **Given** 一个 bug 结论，**When** 沿共享状态回溯，**Then** 能追溯到具体的确定性证据（某 checker 的 verdict 或一次可复现的执行差分）。

---

### User Story 4 - 覆盖率反馈 agent（未违反时） (Priority: P2)

oracle 判定未违反时，编排器调用一个上下文隔离的反馈 subagent，带覆盖率 diff 之类的 tool，把覆盖增量和 oracle 的 NotApplicable/Pass 结果提炼成下一轮的引导，写回共享状态。

**Why this priority**: 它补上语义引导力、提升搜索效率，但 P1 闭环（US1+US2+US3）已能跑通运行；反馈 agent 是增强项，可在骨架稳定后接入并单独 ablation。

**Independent Test**: 给定一轮未违反的结果（覆盖增量 + Pass/NA verdict），让反馈 agent 产出引导并写回共享状态，验证引导格式可被下一轮 Generator 消费，且 Generator 上下文不被反馈 agent 的中间推理污染。

**Acceptance Scenarios**:

1. **Given** 某轮 oracle 返回 Pass/NotApplicable，**When** 编排器调用反馈 agent，**Then** 反馈 agent 接收覆盖增量与 verdict、产出下一轮引导并写回共享状态。
2. **Given** 反馈 agent 是上下文隔离的 subagent，**When** 它运行，**Then** Generator 的上下文保持干净，不混入反馈 agent 的中间轨迹。
3. **Given** 覆盖率，**When** 任一轮执行，**Then** 它由 coverage 节点强制测量写入共享状态，不作为 agent 可自报的 tool（防止 agent 伪造覆盖指标）。

---

### User Story 5 - PoC 最小化 agent（违反时） (Priority: P2)

oracle 判定违反时，编排器调用最小化 agent，它读流水线记在共享状态里的确定性 bug 证据来缩小 PoC，加快人审。主力是确定性的 delta-debugging（creduce），LLM 只在旁边做语义引导。

**Why this priority**: 它加快人审、提升交付质量，但发现 bug 本身由 P1 闭环 + oracle 完成；最小化是 bug 已确认后的下游增强，独立性强、可单独接入。

**Independent Test**: 给定一份触发违反的种子与其确定性 bug 证据，运行最小化 agent，验证它输出一份更小、仍触发同一 bug 的 PoC，并写回共享状态交人审。

**Acceptance Scenarios**:

1. **Given** oracle 返回 Fail 并把确定性 bug 证据写入共享状态，**When** 编排器调用最小化 agent，**Then** 它读取该证据并产出最小化 PoC 写回共享状态。
2. **Given** 最小化以确定性 delta-debugging 为主力，**When** LLM 参与，**Then** LLM 仅做语义引导，最小化结果仍触发原 bug（而非删成另一个 bug）。
3. **Given** 最小化完成，**When** 该轮结束，**Then** 违反分支为终点，PoC 交人审、不回环进入下一轮。

---

### Edge Cases

- 当 Generator 产出的种子编译失败时，流水线如何处置该轮（跳过 coverage/oracle 还是记为一类反馈信号）？
- 当某 checker 声明的 ISA 在当前环境不可用（缺 QEMU / 缺工具链）时，build/执行节点如何降级而不污染 verdict？
- 当 differential checker 在部分 ISA 上 build 成功、部分失败时，差分判定如何处理不完整矩阵？
- 当共享状态版本回放时遇到外部不确定性（LLM 非确定性输出）时，"可复现"的边界落在哪里（锁定输入 vs. 锁定输出）？
- 当反馈 agent 或最小化 agent 自身失败/超时时，编排器如何继续推进而不卡死闭环？
- 当 oracle 返回 Error（基础设施失败）而非 Pass/Fail/NA 时，编排器走哪条分支？

## Requirements *(mandatory)*

### Functional Requirements

**编排与流水线**

- **FR-001**: 系统 MUST 由单一显式编排器驱动，按固定顺序执行 generate → build → coverage → oracle → 路由，顺序写死、不由 agent 临场决定。
- **FR-002**: build、coverage、oracle MUST 是确定性节点，不调用任何 agent。
- **FR-003**: 系统 MUST 仅在三个固定位置调用 agent：开头（Generator）、未违反（反馈 agent）、违反（最小化 agent）。
- **FR-004**: 每次 agent 调用 MUST 是一对清晰的输入/输出：流水线传入上下文、agent 传出产物；agent 不自行推进流程、不调用其他 agent。
- **FR-005**: 未违反分支 MUST 在写回共享状态后回到开头进入下一轮；违反分支 MUST 为终点，产物交人审。

**共享状态（blackboard）**

- **FR-006**: 系统 MUST 维护一块由流水线持有并版本化的共享状态，作为所有 agent 联动的唯一通道。
- **FR-007**: 共享状态 MUST 至少持有：种子语料与家系、覆盖率累积、oracle verdict 历史、反馈 agent 写回的引导。
- **FR-008**: agent 之间 MUST NOT 直接通信；每个 agent 只对共享状态做"读输入、写产出"。
- **FR-009**: 系统 MUST 支持锁定共享状态的某个版本以重放轨迹，使任一 agent 的输入在该版本下完全确定。
- **FR-010**: 系统 MUST 支持单独开关每条联动边（如"反馈 agent → Generator"）以做 ablation。
- **FR-011**: 系统 MUST 支持从一个 bug 结论沿共享状态回溯到产生它的确定性证据。

**Generator 与 checker 路由**

- **FR-012**: Generator MUST 产出新种子及其选中的 checker 集，并带源码检索、不变量查询两类只读 tool。
- **FR-013**: Generator MUST NOT 输出 ISA 维度；ISA 由流水线按 checker 元数据查表确定。
- **FR-014**: 每个 checker MUST 静态声明其元数据：`applicable_isas`（集合）、`mode`（single/differential）、`cost`（cheap/expensive）。
- **FR-015**: 流水线 MUST 在 build 节点按"选中 checker → 绑定 ISA"展开编译/执行矩阵。
- **FR-016**: 对 differential 类 checker，系统 MUST 强制全跑其绑定的全部 ISA、不允许剪枝。
- **FR-017**: 廉价（cheap）静态 checker MUST 默认全开、不进 agent 决策；只有昂贵（expensive）checker 才由 Generator 路由。
- **FR-018**: Generator 的 checker 选择 MUST 遵循超集原则（宁可多选多跑，不可漏选）；选错只影响昂贵 checker 是否运行，不影响正确性。

**Oracle 复用与裁决**

- **FR-019**: 系统 MUST 原样复用现有 oracle 框架（机制聚合器 + checker，四态 verdict、NotApplicable 透明）作为 ground truth。
- **FR-020**: 系统 MUST 仅在现有 oracle 之上新增声明式属性（checker 绑哪些 ISA、single/differential、cheap/expensive），不改动 oracle 的零假阳性裁决逻辑。
- **FR-021**: 一个 bug 上报 MUST 落到某个确定性 checker 的 Fail 判定或一次可复现的执行差分上；agent 单独声称"这是 bug"不足以上报。
- **FR-022**: 覆盖率 MUST 由 coverage 节点强制测量并写入共享状态，MUST NOT 作为 agent 可自报的 tool。

**反馈 agent**

- **FR-023**: 反馈 agent MUST 是上下文隔离的 subagent，使 Generator 的上下文不被其中间轨迹污染。
- **FR-024**: 反馈 agent MUST 在 oracle 返回 Pass/NotApplicable 时被调用，接收覆盖增量与 verdict，产出下一轮引导写回共享状态。

**最小化 agent**

- **FR-025**: 最小化 agent MUST 在 oracle 返回 Fail 时被调用，读取共享状态里的确定性 bug 证据产出最小化 PoC。
- **FR-026**: 最小化 MUST 以确定性 delta-debugging（creduce）为主力，LLM 仅做语义引导，最小化结果 MUST 仍触发原 bug。

### Key Entities *(include if feature involves data)*

- **共享状态 / Blackboard**: 流水线持有并版本化的结构化状态。属性：种子语料与家系、覆盖率累积、oracle verdict 历史、反馈引导。是所有 agent 联动的唯一通道。
- **种子 / Seed**: Generator 产出的 C 源码及其家系（parent/lineage）、其被选中的 checker 集。
- **Checker（带元数据）**: 现有不变量 checker，新增声明式属性 `applicable_isas`、`mode`(single/differential)、`cost`(cheap/expensive)。
- **Verdict**: oracle 对每个 checker 的四态结果（Pass / Fail / NotApplicable / Error），构成 verdict 历史。
- **引导 / Guidance**: 反馈 agent 提炼覆盖增量与 verdict 后写回共享状态、供下一轮 Generator 消费的下一步方向。
- **PoC（最小化产物）**: 最小化 agent 在违反分支输出的、仍触发原 bug 的缩小后种子，交人审。
- **编排器 / Orchestrator**: 确定性骨架，唯一调度者，按固定时序推进并唤起 agent。

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 在不接入反馈 agent / 最小化 agent 的情况下，仅用 Generator + 三个确定性节点即可跑通完整的单轮闭环（generate→build→coverage→oracle→路由），证明骨架是可独立运行的 MVP。
- **SC-002**: agent 的输出空间从"语义 × ISA"塌成"语义"——Generator 输出中 100% 不含 ISA 维度，ISA 全部由 checker 元数据查表得出。
- **SC-003**: 锁定共享状态某版本重放时，在输入确定的前提下轨迹可复现（同输入产出可比对），重放路径不依赖任何 agent 间直连通道。
- **SC-004**: 每条联动边（至少含"反馈 agent → Generator"、"覆盖率反馈 开/关"、"oracle grounding 开/关"、"checker 路由 vs. 笛卡尔积全跑"）均可被单独开关并度量其影响。
- **SC-005**: 任一 bug 结论均可在共享状态中追溯到一条确定性证据（checker verdict 或可复现执行差分），追溯成功率 100%。
- **SC-006**: differential 类 checker 选中后其绑定 ISA 的全跑率为 100%（无静默剪枝），守住跨 ISA 静默失效信号。
- **SC-007**: 相比现有覆盖率驱动 fuzz loop，在"挖到静默失效 bug"指标上可做出量化对照（差距可被实验度量，用于论证 agentic 的必要性）。
- **SC-008**: 在 Generator 故意漏选某昂贵 checker 的注入实验中，正确性不受影响（廉价 checker 全开 + NotApplicable 兜底，无错误的"无 bug"结论）。

## Assumptions

- 现有 `internal/oracle` 的四态 verdict 框架、`internal/coverage`、`internal/compiler` 等确定性组件可被复用，本方案在其上加声明式元数据与编排层，不重写其裁决逻辑。
- 不变量调研档案（`docs/tech-docs/invariants/`）已按 ISA 锚定，可作为 checker `applicable_isas` 元数据的声明式来源，无需运行时推算。
- 跨 ISA 执行依赖现有 QEMU user-mode 路径（`internal/seed_executor`），ISA 可用性以环境为准。
- LLM provider 经 Python 侧 provider 模块（langchain/langgraph，`orchestrator/defuzz_loop/llm/provider.py`）接入；Go 侧 `internal/llm` 已删除，LLM 全走 Python。"可复现"指锁定共享状态输入版本，LLM 输出本身的非确定性不在本方案保证范围内（属可复现边界的设计点）。
- 本 spec 聚焦结构与职责；三个 agent 的 tool 接口/prompt 结构、共享状态 schema/版本化/读写契约、checker 元数据字段定义与注册方式、Generator 选 checker 的输出 schema、subagent 协议、broad-sweep 兜底抽样触发策略，以及与现有 `internal/fuzz`/`internal/oracle`/`internal/coverage` 的具体改造点与迁移路径，留待后续 plan / ADR / spec 展开。
- 覆盖率仍是有用的推进信号，但不再独自驱动主循环，而是作为喂给 Generator 的反馈信号之一。
