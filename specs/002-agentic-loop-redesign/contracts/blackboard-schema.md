# Blackboard Schema Contract (LangGraph checkpointed state)

Python `orchestrator/defuzz_loop/state.py` 的 pydantic 定义契约。整个 state 由 LangGraph checkpointer 版本化，是三个 agent 的唯一联动通道（FR-006/008）。

## 顶层 State

```python
class Blackboard(BaseModel):
    round: int = 0
    corpus: list[Seed] = []
    coverage: CoverageState = CoverageState()
    verdict_history: list[OracleVerdict] = []
    guidance: Guidance | None = None
    tool_call_log: list[ToolCall] = []      # MCP 调用轨迹（R5）
    ablation_flags: AblationFlags = AblationFlags()
    pending_bug: BugEvidence | None = None
    # 本轮临时（写回后清空）
    current_seed: Seed | None = None
    build_matrix: BuildMatrix | None = None
    build_artifacts: list[BuildArtifact] = []
    last_verdict: OracleVerdict | None = None
```

## 写权限矩阵（强制约束）

| 字段 | 唯一可写者 | 规则来源 |
|---|---|---|
| `corpus` | Generator 节点 / 编排器 | FR-007 |
| `coverage` | **仅** coverage 节点 | FR-022（agent 路径写入即违规） |
| `verdict_history` | oracle 节点 | FR-007 |
| `guidance` | **仅**反馈 agent | FR-008（下一轮 Generator 只读） |
| `tool_call_log` | tool 调用封装层 | R5 |
| `pending_bug` | oracle 节点（违反时） | FR-025 |
| `build_matrix` | routing（查表，非 agent） | FR-013/015 |

校验：编排器在每个节点出口断言写权限矩阵，越权写入抛错（测试 `test_blackboard.py` 覆盖）。

## 版本化与 replay 契约

- 每次节点/agent 出口产生一个新 checkpoint（thread_id + checkpoint_id）。
- 锁定某 checkpoint_id → 该点之前的 state 完全确定 → 重放下游（FR-009 / SC-003）。
- replay 重放 `tool_call_log` 与 LLM 请求/响应记录，而非逐 token 重算（research R7）。

## ablation 契约（SC-004）

`AblationFlags` 的每个布尔对应一条联动边；编排器在路由处读 flag 决定是否走该边：

```python
class AblationFlags(BaseModel):
    feedback_to_generator: bool = True
    coverage_feedback: bool = True
    oracle_grounding: bool = True
    checker_routing: bool = True   # False → 退回全 ISA 笛卡尔积全跑
```

关闭任一边，其余流程仍推进，影响可单独度量。

## 路由契约（checker→ISA，FR-013/015/016）

`routing.py` 消费 gRPC `CheckerMetadataService` 拉取的元数据：

1. `current_seed.selected_checkers`（agent 输出，**不含 ISA**）∪ 所有 `cost=cheap` checker（默认全开，FR-017）。
2. 对每个 checker 查 `applicable_isas`，笛卡尔展开成 `BuildMatrix.cells`。
3. `mode=differential` 的 checker → 加入 `forced_full`，其 ISA 不可剪枝（FR-016）。
4. `ablation_flags.checker_routing=False` → 忽略 agent 选择，全 checker × 全 ISA 全跑（对照组）。
