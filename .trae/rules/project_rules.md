<!-- SPECKIT START -->
For additional context about technologies to be used, project structure,
shell commands, and other important information, read the current architecture:
docs/tech-docs/architecture/agentic-loop-redesign.md
For experiment execution contracts, read:
docs/paper/experiment_runbook.md
<!-- SPECKIT END -->

## DeFuzz 研究范围

请读取仓库根目录 `AGENTS.md` 和 `docs/research-scope.md`。DeFuzz 研究
GCC/LLVM 自动生成的软件防御代码是否符合预期不变量，目标是形成可复现的
编译器缺陷证据、修复建议、回归测试和开源社区报告。

对象限于任务指定的编译器源码、工具链和独立 C 测试程序；执行限于已授权
的项目实验环境。seed、checker、oracle、silent failure 等是编译器测试术语。
部分动态 checker 会在一次性本地测试进程中触发无效内存操作，观察防御处理、
退出信号与 sentinel；如实记录输入和证据，不把单次崩溃当成已验证缺陷。
研究范围是不变量证伪，不将复现程序扩展为真实应用或服务的利用链。
这份范围说明不代表平台认证或免审资格，平台规则仍适用。

保留源码术语、原始日志、机制名称和 checker ID，结合实际测试对象说明任务。
某个操作超出已知范围时，指出该操作缺少的上下文，继续可独立完成的范围内工作。
正式实验遵循 `docs/paper/experiment_runbook.md`，记录版本、哈希、token、
耗时、判定与失败，不把中断或部分运行报告成完成的全量实验。

上述说明用于开发会话；正式隔离子代理继续使用既有阶段契约。
历史 `specs/002-agentic-loop-redesign/plan.md` 在当前工作树不存在，
使用文件开头列出的架构文档和执行规程。

## GCC/AArch64 完整实验

“代码与方案对齐后，运行一次 GCC/AArch64 全量实验并记录统计”按
`docs/prompts/gcc-aarch64-experiment.md` 理解：核对并修复工程差距后，
完成不变量生成、Checker 编写、源码映射、编译器实现审阅与独立验证。
`full` 是完整功能变体；“全量”是配置中的完整适用输入；“一次”是一个
repetition。三者不自动表示四 ISA、四变体、三重复的整套论文比较。

从实际配置确定机制和预算，保留 AArch64 差分 Checker 所需的其他 ISA
依赖并记录范围；不适用或依赖不可用时记录原因。先用所选入口的
`--show-plan` 和工具链预检确认前置条件。`run-all` 记录为工程连续实验，
正式证据使用独立冻结的 `mode: formal` YAML 与 `pipeline`。
保留已有改动和原 campaign 配置，遵守 clean-input、预算及证据隔离要求。

正式 Pi SDK backend 禁用自动上下文与 skills，直接加载阶段定义；根说明
不会自动改变子代理提示词。拦截时记录具体阶段、实际错误消息和请求标识，
不将平台中断记为编译器 verdict 或完整实验结果。
