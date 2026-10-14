# Pi agents 的 MCP 接入验证

2026-10-12 实测：**MCP 可以正常接入 Pi agents，工具发现、调用、结果消费和最终返回均通过。**

使用仓库当前 Pi SDK 1.1.0 worker、原有 agent 定义、真实本地 CallGraph MCP，
以及可用的 Coconut 模型网关 `defuzz/coconut-gpt-5-6-terra-max`。
执行在 macOS `sandbox-exec` 下，单次上限为 10 个模型请求、50,000 tokens、
240 秒。没有修改生产代码或 agent 定义。

| Agent | 数据 | MCP 调用 | 模型请求 | 实际 tokens | 结果 |
| --- | --- | --- | --- | --- | --- |
| source-mapper，接入短任务 | GCC16.2.0 / AArch64 | 3 次成功，含 2 次数据查询 | 4 | 13,036 | 通过 |
| audit-family-a，接入短任务 | LLVM23.1.2 / AArch64 | 3 次成功，含 2 次数据查询 | 4 | 8,665 | 通过 |
| source-mapper，额外完整映射尝试 | GCC16.2.0 / AArch64 | 3 次成功，含 2 次数据查询 | 7 | 37,174 | 后续源码探索触发预算，任务未完成 |

合计 15 个真实模型请求、58,875 reported tokens，usage 无缺失。
短任务验证的是接入能力，没有将短任务返回报告计作正式源码映射或编译器审计结果。

## 证据

两个成功短任务均执行：

```text
Pi agent
  → mcp__defuzz_callgraph__list_targets
  → mcp__defuzz_callgraph__find_function
  → mcp__defuzz_callgraph__get_callers
  → 模型读取返回数据
  → 最终 JSON
```

- `tools.json`：六个 `mcp__defuzz_callgraph__*` 工具在真实 session 中可见。
- `provider-events.jsonl`：四个请求中的 `function_call_output` 数量依次为
  0、1、2、3，证明 MCP 结果进入后续模型请求上下文。
- `events.jsonl`：三次查询都有 `tool_execution_end`，`isError=false`。
- `final.raw.txt`：最终 symbol 来自 `find_function`，caller_count 与实际边数一致。
- `callgraph-binding.json`：匹配源码版本、ISA、目标及数据库 SHA-256。
- 离线 `inspect_attempt` 校验：三个尝试均无完整性错误；两个短任务 admission 成功。
- 三个尝试的 MCP 子进程均已退出，无遗留进程。

GCC 最终返回 `aarch64_stack_protect_canary_mem`，caller_count 为 0；
LLVM 返回 `(unnamed namespace)::MachineVerifier::verifyStackProtector`，
caller_count 为 1。GCC 空 caller 结果是正常查询返回，不代表连接失败。

结构化汇总在 [report.json](report.json)，各 agent 子目录保留请求、工具清单、
协议、MCP 日志、模型事件、usage、最终结果和 artifact manifest。
临时 runtime/auth/cache 没有复制到验收目录。

容器中也核对了正式配置解析：`source-mapper` 和 `audit-family-a` 都获得六个
CallGraph 工具；`audit-neutral` 不获得这些工具，符合裸 Agent 的隔离契约。
MCP 相关 SDK 回归测试 6 项通过。

本次真实模型链路在 macOS 验证。当前 amd64-on-ARM Pod 的正式 Pi
用户命名空间限制仍按此前报告保留，不能把本次结果报告成容器正式 campaign
已经通过或全量实验已经完成。
