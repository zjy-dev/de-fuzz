# Ablation Study Framework Plan

## 1. 目标 (Objective)

构建一个灵活的消融测试框架，允许通过配置文件动态切换 Fuzzer 的核心组件（特别是 Prompt 构建策略和变异逻辑），以便评估不同模块（如：覆盖率反馈、未覆盖代码摘要、LLM 上下文等）对 Fuzzing 效率的具体贡献。

## 2. 配置设计 (Configuration Design)

在 `config.yaml` 中新增 `ablation` 字段，用于控制实验模式。

```yaml
ablation:
  # 模式名称，用于记录日志和区分实验
  mode: "no-uncovered-context"

  # Prompt 策略配置
  prompt_strategy:
    # 选项: "standard" (全量), "no-coverage" (无覆盖率反馈), "no-abstract" (无代码摘要), "random" (纯随机)
    type: "no-abstract"
```

## 3. 核心接口设计 (Core Interfaces)

我们将重构 `internal/prompt` 和 `internal/fuzz` 包，引入策略模式。

### 3.1 Prompt 策略接口

不再硬编码 `BuildMutatePrompt`，而是定义一个接口来决定 Prompt 的内容。

```go
package prompt

// PromptContext 包含构建 Prompt 所需的所有上下文信息
// 策略实现者可以根据需要选择性地使用这些信息
type PromptContext struct {
    ExistingSeed      *seed.Seed
    CoverageInfo      *coverage.CoverageIncrease
    UncoveredAbstract string // 未覆盖代码的摘要
    SystemPrompt      string // 基础系统提示词
}

// Strategy 定义了如何构建发给 LLM 的 Prompt
type Strategy interface {
    // Name 返回策略的唯一标识
    Name() string

    // Build 根据上下文构建最终的 Prompt 字符串
    Build(ctx *PromptContext) (string, error)
}
```

### 3.2 策略工厂 (Strategy Factory)

```go
package prompt

// NewStrategy 根据配置名称返回具体的策略实现
func NewStrategy(name string) Strategy {
    switch name {
    case "standard":
        return &StandardStrategy{}
    case "no-abstract":
        return &NoAbstractStrategy{}
    case "no-coverage":
        return &NoCoverageStrategy{}
    case "random":
        return &RandomMutationStrategy{}
    default:
        return &StandardStrategy{}
    }
}
```

## 4. 策略实现规划 (Implementation Plan)

我们需要实现几种典型的消融对照组：

### 4.1 `StandardStrategy` (Baseline)

- **描述**: 当前的完整逻辑。
- **内容**: 包含 Seed 代码 + 覆盖率增长信息 + 未覆盖代码摘要 (Abstract)。

### 4.2 `NoAbstractStrategy` (Ablation A)

- **描述**: 验证“未覆盖代码摘要”的作用。
- **内容**: 包含 Seed 代码 + 覆盖率增长信息。
- **移除**: `UncoveredAbstract`。

### 4.3 `NoCoverageStrategy` (Ablation B)

- **描述**: 验证“覆盖率反馈”的作用。
- **内容**: 仅包含 Seed 代码。
- **移除**: `CoverageInfo` 和 `UncoveredAbstract`。
- **Prompt**: 仅提示 LLM "Mutate this code to find bugs"。

### 4.4 `RandomMutationStrategy` (Baseline Zero)

- **描述**: 模拟无指导的随机变异（虽然还是用 LLM，但不给任何方向）。
- **内容**: 仅包含 Seed 代码。
- **Prompt**: "Generate a random variation of the following C code."

## 5. Engine 改造 (Engine Refactoring)

`internal/fuzz/engine.go` 需要进行以下调整：

1.  **成员变量**: `Engine` 结构体中不再直接持有 `PromptBuilder`，而是持有 `prompt.Strategy`。
2.  **初始化**: 在 `NewEngine` 时，根据 `config.Ablation.PromptStrategy.Type` 初始化对应的策略。
3.  **执行循环**: 在 `fuzzLoop` 中，收集完所有信息（Coverage, Abstract 等）后，打包成 `PromptContext`，调用 `Strategy.Build()`。

```go
// internal/fuzz/engine.go 伪代码

type Engine struct {
    // ... 其他字段
    promptStrategy prompt.Strategy
}

func (e *Engine) fuzzLoop(ctx context.Context) {
    // ... 获取 seed, 执行, 获取 coverage ...

    // 准备上下文
    pCtx := &prompt.PromptContext{
        ExistingSeed:      s,
        CoverageInfo:      covIncrease,
        UncoveredAbstract: uncoveredAbstract, // 即使策略不用，Engine 也可以先获取（或者懒加载优化）
    }

    // 使用策略构建 Prompt
    promptContent, err := e.promptStrategy.Build(pCtx)

    // ... 调用 LLM ...
}
```

## 6. 报告与指标 (Reporting)

为了对比消融测试结果，输出目录结构或日志需要包含模式信息。

- **Output Directory**: `fuzz_out/{isa}/{strategy}/{ablation_mode}/`
- **Metadata**: 在生成的 Seed Metadata 中记录 `strategy_used` 字段，以便后续分析哪个策略生成的 Seed 质量更高。

## 7. 开发步骤

1.  **Step 1**: 修改 `internal/config`，添加 Ablation 配置结构。
2.  **Step 2**: 在 `internal/prompt` 中定义 `Strategy` 接口和 `PromptContext`。
3.  **Step 3**: 将现有的 `BuildMutatePrompt` 逻辑迁移为 `StandardStrategy`。
4.  **Step 4**: 实现 `NoAbstractStrategy` 等变体。
5.  **Step 5**: 修改 `internal/fuzz/engine.go` 以使用接口。
6.  **Step 6**: 更新 `cmd/defuzz` 入口
