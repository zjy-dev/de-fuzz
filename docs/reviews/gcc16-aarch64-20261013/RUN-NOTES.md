# GCC 16.2.0 / AArch64 实验观测记录

观测时间：2026-10-13 13:34:04 +08:00（05:34:04 UTC）。

**实验已停在 Checker 编写阶段，没有完成四阶段实验。** 原 launcher
PID 68020 已退出。源码映射、日志插桩、GCC 重编译和 Agent 审计均未执行。
本次观测没有重新启动实验或修改原始证据。

## 实际执行范围

- 工程连续实验入口：`run-all`，Pi SDK，`full`，一个 repetition，
  `compiler=gcc`、`isa=aarch64`；不是 `mode: formal` 论文 campaign。
- run prefix：`gcc16-aarch64-full-20261013`。
- 结果根目录：`.work/gcc16-aarch64-run/`；原始命令见其 `run.sh`。
- GCC 16.2.0 发布 revision：
  `78d4ac73dd391005b895a6148cd9831e28e1208b`，由源码 `LAST_UPDATED`
  和工具链 lock 记录；源码 `gcc/BASE-VER` 为 `16.2.0`。
- 模型：`defuzz/coconut-gpt-5-6-terra-max`；Responses API，本地 gateway。
- 每阶段固定上限：20,000,000 tokens / 480 分钟；Pi 单 attempt
  配置另有 200,000 tokens、32 model calls、600 秒上限。
- 分段输入是从 GCC 16.2 源码生成的配置内完整安全语料：
  28 个文件、五个 family。173/173 段全部选中，`max_segments=null`，
  `shard_count=1`。这个覆盖数不代表审阅了整个 GCC checkout。
- x86_64 工具链在配置中保留，供差分依赖使用；本次尚未生成可用
  checker，因此没有完成差分依赖的实际执行验证。

## 阶段状态与统计

| 环节 | 实际状态 | 耗时 | 已确认模型请求 | 已报告 tokens | 缺失 usage 记录 |
| --- | --- | ---: | ---: | ---: | ---: |
| 不变量生成 | completed：50 条接受不变量，173/173 段 | 46.652 分钟 | 539 | 1,576,298 | 0 |
| Checker 编写 | failed：43 条选中输入全部启动失败，0 个通过 | 14.364 秒 | 0 | 未报告 | 43 |
| Source mapping | 未启动 | — | 0 | — | 0 |
| 日志插桩 / GCC 重编译 | 未执行 | — | 0 | — | 0 |
| Agent 审计及独立验证 | 未启动 | — | 0 | — | 0 |

耗时按 repetition manifest 的 `created_at` 到 `updated_at` 计算，
是阶段记录时间区间，不包含整个 launcher 的全部预检开销。

Part I 从 11:00:55 到 11:47:34（+08:00），所有 539 条 usage 记录均有
provider 用量。明细如下：

| Part I 子环节 | 请求数 | tokens |
| --- | ---: | ---: |
| segmented-cot | 173 | 843,585 |
| distill_query | 29 | 47,840 |
| analogy | 232 | 445,546 |
| specialize | 7 | 21,477 |
| entailment | 98 | 217,850 |

Part II 从 11:48:00 到 11:48:15（+08:00）。总输入 50 条，
当前 ISA matcher 选中 43 条、排除 7 条。scope 文件保留全部 ID 和原因：
5 条明确指向 x86/RISC-V，另 2 条标注 Solaris/Linux-Android 并被记作
`isa_out_of_scope`。后两项是否对 AArch64 不适用仍需另行核对；
matcher 的排除不是独立的不适用性证明。

## 停止原因

43 次本地 attempt 的错误类别全部为 `WorkspaceSecurityError`。
第一条原文：

```text
WorkspaceSecurityError: deny_read_path /Users/bytedance/projects/research/de-fuzz/main collides with agent output_dir /Users/bytedance/projects/research/de-fuzz/main/.work/gcc16-aarch64-run/gcc16-aarch64-full-20261013-checker-authoring/rep-001/artifacts/checker-authoring/invgen-02c9e946ee52ca5d-71c0a44f/agents/attempt-001
```

Checker author 需要禁止读取原始 checker source root，而 attempt output
位于该目录之内，违反现有路径隔离契约。`_invoke_agent()` 在调用
`PiAgentBackend.run()` 之前执行 `validate_agent_path_isolation()`，
所以 worker 没有启动。Part II 的产物目录没有 `request.json`
或 `provider-events.jsonl`；43 条 `usage_missing=true` 是这些本地启动
失败的 ledger 记录，不能当成 43 个已发出的模型请求，也不能改写为
零 token 的已测量响应。

`checker-bundle.patch` 为 0 字节，bundle 不 ready，43 个 checker 都没有
实现变更。`run-all` 遵循停止契约，在 Part II exit 1 后停止。
这属于工程路径配置失败，没有任何编译器 PASS/FAIL verdict。

## 日志插桩的实际完成度

必须更正上一轮“插桩已接通”的说明：

- 新增 `log_instrumentation_stage.py` 能生成 instrumentation diff、
  anchor 注释和日志 ID catalog。
- 当前函数在临时源码副本生成 diff 后移除该副本，没有构建 GCC，
  也没有把重编译后的编译器交给审计工具链。
- `run-all` 的审计 argv 仍引用原 `toolchains.host.yaml`，没有消费
  instrumentation 产物以替换 compiler driver。
- 之前单独编译了 probe block；那不是编译了插桩后的 GCC，且不能证明
  每个 anchor 的运行期命中或 GCC 输出保持一致。
- 本次 source mapping 尚未执行，故没有本次生成的插桩补丁或命中日志。

因此，目前不能报告已完成“source mapping → 源码日志插桩 → GCC
重编译 → 使用新编译器审计”。

## 证据与后续执行条件

原始 stage result 引用的 96 个 artifact 均通过 SHA-256 与 size 核对，
其中 Part I 5 个、Part II 91 个；完整结果见 `artifact-integrity.json`。
50 条接受不变量文件 SHA-256：

```text
bfeb1979077f4f52e12c6c6efbb4c455bd72de6a0f221d1042e2bcea7477861c
```

CallGraph DB SHA-256：

```text
18cb0e494f916388f7af98e75a0fa3931eed0b6c95016d83cfec5f4d86aae475
```

GCC corpus manifest 的 `source_identity.git_head` 实际来自包含源码目录的
DeFuzz 仓库，不是 GCC commit；它作为原始字段保留，GCC 身份依据上文
release metadata 和 lock。`statistics.json` 记录了这个限制。

恢复执行需要在保持读隔离的前提下，将 worker output 放在 denied source
root 之外，使用独立、明确链接原 Part I 的续跑目录和 run ID，保留本次
失败证据与固定预算。正式论文 campaign 另需满足 clean-input 和隔离预检。
在声称本次完整流程完成之前，还需接通并实际验证 GCC 重编译、compiler
哈希、日志命中证据和审计工具链交接。

交付文件：

- `statistics.json` / `statistics.csv`：各阶段统计与明确的未完成范围。
- `token-usage-detail.csv`：539 条 Part I usage 和 43 条 Part II 本地失败。
- `stage-failures.json`：原始错误、invariant ID、attempt ID、证据路径。
- `observations.jsonl`：带时间戳的观测快照。
- `evidence/`：原始 manifest、plan、结果和配置的归档副本，保留原相对路径，
  用于核对而非直接启动实验。
