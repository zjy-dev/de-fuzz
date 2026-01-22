# Prompt Architecture

本文档描述了 DeFuzz 的提示词架构设计，包括提示词的层次结构、组装逻辑和文件组织。

## 概述

DeFuzz 使用分层的提示词架构，将通用逻辑与特定领域知识分离：

```
System Prompt = Base Prompt + Understanding
```

这种设计允许：
- **复用**：基础提示词在所有 ISA 和防御机制间共享
- **上下文**：understanding.md 提供编译器内部实现细节和领域知识

## 目录结构

```
de-fuzz/
├── prompts/
│   └── base/                      # 基础提示词 (通用)
│       ├── generate.md            # 种子生成阶段
│       ├── constraint.md          # 约束求解阶段
│       ├── compile_error.md       # 编译错误重试
│       └── mutate.md              # 变异阶段
│
└── initial_seeds/
    └── {isa}/
        └── {strategy}/            # ISA + 防御机制特定
            ├── understanding.md   # 编译器内部实现上下文 + 领域知识
            ├── stack_layout.md    # 栈布局参考 (可选)
            └── function_template.c # 函数模板
```

## 提示词类型

### 1. Base Prompts (`prompts/base/`)

通用的 LLM 行为指导，适用于所有目标：

| 文件 | 阶段 | 用途 |
|------|------|------|
| `generate.md` | 种子生成 | 指导 LLM 生成符合规范的 C 代码 |
| `constraint.md` | 约束求解 | 指导 LLM 修改代码以触发特定基本块 |
| `compile_error.md` | 编译错误处理 | 指导 LLM 修复编译错误 |
| `mutate.md` | 随机变异 | 指导 LLM 变异现有种子 |

**示例** (`constraint.md`):
```markdown
You are a compiler security testing expert.

Your task is to modify C code to trigger specific basic blocks 
in the compiler's source code during compilation.

Rules:
- Make minimal changes to the code
- Use C99/C11 standard features
- Focus on triggering the target function/block
```

### 2. Understanding (`initial_seeds/{isa}/{strategy}/understanding.md`)

编译器内部实现的详细上下文和领域知识，帮助 LLM 理解要触发的代码路径：

**内容包括：**
- 目标函数的作用和实现细节
- 关键变量和条件分支
- 如何生成能触发特定分支的测试用例
- ISA/策略特定的领域知识（如 VLA/alloca 模式）

**示例片段**:
```markdown
## expand_stack_vars

This function handles stack variable allocation. Key behaviors:
- Variables with `DECL_NO_TBAA_P` flag bypass normal alignment
- Large arrays (> MAX_SUPPORTED_STACK_ALIGNMENT) trigger special handling
- VLAs are allocated dynamically and affect canary placement

## AArch64 Stack Canary Patterns

VLA and alloca() have different stack layouts on AArch64.
On AArch64, the stack canary is placed ABOVE dynamically-sized 
arrays, leaving the return address vulnerable.

Stack Layout with VLA:
High Addr → [Canary]          ← Protected but above VLA
            [Saved LR]        ← VULNERABLE!
            [Saved FP]
            [VLA Buffer]      ← Overflow starts here
Low Addr  → [Stack Pointer]
```

## 提示词组装逻辑

`PromptService` 负责组装最终的系统提示词：

```go
func (s *PromptService) GetSystemPrompt(phase Phase) (string, error) {
    // 1. 加载 base prompt
    baseContent := readFile("prompts/base/" + phase + ".md")
    
    // 2. 追加 understanding (如果存在)
    if understanding != "" {
        result += "\n\n" + understanding
    }
    
    return result
}
```

**组装顺序**:
```
┌─────────────────────────┐
│     Base Prompt         │  ← 通用行为指导
├─────────────────────────┤
│     Understanding       │  ← 编译器实现细节 + 领域知识
└─────────────────────────┘
```

## 配置

在 YAML 配置文件中指定提示词路径：

```yaml
compiler:
  fuzz:
    # 基础提示词目录 (默认: prompts/base)
    base_prompt_dir: "prompts/base"
    
    # 函数模板路径
    function_template: "initial_seeds/aarch64/canary/function_template.c"
```

`understanding.md` 路径由系统自动推导：
```
initial_seeds/{isa}/{strategy}/understanding.md
```

## 添加新的 ISA/策略

1. **创建目录结构**:
   ```bash
   mkdir -p initial_seeds/{new_isa}/{new_strategy}
   ```

2. **创建必需文件**:
   - `understanding.md` - 目标编译器函数的上下文 + 领域知识
   - `function_template.c` - 种子代码模板

3. **更新配置文件**:
   ```yaml
   isa: "new_isa"
   strategy: "new_strategy"
   compiler:
     fuzz:
       function_template: "initial_seeds/new_isa/new_strategy/function_template.c"
   ```

## API 参考

`PromptService` 提供以下方法：

| 方法 | 返回值 | 用途 |
|------|--------|------|
| `GetSystemPrompt(phase)` | `(string, error)` | 获取指定阶段的系统提示词 |
| `GetConstraintPrompt(ctx)` | `(system, user, error)` | 约束求解提示词对 |
| `GetRefinedPrompt(ctx, div)` | `(system, user, error)` | 带分歧分析的提示词 |
| `GetCompileErrorPrompt(ctx, err)` | `(system, user, error)` | 编译错误重试提示词 |
| `GetMutatePrompt(path, ctx)` | `(system, user, error)` | 变异阶段提示词 |
| `GetGeneratePrompt(path)` | `(system, user, error)` | 种子生成提示词 |
| `ParseLLMResponse(resp)` | `(*seed.Seed, error)` | 解析 LLM 响应 |

## 文件状态

| 文件 | 状态 | 说明 |
|------|------|------|
| `prompts/base/*.md` | ✅ 活跃 | 所有阶段的基础提示词 |
| `understanding.md` | ✅ 活跃 | 编译器上下文 + 领域知识，参与提示词组装 |
| `function_template.c` | ✅ 活跃 | LLM 生成代码的模板 |
