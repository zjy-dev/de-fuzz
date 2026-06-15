# MCP Tools Contract: agent 只读 tool

agent 在 ReAct 里经 MCP 调用的 tool。MCP server 与 gRPC server 在同一 Go 进程（`cmd/defuzz-core`），link 同一套 `internal/` 包。

**通用约束**：
- 所有 tool 均为**只读**——不改 blackboard、不触发副作用测量。
- 每次 tool 调用与返回都记入 `Blackboard.tool_call_log`（checkpoint），保证 replay/审计不断档（research R5）。
- tool **不暴露裁决能力**：判 bug 只能由 gRPC OracleService 做（FR-021）。

---

## Generator 的 tool

### `search_source`
源码检索（只读）。

- **输入**: `{ query: string, scope?: string }`
- **输出**: `{ matches: [{ path, line, snippet }] }`
- **用途**: Generator 理解被测防御实现的结构，辅助构造能触发目标 checker 的种子。

### `query_invariants`
不变量查询（只读，读 SSOT 元数据视图）。

- **输入**: `{ checker_id?: string, mechanism?: string }`
- **输出**: `{ checkers: [{ id, applicable_isas, mode, cost, category, description }] }`
- **用途**: Generator 回答"这颗种子结构能触发哪些 checker"。**注意**：返回含 `applicable_isas` 仅供 Generator 理解语义，Generator 输出**只选 checker、不选 ISA**（FR-013）；ISA 展开由 Python `routing.py` 据同一元数据完成。
- **SSOT**: 数据来自 `internal/oracle/metadata.go`，与 gRPC `CheckerMetadataService` 同源（R4）。

---

## 反馈 agent 的 tool

### `coverage_diff`
覆盖率 diff（**严格只读**，research R6 / FR-022）。

- **输入**: `{ base?: string, new?: string }`（编排器传入 coverage 节点已测好的 gcovr JSON 报告；不接受触发测量的参数）
- **输出**: `{ delta: {...}, cumulative_summary: {...} }`
- **数据源**: 只读编排器传入的 `CoverageState.last_delta` / `cumulative`，即 coverage 节点已测好的结果。`base`/`new` 即这两份已测报告，tool 仅做 diff，不自行测量。
- **物理保证**: 该 tool 端点无测量代码路径，无法自行触发 build/run。

---

## 最小化 agent 的 tool

### `creduce_run`
确定性 delta-debugging（主力，FR-026）。

- **输入**: `{ source: string, interestingness_cmd: string }`
- **输出**: `{ reduced_source: string, iterations: int }`
- **说明**: LLM 仅做语义引导，最小化由 creduce 确定性执行；interestingness 脚本绑定"仍触发原 failing_checker"。

### `compile_exec`
编译并执行候选 PoC，校验是否仍触发原 bug（只读式验证，不写 blackboard）。

- **输入**: `{ source: string, isa: string, checker_id?: string }`
- **输出**: `{ exit_code: int, stdout: string, stderr: string, still_triggers: bool }`
- **说明**: 复用 `internal/compiler` + `internal/seed_executor`；`still_triggers` 由再跑一次对应 checker 判定（防止删成另一个 bug，FR-026）。`checker_id` 指定原 failing checker：`still_triggers` 为 true 当且仅当该 checker 再次 Fail；省略时退化为"任一 checker Fail 即算仍触发"。

---

## MCP ↔ gRPC 边界总结

| 调用者 | 协议 | tool / service | 语义 |
|---|---|---|---|
| 编排器（确定性、写死顺序） | gRPC | Build / Coverage / Oracle / CheckerMetadata | RPC，节点 |
| agent（LLM 临场决定） | MCP | search_source / query_invariants / coverage_diff / creduce_run / compile_exec | tool-call，ReAct |

两者读同一 Go core 的同一份 checker 元数据（SSOT），但能力面不同：gRPC 侧能裁决，MCP 侧全部只读。
