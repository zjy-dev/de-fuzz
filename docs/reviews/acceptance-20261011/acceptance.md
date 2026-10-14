# 四实验与 OpenCode 接入验收

日期：2026-10-11。结论：**部分组件通过，整体功能验收不通过。**

上一轮“实现已完成、可验收”的结论过早。四个子命令和 run-all 已存在，但没有完成所期望的主实验接入，且 source-mapping 入口存在确定的失败路径。

本次仅做源码检查、单元测试、本地 MCP 只读查询及替身进程的 CLI 集成复现。没有启动真实模型实验，没有连接敏感服务器，没有修改业务代码。

## 三个问题的直接答案

| 问题 | 现在的实际状态 |
| --- | --- |
| Call graph 用上了吗？ | **本地 MCP 可用，正式主审计没有用上。** OpenCode 配置向 source-mapper 和 seed-generator 开放 CallGraph，但 pipeline/agent-audit 的 HTTP backend 默认仅提供文件读写工具，没有 CallGraph/MCP 桥接。未运行真实模型，不能把“有权限调用”当成“模型确实查询过”。 |
| 不变量 → 源码映射做了吗，是 OpenCode 吗？ | **有实现，调用的是 OpenCode source-mapper。** 新独立入口和 run-all 第三步会调用它；formal pipeline 仍只有原三阶段。独立入口即使获得合法映射，也会因 token 记录缺失被判失败；映射产物没有进入后续主审计。 |
| 主审计是 OpenCode subagents 军团吗？ | **不是。** pipeline 和 run-all 的 agent-audit 都调用 Python `_run_worker`，由选定 AgentBackend 执行；文档中的 HTTP 配置实际走 Responses API。另一条 `defuzz-loop run` 路径创建 OpenCode 的 seed-generator、feedback-analyzer、llm-oracle 和 minimizer，但四实验入口没有接到该 loop。 |

另外，主审计当前调用冻结的 checker bundle 获取反馈，再让 worker 修订审计报告；没有 checker 更新执行步骤。运行指南中“主审计也会更新 checker”的表述没有对应实现。

## 关键证据

- `orchestrator/defuzz_loop/experiment_engine/pipeline.py:65,344-382,1891-1904`：三阶段枚举、默认 stage runner、HTTP/Exec 后端选择。
- `orchestrator/defuzz_loop/experiment_engine/http_agent_backend.py:242-348,763-782`：默认工具只有 list_files、search_text、read_file，以及可写请求的 write_file、replace_text。
- `orchestrator/defuzz_loop/agent_audit.py:696-756,1230-1304`：Python worker 调后端；oracle 反馈后重新审计，worker 请求 `writable=False`，没有 checker-authoring 更新调用。
- `orchestrator/defuzz_loop/source_mapping/mapper.py:122-130` 与 `opencode/client.py:83-103`：实际构造 `opencode run --agent source-mapper`。
- `orchestrator/defuzz_loop/cli.py:92-139`：另一条在线 loop 确实创建 OpenCode 子代理。
- `.opencode/opencode.jsonc:7-12`：本地 CallGraph stdio 配置；两个代理提示词具有 CallGraph 权限。

## 五项需修复的问题

均为 P1，置信度 10/10。这里的 P1 表示本轮应修复的功能问题；没有发现需要报告的 P0。

1. **source-mapping 成功后仍判失败。** 新阶段 `source_mapping_stage.py:141-147` 直接创建 `OpenCodeClient()`，忽略注入的 backend。该 client 只返回文本，没有将 usage 写入统一 sink。`experiments_cli.py:2402-2427` 执行 `if result.success and not token_comparable` 后将其改为 failed。替身进程返回合法映射及 usage，真实 CLI 得到 `mapped=3, failed=0`，最终仍退出 1：`token comparison invalid: no model usage was recorded`。应接通 OpenCode 的配置、预算和计量契约，避免把 HTTP 计划当成实际执行后端。

2. **映射产物未传入主审计。** `experiments_cli.py:1226-1239` 仅把 `--from-run str(checker_run_dir)` 交给 audit。`source-mappings.jsonl` 没有对应 audit 输入或读取逻辑。应增加按 repetition 的映射输入、哈希校验及实际消费逻辑。这是功能接入缺口，不是既有 formal pipeline 回归。

3. **两种 source root 混用。** `experiments_cli.py:1209-1223` 给 checker-authoring 和 source-mapping 都传 `args.source_root`。Part II 的默认验证命令在 `core` 下执行 `go test ./internal/oracle/...`，需要 DeFuzz 工程；mapping 需要 GCC/LLVM 源码。新文档将该参数写为 compiler-source-tree。应拆开 checker 工程路径和编译器源码路径，并提前验证。

4. **映射 scope 不生效。** `source_mapping_stage.py:135-139` 对全部 `records` 构建 Invariant，未读取 `mechanisms/isas`。替身验证中选择 canary+aarch64，仍映射了 fortify+x86_64 和 LoongArch 条目。应复用 checker 的 scope 规则并保存筛选统计。

5. **LoongArch 被改成 x86_64。** `source_mapping_stage.py:76-81` 使用 `return normalized if normalized in _SUPPORTED_ISAS else "x86_64"`，允许集合和 OpenCode schema 均缺少 loongarch64。已复现输入 LoongArch、输出 prompt 却为 x86_64。应增加该 ISA 支持，对未知架构明确报错。

## 验证结果与边界

- CLI + pipeline + source_mapping + CallGraph 配置测试：159 passed。
- CallGraph MCP 测试：3 passed。
- 本地 MCP initialize/tools/list 和六个查询工具都成功；`paths_between` 用实际邻接边补查，返回两条路径。数据库 list_targets 报告 3 个 target、3,188,386 条边。
- 当前 bundled 目标标签是 GCC 16.2.0 x86_64、GCC 16.2.0 aarch64、LLVM 23.1.2；不能由这些标签推断已经覆盖其他编译器版本或四个 ISA。
- 旧手写 mock 生成器已删除，build_dataset 只导入本地 CSV；两个远程脚本已删除，在本轮配置、说明和 smoke 范围没有残留远程启动引用。本次未重新访问数据来源服务器，也未重做外部来源认证。
- 新增验收复现使用 `/tmp/defuzz-acceptance-20261011/probe_mapping.py`：仅替换进程/模型边界，真实执行 CLI → mapper → RunStore → token 判定；临时输入和运行目录自动删除。该复现不是模型实验。
- 既有 run-all 测试把 `cli.main` 替换为 argv 记录器，因此只能证明顺序与转发，未验证 source-mapping 的实际结果判定。原 mapper 测试也只覆盖底层重试/解析，不经过统一 CLI。

建议先统一第四实验的实际执行引擎与 checker 更新协议，再补齐映射输入、独立路径、OpenCode 计量和 scope，最后用无模型集成测试验收四实验链。当前应继续保持真实实验未启动。
