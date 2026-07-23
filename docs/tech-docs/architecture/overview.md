---
title: System Overview
description: DeFuzz 整体架构、组件依赖、数据流（Go core 双适配器 + Python LangGraph 编排）
priority: CRITICAL
last_updated: 2026-06-26
status: IMPLEMENTED
related_docs:
  - ./agentic-loop-redesign.md
  - ./oracle-mechanism-framework.md
---

# System Overview

> 本文讲当前架构的组件分层与数据流；高层动机与论文定位见 [agentic-loop-redesign.md](./agentic-loop-redesign.md)，各环节代码落点见其 §7。早期那条覆盖率驱动的 Go fuzz 主循环（`internal/fuzz` / `internal/prompt` / `internal/state` / Go 侧 `internal/llm`）已被本架构取代，相关 Go 包已删除。

DeFuzz 是一套挖编译器自身防御实现 bug 的 agentic 系统。它把"测程序"换成"测编译器"：让 agent 生成 C seed，由插桩 GCC 按 (checker→ISA) 矩阵编译，再用零假阳性的 oracle 跑出 invariant verdict 判定防御机制是否静默失效。系统分两段——一个 Go 写的确定性 core（gRPC + MCP 双适配器），一个 Python 写的编排层（LangGraph 显式编排 + 三个 agent）。

## 1. 一句话定位

> 编排器把整个 checker 目录枚举成确定性调度队列，逐个 invariant 让 Generator agent 生成 seed；seed 经 routing → build → coverage → oracle 这条写死的流水线跑出 verdict，违反则转最小化 agent，未违反则经反馈 agent 回到下一轮。轨迹全程版本化进 per-run 的 checkpoint 链，可复现、可 ablation、可审计。

## 2. 组件分层

两段式：Python 编排层持有流程控制权与共享状态，Go core 只提供确定性能力（gRPC 节点）与 agent 只读 tool（MCP）。

```
   ┌──────────────────────────────────────────────────────────────┐
   │  Python orchestrator (defuzz_loop, LangGraph)                  │
   │                                                                │
   │   cli.py            run / inspect / replay / trace-bug         │
   │     │ 装配                                                      │
   │     ▼                                                          │
   │   graph.py          固定边序流水线 + 条件路由 + bump 枚举游标      │
   │   state.py          Blackboard 共享状态 (pydantic)              │
   │   audit.py          per-run 目录 + manifest + checkpointer      │
   │   permissions.py    按节点写权限矩阵 + guard 断言                 │
   │   routing.py        CheckerCatalog + (checker→ISA) 矩阵展开      │
   │   agents/           generator · feedback · minimizer           │
   │   nodes/            build · coverage · oracle (gRPC 调用包装)     │
   │   llm.py            LLMConfig + chat model (OpenAI / Anthropic) │
   └───────────────┬─────────────────────────────┬────────────────┘
        gRPC :50051 │ (确定性节点)      MCP :50052 │ (agent 只读 tool)
                    ▼                             ▼
   ┌──────────────────────────────────────────────────────────────┐
   │  Go core (github.com/zjy-dev/de-fuzz, cmd/defuzz-core)         │
   │                                                                │
   │   internal/service/grpc_server.go                             │
   │     BuildService · CoverageService · OracleService            │
   │     · CheckerMetadataService                                  │
   │   internal/service/mcp_server.go                              │
   │     search_source · query_invariants · coverage_diff          │
   │     · creduce_run · compile_exec                              │
   │   internal/service/toolchains.go   ISA→toolchain (yaml)        │
   │                                                                │
   │   internal/oracle   MechanismOracle · Checkers · metadata(SSOT)│
   │                     · BinaryInspector · disasm/                │
   │   internal/compiler · internal/seed · internal/exec           │
   │   internal/seed_executor (native / QEMU)                      │
   └──────────────────────────────────────────────────────────────┘
```

gRPC 与 MCP 两个适配器共读同一份 `internal/oracle/metadata.go`（checker 元数据 SSOT），裁决逻辑只此一处。

## 3. 主循环数据流（单轮迭代）

流水线由 `graph.py` 按写死的边序推进，只有 generator/feedback/minimizer 三处是 agent，其余是确定性 gRPC 节点：

```
START → generator → routing → build → coverage → oracle → ⟨route⟩
            ▲                                                 │
            └──────────── bump (round+1, 枚举游标) ←─ not_violated ┘
                                              violated → END

  generator   Generator agent 为 current_target() 生成 seed + 选 checker 集
  routing     查表把 (checker→ISA) 展开成编译矩阵；cheap 全开、differential 强制全跑
  build       BuildService 按矩阵编译；缺 toolchain 的 ISA 产 error cell（不崩溃）
  coverage    CoverageService 强制测量，累积/增量写回 Blackboard
  oracle      OracleService 跑 checker 得四态 verdict；任一 Fail → violated
  route       violated → (可选)最小化 agent → END；
              not_violated → (可选)反馈 agent 写 guidance → bump → 下一轮
  bump        round+1；当前 invariant 预算耗尽（N 轮 OR T 秒）则游标进下一个
```

调度是确定性的：run init 把 `catalog.all_ids` 灌进 `target_queue`，bump 节点用游标逐个扫，每个 invariant 花固定预算。agent 不挑攻击哪个 invariant，只为分配到的 target 生成 seed。

## 4. 与外部世界的接口

| 边界 | 由谁负责 | 关键接口 |
| --- | --- | --- |
| 测试目标 (`xgcc` / 交叉 gcc) | 项目外构建，见 `guides/building-instrumented-gcc.md` | 命令行参数 + `.gcda` 副作用；路径在 `configs/toolchains.yaml` |
| 覆盖率工具 (`gcovr`) | 系统安装；coverage measurer 装配层调用 | JSON 报告 |
| LLM Provider | Python orchestrator（`orchestrator/defuzz_loop/llm.py` + `orchestrator/configs/llm.yaml`） | OpenAI 兼容 / Anthropic API |
| ELF 解析 | `debug/elf` (stdlib) | `BinaryInspector`（`internal/oracle/inspector.go`） |
| 跨架构执行 | QEMU user-mode；`internal/seed_executor` | exec.Cmd；`qemu_path` 在 `configs/toolchains.yaml` |
| PoC 最小化 | `creduce`；MCP tool `creduce_run` | 最小化 agent 调用 |

## 5. 审计产物

每个 run 落到 `orchestrator/runs/<experiment>_<mechanism>_<UTC时间戳>/`：

| 产物 | 内容 | 谁读它 |
| --- | --- | --- |
| `checkpoints.sqlite` | LangGraph 每个节点出口的共享状态版本链 | `inspect`（列 checkpoint）/ `replay`（锁版本看输入）/ `trace-bug`（顺链回溯到确定性证据） |
| `manifest.json` | git_sha / toolchains / checker_catalog / llm / ablation / disabled_agents | 三个只读子命令解析 thread；复现的元数据锚点 |

这套 per-run 隔离 + 版本化正是 [agentic-loop-redesign.md](./agentic-loop-redesign.md) §1.5 那条护城河（可复现 / 可 ablation / 可审计）的落地处。

## 6. 进一步阅读

- 当前架构权威文档（Python 编排 + Go core 双适配器）：[agentic-loop-redesign.md](./agentic-loop-redesign.md)（§7 列出各环节代码落点）
- Oracle 多 invariant 框架的实现态参考：[oracle-mechanism-framework.md](./oracle-mechanism-framework.md)
- 添加新防御机制的端到端 checklist：`../guides/adding-a-defense-mechanism.md`
