# OpenCode 迁移方案：Source Mapping SubAgent + 定向 Fuzz 自研轻量编排

**作者**：张景耀
**分支**：`feat/opencode-migration-20260928`
**Rev 1 日期**：2026-09-28
**Rev 2 日期**：2026-09-29（引入"自研轻量 orchestrator + `litellm`"决策，替换 Rev 1 决策 B）
**关联**：DeFuzz 方法四项改造中的"把不变量映射到编译器源代码"、"定向 Fuzz 阶段的多 Agent 编排"

---

## Rev 2 摘要（先看这里）

Rev 1 决定"OpenCode 做 Source Mapping（决策 A）+ LangGraph 保留 online loop（决策 B）
+ agents 后端后续 PR 换成 OpenCode SubAgent"。这引出用户合理的质疑：

> 那如果 agents/generator.py、agents/feedback.py、agents/minimizer.py 全都换成
> 调一个 SubAgent，LangGraph 的意义就大幅削弱了。可以自研编排 + SDK 调 OpenCode
> 吗？OpenCode SDK 不够灵活的话，能不能换 Claude Code？

Rev 2 的最终决定：

| 决策 | Rev 1 | **Rev 2**（最终） |
|---|---|---|
| **A. Source Mapping**（离线，一次性代码探索） | OpenCode CLI SubAgent | **保持 OpenCode CLI SubAgent** |
| **B. 在线定向 Fuzz Loop 的编排** | LangGraph `StateGraph` | **自研 200 行 `IterationLoop`，替换 LangGraph** |
| **B'. Online agents 的 LLM 后端** | LangChain ChatModel（后续换 OpenCode SubAgent） | **`LLMBackend` 抽象 + `litellm.acompletion`（provider-agnostic）** |
| **C. 并行 N 条不变量 campaign** | `asyncio.gather` 起 N 份 compiled LangGraph | **`asyncio.gather` 起 N 份 `IterationLoop`** |
| **D. 确定性重放** | LangGraph checkpointer + write-permission matrix | **`(prompt, model, response)` JSONL 记录 + `--replay` 短路 `LLMBackend`** |

**不引入 Claude Agent SDK**。理由见 §5.3。

Rev 1 的 §1–§4 描述的落地结构（`.opencode/agent/source-mapper.md`、
`orchestrator/defuzz_loop/opencode/`、`source_mapping/`、`campaign/*` 已提交在
`5da934d`）**继续有效**；只是 §3 步骤 8"后续 PR 把三个 online agent 的 LLM
后端换成 OpenCode SubAgent"被 Rev 2 §6 替换成"下一 PR 拆掉 LangGraph 换
`IterationLoop` + `litellm`"。

---

## 0. 背景与本文范围

（Rev 1 内容保留在下面 §1–§4，Rev 2 追加 §5–§7。）

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

用户同时提出："也许我们要全部使用 OpenCode，把 LangGraph 全部去掉"，并要求本
文档做出**决策**。

---

## 1. 决策（Rev 1 - Source Mapping 部分仍然有效）

**采用混合架构**，而不是"全部改用 OpenCode"。理由如下：

| 维度 | LangGraph 是否胜任 | OpenCode 是否胜任 |
|---|---|---|
| 显式 DAG / 边序 | ✅ `StateGraph.add_edge` 原生支持 | ❌ 只有隐式的 Task 工具调用 |
| 确定性重放 R5 / R8 零假阳 | ✅ 边序固定 + write matrix + tool_call_log | ❌ session 是一次性对话，重放需要 export/import JSON，且模型自由决定分派子 agent |
| 每轮的 write-permission 矩阵 | ✅ 已经在 `permissions.py::guard` 里实现 | ❌ 只能靠 permission 限制某个工具是否可用，粒度更粗 |
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

### 决策 A（Rev 1，Rev 2 保留）：Step 3 「Source Mapping」用 OpenCode SubAgent

- 在 `.opencode/agent/source-mapper.md` 里定义一个 markdown SubAgent。
- 它拥有 `read` / `glob` / `grep` / `list` 的 allow 权限，`edit` / `write` /
  `bash` 一律 `deny`（除了受限的 `git log` / `git show`），确保是**只读探索**。
- Python 侧通过 `opencode run --agent source-mapper --format json --dir
  <gcc-src>` 拉起一次 headless 会话，把不变量 `phi_i` 作为 message 传入，
  解析 JSON 输出拿到 `{pass, functions[], structs[], critical_conditions[]}`。
- Call graph MCP **占位**（用户在另一路做），mapping subagent 现在只用
  read/grep/AST；等 call graph MCP 就绪，通过 `.opencode/mcp` 挂载即可，
  Python 侧不用改。

### 决策 B（Rev 1，**Rev 2 已替换**）：Online Loop 保留 LangGraph

（这一节被 Rev 2 §5–§6 替换。请以 Rev 2 决策为准。）

### 决策 C（Rev 1，Rev 2 保留但换承载体）：并行 N 条不变量 workflow

- 保持"整个 catalog 由 orchestrator 枚举"的确定性，但把"budget 内跑完一条
  不变量"抽出来成一个**独立入口**：`run_single_invariant_campaign(phi_i,
  budget_rounds, budget_secs) -> CampaignResult`。
- 上层用 `asyncio.gather(...)` 起 N 个不同的 phi_i，共用一份 compiled graph
  的 shape（内部各自持有独立的 Blackboard），互不干扰。
- N 的上限由：Go core 的 gRPC / MCP 服务并发能力 + OpenCode server 的连接池
  容量 + LLM provider 的限速共同决定，先以 `--max-parallel 4` 起步，
  scripts/ 下加一个 profile。

（Rev 2 说明：`compiled graph` 换成 `IterationLoop` 实例，其余不变。）

---

## 2. 模块变更清单（Rev 1）

保留原状。Rev 2 追加的变更见 §6。

## 3. 实施步骤（Rev 1，步骤 8 被 Rev 2 §6 替换）

## 4. 为什么不"全部用 OpenCode"（Rev 1，仍然有效）

（原文保留，摘要：显式 DAG 缺失、R8 依赖代码显式的判定顺序、消融实验 flag、
并行隔离成本）。

---

# Rev 2 追加：为什么放弃 LangGraph，也不改用 Claude Agent SDK

## 5. 重新评估

### 5.1 用户的观察是对的

Rev 1 决策 B 里，我们说"agents 是 LangGraph 节点里的 LLM 后端，未来 PR 把
LangChain 换成 OpenCode SubAgent"。但一旦真的换过来，`generator.py` /
`feedback.py` / `minimizer.py` 的实现就变成：

```python
async def generate(bb: Blackboard) -> Seed:
    result = await opencode_run(agent="seed-generator", input=bb.to_dict())
    return Seed.model_validate(result.payload)
```

此时 LangGraph 里剩下的"节点"就只是把 4 个 SubAgent 调用串起来，加一个 R8
gate 和 budget 检查。**这不需要 LangGraph——一段带条件跳转的 asyncio Python
就足够。** LangGraph 的价值（typed state 图、动态路由、checkpointer）都用不上。

同时 Rev 1 §4 的四条"必须留 LangGraph 的理由"里，只有**"显式边序"和
"per-edge ablation"**是真需求。这两条恰恰不需要 LangGraph——一段直白的
`for _ in rounds: gen(); build(); cov(); oracle();` + 几个 `if ablation_flags:`
就是最显式、最可读的实现。

### 5.2 online loop 的性质：不是 coding 任务

在线循环的 LLM 调用**不是** coding-agent 场景：

- Generator：给定 blackboard 状态 → 输出下一条 seed（严格结构化 JSON）。
- Feedback：给定 coverage delta + oracle verdict → 输出 mutation 策略摘要。
- Minimizer：给定崩溃 program → 触发 creduce（Python 直接调 MCP tool，不需要 LLM 决定要不要调用）。
- LLM Oracle：给定 deterministic verdict → 是否升级为 FAIL（严格结构化，且 R8 只允许一个方向的改写）。

**它们全部是"给定输入 → 严格结构化 JSON 输出"** 的调用。让一个 coding agent
（OpenCode / Claude Code）自主决定要不要 Read、要不要 Grep、要不要 Bash——
在这个场景下是纯 overhead，还破坏 R8 的确定性（因为工具调用顺序变成 LLM
自主决定）。

所以：**online 循环的 LLM 调用应该是"裸 LLM API + JSON schema output"，
不是 coding-agent session。**

### 5.3 为什么不用 Claude Agent SDK（Python）

Claude Agent SDK 是 Anthropic 官方 Python SDK，我认真评估过。它有很多我们表面
上会想要的能力：

| 能力 | Claude Agent SDK | 对我们有用吗 |
|---|---|---|
| Native Python，`query()` + `ClaudeSDKClient` | ✅ | 是 |
| 程序化 `agents={"name": AgentDefinition(...)}` | ✅ | 部分是（Source Mapping 用得上，online 不需要） |
| `hooks`（PreToolUse / PostToolUse / lifecycle） | ✅ | 用来做 write-permission gate 可以 |
| `permissionMode` + allow/deny rules | ✅ | 用来做 phase-write matrix 可以 |
| `max_budget_usd` + `resume=session_id` | ✅ | 用来做 campaign budget + 重放可以 |
| 结构化输出 | ✅ | 用得上 |
| `sdk_mcp_server` 挂 in-process 工具 | ✅ | 挂 Go core MCP 可以 |
| **provider 覆盖** | ❌ **Anthropic Claude 独家**（Claude API / Bedrock / Vertex） | **致命** |

**否决理由：DeFuzz 是一篇论文，需要 LLM ablation table。** 论文肯定要对比
GPT-4o / Claude / Gemini / Qwen / DeepSeek 等多个 provider 上的效果——这是
审稿人一定问的问题。锁死 Claude 后无法做这个 ablation，直接影响可发表性。

其次，Claude Agent SDK 的设计目标是**"把 Claude Code 当库嵌入你的应用"**，
里面很多 API 表面（hooks、permissions、subagent 分派）都是为一个**会自主
Read/Write/Bash 的 coding agent** 而设计的。我们的 online 循环恰恰不是那种
agent。把它引进来，等于用一个更大的框架替代一个已经嫌重的框架，方向反了。

### 5.4 为什么不用 OpenCode SDK（做 online loop）

- **OpenCode SDK 只有 JS/TS 版本**（`@opencode-ai/sdk`），Python 只能用
  REST 客户端或者 `opencode run` 子进程。
- 即便走 REST，我们付出的仍然是 "coding agent session" 的架构成本
  （session、subagent 分派、事件流）——同 §5.2，online 循环不需要。
- OpenCode 用在**Source Mapping 这种"探索代码"**的场景下才是对味的。

---

## 6. Rev 2 决策：自研 200 行 `IterationLoop` + `litellm`

### 6.1 架构

```
┌─────────────────────────────────────────────────────────────┐
│ campaign/parallel.py: asyncio.gather( run_campaign(phi_i) ) │
└─────────────────────────────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────┐
│ campaign/single.py::run_single_invariant_campaign(phi)      │
│   loop = IterationLoop(phi, budget, backends, tools)        │
│   return await loop.run()                                   │
└─────────────────────────────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────┐
│ defuzz_loop/loop.py::IterationLoop                          │
│                                                             │
│   for round in range(budget_rounds):                        │
│     seed  = await generator.propose(state)                  │
│     build = await self.build(seed)         # Go MCP        │
│     cov   = await self.coverage(build)     # Go MCP        │
│     det   = await oracle.determ(cov, seed) # Go MCP        │
│     if det.violated: return Verdict.FAIL                    │
│     if ablation.llm_oracle:                                 │
│         llm  = await llm_oracle.judge(det, cov)             │
│         if llm.escalate_to_fail: return Verdict.FAIL   # R8 │
│     if ablation.feedback_to_gen:                            │
│         strategy = await feedback.summarize(cov, det)       │
│         state = state.evolve(strategy=strategy)             │
│     if state.budget_exceeded(): return Verdict.TIMEOUT      │
│   return Verdict.EXHAUSTED                                  │
└─────────────────────────────────────────────────────────────┘
                             │              │              │
                             ▼              ▼              ▼
                    LLMBackend      GoMCPClient      Recorder
                    (litellm)      (existing)      (JSONL)
```

### 6.2 关键组件

**`LLMBackend`（新，`orchestrator/defuzz_loop/llm/backend.py`）**

```python
class LLMBackend(Protocol):
    async def structured(
        self,
        messages: list[Message],
        schema: type[BaseModel],
        model: str,
        temperature: float = 0.0,
    ) -> BaseModel: ...
```

实现：`LitellmBackend(base_url, api_key)` — 一个 `await litellm.acompletion(..., response_format=schema)` 的薄包装，20 行。
`litellm` 覆盖 Anthropic、OpenAI、Bedrock、Vertex、Azure、Cohere、Ollama、
vLLM 本地，对论文 ablation 足够。

**`IterationLoop`（新，`orchestrator/defuzz_loop/loop.py`）**

- ~200 行。全部是显式的 `async` 顺序控制 + 一段 `if ablation:` gate。
- 不需要 typed state graph；`RunState` 就是一个 `@dataclass`。
- 消融实验 = 构造 `IterationLoop` 时传入 `ablation_flags`；每个 flag 决定
  "跳过某一 phase / 强制某边"——比 LangGraph 里改边更直接。
- write-permission matrix = "只有 `IterationLoop` 里对应 phase 的代码路径
  才允许调 Go MCP 的写工具"——由 Python 显式，不需要一层 permission DSL。

**`Recorder`（新，`orchestrator/defuzz_loop/replay/recorder.py`）**

- 每次 `LLMBackend.structured(...)` 调用，把 `(campaign_id, phi_id,
  round, phase, prompt, model, response, tokens, latency_ms)` 追加到
  `runs/<campaign>/<phi>/<iter>/<phase>.json`。
- `--replay <run-dir>` 模式下，`ReplayBackend(record_dir)` 读同样的 key
  返回记录的 response，不打模型。**这就是我们的 R5 确定性重放**——比
  LangGraph checkpointer 还简单，因为记录粒度就是 LLM 调用而不是状态图。

**Agents 变形**

- `agents/generator.py`：删掉 LangChain / LangGraph 依赖，改成一个持有
  `LLMBackend` + `GoMCPClient` 的 dataclass，一个 `async propose(state)`
  方法。
- `agents/feedback.py` / `agents/llm_oracle.py`：同上模式。
- `agents/minimizer.py`：其实**不是一个 LLM agent**，它就是"崩溃程序 →
  creduce"这一步 Python 调用，Rev 2 里干脆改名 `MinimizerStep`，从
  `agents/` 挪到 `steps/`。

**`graph.py` / `routing.py` / LangGraph `state.py` 里的 StateGraph 部分**

- 用一层保留（`graph_legacy.py` 只读回归 baseline），主体删除。
- `permissions.py::guard` 保留作为"phase mutex"工具函数被 `IterationLoop`
  显式调用。

### 6.3 落地 PR 顺序（Rev 2）

Rev 1 已合并的 `5da934d` 不动。后续：

1. **PR-2 (`refactor/iteration-loop`)**：
   - 引入 `defuzz_loop/llm/backend.py`（`LLMBackend` + `LitellmBackend`）。
   - 引入 `defuzz_loop/replay/recorder.py` 和 `ReplayBackend`。
   - **不动** `graph.py`。加金丝雀测试：老 LangGraph baseline vs 新
     `LLMBackend`——同 prompt 同 model 应该返回同 JSON。
2. **PR-3 (`refactor/iteration-loop-core`)**：
   - 新增 `defuzz_loop/loop.py::IterationLoop`。
   - Rewrite `agents/generator.py`、`agents/feedback.py`、`agents/llm_oracle.py`
     为 plain classes。
   - `campaign/single.py::run_single_invariant_campaign` 从 `build_graph`
     切到 `IterationLoop`。
   - 全部 e2e 测试跑通后，把 `graph.py` 挪到 `graph_legacy.py`（只保留
     baseline 回归用）。
3. **PR-4 (`refactor/drop-langgraph`)**：
   - 删除 `graph_legacy.py`。
   - `pyproject.toml` 里删除 `langgraph`、`langchain-*` 依赖。
   - `uv lock` 重新生成。
4. **PR-5+（等 call graph MCP 就绪）**：挂 call graph MCP 到 source-mapper。

### 6.4 消融矩阵在新架构下如何表达

Rev 1 里 SC-004 消融通过在 LangGraph 图上关一条边实现。Rev 2 里：

```python
@dataclass
class AblationFlags:
    feedback_to_generator: bool = True   # SC-004a
    coverage_to_feedback: bool = True    # SC-004b
    llm_oracle_enabled: bool = True      # SC-004c
    lateral_exploration: bool = True     # SC-004d

# IterationLoop 里：
if self.ablation.coverage_to_feedback:
    cov = await self.coverage(build)
else:
    cov = CoverageResult.empty()
```

比 Rev 1 更**显式**（在同一个函数里就能看到所有分支），审稿人一眼能读。

### 6.5 为什么不担心"自己造轮子"

- 我们要造的其实是 100–200 行的 asyncio 循环 + 记录器；LangGraph 帮我们
  节省的正是这 100–200 行，代价是引入 `langgraph` + `langchain-core` +
  `langchain-openai` 等一大票依赖（我们已经在 `pyproject.toml` 里花过 6
  个 pin）。
- 论文合规基线是"**显式的多 agent 编排 + 确定性重放**"——这两条我们靠 200
  行 Python 反而实现得更直白。
- 未来 call graph MCP、其它 MCP 挂到 online 循环，直接在 `IterationLoop`
  里显式调用即可，不需要图 API 学习成本。

---

## 7. 风险与回滚（Rev 2 更新）

- **R2-1**：`litellm` 对某个新 provider 的 JSON-schema 支持有 bug。→
  `LLMBackend` 抽象出来后可以在 15 分钟内换成直连 provider SDK（Anthropic
  Python SDK / OpenAI SDK / google-genai）。
- **R2-2**：手写循环里遗漏了 LangGraph 曾经提供的某个不变量（例如"节点
  之间 state 不可变"）。→ 保留 `graph_legacy.py` 作为 baseline，PR-3 的
  验收指标是"两条实现同 prompt / model / seed 下产生相同 verdict 序列"。
- **R2-3**：`Recorder` 记录粒度不足以复现某些 non-determinism（如
  compile_exec 的随机性）。→ Go core MCP 已经有 `tool_call_log`，
  Recorder 只需记 LLM 响应；Go 侧确定性由 Go core 负责。
- **回滚**：PR-2 / PR-3 均**只加不删**（`graph.py` 保留），PR-4 才真正
  删除依赖。三步 PR 每步都能独立回滚。

---

## 8. Rev 2 之后的最终架构一句话总结

> **coding agent（OpenCode）留在离线代码探索这一步；在线定向 fuzz
> 是"200 行 Python 循环 + 结构化 JSON LLM 调用 + Go MCP 工具调用 +
> JSONL 记录"，provider 由 `litellm` 抽象。LangGraph 与 Claude Agent SDK
> 都不引入。**
