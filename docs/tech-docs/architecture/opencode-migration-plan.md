# OpenCode 迁移方案：Source Mapping SubAgent + LangGraph 编排的混合架构

**作者**：张景耀
**分支**：`feat/opencode-migration-20260928`
**日期**：2026-09-28
**关联**：DeFuzz 方法四项改造中的 "把不变量映射到编译器源代码"、"定向 Fuzz 阶段的多 Agent 编排"

---

## 0. 背景与本文范围

DeFuzz 当前的 orchestrator 用 **LangGraph** 实现了一条固定边序的确定性管线：

```
START → generator → routing → build → coverage → oracle → ⟨route⟩
         ▲                                                   │
         └───── bump (round+1, cursor) ← not_violated ───────┘
                                          violated → END
```

外加 Generator / Feedback / Minimizer / LLMOracle 四个 LLM Agent（都是 LangChain
`ChatModel` 直接调用，没有工具链）。

现在有两个新需求：

1. **把不变量映射到编译器源代码**（论文流程 Step 3）——需要一个能读代码、grep、
   AST 的自主 Agent 来完成 Level 1 (Pass) → Level 2 (Functions) → Level 3
   (Structs & Critical Conditions) 的三级下钻。用户希望这一步用 **OpenCode
   SubAgent** 定义，避免从零搭建文件工具。
2. **定向 Fuzz 阶段的多 Agent 编排**——用户希望：
   - 有**显式**的 agent 间流转（不是 LLM 自由 route）；
   - 每条不变量封装成一个**固定工作流**，可以**并行**启动 N 条。

用户同时提出：也许我们要全部使用 OpenCode，把 LangGraph 全部去掉，并要求本
文档做出**决策**。

---

## 1. 决策

**采用混合架构**，而不是"全部改用 OpenCode"。理由如下：

| 维度 | LangGraph 是否胜任 | OpenCode 是否胜任 |
|---|---|---|
| 显式 DAG / 边序 | ✅ `StateGraph.add_edge` 原生支持 | ❌ 只有隐式的 Task 工具调用 |
| 确定性重放 R5 / R8 零假阳 | ✅ 边序固定 + write matrix + tool_call_log | ❌ session 是一次性对话，重放需要 export/import JSON，且模型自由决定分派子 agent |
| 每轮的 write-permission 矩阵 | ✅ 已在 `permissions.py::guard` 里实现 | ❌ 只能靠 permission 限制某个工具是否可用，粒度更粗 |
| 消融实验 SC-004（feedback→gen / coverage→feedback 单边开关） | ✅ `ablation_flags` 在 blackboard 上，节点里直接读 | ❌ 需要在 opencode.json 里 fork 多份 agent 配置 |
| 并行 N 条不变量 workflow | ✅ `asyncio.gather` 起 N 份 compiled graph 即可 | ⚠️ 需要起 N 个 opencode server（端口 / 状态隔离），成本更高 |
| 让 Agent 拿到"文件读、grep、bash"工具 | ⚠️ 要自己包 MCP client 或 LangChain Tools | ✅ **built-in**，就是它最强的地方 |
| 探索性、开放式代码理解任务（Source Mapping） | ⚠️ 不擅长，要自己搭一个"read/grep + LLM"循环 | ✅ **Explore / Scout / General subagent 就是为这个设计的** |

**核心洞察**：OpenCode 是一个 *coding assistant*，不是一个 *orchestration
framework*。它的抽象是"session + subagent + tools"，天然适合**一次性、开放式
任务**（如源代码探索）。而 DeFuzz 论文的核心贡献（R8 零假阳、确定性重放、
per-edge 消融）依赖**显式的图和确定性节点**——这正是 LangGraph 的强项，
OpenCode 反而是障碍。

因此：

### 决策 A：Step 3 「Source Mapping」用 OpenCode SubAgent

- 在 `.opencode/agent/source-mapper.md` 里定义一个 markdown SubAgent。
- 它拥有 `read` / `glob` / `grep` / `list` 的默认 allow（继承全局 config），
  `edit` / `bash` / `webfetch` / `websearch` 一律 `deny`，确保是**只读探索**。
- Python 侧通过 `opencode run --agent source-mapper --format json --dir
  <gcc-src>` 拉起一次 headless 会话，把不变量 `phi_i` 作为 message 传入，
  解析 JSON 输出拿到 `{pass, functions[], structs[], critical_conditions[]}`。
- Call graph MCP **占位**（用户在另一路做），mapping subagent 现在只用
  read/grep/list；等 call graph MCP 就绪，通过 `.opencode/mcp` 挂载即可，
  Python 侧不用改。

### 决策 B：Online Loop 保留 LangGraph

- 现有的 `graph.py`/`routing.py`/`state.py`/`nodes/*.py` 全部保留，是论文
  合规基线，不能动。
- **精简 agents/**：`generator.py`、`feedback.py`、`minimizer.py`、
  `llm_oracle.py` 里现在都是"直接调 LangChain ChatModel + hand-rolled tool
  binding"。这层**可以在后续 PR** 改造为"调用一个 OpenCode SubAgent 拿结果"——
  LangGraph 仍然是编排层，OpenCode 作为**能被工具化调用的 LLM 后端**。
- 这样每个 agent 都能自动获得 read / grep / bash 工具，不再需要我们手写
  `search_source` MCP 工具，也不用维护 LangChain tool schema。
- **本 PR 只做架构骨架 + Source Mapping**，agents 内部不动，避免 regression。

### 决策 C：并行 N 条不变量 workflow

- 保持"整个 catalog 由 orchestrator 枚举"的确定性，但把"budget 内跑完一条
  不变量"抽出来成一个**独立入口**：`run_single_invariant_campaign(phi_i,
  budget_rounds, budget_secs) -> CampaignResult`。
- 上层用 `asyncio.gather(...)` 起 N 个不同的 phi_i，共用一份 compiled graph
  的 shape（内部各自持有独立的 Blackboard），互不干扰。
- N 的上限由 Go core 的 gRPC / MCP 并发容量 + LLM provider 限速共同决定，
  先以 `--max-parallel 4` 起步。

---

## 2. 模块变更清单

### 新增（本 PR）

```
.opencode/
  agent/
    source-mapper.md                # Decision A
orchestrator/defuzz_loop/
  opencode/
    __init__.py
    client.py                       # OpenCode CLI 封装 (headless invocation)
    schemas.py                      # SourceMappingResult pydantic model
  source_mapping/
    __init__.py
    mapper.py                       # 调 source-mapper subagent 的 orchestrator glue
  campaign/
    __init__.py
    single.py                       # run_single_invariant_campaign
    parallel.py                     # gather N 个 campaign
    cli.py                          # `defuzz-loop campaign ...`
tests/
  opencode/
    test_client_smoke.py            # 需要本机 opencode 才能跑
    test_schemas.py                 # 纯离线
  source_mapping/
    test_mapper_offline.py          # fake OpenCodeClient
  campaign/
    test_parallel_offline.py        # fake graph, 只验编排逻辑
docs/tech-docs/architecture/
  opencode-migration-plan.md        # 本文
```

### 保留 (**不动**)

```
orchestrator/defuzz_loop/graph.py                 # LangGraph 编排是论文合规基线
orchestrator/defuzz_loop/routing.py
orchestrator/defuzz_loop/state.py                 # Blackboard / Aggregate / Verdict
orchestrator/defuzz_loop/nodes/*.py               # 全是确定性 gRPC 节点
orchestrator/defuzz_loop/permissions.py           # write-permission matrix
orchestrator/defuzz_loop/audit*.py
orchestrator/defuzz_loop/experiment_engine/*      # 实验产物, 保持兼容
orchestrator/defuzz_loop/specgen/*                # 离线 spec 生成也保持不变
orchestrator/defuzz_loop/agents/*.py              # 本 PR 不动, 留给下一个 PR
```

### 占位（**不做**，等 call graph MCP 就绪）

- `.opencode/agent/source-mapper.md` 里 `mcp:` 段先不加，注释里说明未来会挂
  `call-graph-mcp`；Python 侧 `SourceMappingResult.unresolved_notes` 字段
  用来收敛 "此处需要 call graph" 的报告。

---

## 3. 实施步骤（本 worktree 内落地顺序）

1. **[本 PR ✅]** 写本决策文档。
2. **[本 PR ✅]** 新建 `.opencode/agent/source-mapper.md`。
3. **[本 PR ✅]** 新增 `orchestrator/defuzz_loop/opencode/{__init__.py,client.py,schemas.py}`：
   headless `opencode run --format json` 的 async subprocess 封装 + pydantic 模型。
4. **[本 PR ✅]** 新增 `orchestrator/defuzz_loop/source_mapping/mapper.py`：
   - Input: 一条不变量 `Invariant` (schema: `phi_id, phi_text, mechanism, compiler, isa, src_root`)。
   - 调用 source-mapper subagent，超时 / 重试 / JSON schema 校验。
   - Output: `SourceMappingResult`。
5. **[本 PR ✅]** 新增 `orchestrator/defuzz_loop/campaign/{single.py,parallel.py,cli.py}`：
   - `run_single_invariant_campaign(phi_i, ...)` 复用现有 `build_graph`，
     把 `target_queue` 限制成 `[phi_i]`，budget 传进去。
   - `run_parallel_campaigns([phi_1, ...], max_parallel=4)` 用
     `asyncio.Semaphore(max_parallel)` 控并发，`asyncio.gather` 收集结果。
   - CLI: `defuzz-loop campaign --mechanism canary --parallel 4 --budget-secs 900`。
6. **[本 PR ✅]** 测试：
   - `tests/opencode/test_client_smoke.py`：`opencode run --agent
     source-mapper` 能起来、返回可解析 JSON（跳过条件：本机没装 opencode）。
   - `tests/opencode/test_schemas.py`：`SourceMappingResult` pydantic 解析
     样本 JSON 通过。
   - `tests/source_mapping/test_mapper_offline.py`：fake OpenCodeClient
     (返回预录的 JSON) 走通 `SourceMappingResult` 解析。
   - `tests/campaign/test_parallel_offline.py`：起 2 条 fake 不变量、
     fake `build_graph` 返回一个 `.ainvoke` async mock，asyncio.gather 能
     正确返回 2 份独立 Blackboard 快照。
7. **[后续 PR]** 改造 `agents/generator.py`、`agents/feedback.py`、
   `agents/minimizer.py` 的 LLM 后端到 OpenCode SubAgent 调用。
8. **[后续]** call graph MCP 就绪后，把它挂到
   `.opencode/agent/source-mapper.md` 的 mcp 段里。

---

## 4. 为什么不"全部用 OpenCode"

用户提出的这个方案我认真评估过。四条硬约束否决了它：

### 4.1 OpenCode 没有"显式 DAG / 边序"

用户明确要求"multi agent 间的流转要显示编排"。OpenCode 的 Task 工具是让**主
agent 决定要不要分派 subagent**——是**模型 driven** 的隐式路由，不是**代码
driven** 的显式路由。这跟 DeFuzz 论文的"边序固定、可重放、可 per-edge
ablate"是根本冲突的。

### 4.2 R8 零假阳依赖"deterministic oracle 先判、LLM oracle 只能升级 FAIL"

这条规则在 `graph.py::make_llm_oracle_node` 里是**代码显式**的，无法用
OpenCode 的 permission 表达。

### 4.3 消融实验 SC-004 是论文核心

`ablation_flags.feedback_to_generator` / `ablation_flags.coverage_feedback`
是**在同一份图上通过 flag 关一条边**——OpenCode 里要做等价效果，得给每一种
消融组合各写一个 opencode.json，config 爆炸。

### 4.4 并行 N 条不变量的隔离

LangGraph 里每份编译图有独立的 Blackboard，`asyncio.gather` 天然隔离。
OpenCode 每个 session 也隔离，但要开 N 个 opencode server 或让一个 server
承载 N 个并发 session，运维成本大于收益，尤其考虑到 Go core 那边同样要开
N 份 gRPC connection pool。

**混合方案兼得两者所长**：LangGraph 管确定性和编排，OpenCode 管 Agent 内部
的工具链和探索。

---

## 5. 风险与回滚

- **风险 R1**：`opencode run --format json` 的事件流 schema 未来变。→ 用
  `opencode/client.py` 收敛所有解析逻辑，schema 变化只改一个文件。
- **风险 R2**：source-mapper 的输出不稳定（LLM 幻觉）。→ 用严格的 JSON
  schema output + Python 侧 pydantic 校验，失败重试 2 次，仍失败 → 该轮标
  `mapping_error`，跳过。
- **回滚**：本 PR 全部是**新增文件** + **不动老代码**。回滚 = 删掉本 PR
  的新增目录即可，`defuzz-loop run` 老命令行为不变。

---

## 6. 附：`opencode run --format json` 关键格式

headless invocation 示例：

```bash
opencode run \
  --agent source-mapper \
  --format json \
  --dir /path/to/gcc-source \
  --model coconut/coconut-gpt-5-6-terra-max \
  "<invariant>{...JSON payload...}</invariant>"
```

JSON 事件流是 stdout 逐行 JSON，每行一个 event。我们的 client 只关心最后一
条 `type=="message"`（或等价的 final assistant message），从其 text 里 grep
出 ```json ... ``` 代码块并 pydantic-parse。
