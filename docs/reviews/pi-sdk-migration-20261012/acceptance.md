# Pi SDK 迁移验收记录

2026-10-12，依据飞书重构计划 revision 4 执行。

原 OpenCode 主链路已切换到 Pi SDK 1.1.0：正式四阶段流水线、四个独立实验入口、
run-all、在线循环、campaign、旧 full-experiment 与 source mapper 均使用同一个
`PiAgentBackend`。旧 client/backend、项目配置、安装脚本和旧运行时测试已移出仓库；
无自动回退。原有未提交改动保留，未提交 commit。

## 验证结果

| 验证 | 结果 |
|---|---|
| 完整 Python 回归 | 670 passed，196.83 秒 |
| 最后修改后的定向回归 | 181 passed，136.92 秒 |
| 最终 SDK／审计／Checker 更新证据回归 | 77 passed，51.23 秒 |
| Node 协议、权限与用量 | 4 passed |
| fd 实际工具调用 | 1 passed |
| Ruff | 全部 Python 源码和测试通过 |
| mypy | 迁移模块定向检查通过；全仓仍有既有缺失 stubs 和类型问题 |
| 锁文件 | `uv lock --check` 通过；npm 使用固定 package-lock |
| 无旧运行时容器 | 原生 AArch64 Pi SDK smoke 通过，10 类定义可加载、隔离和离线 trace 正常 |
| amd64 最终镜像 | 构建、Pi 配置解析及旧运行时不存在检查通过 |

最终镜像为 `localhost/defuzz-orchestrator:pi-final`，其不可变 ID 见同目录 `evidence.json`。
底层镜像与最后源码 overlay 的哈希保存在 `.work/podman/pi-final-images.json`。

真实模型验证使用已有本地网关，共 **18 次请求、101,249 reported tokens**，
usage 缺失为 0，价格 unknown。在线 C seed 生成并编译通过；Checker 写入探针经
gofmt/go test 通过；格式修复保留原任务、候选输出和精确错误并通过第二次尝试。
源码映射使用真实 CallGraph，完整记录了源码与图版本差异。

一次三文件 mapping 探针在 5 次成功 CallGraph 查询后触及固定预算，已记为失败；
随后独立的一文件有界 mapping 探针通过源码锚点验证。失败没有被删除或算成完成。
这些是运行时验收，不是正式 GCC/AArch64 全量实验；写入探针是独立 Go 观察函数，
不代表新科学 Checker 已通过正式准入。

## 已落实的执行约束

每个 attempt 独立 worker/session，配置、模型、MD、schema、工具和实现文件均有
解析记录及哈希。JSONL 持续排空 stdout/stderr，超大正文以哈希引用归档；
成功需要终态、schema、产物、证据共同通过。每次实际模型请求计量，reasoning/cache
按供应商口径处理；缺用量不归零，内部后续调用也检查预算。

读写工具由执行门禁和 OS 沙箱限制。Checker 证据写入永久目录，工作树清理后仍保留
请求、session、provider/MCP 日志、patch 和命令输出。空实现或缺注册／metadata
不能通过验收。审计原始输出单独归档到 evaluator-only 目录，worker 明确无法读取。
schema 修复保留原输入和 parent_attempt_id，基础设施错误不走格式修复。

`defuzz-inspect-attempt <目录>` 只校验并重建文件证据，不请求模型或重新执行工具。
fd/rg 固定到本次隔离 runtime 并记录哈希；`PI_OFFLINE=1` 禁止自动下载工具。

## 环境限制

当前本机为 Apple Silicon，冻结 GCC 容器是 amd64，经 AArch64 Podman VM 的 QEMU
执行。该模拟环境不能创建 bubblewrap 命名空间，正式 Pi 实验因此在模型调用前拒绝。
原生 AArch64 验证容器使用 rootless user namespace 和允许嵌套 namespace 的 seccomp
设置，Pi 仍在 bubblewrap 中运行。正式冻结 GCC amd64 环境应使用原生 amd64 Linux。

历史运行结果和旧设计保留原 runtime 名称；精确清单见
`docs/tech-docs/architecture/pi-sdk-migration.md`。可执行源码、当前配置及构建没有旧
runtime 依赖；容器 smoke 中的两处旧名称仅用于确认其不存在。

结构化结果、用量、源码挂载哈希和镜像标识见同目录 `evidence.json`；
十类定义的原文件哈希及迁移正文差异见 `definitions.json`。
