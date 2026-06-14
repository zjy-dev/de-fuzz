# Phase 0 Research: Agentic Loop Redesign

本文消解 plan.md Technical Context 中的设计未决项。每条采用 Decision / Rationale / Alternatives 三段式。

## R1. 编排语言：Python + LangGraph

**Decision**: Python 3.12 + LangGraph 承载显式编排骨架、三个 agent、blackboard。

**Rationale**:
- 用户从可读性角度选 Python；LLM/agent/研究复现生态在 Python 最厚。
- LangGraph 的 checkpointer 天然实现方案 §3.5 的"共享状态版本化 + replay"，无需自造状态机。
- 显式 graph（节点 + 固定边 + 条件路由）正好把"编排权归骨架"写进代码结构，而非 prompt。

**Alternatives considered**:
- TypeScript + LangGraph.js：强类型更适合 schema，但 checkpointer / human-in-loop / 细粒度持久化落后 Python 一个身位，研究复现脚本生态也弱。
- 自写 Python 状态机（不用 LangGraph）：要自造 checkpoint/replay/可视化，重复造轮子。

## R2. 确定性节点接入：gRPC 常驻 Go 服务

**Decision**: build / coverage / oracle 由编排器经 gRPC 同步调用一个常驻 Go 服务。

**Rationale**:
- 调用者是编排器（确定性、顺序写死）→ RPC 语义最贴。
- 常驻服务消除 per-call 进程 spawn 冷启动；30+ checker 累积开销显著。
- 强类型 proto 契约可版本化，对齐"可审计"。
- 复用 `internal/oracle` 已验证的四态 verdict / 零假阳性框架，不碰裁决逻辑。

**Alternatives considered**:
- Go CLI 子进程（JSON in/out）：实现最快但每次 spawn + 冷启动，契约 ad-hoc。保留为 MVP 回退。
- 纯 Python 重写 oracle：用户明确否决——重写 30+ checker 风险高、易引入假阳性。

## R3. agent tool 接入：MCP server（同一 Go core）

**Decision**: agent 只读 tool（源码检索、不变量查询、creduce / compile-exec）由 agent 在 ReAct 里经 MCP 调用，MCP server 与 gRPC server link 同一套 `internal/` 包。

**Rationale**:
- 调用者是 LLM（临场决定）→ MCP 是 tool-calling 原生协议，LangGraph 有现成 MCP client 接入。
- "一个 Go core 两张脸"避免逻辑双写；尤其 checker 元数据只有一份。

**Alternatives considered**:
- 把 agent tool 也走 gRPC：丢掉 MCP 的 LLM 原生 tool 生态，需自造 tool-calling 适配。
- 把确定性节点也走 MCP：会把"骨架调用"误建模成"agent 自调"，破坏 §1.5 的编排权归属。

## R4. checker 元数据单一真相源（SSOT）— 已确认约束 (1)

**Decision**: 在 `internal/oracle/metadata.go` 集中声明每个 checker 的 `applicable_isas`(集合) / `mode`(single/differential) / `cost`(cheap/expensive)。gRPC 的 oracle 节点与 MCP 的"不变量查询"tool 读同一份注册表。

**Rationale**:
- §4 的"checker 绑定 ISA"路由与 oracle 裁决必须基于同一份元数据，否则 Generator 选的 checker 集与流水线展开的 ISA 矩阵会对不上。
- 元数据来源是 `docs/tech-docs/invariants/*.md`（已按 ISA 锚定），声明式、非运行时算出。

**实现注意**:
- 现有 checker 经 `mechanism()` 注册（见 `oracle-mechanism-framework.md` §7）。元数据应与 checker ID（如 `INV-SP-L01`）一一绑定，挂在同一注册点，避免两处维护。
- MCP "不变量查询" tool 暴露的是元数据**只读视图**，不暴露裁决能力。

**Alternatives considered**:
- Python 侧另存一份元数据：双写必然漂移，被否决。
- 运行时从 binary 推 ISA：违背"声明式元数据"原则。

## R5. blackboard 与 MCP tool 调用都进 checkpoint — 已确认约束 (2)

**Decision**: blackboard = LangGraph checkpointed state（pydantic schema）。agent 在 ReAct 中发出的每次 MCP tool 调用（及其返回）也记入 checkpoint，与 gRPC 节点调用一视同仁。

**Rationale**:
- 护城河靠可复现/可审计（SC-003 / SC-005）。gRPC 调用由编排器记账天然进 state；MCP 调用发生在 agent 内部，若不显式落账，锁定版本 replay 时 agent 轨迹断档。
- LangGraph 的 tool-call 事件可经 checkpointer 持久化，需在 agent 封装层确保 tool 调用走可记录通道。

**Alternatives considered**:
- 只记 agent 最终产物、不记中间 tool 调用：replay 时无法还原 agent 决策路径，违反可审计。

## R6. 覆盖率 tool 严格只读 — 已确认约束 (3)

**Decision**: 反馈 agent 的"覆盖率 diff" tool 只读 blackboard 里已由 coverage 节点测好的覆盖数据做 diff，**不**触发任何测量。覆盖率测量只发生在确定性 coverage 节点（FR-022）。

**Rationale**:
- 防止 agent 伪造/绕过覆盖指标；覆盖率必须是强制测出的确定性事实。

**实现注意**: tool 定义在 MCP server 侧就限定为只读端点（读取 state 快照传入的覆盖数据），物理上无测量能力。

## R7. 可复现的边界（LLM 非确定性）

**Decision**: "可复现"指**锁定 blackboard 输入版本后，喂给 agent 的输入完全确定，且 agent 的 tool 调用轨迹被记录可重放**。LLM 采样输出本身的非确定性不在保证内；通过记录每次 LLM 请求/响应到 checkpoint 实现"轨迹回放"，而非"逐 token 重算"。

**Rationale**:
- 对齐 spec Assumptions 与 Edge Case："锁定输入 vs. 锁定输出"。SC-003 要求"同输入产出可比对"，靠记录而非重算达成。

**Alternatives considered**:
- 强制 temperature=0 求逐 token 确定：provider 不保证跨版本确定，且削弱探索；放弃作为强约束。

## R8. 失败/降级语义（Edge Cases 落地）

**Decision**:
- **种子编译失败**：build 节点返回失败状态写回 blackboard，跳过 coverage/oracle，作为一类反馈信号交反馈分支（非崩溃）。
- **ISA 不可用（缺 QEMU/工具链）**：该 (checker, ISA) cell 记 `Error`/`NotApplicable`，不污染其他 ISA 的 verdict；差分类 checker 若矩阵不完整，差分判定降级为 NotApplicable 并在 state 标注。
- **agent 失败/超时**：编排器捕获，写回 state 错误并按分支推进（反馈分支回到开头、违反分支终止），不卡死闭环。
- **oracle 返回 Error**：归入"非违反"分支（与 Pass/NA 同侧，FR-021 要求 bug 必须是确定性 Fail）。

**Rationale**: 对齐现有 oracle 的四态语义（`invariant.go`：Pass/Fail/NotApplicable/Error，NA/Error 永不报 bug）与 spec Edge Cases。

## R9. 进程通信与部署形态

**Decision**: 单机两进程：Python 编排进程 + Go core 进程（`cmd/defuzz-core` 同时起 gRPC + MCP 两个 listener）。本地走 UDS / loopback TCP。

**Rationale**: 单机单 run 场景，无需分布式；两 server 同进程共享 `internal/` 内存态（含元数据注册表），强化 SSOT。

**Alternatives considered**: gRPC 与 MCP 拆两进程——会让元数据 SSOT 退化为跨进程同步问题，否决。
