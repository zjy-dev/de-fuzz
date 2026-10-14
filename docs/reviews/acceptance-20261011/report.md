# 代码评审报告

- 仓库：de-fuzz
- 检测模式：通用检测
- 检测范围：HEAD → 工作区：四实验入口与本地 MCP，含新增 source_mapping_stage.py
- 生成时间：2026-10-11 20:31
- 检查文件：7
- 变更行数：1147

## 缺陷统计

- P0：0
- P1：5
- P2：0
- 合计：5

## 缺陷详情

### 1. [P1][逻辑错误] source-mapping 成功返回后仍因缺失 token 记录被判失败

- 位置：`orchestrator/defuzz_loop/experiment_engine/source_mapping_stage.py:141-147`
- 置信度：10/10

**问题描述**

新增阶段忽略传入的 AgentBackend，直接创建 OpenCodeClient；该客户端只返回文本，没有将 provider usage 写入当前 TokenUsageSink。统一执行器 experiments_cli.py:2402-2427 对空 usage 汇总必然将成功结果改成 failed。隔离调用真实 source-mapping CLI、仅替换进程边界，提供合法映射和 usage 事件后，summary 为 mapped=3、failed=0，而 CLI 退出 1，错误为 token comparison invalid: no model usage was recorded。因此 run-all 在此停止。CLI 宣称的 HTTP 后端与实际 OpenCode 执行也不一致。

**修复建议**

为 OpenCode 增加与执行器一致的后端契约：在每次调用和重试中记录真实 usage、执行预算检查、校验实际可执行文件及模型配置；为 source-mapping 提供明确的 OpenCode 配置入口，并补充不调用真实模型的 CLI 集成测试。不要通过伪造 token 或关闭计量来让阶段通过。

---

### 2. [P1][业务语义问题] run-all 未将 source mapping 产物传给主审计

- 位置：`orchestrator/defuzz_loop/experiments_cli.py:1226-1239`
- 置信度：10/10

**问题描述**

run-all 的第三步写出 source-mappings.jsonl，第四步只接受 checker_run_dir，没有 mapping run、产物路径或哈希。agent_audit.py 也没有该产物的读取逻辑。即使第三步计量问题修复，映射结果仍不会影响主审计；这条命令实现了执行顺序，但没有完成用户验收关注的不变量到源码映射对审计的接入。该问题是功能接入缺口，不是说现有三阶段 formal pipeline 发生了回归。

**修复建议**

定义审计的映射输入和每 repetition 的产物校验，将映射传入目标定位或 worker 上下文，并记录哈希及使用证据；同时明确四实验顺序入口与 formal pipeline 的关系。

---

### 3. [P1][逻辑错误] run-all 将 checker 工程根目录与编译器源码根目录合并

- 位置：`orchestrator/defuzz_loop/experiments_cli.py:1209-1223`
- 置信度：10/10

**问题描述**

同一 args.source_root 被分别传给 checker-authoring 和 source-mapping。按新文档传 compiler-source-tree 时，Part II 会在 GCC/LLVM 树中寻找 core/internal/oracle，并以 core 为 cwd 运行 go test/go vet（checker_authoring.py:110-130）；这些路径属于 DeFuzz 工程。反过来传 DeFuzz 树时，source-mapper 遍历的又不是编译器源码。隔离解析 run-all 子命令确认两步收到同一根目录。

**修复建议**

为 checker 工程与 compiler source 定义两个独立路径参数，映射默认使用已显式给定的编译器 target-tree，并在调用首个模型前验证全部阶段的路径要求。

---

### 4. [P1][逻辑错误] source-mapping 接收 scope 参数但映射前未筛选不变量

- 位置：`orchestrator/defuzz_loop/experiment_engine/source_mapping_stage.py:135-139`
- 置信度：10/10

**问题描述**

CLI 提供 --mechanism/--isa 并说明在 source-mapper 启动前筛选，run-all 也转发 scope；阶段实现却遍历 accepted-invariants 的所有记录，未读取 mechanisms/isas。隔离 CLI 设置 canary+aarch64，仍调用了 fortify+x86_64 和 canary+loongarch64 记录，导致映射实验范围与 checker/audit 不一致并浪费预算。

**修复建议**

在构造 Invariant 前复用与 checker-authoring 一致的机制/ISA 归一化筛选规则，保存选中/排除数量并处理空选择；测试跨机制、跨架构的输入。

---

### 5. [P1][逻辑错误] LoongArch 不变量被静默改写为 x86_64

- 位置：`orchestrator/defuzz_loop/experiment_engine/source_mapping_stage.py:76-81`
- 置信度：10/10

**问题描述**

_SUPPORTED_ISAS 不包含 loongarch64；_resolve_isa 对所有不在集合内的值返回 x86_64。项目已有四 ISA 实验范围，因此 LoongArch 输入会被送到错误目标。隔离复现显示 target=loongarch64 的记录在 OpenCode prompt 中成为 isa=x86_64。

**修复建议**

扩展 OpenCode 输入 schema 和允许集合以支持 loongarch64；对未知或无法映射的 ISA 显式报错/标记不支持，禁止替换成其他架构。

---
