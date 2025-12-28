# DeFuzz

一个基于 LLM 约束求解的编译器防御策略模糊测试工具。

使用 Golang 编写。

## 核心思想

DeFuzz 灵感来自 HLPFuzz 论文。它使用 **LLM 驱动的渐进式约束求解** 来系统地探索编译器防御实现中难以触达的代码路径。

### 核心概念：基于 LLM 的约束求解

与传统覆盖率引导的模糊测试（随机变异）不同，DeFuzz 主动选择目标并引导 LLM 生成满足特定路径约束的输入。

### Seed 定义

一个 seed 是一个独立的测试用例，包含：

```go
// internal/seed/seed.go
type Seed struct {
	ID        uint64      // 唯一标识符
	Content   string      // C 源代码
	TestCases []TestCase  // 多个测试用例
	Meta      SeedMetadata
}

type SeedMetadata struct {
	FilePath   string    // Seed 目录路径
	ParentID   uint64    // 父种子 ID（初始种子为 0）
	Depth      int       // 迭代深度
	State      string    // 种子状态
}
```

### 模糊测试算法（约束求解循环）

针对每个防御策略和 ISA：

```
1. 维护 mapping：代码行 → 首次覆盖它的种子 ID

2. 运行初始种子，建立 mapping，将种子持久化到磁盘

3. 约束求解循环：
   a. 选择目标：后继最多的未覆盖基本块（CFG 引导）
   b. 构建 prompt：
      - 目标函数代码（标注：已覆盖/未覆盖/目标行）
      - Shot：覆盖目标前驱的种子
   c. 发送给 LLM：根据 shot 变异以覆盖目标 BB
   d. 编译并测试变异后的种子

   IF 变异种子覆盖了目标 BB：
      - 更新 mapping
      - 送入 Oracle
      - 跳转到步骤 3a
   ELSE：
      - 运行发散分析（uftrace）查找 call trace 差异
      - 将发散信息发送给 LLM 进行精细化变异
      - 跳转到步骤 3d
```

### 测试预言机

动态测试与 LLM 分析：

```
运行种子 → 获取反馈（返回码 + stdout + stderr）→ LLM 判断是否存在漏洞
```

**注意：** 所有编译和执行直接在主机上进行。请确保系统中已安装所需的工具链（GCC、QEMU 等）并在 PATH 中可用。

## 实现

### 覆盖率与 CFG 分析

#### 1. GCC CFG Dump

用于选择目标（后继最多的基本块）：

1. 为目标文件构建带 `-fdump-tree-cfg-lineno` 的目标编译器
2. 收集 `.cfg` 文件（如 `cfgexpand.cc.015t.cfg`）
3. 构建映射：`File:Line -> BasicBlockID` 和 `BasicBlockID -> SuccessorCount`
4. 运行时查询以选择目标 BB

#### 2. gcov 覆盖率测量

测试 gcc 时，使用 gcc 的 gcov 生成覆盖率信息，gcovr 进行统计：

1. 使用 gcov 覆盖率编译选项定制目标编译器 `tc`
2. 清理 `gcovr_exec_path`（在 compiler-isa-strategy.yaml 中配置）
3. 编译种子 → 生成 `*.gcda` 文件
4. 运行 gcovr 生成 JSON 报告
5. 与 total.json 对比检查覆盖率增长
6. 合并报告

### 带覆盖率的变异

当种子导致覆盖率增加时：

1. 获取覆盖率增加详情（哪些函数/行被新覆盖）
2. 获取当前总覆盖率统计
3. 构建变异 prompt，包含：
   - 原始种子源代码
   - 覆盖率增加摘要
   - 详细的覆盖率增加报告
   - 当前总覆盖率百分比

### 发散分析

当变异未能覆盖目标时，使用 uftrace 进行函数级发散分析：

1. 记录基准种子和变异种子的 traces：
   ```bash
   uftrace record -P '.*' -d trace_dir gcc -c seed.c -o /dev/null
   ```
2. 从 task.txt 提取 cc1 进程 ID
3. 导出调用序列（过滤噪声）
4. 找到第一个发散的函数调用
5. 将发散信息返回给 LLM 进行精细化变异

## 模块架构

`internal` 目录包含核心逻辑：

### 核心数据结构

- **`seed`**：Seed 结构（源代码 + 测试用例 + 元数据）。在模块间传递的中心数据类型。
- **`state`**：全局持久化状态（唯一 ID、全局覆盖率统计）。支持恢复。
- **`corpus`**：管理种子队列，处理优先级、选择和持久化。

### 执行与环境

- **`exec`**：`os/exec` 的低级封装，用于 shell 命令执行。
- **`vm`**：抽象执行环境：`LocalVM`（本地）和 `QEMUVM`（跨架构）。
- **`compiler`**：带覆盖率插装支持的 GCC 编译器封装。
- **`seed_executor`**：使用 `vm` 模块编排种子执行。

### 分析与反馈

- **`coverage`**：覆盖率测量与分析：
  - `Measure()`：编译种子并生成覆盖率报告
  - `HasIncreased()`：检查覆盖率是否增加
  - `GetIncrease()`：获取详细的覆盖率增加信息
  - `Merge()`：将新覆盖率合并到总覆盖率
  - `CFGGuidedAnalyzer`：CFG 引导的目标选择
  - `DivergenceAnalyzer`：基于 uftrace 的 call trace 分析
- **`oracle`**：使用 LLM 分析执行结果的测试预言机
- **`report`**：以 Markdown 格式持久化漏洞报告

### LLM 集成

- **`llm`**：LLM 提供商的统一接口（如 DeepSeek）
- **`prompt`**：为 LLM 构建 prompt，包含：
  - ISA 细节和防御策略
  - 源代码和覆盖率信息
  - Shots（示例种子）
  - 发散分析结果

### 编排

- **`fuzz`**：实现约束求解循环的中心引擎：
  1. 选择目标 BB（CFG 引导）
  2. 构建带 shot 的 prompt
  3. LLM 变异
  4. 编译和测试
  5. 失败 → 发散分析 → 精细化变异
  6. 成功 → 更新 mapping → Oracle 分析
- **`config`**：使用 Viper 的集中配置管理

## 使用方法

### 前置条件

1. 已安装 **Go 1.21+**
2. 已安装 **带 gcov 支持的 GCC**（用于覆盖率测量）
3. 已安装 **gcovr**（`pip install gcovr`）
4. 已安装 **uftrace**（用于发散分析）
5. 已安装 **QEMU user-mode**（可选，用于跨架构模糊测试）
6. **LLM API 访问**（如 DeepSeek API key）

### 配置文件

#### 1. 主配置 (`configs/config.yaml`)

```yaml
config:
  llm: "deepseek"
  isa: "x64"
  strategy: "canary"
  compiler:
    name: "gcc"
    version: "12.2.0"
```

#### 2. LLM 配置 (`configs/llm.yaml`)

```yaml
llms:
  - provider: "deepseek"
    model: "deepseek-coder"
    api_key: "sk-your-api-key-here"
    temperature: 0.7
```

#### 3. 编译器配置 (`configs/gcc-v{version}-{isa}-{strategy}.yaml`)

```yaml
compiler:
  path: "/path/to/gcc/xgcc"
  gcovr_exec_path: "/path/to/gcc-build"
  source_parent_path: "/path/to/source"

targets:
  - file: "gcc-releases-gcc-12.2.0/gcc/cfgexpand.cc"
    functions:
      - "stack_protect_classify_type"
```

### 目录结构

```
de-fuzz/
├── configs/
│   ├── config.yaml
│   ├── llm.yaml
│   └── gcc-v12.2.0-x64-canary.yaml
├── initial_seeds/
│   └── {isa}/
│       └── {strategy}/
│           ├── understanding.md
│           └── *.seed
└── fuzz_out/
    ├── corpus/
    ├── coverage/
    ├── build/
    └── state/
```

### 命令

#### 生成初始种子

```bash
./defuzz generate --count 5
```

#### 开始模糊测试

```bash
./defuzz fuzz
./defuzz fuzz --max-iterations 100 --max-new-seeds 3 --timeout 30
./defuzz fuzz --use-qemu --qemu-path qemu-aarch64 --qemu-sysroot /usr/aarch64-linux-gnu
```

### 测试

```bash
# 单元测试
go test -v -short ./internal/...

# 集成测试（需要 QEMU 和交叉编译器）
go test -v -tags=integration ./internal/...
```
