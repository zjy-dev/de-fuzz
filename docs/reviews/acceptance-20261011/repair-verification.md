# 四实验接入修复与离线验收

日期：2026-10-11。验收范围为原报告的五项功能缺陷，以及正式流水线的
OpenCode、源码映射、CallGraph 和 checker 更新接线。

结论：上述工程功能已修复，离线回归通过。没有启动真实模型实验，也没有访问原数据来源服务器。
这份记录不代表正式实验完成或模型审计效果达标。

## 三个问题的当前答案

| 问题 | 修复后的行为与验证 |
| --- | --- |
| Call graph 用上了吗？ | 新 v2 formal pipeline 和使用 OpenCode 的 run-all 已接入本地 MCP。source-mapper、非 bare 的审计调用必须有成功 CallGraph 工具调用，否则失败。本机 OpenCode 1.18.31 的离线测试实际执行了本地 `list_targets`，并保留子会话工具记录。 |
| 不变量 → 源码映射做了吗，是 OpenCode 吗？ | 是。统一后端原生派发 `source-mapper` 子代理，记录用量；映射按不变量语义、mechanism、ISA、编译器源码内容绑定到审计提示。独立入口、run-all 和 v2 pipeline 均支持。 |
| 主审计是 OpenCode subagents 军团吗？ | 新路径是。Python 管理预算、工人顺序和验证，实际推理由 OpenCode 原生 Task 派发 `audit-family-a` 至 `audit-family-e`；按 scope 选择所需 family。离线测试实测 `audit-family-a` 子会话及 MCP；bare 使用 `audit-neutral`。旧 v1 或显式 HTTP/Exec 路径保持兼容。 |

数据库目前有 GCC 16.2.0 x86_64/aarch64、LLVM 23.1.2 三个 target 标签。
成功调用 `list_targets` 证明 MCP 接入，不证明相关调用图覆盖，更不能证明 GCC 17、
RISC-V、LoongArch 已有图数据。`callgraph_data_queries` 单独记录非目标列表查询。
源码映射支持四 ISA，不会以 x86_64 替代未知架构；缺失图覆盖须在结果中说明。

## 原验收缺陷的处理

| 原问题 | 修复 |
| --- | --- |
| 合法 mapping 仍因空 token sink 失败 | mapping 使用注入的计量后端；导出原生 child-session，将父子会话 usage 纳入统一 sink。缺失 usage、全零未知用量和超预算均使运行无效。 |
| 映射未传入 audit | audit 支持 `--mapping-from-run` / `--source-mappings`；按 repetition 验证三份产物，检查语义、源码快照和锚点，再重新计算 worker prompt hash。 |
| checker 与 compiler source root 混用 | run-all 分开 `--checker-source-root`（DeFuzz）和 `--source-root`（GCC/LLVM）。四阶段执行前预检目录、配置和工具链。 |
| mapping 忽略 scope | 复用 checker 的筛选规则，保存总量、选中、排除统计；generic 和多 ISA 正确扇出。 |
| LoongArch 静默变成 x86_64 | schema 与 runner 支持 loongarch64；单项或混合列表中的未知 ISA 明确拒绝。 |

另外修复了原生编辑权限匹配、环境 token sink 未显式传入 request、目录 symlink 快照与
copy 不一致，以及审计副本已有文件仍可原地写入的问题。

## Checker 更新与四阶段流水线

Full 在线反馈后可进行有界 checker 更新，默认一次，可设
`--checker-update-rounds 0–3`，且受 `oracle_rounds` 与审计预算共同限制。
`checker-update-planner` 返回是否需要更新和 generated invariant IDs；
`checker-author` 在副本中基于父 bundle 的累积 patch 修改实现。
通过格式化、Go 测试、vet、dispatcher 编译、catalog 校验、完整覆盖及 ID 保持检查后，
新 bundle 才采用，并立即重新执行在线 checker，反馈交回审计工人。

原冻结 Part II bundle 始终用于最终 offline verifier；失败更新会使审计无效。
更新链保存决策、parent/new bundle ID、哈希、patch 和验证记录，并加入 stage artifacts
供续跑校验。真实 Go fixture 验证了更新成功、未请求更新、编译失败和原 bundle 保持不变。

`schema_version: 2` 的 formal 配置要求 OpenCode，阶段顺序是：

1. `part_i`：不变量生成。
2. `part_ii`：checker 编写。
3. `source_mapping`：源码映射。
4. `part_iii`：主审计与有界 checker 更新。

每阶段有独立预算、输入/产物哈希、失败阻断、阶段结果和 campaign 行。
Full 的冻结 checker 与 mapping 供 without-oracle/bare 复用；bare 工人不接收映射。
`schema_version: 1` 保留三阶段运行/续跑契约，新阶段不注入旧配置的语义哈希。

## 验证范围与结果

所有模型响应来自 localhost 的固定 HTTP fixture。原生验证运行了已安装的 OpenCode、
macOS sandbox、native Task、child export 和真实本地 CallGraph MCP；没有调用上游模型。
Go 更新验证使用临时最小工程、真实编译和真实 dispatcher 执行。

| 验证集合 | 结果 |
| --- | --- |
| 既有 CLI、pipeline、checker-authoring、audit、campaign、CallGraph 配置回归 | 268 passed |
| 新增 OpenCode、v2 pipeline、checker 更新、映射传递及 source_mapping 测试 | 43 个不同用例通过：前一轮 42 passed，最终 OpenCode 全文件 12 passed（含新增写入用例） |
| 本地 CallGraph MCP 测试 | 3 passed |
| 最后 mapping reference 隔离与 update artifacts 调整后的定向复测 | 22 passed |
| 业务修改和新增测试 Ruff | 通过 |

关键场景覆盖：合法映射经真实 CLI 退出 0；usage 缺失、预算超限退出非零；
映射/summary/snapshot 篡改阻断 resume；v2 上游失败不派发 audit；
v1 三阶段回放；generic/多 ISA/LoongArch/未知 ISA；mechanism alias；
bare 映射不可见；checker 更新编译失败、累积 patch、冻结 verifier、更新反馈进入 worker。

测试轮次曾发现混合未知 ISA、全零 usage、直接阶段调用计量、原生编辑权限四处问题；
修复后保留相同正确行为断言。另一次 fixture 失败由不足五行的候选证据触发 admission，
修正 fixture 后完成真实最终 verifier 路径验证；没有放宽 admission。

未设置覆盖率门槛，也未声明覆盖率百分比。测试技能的 Step6 按规则跳过覆盖率检查。
自动测试摘要 `utree flush` 已完成，exit 0。

## 运行入口

独立的四命令仍为 `invariant-generation`、`checker-authoring`、
`source-mapping`、`agent-audit`。新示例均显式选择
`--backend opencode --opencode-config <provider-only.json>`。
standalone agent-audit 保留旧 traex 默认，使用新路径时须显式选择 OpenCode；
run-all 和 source-mapping 默认 OpenCode。

完整命令与路径含义见 [experiment_runbook.md](../../paper/experiment_runbook.md)；
formal 模板见 [formal.example.yaml](../../../configs/experiments/formal.example.yaml)，
provider 模板见 [opencode-agent.example.json](../../../configs/experiments/opencode-agent.example.json)。
示例包含环境占位路径，须完成冻结配置后才能执行。当前真实实验仍未启动。
