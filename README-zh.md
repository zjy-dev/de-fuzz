# DeFuzz

> 本文档由 AI 翻译生成

一个针对软件防御策略的模糊测试器。

使用 Golang 编写。

## 核心思想

### Seed 定义：

一个 seed 是一个独立的测试用例，包含：

```go
// internal/seed/seed.go
type Seed struct {
	ID        string // Seed 的唯一标识符
	Content   string // C 源代码
	TestCases []TestCase // 一个 seed 对应多个测试用例
}
```

编译命令是手动编写的。

### 变异原则

基于覆盖率增长。

### 测试预言机

动态测试。

运行 seeds -> 获取反馈（返回码 + stdout/stderr(s) + 日志文件）-> 让 LLM 判断是否存在漏洞。

### 模糊测试算法

针对每个防御策略和指令集架构（ISA）：

1. 使用 podman 和 qemu 准备环境

2. 构建初始提示 `ip`：

   - 当前环境和工具链
   - 手动总结防御策略和 ISA 的栈布局
   - 手动总结关于该策略和 ISA 的编译器源代码的伪代码
   - 同时将源代码作为"附件"保留在下方

3. 将 `ip` 喂给 LLM 并将其"理解"存储为记忆
   <!-- 如果 LLM 不理解你的需求，那么如何使用 LLM 进行模糊测试？ -->

4. 初始化 seed 池：

   - 让 LLM 生成初始 seed(s) || 使用官方单元测试
   - 手动调整初始 seeds
   - 运行初始 seeds 并记录其覆盖率信息

5. 从 seed 池中取出 seed `s`

6. 编译 `s` 并记录覆盖率信息

   - 如果覆盖率提高，则将 `s` 变异为 `s'` 并将 `s'` 推入 seed 池

7. 预言机(s)：
   <!-- TODO: 未来可能使用多臂老虎机进行变异 -->
   - 记录是否发现了漏洞

## 实现

### 覆盖率

使用 gcc 的 gcov 生态系统。

简化的工作流程如下：

```bash
#!/bin/bash

# --- 初始文件夹设置 ---
SRCDIR=/root/fuzz-coverage/gcc-release-gcc-12.2.0
BUILDDIR=/root/fuzz-coverage/gcc-build
REPORTDIR=/root/fuzz-coverage/coverage_report

# --- 步骤 1: 使用覆盖率标志重新编译目标编译器 `tc` ---
echo "=== [1/4] 配置并编译带覆盖率标志的 GCC... ==="
mkdir -p $BUILDDIR $REPORTDIR
cd $BUILDDIR
$SRCDIR/configure \
    --enable-coverage \
    --disable-bootstrap \
    --enable-languages=c,c++
make -j$(nproc)

# --- 步骤 2: 运行 `tc` 以生成 .gcda ---
echo "=== [2/4] 运行插桩的 GCC 以生成覆盖率数据... ==="
echo 'int main() { return 0; }' > /tmp/test.c
$BUILDDIR/gcc/xgcc -fstack-protector-strong -o /tmp/test.o -c /tmp/test.c

# --- 步骤 3: 使用 lcov 处理覆盖率信息 ---
echo "=== [3/4] 使用 lcov 捕获覆盖率数据... ==="
cd $BUILDDIR
lcov --capture --directory . --output-file coverage.info

# --- 步骤 4(可选): 生成 HTML 报告 ---
echo "=== [4/4] 生成 HTML 报告... ==="
genhtml coverage.info --output-directory $REPORTDIR
```

<!-- ## 使用方法

DeFuzz 是一个具有多个子命令的命令行工具。

### `generate`

此命令用于为特定的 ISA 和防御策略生成初始 seed 池。

**使用方法：**

```bash
go run ./cmd/defuzz generate --isa <目标-isa> --strategy <目标-策略> [标志]
```

**标志：**

- `--isa`: （必需）目标 ISA（例如 `x86_64`）。
- `--strategy`: （必需）防御策略（例如 `stackguard`）。
- `-o, --output`: seeds 的输出目录（默认：`initial_seeds`）。
- `-c, --count`: 要生成的 seeds 数量（默认：`1`）。

**注意：** 在运行 generate 命令之前，请确保已使用提供的容器脚本设置模糊测试环境：`./scripts/build-container.sh`

### Seed 存储

`initial_seeds/` 目录存储与特定模糊测试目标（ISA 和防御策略的组合）相关的所有数据。这包括 LLM 对目标的缓存理解和各个 seeds。

```
initial_seeds/<isa>/<防御_策略>/
├── understanding.md
└── <id>/
    ├── source.c
    ├── Makefile
    └── run.sh
```

- **`<isa>`**: 目标指令集架构（例如 `x86_64`）。
- **`<防御_策略>`**: 正在模糊测试的防御策略（例如 `stackguard`）。
- **`understanding.md`**: 包含 LLM 对初始提示的总结和理解的缓存文件。这在首次运行时生成并重复使用，以节省时间和 API 调用。
- **`<id>`**: 每个单独 seed 的目录，包含：
  - **`source.c`**: seed 的 C 源代码
  - **`Makefile`**: 构建说明和编译标志
  - **`run.sh`**: 用于测试编译二进制文件的执行脚本

## 项目结构

该项目的结构旨在分离模糊测试器的不同逻辑组件，遵循标准的 Go 项目布局约定。这使得代码库更易于理解、维护和测试。

- **`cmd/defuzz/`**: 这是应用程序的主要入口点。此目录中的 `main.go` 文件负责解析命令行参数，处理不同的执行模式（`generate` 和 `fuzz`），并启动适当的进程。

- **`internal/`**: 此目录包含模糊测试器的所有核心逻辑。由于它是 `internal`，因此此代码不能被其他外部项目导入。

  - **`config/`**: 提供一种通用方法，从 `configs/` 目录中存储的 YAML 文件加载配置（例如用于 LLM）。它使用 Viper 库按名称自动查找和解析文件（例如 `llm.yaml`），并包含针对格式错误或缺失文件的健壮错误处理。
  - **`exec/`**: 一个低级实用程序包，提供用于在主机系统上执行外部 shell 命令的健壮辅助函数。
  - **`vm/`**: 管理容器化执行环境。它处理 Podman 容器的创建、启动和停止。它提供在容器_内部_运行命令的函数（用于编译和执行 seeds），通过使用 `exec` 包调用 `podman exec`。
  - **`llm/`**: 负责与大型语言模型的所有交互。它采用模块化设计，具有 `LLM` 接口以支持不同的提供商。`New()` 工厂函数根据 `configs/llm.yaml` 初始化客户端（例如 `DeepSeekClient`），允许轻松扩展和测试。其职责包括处理初始提示、生成和变异 seeds 以及分析反馈。
  - **`prompt/`**: 专注于为 LLM 构建详细的初始提示，包括环境详细信息和防御策略摘要。
  - **`seed_executor/`**: 在 VM 中执行 seed。它准备环境，运行 seed 的命令，并返回结果。
  - **`seed/`**: 定义 seeds 的数据结构并管理 seed 池（例如添加、保存和加载 seeds）。
  - **`analysis/`**: 处理模糊测试反馈的分析。它将解释 seed 执行的结果，以确定是否发现了漏洞。
  - **`report/`**: 处理将有漏洞的 seeds 及其相关反馈保存为报告。
  - **`fuzz/`**: 包含高级编排逻辑。在 `generate` 模式下，它协调 `prompt`、`llm` 和 `seed` 以创建初始 seed 池。在 `fuzz` 模式下，它运行主模糊测试循环，管理漏洞计数，并确定何时退出。

- **`pkg/`**: 用于可以安全导入和由外部应用程序使用的代码。目前为空，但保留供将来使用。

- **`configs/`**: 配置文件的指定位置，例如 LLM 或不同模糊测试目标的设置。

- **`scripts/`**: 用于存储辅助脚本，例如自动化构建、运行测试或设置环境。

- **`testdata/`**: 包含运行测试所需的示例文件和数据，例如示例 C/汇编源文件。

## 工作流程

- 2025-01-23: 更新文档以反映统一的 seed 结构（C + Makefile + run.sh）并移除已弃用的 seed 类型参数。
- 2025-08-01: 更新 seed 计划以反映三种 seed 类型。
- 2025-07-31: 为报告模块创建计划。
- 2025-07-31: 审查并更新所有模块计划。 -->
