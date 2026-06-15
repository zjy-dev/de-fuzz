---
title: "Agentic Loop Redesign"
description: 用 agentic loop 替代 fuzz loop 的高层方案——显式编排的确定性流水线，外加三个各带 tools 的 agent，checker 绑定 ISA 做种子路由
priority: HIGH
last_updated: 2026-06-15
status: Implemented
related_docs:
  - ./overview.md
  - ./oracle-mechanism-framework.md
  - ./decisions/003-oracle-multi-invariant-redesign.md
  - ../invariants/README.md
  - ../../../specs/002-agentic-loop-redesign/spec.md
  - ../../../specs/002-agentic-loop-redesign/plan.md
---

# Agentic Loop Redesign

> **本文性质**：高层方案，讲结构和职责。落地实现见 `specs/002-agentic-loop-redesign/`（spec/plan/tasks）：Python (LangGraph) 编排 + Go core 双适配器（gRPC 确定性节点 + MCP agent tools），LLM 全经 Python provider。

## 1. 动机

现在的系统是一条覆盖率驱动的 fuzz 主循环：在没覆盖到的基本块里挑目标，让 LLM 把相邻的已覆盖 seed 改写成能命中目标的新 seed，编译出来再交给 oracle 判不变量。它有两个毛病：

1. **fuzz 效果差**。覆盖率是有用的推进信号，但它只管"代码走到没走到"，管不了"防御机制还满不满足安全契约"这层语义。光靠它，种子在防御逻辑附近的探索效率很低，大量做无效游走。
2. **维度爆炸**。现在按"机制 × ISA"做笛卡尔积下发，每颗种子都得在所有 ISA 上重编重跑一遍。后果是实验量过大、系统整体 token 效率太低，而且会有跟这颗种子根本不相关的 ISA checker 被迫陪跑，搜索被无关组合稀释。

我们已经验证过：之前只让 agent 拿着源码，就挖到了真实 bug。所以这个提案要做的就是：把 fuzz 循环里"生成种子"那一环换成 agent，oracle 原样复用做 ground truth，ISA 调度从笛卡尔积收进 agent 的语义判断里。

## 1.5 护城河：ReAct 的归 agent，确定性的归编排

我们看重 agent 的 ReAct-loop 范式——让 agent 在生成种子、提炼反馈、缩小 PoC 这些语义活上自己边想边试、调 tool、根据结果再调整。这种自主性正是它能挖到 bug 的原因，我们不砍它。

但 FuzzAgent、Claude Code 那类系统把**一切**都交给一个 agent 自由发挥，连流程往哪走、什么时候编译、什么时候判 bug 都由它临场决定。代价是轨迹没法复现，ablation 做不了，reviewer 也说不清到底是哪一步起了作用。

我们的取舍是分层：**ReAct 的归 agent，确定性强的环节归显式编排**。build、coverage、oracle 这些有确定答案的步骤，写死在一条流水线里，按固定顺序走；agent 只在固定的几个位置被调用，在那里尽情 ReAct，但不掌管流程往哪走。agent 之间也不直接对话，要传的东西都写进一块共享状态（见 §3.5），下一个环节再从那里读。

这种分层不是把 agent 的能力砍掉，而是换来三件对发论文很要紧的事：轨迹能复现、每条联动能单独开关做 ablation、bug 结论能往回追溯。它让"用 agent 挖洞"从一次性的 demo 变成一套站得住的方法。

## 2. 设计原则

四条，贯穿全文：

- **流水线是确定性的，编排写死**。generate → build → coverage → oracle → 路由，顺序固定，可复现。这和 Orion 等近期工作的共识一致：LLM 做语义引导，确定性工具做验证。
- **agent 只在固定位置被调用**，做语义判断。流水线掌握编排权，对 agent 内部怎么想的不关心。
- **agent 之间不直接对话**，只通过共享状态传递。这样每一跳都经过一个能记录、能重放、能单独开关的通道。
- **正确性由确定性证据守住**。agent 说"这是 bug"不算数，最后必须落到某个确定性 checker 的判定、或一次能复现的执行差分上才允许上报。agent 负责提议，oracle 负责裁决。

这四条同时治两个毛病：agent 推理补上语义引导力（治痛点 1），checker 路由加 ISA 绑定收掉笛卡尔积（治痛点 2），而 oracle 零假阳性的本钱一点没动。

## 3. 总体结构

整条流水线由一个**显式编排器**驱动，按固定顺序往下走。中间三个环节是传统的确定性节点（build、coverage、oracle），另外三处会调用 agent 去做语义判断。agent 各自带着自己的 tools，做完把结果交回流水线，不自己往下推流程，也不互相调用。

下图按单轮迭代的时序展开。竖线是流水线的推进，向右的箭头是它在某一步调用 agent（标了传入和传出）：

```
显式编排的确定性流水线                          被调用的 agent (各带 tools)
══════════════════════                        ════════════════════════

读共享状态(上轮反馈 + 语料 + 覆盖率)
        │
        │  调用 ── 传入: 反馈/语料/覆盖率 ──▶  ┌──────────────────────┐
        │                                      │  Generator            │
        │  ◀── 传出: 新种子 + 选中 checker 集 ─ │  tools: 源码检索、     │
        ▼                                      │         不变量查询      │
   build  (节点: 按 checker→ISA 编译)          └──────────────────────┘
        │
        ▼
   coverage  (节点: 强制测量, 写回共享状态)
        │
        ▼
   oracle  (节点: 跑 checker 得 verdict)
        │
        ├─ 未违反 ─▶ 调用 ─ 传入: 覆盖增量+verdict ─▶ ┌──────────────────┐
        │                                              │  反馈 agent        │
        │           ◀──── 传出: 下一轮引导 ──────────── │  tools: 覆盖率 diff │
        │                 (写回共享状态, 回到开头)        └──────────────────┘
        │
        └─ 违反 ───▶ 调用 ─ 传入: 确定性 bug 证据 ──▶  ┌──────────────────┐
                                                        │  最小化 agent      │
                    ◀──── 传出: 最小化 PoC ──────────── │  tools: creduce、  │
                          (写回共享状态, 交人审)          │         编译/执行    │
                                                        └──────────────────┘

        ▲ 三个 agent 之间没有横向箭头：它们只跟流水线打交道，靠共享状态间接联动
```

看图的几个点：

- build、coverage、oracle 是确定性节点，不调用任何 agent。只有开头、未违反、违反这三处会调 agent。
- 每次调用都是一对箭头：流水线传入上下文，agent 传出产物。联动关系就这几对，数得清。
- 闭环走法：反馈 agent 的引导写回共享状态，回到开头，进入下一轮。违反那一支是终点，PoC 交给人。
- 反馈 agent 的产出不会直接递给 Generator，而是先落到共享状态，下一轮开头再被读出来。这就是"靠共享状态联动，不走 agent 直连"。

三个 agent 各自的活：

- **Generator**（开头调用）。带源码检索、不变量查询两类只读 tool，产出新种子和它选中的 checker 集。覆盖率不做成 agent 能自报的 tool——它由 coverage 节点强制测出来写进共享状态，下一轮再喂回去，免得 agent 伪造覆盖指标。
- **反馈 agent**（未违反时调用）。一个上下文隔离的 subagent，带覆盖率 diff 之类的 tool，把覆盖增量和 oracle 的 NotApplicable/Pass 结果提炼成下一轮的引导，写回共享状态。隔离是为了让 Generator 的上下文保持干净，不算单独的贡献点。
- **最小化 agent**（违反时调用）。读流水线记在共享状态里的确定性 bug 证据来缩小 PoC，加快人审。主力是确定性的 delta-debugging（creduce），LLM 只在旁边做语义引导，免得纯靠 LLM 删着删着把触发 bug 的关键结构删掉、变成另一个 bug。

### 3.5 联动通道：共享状态（blackboard）

这块共享状态是整个系统跟"自由发挥型"系统拉开差距的地方，也是 §1.5 那条护城河真正落地的位置。

联动是真有的，是个完整闭环：反馈 agent 给出引导，Generator 下一轮拿它生成，oracle 判完，没违反就回到反馈 agent，违反就转给最小化 agent。信息一直在流，没有哪一环被切断。

但所有联动都走共享状态，不走 agent 直连。每个 agent 只对这块状态做"读输入、写产出"，彼此不互发 prompt。状态由流水线持有并版本化，至少装这些：种子语料和家系、覆盖率累积、oracle verdict 历史、反馈 agent 写回的引导。

这样换来三件事，正是护城河：

1. **可复现**。锁定共享状态的某个版本，任一 agent 的输入就完全确定，轨迹能重放。
2. **可 ablation**。每条联动边（比如"反馈 agent → Generator"）都能单独关掉做对照。
3. **可审计**。一个 bug 结论能顺着共享状态回溯到具体的确定性证据。

## 4. 核心机制：种子按 checker 路由，checker 绑定 ISA

这是相对"让 agent 直接挑 ISA"的关键改进。

### 4.1 思路

不让 agent 去碰 ISA 这个裸维度，而是：

- **每个 checker 静态绑定它适用的 ISA 集合**，外加它是单 ISA 专属还是跨 ISA 差分。这绑定来自不变量调研档案——那里本来就是按 ISA 锚定的，是声明式元数据，不是运行时算出来的。
- **agent 只回答它真正擅长的语义问题**："这颗种子的结构，能触发哪些 checker？" 它选中一个 checker，就自动认领了这个 checker 绑定的全部 ISA。ISA 这个维度它从头到尾不碰。

### 4.2 为什么更好

- **少一个自由维度**。agent 的输出空间从"语义 × ISA"塌成"语义"，幻觉面更小，更好复现，更好 ablation。
- **差分信号不靠 agent 自觉**。同一条防御契约在 x86 有效、在 AArch64 悄悄失效，这种跨 ISA 静默失效是本项目最值钱的一类 bug。差分类 checker 自带"绑多个 ISA + 差分判定"的语义，agent 一旦选中就强制全跑，没法剪枝，差分信号由元数据兜着。
- **关注点分开**。ISA 调度变成 checker 的确定性副产品，由流水线查表编排，agent 跟它解耦。

### 4.3 兜底：防止 agent 选错 checker 漏检

agent 选 checker 是个性能优化——省掉跑昂贵 checker 的无谓开销，不是正确性的把关。两道防线：

1. **廉价静态 checker 默认全开，不进 agent 决策**。ELF 符号、指令模式这类毫秒级检查成本近乎为零，而且会诚实返回 NotApplicable，全跑就行。只有昂贵 checker（动态二分、跨 ISA 执行）才值得让 agent 路由。
2. **agent 选的是超集，不是精确集**。宁可多选多跑，不能漏选。选择标准是"排掉明显不可能的，剩下都留着"。

这样就算 agent 路由判断错了，影响的也只是昂贵 checker 跑没跑，正确性始终由"静态 checker 全开 + checker 自带 NotApplicable"这一层兜住。

## 5. 与现有系统的关系

- **oracle 复用**。现有的"机制聚合器 + checker（四态 verdict、NotApplicable 透明）"框架原样保留做 ground truth。这个提案只在它上面加三类声明式属性：checker 绑哪些 ISA、单 ISA 还是差分、廉价还是昂贵。
- **主循环被替换**。覆盖率驱动的目标选择和变异循环，让位给"Generator 生成 + 流水线编排"。覆盖率仍然有用，只是不再独自驱动循环，而是作为喂给 Generator 的反馈信号之一。
- **配置简化**。原来"机制 × ISA"的笛卡尔积下发，被"agent 选 checker、checker 自己带着 ISA"取代。

## 6. 论文贡献定位

| 贡献 | 强度 | 落点 |
| --- | --- | --- |
| Oracle 填补"静默失效"的预言机空白（Logic 层 sanitizer，给 agentic 挖洞提供去假阳性的 ground truth） | 主线 | §2、§3 |
| 防御机制安全不变量的系统化（taxonomy + 形式化方法 + 每条可机械判定） | 支撑 | §4、复用 invariants 档案 |
| 编排显式的 agent 系统：和"自由发挥型"（FuzzAgent / Claude Code）相反，流水线掌握编排权、agent 经共享状态联动，换来可复现 / 可 ablation / 可审计 | 护城河 | §1.5、§3.5 |
| Agentic loop：被预言机 grounding 的、覆盖率引导的、针对静默失效的种子生成与 ISA 路由 | 载体（新颖点在"问题 + grounding + ISA 路由 + 显式编排"，不在 agent plumbing） | §3、§4 |

## 7. 暂不展开（待后续 ADR / spec）

- 三个 agent 各自的 tool 接口和 prompt 结构；
- 共享状态的 schema、版本化、读写契约；
- checker 元数据的字段定义和注册方式；
- Generator 选 checker 的输出 schema，以及流水线怎么消费它；
- 反馈 agent、最小化 agent 的 subagent 协议；
- 廉价 broad-sweep 兜底抽样的触发策略；
- 跟现有 `internal/fuzz`、`internal/oracle`、`internal/coverage` 的具体改造点和迁移路径。

## 8. 待验证的实验假设

1. agentic loop 对现有 fuzz loop，在"挖到静默失效 bug"上差多少（证明为什么要 agentic）。
2. ablation：覆盖率反馈开/关、oracle grounding 开/关、各条联动边开/关（比如反馈 agent → Generator）、checker 路由对笛卡尔积全跑。
3. 显式编排的实证：同一共享状态版本下轨迹能不能复现，以及"显式编排对自由编排"在稳定性和命中率上的对照。
4. 真实 bug 兑现：已经挖到的 GCC/LLVM bug 拿到上游确认 / CVE。
