# DeFuzz

一套挖编译器自身软件防御实现 bug 的 agentic 系统（stack canary、FORTIFY、CFI、shadow stack、IBT 等）。

它测的是**编译器**而非程序：让 agent 生成 C seed，由插桩 GCC 按 (checker→ISA) 矩阵编译，再用零假阳性的 oracle 跑 invariant checker，判定防御机制是否**静默失效**。

研究范围是在项目控制的测试环境中验证防御不变量、定位编译器缺陷并支持修复。
产物包括最小复现、checker 证据、修复建议与回归测试，用于贡献给开源编译器社区。
研究上下文和可直接使用的任务前言见 [研究范围说明](docs/research-scope.md)，
开发代理入口见 [AGENTS.md](AGENTS.md)。

## 架构

DeFuzz 分两段：

- **Go core**（`core/`，module `github.com/zjy-dev/de-fuzz`）—— 一个确定性进程，在同一套 `internal/` 包上暴露两个面：
  - **gRPC**（`:50051`）：确定性节点 —— `BuildService` / `CoverageService` / `OracleService` / `CheckerMetadataService`。
  - **MCP**（`:50052`）：agent 只读 tool —— `search_source`、`query_invariants`、`coverage_diff`、`creduce_run`、`compile_exec`。
- **Python orchestrator**（`orchestrator/`，包 `defuzz_loop`）—— 显式 Python 编排，驱动主循环和 Generator / Feedback / LLM Oracle / Minimizer。
- **Pi SDK worker**（`agent-runtime/`）—— 每次 attempt 创建独立会话，加载 `agents/definitions/` 中的定义，执行模型和授权工具调用并保存完整证据。

设计原则：**ReAct 的归 agent，确定性的归编排**。build/coverage/oracle 写死在一条固定边序的流水线里；agent 只在固定位置被调用，彼此从不直连，只通过版本化的共享状态（blackboard）联动。这样换来可复现的轨迹、可按边 ablation、可审计的 bug 结论。

## 主循环

```
START → generator → routing → build → coverage → oracle → ⟨route⟩
            ▲                                                 │
            └──────────── bump (round+1, 枚举游标) ←─ not_violated ┘
                                              violated → END
```

- `generator` —— Generator agent 为当前 target invariant 生成 seed 并选 checker 集；ISA 不是自由维度（每个 checker 静态绑定它适用的 ISA）。
- `routing` —— 把 (checker→ISA) 展开成编译矩阵；廉价静态 checker 全开，差分 checker 强制跑全 ISA 集（不剪枝）。
- `build` / `coverage` / `oracle` —— 确定性 gRPC 节点。缺 toolchain 的 ISA 产 error cell 而非崩溃。任一 checker `Fail` ⇒ violated。
- `route` —— violated → （可选）Minimizer agent → END；not_violated → （可选）Feedback agent 写 guidance → `bump` → 下一轮。

调度是确定性的：run init 把整个 checker 目录枚举成队列，游标逐个扫，每个 invariant 花固定预算（N 轮 OR T 秒）。agent 不挑测试哪个 invariant。

## 跑起来

先编 Go core，再从编排层驱动：

```sh
make build-core                       # → bin/defuzz-core (gRPC + MCP)
bin/defuzz-core --mechanism canary    # 启动确定性 core

cd agent-runtime
npm ci && npm run build              # Node >=22.19.0，锁定 Pi 1.1.0
cd ..

cd orchestrator
uv run defuzz-loop run --mechanism canary --experiment demo \
  --pi-config ../configs/agents/pi-runtime.example.json
```

运行配置显式引用模型和 MCP 文件；凭证由配置列出的环境变量提供。
每个 run 保存 `manifest.json`、逐轮 blackboard、编译证据及 Pi attempt 目录。
离线检查单次调用，不会重新请求模型或执行工具：

```sh
uv run defuzz-inspect-attempt <attempt-directory>
```

被测的插桩 GCC 在项目外构建，见 [docs/tech-docs/guides/building-instrumented-gcc.md](docs/tech-docs/guides/building-instrumented-gcc.md)。ISA→toolchain 路径配在 [`configs/toolchains.yaml`](configs/toolchains.yaml)。
单机运行 `x86_64 / aarch64 / riscv64 / loongarch64` 时，从 [`configs/toolchains.qemu.example.yaml`](configs/toolchains.qemu.example.yaml) 复制正式配置。每个 ISA 必须显式标记 native，或为动态 checker 配置 QEMU user-mode；正式 plan 会记录并哈希编译器与 QEMU runner，避免跨 ISA 二进制误走本机执行。

## 文档

- 架构权威文档：[docs/tech-docs/architecture/agentic-loop-redesign.md](docs/tech-docs/architecture/agentic-loop-redesign.md)
- Pi 迁移及验收：[docs/tech-docs/architecture/pi-sdk-migration.md](docs/tech-docs/architecture/pi-sdk-migration.md)
- 正式四阶段实验：[docs/paper/experiment_runbook.md](docs/paper/experiment_runbook.md)
- 系统总览（组件 + 数据流）：[docs/tech-docs/architecture/overview.md](docs/tech-docs/architecture/overview.md)
- 技术栈：[docs/tech-docs/reference/tech-stack.md](docs/tech-docs/reference/tech-stack.md)
- 总入口：[docs/tech-docs/README.md](docs/tech-docs/README.md)
