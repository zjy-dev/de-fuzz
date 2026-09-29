# OpenCode 迁移方案：markdown SubAgents + 自研显式编排

**作者**：张景耀
**分支**：`feat/opencode-migration-20260928`
**Rev 1 日期**：2026-09-28（Source Mapping + LangGraph 保留）
**Rev 2 日期**：2026-09-29（改为 200 行 `IterationLoop` + `litellm`）
**Rev 3 日期**：2026-09-29（**当前决策**：全部用 OpenCode markdown subagent + 外部 Python 显式编排）
**关联**：DeFuzz 方法四项改造中的"把不变量映射到编译器源代码"、"定向 Fuzz 阶段的多 Agent 编排"

---

## Rev 3 摘要（先看这里）

Rev 2 决定"手写 `LLMBackend` + `IterationLoop`，agent 只做结构化 JSON 生成"。
用户 push back：

> 定向 fuzz 时也需要 read/grep 等基本工具，我们自己写 agent 有点亏。
> 能否把所有 agent 都用自然语言定义 + 自己加显式编排？pbfuzz 用本地
> 持久化状态让 free agent 流转，我更想做显式编排。可以换 SDK。

**Rev 3 最终决定：全部用 OpenCode markdown subagents + 200 行 Python
`IterationLoop`。不引入 Claude Agent SDK / CrewAI / Autogen / Pydantic AI。**

| 层 | Rev 1 | Rev 2 | **Rev 3（最终）** |
|---|---|---|---|
| **Source Mapping**（离线） | `.opencode/agents/source-mapper.md` | 同 Rev 1 | **同 Rev 1，不变** |
| **online agents 的定义方式** | `agents/*.py`（LangChain ChatModel） | `agents/*.py`（`LLMBackend.structured`） | **`.opencode/agents/*.md`（markdown + YAML）** |
| **agent 内部的 read/grep/bash** | ❌ 无 | ❌ 无 | **✅ OpenCode built-in tools + 权限矩阵** |
| **online 编排** | LangGraph `StateGraph` | 手写 `IterationLoop`（Python） | **手写 `IterationLoop`（Python），每 phase = `opencode run --agent NAME`** |
| **状态归属** | LangGraph blackboard | Python dataclass | **Python dataclass**（subagent 无状态） |
| **对比 pbfuzz "shared-state 让 free-agent 自组织流转"** | — | — | **完全相反：subagent 是无状态 worker，Python 独占状态与调度** |
| **provider ablation** | ✅ via LangChain | ✅ via litellm | **✅ 通过 `opencode.jsonc` 换 `model`（每 agent 可独立换）** |
| **budget 控制** | Python 累计 | Python 累计 | **Python 累计 + OpenCode `steps` 每 subagent 上限** |
| **确定性重放** | LangGraph checkpointer | JSONL recorder | **JSONL recorder（每 subagent 一条 event stream）** |

---

## Rev 3 落地状态（2026-09-29 21:xx CST，commit `8057647`）

Rev 3 已经**一次性**落地在同一分支 `feat/opencode-migration-20260928` 上，
未拆 PR（按用户指示）。落地要点：

- 4 个 markdown subagent：`.opencode/agents/{seed-generator,feedback-analyzer,llm-oracle,minimizer}.md`（新增）
- `.opencode/opencode.jsonc`（骨架，MCP wiring 留 TODO）
- `orchestrator/defuzz_loop/loop.py::IterationLoop`（新增；替代 LangGraph `build_graph`）
- `orchestrator/defuzz_loop/opencode/{runner,output_schemas}.py`（3 层防护实现：Prompt 提示 + Pydantic 校验 + Retry）
- `agents/{generator,feedback,llm_oracle,minimizer}.py` 全部改写为 OpenCode SubAgent 后端
- `campaign/{single,parallel,cli}.py`、根级 `cli.py`、`audit.py` 更新到 IterationLoop
- 删除：`defuzz_loop/graph.py`、`tests/test_graph_loop.py`
- `pyproject.toml`：移除 `langgraph`、`langgraph-checkpoint-sqlite`、`langchain-*`；`uv.lock` 已同步
- 新增测试：`tests/loop/test_iteration_loop_offline.py`、`tests/opencode/test_subagent_runner.py`

变更规模：**28 files changed, +2004 / −2381**（净减 377 行 & 拿掉 5 个 LangGraph/LangChain 依赖）。

### 验证结果

- `uv sync --group dev`：✅
- `uv run pytest -q --ignore=tests/specgen`：**619 passed / 1 skipped / 1 failed**
  - 唯一失败：`tests/test_demo_parity.py::test_real_demo_corpus_parser_smoke_is_read_only`
    （真实 demo corpus 从 30 项漂到 44 项）——**Rev 3 之前就已存在**，已用
    `git checkout HEAD~1 -- tests/test_demo_parity.py` 复现，与本次重构无关。
- `uv run ruff check defuzz_loop tests`：✅ All checks passed
- 冒烟 import（`defuzz_loop.loop / opencode.runner / output_schemas / agents / campaign / cli / audit`）：✅

### 已知偏离

- `defuzz_loop/llm.py` **未物理删除**，仅缩减为 Rev 3 shim（保留 `LLMConfig`
  数据类 + 一个 duck-typed `ainvoke_structured` 的 fake-model 走线）。
  原因：`audit.py`、根 `cli.py`（Rev 3 notice stub 的类型标注）、
  `specgen/*`（本次不在范围）仍需 `LLMConfig` 类型。清理路径：后续独立小
  PR 把 `LLMConfig` 挪去 `config.py` 后再删 `llm.py`。
- `GeneratorOutput.rationale` 目前是 `default=""`，为兼容既有
  `test_checker_routing` 里 `GeneratorOutput(source=...)` 的直接构造。
  subagent markdown 仍在 prompt 层要求模型输出该字段。若想收紧成必填，
  同步改动 fixture 即可。
- 全仓 `ruff check .` 会在未触及的 `scripts/author_inv_transcript.py` 报
  27 处 E501，本次不修改（不在范围）。

---

## 0. 背景与本文范围

（Rev 1 内容保留在下面，Rev 2 附录见 §5–§7，Rev 3 追加见 §8–§10。）

DeFuzz 当前的 orchestrator 用 **LangGraph** 实现了一条固定边序的确定性管线。
外加 Generator / Feedback / Minimizer / LLMOracle 四个 LLM Agent（都是
LangChain `ChatModel` 直接调用，没有工具链）。

有两个新需求：

1. **把不变量映射到编译器源代码**——需要一个能 read / grep / AST 的自主 Agent。
2. **定向 Fuzz 阶段的多 Agent 编排**——每条不变量封装成固定 workflow，
   可并行启动 N 条；用户要**显式编排**。

---

## 1. Rev 1 决策：Source Mapping 用 OpenCode SubAgent（保留）

已合并 commit `5da934d`。`.opencode/agents/source-mapper.md` 已生效，路径不变。

---

## 4. Rev 1 §4 "为什么不全部用 OpenCode"（**Rev 3 已推翻**）

Rev 1 当时的论据是"OpenCode 是 coding assistant，不是 orchestration framework"。
Rev 3 里这个论据仍然成立，但结论错了——**我们不需要 OpenCode 做编排，
只需要它做 subagent 载体**。编排由外部 Python 做。这样两边各取所长：

- 编排（我们的显式控制流、budget、R8 gate、per-edge ablation） → **Python**。
- Subagent 内部（read / grep / MCP 工具调用、LLM 推理循环） → **OpenCode**。

---

# Rev 3 追加：SDK 选型 + 全 OpenCode markdown 架构

## 8. SDK 选型

用户的需求可以拆成 4 条硬指标：

1. **subagent 用自然语言定义**（不是 Python decorator / class）。
2. **built-in read/grep/bash 工具**（避免自己包一层文件工具）。
3. **provider-agnostic**（论文 ablation 要横跨 Claude / GPT / Gemini / Qwen / DeepSeek）。
4. **显式外部编排**（外部驱动决定谁跑、什么时候跑、跑多久，agent 是无状态 worker）。

对比一下主流 agent SDK / 产品（截至 2026-09）：

| 产品 | ①natural-language def | ②built-in read/grep/bash | ③provider-agnostic | ④外部显式编排 | 备注 |
|---|---|---|---|---|---|
| **OpenCode** | ✅ markdown + YAML frontmatter | ✅ read/glob/grep/list/bash/edit/write + 每命令粒度权限 | ✅ | ✅ headless CLI `opencode run --agent NAME --format json` | **全 hit** |
| Claude Agent SDK | ✅ `AgentDefinition(prompt=..)` | ✅ | ❌ **Claude-only** | ✅ Python 驱动 | ablation 死 |
| OpenAI Agents SDK | ⚠️ Python `Agent(name, instructions)` | ⚠️ 需自己包 | ⚠️ OpenAI-first（可换但 hop 多） | ✅ | 非 markdown，非 provider-first |
| CrewAI | ⚠️ YAML 但有自带 Crew/Task 抽象 | ❌ 需自己包 | ✅ via LiteLLM | ⚠️ Crew 自己编排 Agents，外部编排要拆 Crew | 抽象与我们冲突 |
| Pydantic AI | ❌ Python `Agent(...)` decorator | ❌ 需自己包 | ✅ | ✅ | 不是自然语言 |
| smolagents (HF) | ❌ Python | ⚠️ code-agent-only | ✅ | ✅ | 面向 CodeAct，方向不对 |
| Microsoft Agent Framework | ❌ Python/.NET | ⚠️ | ✅ | ✅ | 重、.NET-first |
| Google ADK | ⚠️ Python | ✅ | ⚠️ Google-first | ✅ | 生态偏向 GCP |
| **OpenCode 是唯一 4 条全 hit 的产品** | | | | | |

**Rev 2 里我否掉 Claude Agent SDK 的理由（provider lock-in）也适用于 Rev 3**。
其它候选（CrewAI / Pydantic AI / Autogen）都不满足"自然语言 subagent 定义"
这条。OpenCode 是唯一天生把 markdown 当一等公民、且 headless-first 设计的
产品。

## 9. Rev 3 架构

### 9.1 全景

```
┌──────────────────────────────────────────────────────────────────┐
│  campaign/parallel.py                                            │
│    asyncio.gather(run_single_invariant_campaign(phi_i, ...))     │
└──────────────────────────────────────────────────────────────────┘
                             │
                             ▼
┌──────────────────────────────────────────────────────────────────┐
│  loop.py::IterationLoop  (Python，~200 行，显式)                 │
│                                                                  │
│  # 一次 iteration，全部是 Python 顺序 + Python 决定               │
│  seed_json = await oc_run("seed-generator", state.to_prompt())   │
│  build     = await mcp.compile_exec(seed_json)                   │
│  cov       = await mcp.coverage_diff(build)                      │
│  det       = det_oracle(cov, seed_json)  # 确定性 R8 前置        │
│  if det.violated:                                                │
│      verdict = await oc_run("llm-oracle", (det, cov).to_prompt())│
│      if verdict.escalate: return Verdict.FAIL                   │
│  if state.rounds % fb_stride == 0 and ablation.fb_to_gen:        │
│      strat = await oc_run("feedback-analyzer", cov.to_prompt())  │
│      state.strategy = strat                                      │
│  if state.oom_or_crash():                                        │
│      await oc_run("minimizer", crash.to_prompt())                │
│  state.bump()                                                    │
└──────────────────────────────────────────────────────────────────┘
       │                    │                       │
       ▼                    ▼                       ▼
  oc_run()             GoMCPClient            Recorder / Replay
  wraps                (compile_exec,          (JSONL of every
  `opencode run        coverage_diff,          opencode session's
  --agent NAME         creduce_run,            full event stream)
  --format json        query_invariants,
  --dir <cwd>`          search_source)
```

### 9.2 SubAgents（`.opencode/agents/*.md`）

**每个 subagent 都是无状态 worker**：拿一段 prompt（含 phi 上下文 + 当前
blackboard 摘要）→ 用允许的工具跑几步 → 输出严格 JSON。

| 文件 | mode | 拥有工具（allow） | 拒绝（deny） | `steps` |
|---|---|---|---|---|
| `source-mapper.md`（已有） | subagent | read/glob/grep/list | edit/write/bash | 20 |
| `seed-generator.md` | subagent | read/glob/grep + MCP `query_invariants` / `search_source` | edit/write/bash | 8 |
| `feedback-analyzer.md` | subagent | read/grep + MCP `coverage_diff` | edit/write/bash | 6 |
| `llm-oracle.md` | subagent | read/grep + MCP `query_invariants` | edit/write/bash | 4 |
| `minimizer.md` | subagent | read + MCP `creduce_run` / `compile_exec`（`bash: deny`） | edit/write | 12 |

**关键点**：`bash` 一律 deny——不允许 subagent 自由跑 shell。真正需要执行
（如 creduce）通过 Go MCP 工具封装：一个 MCP tool = 一次带审计的 exec。
这既保住了 read/grep 的便利，又保证了 R8 所需的**确定性 exec 通路**。

### 9.3 Python 侧胶水（`orchestrator/defuzz_loop/opencode/runner.py`）

已经有 `OpenCodeClient`（`5da934d`）。Rev 3 扩展它：

```python
class OpenCodeClient:
    async def run(
        self,
        agent: str,
        prompt: str,
        *,
        cwd: Path,
        timeout_s: int,
        model: str | None = None,   # per-invocation override for ablation
        session_id: str | None = None,  # for --replay
        record_path: Path | None = None,  # dump event stream JSONL
    ) -> OpenCodeInvocation: ...
```

调用 `opencode run --agent <name> --format json --dir <cwd> [--model X]`，
stdout 是最终 assistant message（我们已经在 `client.py::extract_json_block`
里解析 fenced JSON）；stderr 里的 event stream 通过 `--json` 或 `--jsonl`
选项写到 `record_path`。

### 9.4 Go MCP 挂到 subagent

`.opencode/opencode.jsonc`：

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "defuzz-core": {
      "type": "local",
      "command": ["./scripts/defuzz-core-mcp.sh"]
    }
  },
  "agent": {
    "seed-generator": {
      "permission": {
        "bash": "deny",
        "edit": "deny",
        "write": "deny",
        "defuzz-core_query_invariants": "allow",
        "defuzz-core_search_source":    "allow",
        "defuzz-core_coverage_diff":    "deny",
        "defuzz-core_compile_exec":     "deny",
        "defuzz-core_creduce_run":      "deny"
      }
    }
    // ... 其它 agent 同理
  }
}
```

这就是我们之前在 LangGraph 里靠 `permissions.py::guard` 强制的
**per-agent write matrix**——现在直接落到 OpenCode 的 permission 系统里，
不用自己写。

### 9.5 确定性重放

- 每次 `oc_run(agent, ...)` 触发一次 subprocess，`--jsonl` 把完整 event
  stream（含每步 tool call & response）写到
  `runs/<campaign>/<phi>/<iter>/<phase>.jsonl`。
- 同一份 JSONL 里包含 session_id → 可以用 `opencode run --session <id>`
  重放（若我们要人肉排查），或者用我们的 `ReplayOpenCodeClient` 直接从
  文件读回最终 JSON（快速回放）。
- **R8 的确定性依据**在编排层（Python）而不是 subagent 内部——
  即使 subagent 内部 tool 调用顺序略有波动，编排层的 phase 顺序、budget、
  R8 gate 都是 Python 决定的。

### 9.6 Provider Ablation

- 一份 `opencode.jsonc` 里所有 subagent 默认走一个 `model`（比如
  `anthropic/claude-sonnet-4`），可以在**单个 subagent 上覆盖**（例如
  `llm-oracle` 用 `openai/gpt-4o`）。
- 论文 ablation 表：起一个 profile 目录 `configs/ablation/gpt-4o.jsonc`、
  `configs/ablation/claude-sonnet.jsonc` 等，`IterationLoop` 起进程时通过
  `OPENCODE_CONFIG=path` 切换。**零代码改动，纯配置。**

### 9.7 vs pbfuzz 的对比

- **pbfuzz**：agent 是"free-thinker"，通过读写共享状态文件相互流转
  ——状态可以放在磁盘、Redis、blackboard 里。优点是极致灵活；缺点是
  顺序不可预测、budget 难卡、R8 零假阳不可能保证。
- **我们（Rev 3）**：subagent **完全无状态**；所有 state 都在 Python
  `RunState` dataclass 里；OpenCode subprocess 拿一段 self-contained
  prompt 进入，出来一段 JSON，进程立刻退出。**Python 拥有全局单点权威**，
  状态永不共享给 subagent。这就是"显式编排"的严格版本，恰是用户要的。

## 10. 实施步骤（Rev 3）

Rev 1 的 commit `5da934d` 不动。Rev 2 的 `refactor/iteration-loop`
分支从未落地——直接跳到 Rev 3 的 PR 序列：

1. **PR-2 `feat/opencode-online-subagents`**：
   - 新增 `.opencode/agents/`：`seed-generator.md`、`feedback-analyzer.md`、
     `llm-oracle.md`、`minimizer.md`。
   - 新增 `.opencode/opencode.jsonc`：挂 Go MCP，配 permission matrix。
   - 扩展 `orchestrator/defuzz_loop/opencode/client.py`：支持 `--model`、
     `--session`、event-stream JSONL 落盘。
   - 增加金丝雀测试：每个 subagent 用 sample input 跑一遍，验证输出
     JSON schema 通过。
2. **PR-3 `feat/iteration-loop`**：
   - 新增 `orchestrator/defuzz_loop/loop.py::IterationLoop`，每个 phase 调
     `oc_run(agent=NAME, ...)`。
   - 新增 `Recorder` + `ReplayOpenCodeClient`。
   - `campaign/single.py::run_single_invariant_campaign` 从 `build_graph`
     切到 `IterationLoop`。
   - 把老 `graph.py` 挪到 `graph_legacy.py`，只保留作 baseline 回归。
3. **PR-4 `refactor/drop-langgraph`**：
   - 删除 `graph_legacy.py`、`agents/generator.py` 等旧 LangGraph 节点。
   - `pyproject.toml` 移除 `langgraph`、`langchain-*`。
   - `uv lock` 重新生成。
4. **PR-5** 等用户 call graph MCP 就绪，通过 `.opencode/opencode.jsonc`
   挂到相关 subagent（source-mapper 首当其冲；seed-generator 也可以用）。

**每一步都独立可回滚**：PR-2 只加不改，PR-3 只加不删（keep `graph_legacy`），
PR-4 才真删依赖。

## 11. Rev 3 与 Rev 2 的差异一句话

> **Rev 2 用 `litellm` 让 agent 只出结构化 JSON；Rev 3 用 OpenCode 让
> agent 内部先自由 read/grep 再出结构化 JSON。两者的编排层（`IterationLoop`）
> 是同一份 Python 代码，只是每个 phase 的 LLM 调用从`llm.structured(...)`
> 换成 `opencode.run(agent=...)`。**

## 12. 风险与回滚

- **R3-1**：每 iteration 有 4 次 subprocess exec（4 个 subagent），
  比 Rev 2 单纯的 API 调用重。→ 用 `asyncio.gather` 并行没意义（每次都是
  同一 phase），但可以起长驻 `opencode serve` HTTP 端口，未来切到 REST
  客户端。当前 subprocess 方案冷启动 ≈ 200ms，可接受。
- **R3-2**：subagent 内部 tool 调用顺序 non-deterministic。→ event stream
  JSONL 已捕获；replay 时用 recorded final JSON 短路。对于极端复现需求，
  用 `opencode run --session <id>` 完整重放。
- **R3-3**：某个 provider 不支持我们期望的 JSON schema 严格度。→ subagent
  markdown prompt 里用 fenced JSON block 约定 + `extract_json_block` 后置
  校验；`SourceMappingRunner` 里已经有重试逻辑，直接复用到其它 subagent。
- **R3-4**：OpenCode 权限矩阵配错，subagent 误改文件。→ 顶层 `permission`
  里 `edit/write/bash` 全 `deny`，每 agent 只**allow**它需要的 MCP 工具
  和 `read/glob/grep/list`；再加一层 CI 校验：`.opencode/opencode.jsonc`
  必须显式为每个 subagent 出现在 permission map 里。
- **回滚**：任何一步失败，`graph_legacy.py` 都保留可用，切回 LangGraph
  实现只需修改 `campaign/single.py` 中一处调用。

---

## 附：Rev 2 讨论（保留作参考）

（Rev 2 §5–§7 的原文——「200 行 IterationLoop + `litellm`」——继续保留在
git 历史 commit `6b61e3b` 里，本文不再复述。Rev 3 的 `IterationLoop` 骨架
与 Rev 2 相同，区别只在每个 phase 的 LLM 调用形式：Rev 2 是
`llm_backend.structured(...)`，Rev 3 是 `oc_run(agent=...)`。）
