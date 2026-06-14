# Phase 1 Data Model: Agentic Loop Redesign

实体来自 spec.md §Key Entities，加上 plan/research 决定的字段。所有"跨 agent 流动"的实体都是 blackboard 的子结构，由 LangGraph checkpointer 版本化。

## 核心实体

### Blackboard（编排状态根）

整个 LangGraph state 的根。由编排器持有、版本化；agent 只读输入、写产出，不互发消息。

| 字段 | 类型 | 说明 | 来源 FR |
|---|---|---|---|
| `round` | int | 当前迭代轮次 | FR-001 |
| `corpus` | list[Seed] | 种子语料与家系 | FR-007 |
| `coverage` | CoverageState | 累积覆盖率（仅 coverage 节点可写） | FR-007, FR-022 |
| `verdict_history` | list[OracleVerdict] | oracle 裁决历史 | FR-007 |
| `guidance` | Guidance \| null | 反馈 agent 写回、下一轮 Generator 读 | FR-007, FR-024 |
| `tool_call_log` | list[ToolCall] | agent 的 MCP tool 调用轨迹（replay/审计用） | FR-009, R5 |
| `ablation_flags` | AblationFlags | 各联动边开关 | FR-010 |
| `pending_bug` | BugEvidence \| null | 违反分支待最小化的确定性证据 | FR-011, FR-025 |

**状态不变量**：
- `coverage` 只能由 coverage 节点写（FR-022）；任何 agent 路径写入即违规。
- `guidance` 只能由反馈 agent 写、只能由下一轮 Generator 读（FR-008：不在 agent 间直传）。
- 每次写入产生新 checkpoint 版本；锁定版本号即可重放（FR-009）。

### Seed（种子）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | str | 唯一标识 |
| `source` | str | C 源码（Generator 产出） |
| `parent_id` | str \| null | 家系/lineage |
| `selected_checkers` | list[str] | Generator 选中的 checker ID 集（**不含 ISA**，FR-013） |
| `origin` | enum{generator, initial, minimized} | 产生来源 |

### CheckerMetadata（元数据，SSOT — Go 侧权威）

定义在 `internal/oracle/metadata.go`，gRPC oracle 节点与 MCP 不变量查询 tool 共读（R4）。Python 侧只持只读副本。

| 字段 | 类型 | 说明 | 来源 FR |
|---|---|---|---|
| `id` | str | checker ID，如 `INV-SP-L01` | FR-014 |
| `applicable_isas` | set[str] | 适用 ISA 集合 | FR-014 |
| `mode` | enum{single, differential} | 单 ISA / 跨 ISA 差分 | FR-014, FR-016 |
| `cost` | enum{cheap, expensive} | 决定是否进 agent 路由 | FR-014, FR-017 |
| `category` | enum{static, dynamic} | 复用现有 `InvariantCategory` | 现状 |

**规则**：
- `cost=cheap` → 默认全开，不进 Generator 决策（FR-017）。
- `cost=expensive` → 由 Generator 路由，遵循超集原则（FR-018）。
- `mode=differential` → 选中后 `applicable_isas` 全跑、不可剪枝（FR-016）。

### BuildMatrix（路由展开产物，Python 侧）

由 `routing.py` 在 build 节点前查表生成，非 agent 产出。

| 字段 | 类型 | 说明 |
|---|---|---|
| `cells` | list[(checker_id, isa)] | 选中 checker × 其绑定 ISA 的笛卡尔展开 |
| `forced_full` | set[checker_id] | differential checker，标记强制全跑 |

### CoverageState

| 字段 | 类型 | 说明 |
|---|---|---|
| `cumulative` | map | 累积覆盖（由 coverage 节点写） |
| `last_delta` | map | 本轮增量（供反馈 agent 只读 diff，R6） |

### OracleVerdict

复用现有四态（`internal/oracle/invariant.go`：Pass/Fail/NotApplicable/Error）。

| 字段 | 类型 | 说明 |
|---|---|---|
| `seed_id` | str | 关联种子 |
| `results` | list[InvariantResult] | 每 checker × ISA 的结果 |
| `aggregate` | enum{violated, not_violated} | Fail→violated；其余→not_violated（R8） |

`InvariantResult` 沿用 Go 现有 schema：`id / category / verdict / evidence / detail / reason`。

### Guidance（反馈引导）

| 字段 | 类型 | 说明 |
|---|---|---|
| `round` | int | 产生轮次 |
| `summary` | str | 覆盖增量 + verdict 提炼的下一步方向 |
| `coverage_delta_ref` | ref | 指向 CoverageState.last_delta |

### BugEvidence（违反分支输入）

| 字段 | 类型 | 说明 |
|---|---|---|
| `seed_id` | str | 触发种子 |
| `failing_checker` | str | 给出 Fail 的 checker ID |
| `isa` | str | 触发的 ISA |
| `evidence` | str | 确定性证据（checker Evidence 或执行差分） |

### MinimizedPoC（最小化产物，终点）

| 字段 | 类型 | 说明 |
|---|---|---|
| `original_seed_id` | str | 来源 |
| `reduced_source` | str | creduce 后仍触发原 bug 的源码 |
| `still_triggers` | bool | 校验仍触发同一 checker Fail |

### AblationFlags

每条联动边一个布尔，支撑 FR-010 / SC-004。

| 字段 | 默认 | 关闭效果 |
|---|---|---|
| `feedback_to_generator` | on | 反馈 agent 不写 guidance |
| `coverage_feedback` | on | 不喂覆盖率给 Generator |
| `oracle_grounding` | on | （对照实验）退化裁决 |
| `checker_routing` | on | 关→退回全 ISA 笛卡尔积全跑 |

## 状态流转（单轮）

```
read state ─► Generator(产 seed+checker集) ─► routing(查表→BuildMatrix)
   ─► build ─► coverage(强制测量,写 coverage) ─► oracle(裁决,写 verdict_history)
   ─► 路由：
        aggregate=not_violated ─► 反馈 agent(写 guidance) ─► round+1, 回 read state
        aggregate=violated     ─► 写 pending_bug ─► 最小化 agent(写 MinimizedPoC) ─► 终止(交人审)
```

每个箭头产生一次 checkpoint 写入；锁定任一版本可重放该轮（FR-009 / SC-003）。
