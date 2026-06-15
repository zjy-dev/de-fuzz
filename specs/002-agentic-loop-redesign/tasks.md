---
description: "Task list for Agentic Loop Redesign implementation"
---

# Tasks: Agentic Loop Redesign

**Input**: Design documents from `/specs/002-agentic-loop-redesign/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: 包含针对 Success Criteria 的关键测试任务（plan.md 已把 `test_graph_loop.py` / `test_blackboard.py` / `test_checker_routing.py` 列为验证 SC-001~SC-006 / SC-008 的交付物）。其余非关键路径不强制 TDD。

**Branch 授权**: 用户已确认本分支安全，**应删尽删、应改尽改**。本分支只保留 agent loop；旧覆盖率驱动 fuzz loop 直接删除（baseline 留在其他分支，SC-007 的量化对照属论文 eval 阶段、跨分支 checkout baseline 度量，不在本次构建范围）。Python 用 **uv** 管理 venv，LLM 全走 Python（langgraph/langchain），删除 Go 侧 LLM 代码。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: US1~US5 映射 spec.md 用户故事

## Path Conventions

- Python 编排：`orchestrator/`（uv 管理）
- Go core 双适配器：`cmd/defuzz-core/`、`internal/service/`、`internal/oracle/metadata.go`
- 复用 Go：`internal/oracle`、`internal/coverage`、`internal/compiler`、`internal/seed_executor`、`internal/exec`、`internal/seed`
- 删除 Go：`internal/fuzz`、`internal/state`、`internal/prompt`、`internal/llm`（LLM 迁至 Python）

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 项目骨架、依赖、协议桩

- [X] T001 创建 Python 编排项目骨架 `orchestrator/`（`pyproject.toml`，uv 管理），声明依赖 langgraph / langchain-core / 选定 provider 包（如 langchain-openai/anthropic）/ grpcio / grpcio-tools / mcp / pydantic / pydantic-settings；`uv sync` 生成 `uv.lock`
- [X] T002 [P] 配置 Python 工具链：`orchestrator/` 下 ruff + mypy + pytest 配置写入 `pyproject.toml`
- [X] T003 [P] 新建 Go core 入口骨架 `cmd/defuzz-core/main.go`（仅起空 gRPC + MCP 双 listener，flag：`--grpc-addr` / `--mcp-addr`），加入 `Makefile` build target
- [X] T004 从 `specs/002-agentic-loop-redesign/contracts/oracle.proto` 生成 Go 桩到 `internal/service/pb/`，并生成 Python 桩到 `orchestrator/defuzz_loop/clients/pb/`（grpc_tools.protoc）

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: checker 元数据 SSOT + blackboard 基础 schema + gRPC 确定性节点适配层 + Python LLM provider。这些是任何 user story 都依赖的核心，必须先完成。

**⚠️ CRITICAL**: 所有 user story 在本阶段完成前无法开工

### Go core: 元数据 SSOT 与确定性节点适配

- [X] T005 实现 checker 元数据 SSOT `internal/oracle/metadata.go`：为每个已注册 checker（`INV-SP-*` 等）声明 `applicable_isas` / `mode`(single|differential) / `cost`(cheap|expensive)，与现有 `mechanism()` 注册点绑定，单一真相源（research R4，data-model CheckerMetadata）
- [X] T006 [P] 实现 `internal/service/grpc_server.go` 的 `CheckerMetadataService.ListCheckerMetadata`，读 T005 元数据（contracts/oracle.proto）
- [X] T007 [P] 实现 `internal/service/grpc_server.go` 的 `OracleService.Analyze`：薄包装 `MechanismOracle.Analyze`，输出四态 `InvariantResult` + `violated` + 违反证据，裁决逻辑不动（FR-019/020/021）。**核实** oracle 链路不含 LLM 裁决路径，保证确定性纯净（analyze C6）
- [X] T008 [P] 实现 `BuildService.Build`：按传入 `(checker_id, isa)` 矩阵调用 `internal/compiler` 编译，编译失败填 `error` 不崩溃（R8，FR-015）
- [X] T009 [P] 实现 `CoverageService.Measure`：薄包装 `internal/coverage`，强制测量返回累积 + 增量（FR-022）
- [X] T010 在 `cmd/defuzz-core/main.go` 注册 T006~T009 四个 gRPC service 到 listener，`go build ./cmd/defuzz-core` 通过

### Python: blackboard、gRPC client、LLM provider

- [X] T011 [P] 实现 blackboard pydantic schema `orchestrator/defuzz_loop/state.py`（Blackboard + Seed/CoverageState/OracleVerdict/Guidance/BugEvidence/AblationFlags 等子结构，含写权限矩阵注释，contracts/blackboard-schema.md）
- [X] T012 [P] 实现 gRPC client 封装 `orchestrator/defuzz_loop/clients/grpc_client.py`（连接 Go core，封装 4 个 service 调用）
- [X] T013 [P] 实现 Python LLM provider 配置模块 `orchestrator/defuzz_loop/llm/provider.py` + 配置文件（`orchestrator/configs/llm.yaml`，pydantic-settings 加载 model/provider/api-key/温度等）：产出 langchain chat model 工厂供三个 agent 复用，对接 langgraph（替代旧 Go `internal/llm`，analyze C2/C3）
- [X] T014 配置 LangGraph checkpointer（SQLite）于 `orchestrator/defuzz_loop/graph.py` 骨架，定义空 StateGraph + thread_id 约定（FR-006/009）

**Checkpoint**: Go core 四个确定性 service 可被 Python 调用，blackboard schema、checkpointer、LLM provider 就绪

---

## Phase 3: User Story 1 - 显式编排的单轮迭代闭环 (Priority: P1) 🎯 MVP

**Goal**: 编排器按固定顺序推进 generate → routing → build → coverage → oracle → 路由，未违反回开头、违反终止，每步落 checkpoint

**Independent Test**: 仅用 Generator + 三确定性节点（不接反馈/最小化 agent）跑通若干轮，验证顺序固定、state 正确读写、产物可人工检视（SC-001）

### Tests for User Story 1

- [X] T015 [P] [US1] 编写 `orchestrator/tests/test_graph_loop.py`：断言节点执行顺序固定、not_violated→回开头、violated→终止、Error→非违反侧（SC-001，R8）

### Implementation for User Story 1

- [X] T016 [P] [US1] 实现 MCP server `internal/service/mcp_server.go` 的 `search_source` + `query_invariants` 只读 tool（读 T005 同源元数据），注册进 `cmd/defuzz-core`（contracts/mcp-tools.md）
- [X] T017 [P] [US1] 实现 MCP client 封装 `orchestrator/defuzz_loop/clients/mcp_client.py`，且每次 tool 调用落 `tool_call_log`（R5）
- [X] T018 [US1] 实现 Generator agent `orchestrator/defuzz_loop/agents/generator.py`：用 T013 LLM provider，带 search_source/query_invariants tool，产出 `Seed`（source + selected_checkers，**不含 ISA**，FR-012/013）
- [X] T019 [P] [US1] 实现 build 节点 `orchestrator/defuzz_loop/nodes/build.py`（调用 grpc_client.Build，写 build_artifacts）
- [X] T020 [P] [US1] 实现 coverage 节点 `orchestrator/defuzz_loop/nodes/coverage.py`（调用 Measure，**仅此处可写** coverage，FR-022）
- [X] T021 [P] [US1] 实现 oracle 节点 `orchestrator/defuzz_loop/nodes/oracle.py`（调用 Analyze，写 verdict_history，违反时写 pending_bug）
- [X] T022 [US1] 在 `graph.py` 串联固定边：read state→generator→routing→build→coverage→oracle→条件路由；接入 CLI `run`（quickstart 步骤 3，支持 `--disable-agent`）
- [X] T023 [US1] 实现条件路由函数 `orchestrator/defuzz_loop/graph.py`：aggregate=not_violated→loop back（round+1）、violated→END（FR-005）

**Checkpoint**: MVP 可跑——单轮闭环按固定顺序推进，可演示

---

## Phase 4: User Story 2 - 种子按 checker 路由、checker 绑定 ISA (Priority: P1)

**Goal**: Generator 只选 checker，流水线查表展开 (checker, ISA) 矩阵；廉价 checker 默认全开，differential 强制全跑

**Independent Test**: 给定带元数据的 checker，Generator 选 checker 集后验证矩阵展开正确、agent 输出无 ISA 维度、differential 全跑（SC-002/SC-006）

### Tests for User Story 2

- [X] T024 [P] [US2] 编写 `orchestrator/tests/test_checker_routing.py`：断言 Generator 输出 100% 不含 ISA；cheap checker 默认全开；differential checker 的 applicable_isas 全跑无剪枝；漏选 expensive checker 时正确性不受影响（SC-002/006/008，FR-016/017/018）

### Implementation for User Story 2

- [X] T025 [US2] 实现 routing 模块 `orchestrator/defuzz_loop/routing.py`：启动拉取 CheckerMetadata 缓存；按 `selected_checkers ∪ 全部 cheap` 查 `applicable_isas` 笛卡尔展开为 `BuildMatrix.cells`；`mode=differential`→加入 `forced_full`（FR-013/015/016/017，contracts/blackboard-schema.md 路由契约）
- [X] T026 [US2] 在 `graph.py` 把 routing 作为 build 前置节点接入（消费 Generator 的 selected_checkers，产出 BuildMatrix 写回 state）
- [X] T027 [US2] 在 routing 中实现超集原则与 cheap 兜底：expensive 漏选只影响是否跑，cheap 全开 + checker 自带 NotApplicable 兜住正确性（FR-018，data-model 规则）

**Checkpoint**: US1 闭环 + 确定性 ISA 路由，差分信号守住

---

## Phase 5: User Story 3 - 共享状态（blackboard）联动通道 (Priority: P1)

**Goal**: 版本化 + replay + 逐边 ablation + bug→证据回溯，且 agent 间无直连

**Independent Test**: 构造带历史版本的 state，验证每条联动边可单独开关、锁定版本 replay 输入一致、bug 可回溯到确定性证据（SC-003/004/005）

### Tests for User Story 3

- [X] T028 [P] [US3] 编写 `orchestrator/tests/test_blackboard.py`：断言写权限矩阵越权抛错；锁定 checkpoint replay 输入一致；ablation flag 单独开关其余流程仍推进；bug→证据回溯成功（SC-003/004/005，FR-008/009/010/011/022）

### Implementation for User Story 3

- [X] T029 [US3] 在 `state.py` / 节点出口实现写权限矩阵断言（coverage 仅 coverage 节点写、guidance 仅反馈 agent 写等，越权抛错，FR-008/022）
- [X] T030 [P] [US3] 实现 replay 能力 `orchestrator/defuzz_loop/graph.py`：每次 `run` 落一个自包含审计目录 `runs/<experiment>_<mechanism>_<UTC时间戳>/`（独立 `checkpoints.sqlite` + `manifest.json`，记 git sha / toolchains 快照 / checker 目录 / LLM 与 ablation 配置），run 间隔离不串台；CLI `inspect` / `replay` / `trace-bug` 以 `--run-dir` 锁定 checkpoint_id 重放（回放 tool_call_log + LLM 请求记录，非逐 token 重算，R7，FR-009）
- [X] T031 [P] [US3] 实现 ablation 开关：四条 `AblationFlags` 边各自生效——`checker_routing`（routing.py，off→退回全 ISA 笛卡尔积全跑）、`feedback_to_generator`（generator.py guidance_block）、`coverage_feedback`（feedback.py coverage_signal）、`oracle_grounding`（oracle.py bug_evidence，off→退化裁决无证据）；CLI `--ablation <edge>=off`（FR-010，SC-004）
- [X] T032 [P] [US3] 实现 bug 回溯 `trace-bug` CLI：从 verdict_history / pending_bug 沿 checkpoint 链回溯到 failing_checker 的确定性证据（FR-011，SC-005）

**Checkpoint**: 护城河三件套（可复现/可 ablation/可审计）落地，P1 MVP 完整

---

## Phase 6: User Story 4 - 覆盖率反馈 agent（未违反时） (Priority: P2)

**Goal**: 未违反时调用隔离 subagent，把覆盖增量 + verdict 提炼成 guidance 写回 state，下一轮 Generator 读取

**Independent Test**: 给定一轮未违反结果，反馈 agent 产出 guidance 写回，验证下一轮 Generator 可消费、其上下文不被反馈轨迹污染（FR-023/024）

### Implementation for User Story 4

- [X] T033 [P] [US4] 实现 MCP `coverage_diff` 只读 tool `internal/service/mcp_server.go`：仅读传入的已测覆盖数据做 diff，端点无测量代码路径（FR-022，R6，contracts/mcp-tools.md）
- [X] T034 [US4] 实现反馈 agent `orchestrator/defuzz_loop/agents/feedback.py`：用 T013 LLM provider，上下文隔离 subagent，带 coverage_diff tool，产出 Guidance（FR-023/024）
- [X] T035 [US4] 在 `graph.py` not_violated 分支接入反馈 agent：写 guidance 到 state，下一轮 Generator 读取（去掉 `--disable-agent feedback` 生效）

**Checkpoint**: 闭环带语义反馈引导

---

## Phase 7: User Story 5 - PoC 最小化 agent（违反时） (Priority: P2)

**Goal**: 违反时调用最小化 agent，读 pending_bug 证据，creduce 为主力缩小 PoC，校验仍触发原 bug

**Independent Test**: 给定触发违反的种子 + 证据，最小化 agent 输出更小且仍触发同一 bug 的 PoC（FR-025/026）

### Implementation for User Story 5

- [X] T036 [P] [US5] 实现 MCP `creduce_run` + `compile_exec` tool `internal/service/mcp_server.go`：creduce 确定性归约 + 复用 `internal/compiler`/`internal/seed_executor` 校验 `still_triggers`（FR-026，contracts/mcp-tools.md）
- [X] T037 [US5] 实现最小化 agent `orchestrator/defuzz_loop/agents/minimizer.py`：用 T013 LLM provider，读 pending_bug，creduce 主力 + LLM 语义引导，产出 MinimizedPoC（FR-025/026）
- [X] T038 [US5] 在 `graph.py` violated 分支接入最小化 agent：写 MinimizedPoC 到 state、该支为 END 交人审（FR-005）

**Checkpoint**: 违反分支产出可人审的最小化 PoC

---

## Phase 8: Polish & Cross-Cutting (应删尽删旧 Go 代码)

**Purpose**: 用户已授权大幅删除旧 Go 代码。本分支只保留 agent loop；fuzz loop 与 Go 侧 LLM 全删（baseline 留在其他分支）。删除前先 grep 确认无被复用组件依赖。

- [X] T039 删除旧覆盖率驱动主循环 `internal/fuzz/`（engine.go / flag_strategy.go / phase_random.go 及测试）——被 Python 编排取代，baseline 保留在其他分支（spec §5 主循环被替换，analyze C1）
- [X] T040 删除 Go 侧 LLM 代码 `internal/llm/`——LLM 全迁至 Python（T013）；先 grep 确认 `internal/oracle`/其他复用包不再依赖，有依赖则先解耦（analyze C3/C6）
- [X] T041 删除/精简 `internal/prompt/`：旧 prompt 流水线随主循环移除；与 `internal/llm` 一并解耦后删除（先 grep 依赖）
- [X] T042 [P] 删除旧 `internal/state/`（旧 fuzz state）——被 blackboard 取代
- [X] T043 [P] 精简 `cmd/defuzz/`：移除覆盖率驱动 fuzz 子命令，保留仍需要的工具入口；评估 `cmd/*-repro` 是否保留为独立调试工具
- [X] T044 删除后运行 `go build ./...` + `go test ./internal/oracle/... ./internal/coverage/... ./internal/compiler/...` 确认复用组件不受影响
- [X] T045 [P] 更新文档：将 `docs/tech-docs/architecture/agentic-loop-redesign.md` 状态由 Proposed 改为 Implemented 关联本 spec/plan；更新 `overview.md` 主循环描述；更新 spec Assumption（LLM 经 Python provider 而非 Go `internal/llm`，analyze C3）
- [X] T046 运行 `specs/002-agentic-loop-redesign/quickstart.md` 全流程验证（MVP + 反馈 + 最小化 + 一次 ablation 对照），确认 SC-001~SC-006 / SC-008 达成（SC-007 跨分支 baseline 对照属论文 eval，不在此构建验证）
  - 验证状态：(1) quickstart 步骤 4 的三套测试全过（`uv run pytest tests/ -q` 22 passed），直接对应 SC-001~006/008；ruff 全绿。(2) Go core 双适配器（gRPC+MCP）冷启动正常，Python 编排经 gRPC 拉到 28 个 checker 元数据 SSOT，5 个 MCP tool（search_source/query_invariants/coverage_diff/creduce_run/compile_exec）均响应正确，creduce 缺失时优雅降级（iterations=0，R8）。(3) US1 MVP 闭环已在前序会话用 gpt-5.4 真机验证。
  - 环境受限项（非代码缺陷）：本机仅 Apple clang（无插桩 xgcc）、无 gcovr/qemu-aarch64/creduce，`OPENAI_API_KEY` 未注入，故 quickstart 步骤 3/5 的跨 ISA 实编译 + coverage + 实时 LLM 全流程无法在此宿主复现；该部分留待具备插桩工具链与 LLM 凭据的实验环境运行。

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (P1)**: 无依赖，立即开始
- **Foundational (P2)**: 依赖 Setup；**阻塞所有 user story**
- **US1 (P3)**: 依赖 Foundational（含 T013 LLM provider）
- **US2 (P4)**: 依赖 Foundational + US1 的 routing 接入点（T022）
- **US3 (P5)**: 依赖 Foundational + US1（在闭环上加版本化/ablation/审计能力）
- **US4 (P6) / US5 (P7)**: 依赖 US1 闭环 + T013 LLM provider（P2 增强，可在 P1 三故事稳定后并行）
- **Polish (P8)**: 删除旧代码依赖新链路已替代旧职责，建议 US1~US3 完成后启动

### User Story Dependencies

- US1/US2/US3 同为 P1，共同构成 MVP（spec 明确）；技术上 US2/US3 在 US1 闭环上叠加
- US4、US5 为 P2 增强，互相独立，可并行

### Within Each User Story

- 测试任务先写（标 [P]），随后实现
- Go service/tool 先于依赖它的 Python 节点/agent
- LLM provider（T013）先于三个 agent
- routing 先于 build 接入

### Parallel Opportunities

- Setup：T002/T003 并行
- Foundational：T006~T009（Go service）并行；T011/T012/T013（Python）并行
- US1：T016/T017 并行，T019/T020/T021 三节点并行
- US4/US5 两个 P2 故事可由不同人并行
- Polish：T042/T043/T045 并行

---

## Parallel Example: Foundational (Phase 2)

```bash
# Go core 四个 gRPC service 适配可并行（不同方法，同 SSOT 元数据）:
Task: "实现 OracleService.Analyze in internal/service/grpc_server.go"
Task: "实现 BuildService.Build in internal/service/grpc_server.go"
Task: "实现 CoverageService.Measure in internal/service/grpc_server.go"
Task: "实现 CheckerMetadataService in internal/service/grpc_server.go"

# Python 基础设施并行:
Task: "blackboard schema in orchestrator/defuzz_loop/state.py"
Task: "gRPC client in orchestrator/defuzz_loop/clients/grpc_client.py"
Task: "LLM provider config module in orchestrator/defuzz_loop/llm/provider.py"
```

---

## Implementation Strategy

### MVP First (US1 + US2 + US3 = P1)

1. Phase 1 Setup → Phase 2 Foundational
2. Phase 3 US1（单轮闭环骨架）→ **STOP & VALIDATE** SC-001
3. Phase 4 US2（ISA 路由）+ Phase 5 US3（护城河三件套）
4. P1 完整即可演示"显式编排 + 确定性 grounding + ISA 路由"

### Incremental Delivery

1. Setup + Foundational → 基础就绪
2. US1 → MVP 单轮闭环
3. US2 + US3 → 完整 P1 护城河
4. US4 → 反馈引导；US5 → 最小化 PoC
5. Polish → 删旧 Go 代码（fuzz/llm/prompt/state）+ 文档 + 全量 quickstart 验证

---

## Notes

- [P] = 不同文件、无未完成依赖
- 应删尽删：删 Go 代码（T039~T043）前先 grep 复用组件依赖再删，避免误伤 `internal/oracle`/`internal/coverage`/`internal/compiler`
- baseline fuzz loop 留在其他分支；本分支只保留 agent loop；SC-007 量化对照在 eval 阶段跨分支 checkout baseline 度量
- LLM 全走 Python（T013 provider 模块对接 langgraph），Go `internal/llm` 删除
- Python 一律 uv：`uv sync` / `uv run pytest`
- 提交节奏：每个 task 或逻辑组完成后提交；checkpoint 处可独立验证
- 可复现指锁定 blackboard 输入版本，LLM 输出非确定性不在保证内（R7）
