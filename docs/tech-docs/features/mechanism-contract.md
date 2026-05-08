---
title: Mechanism Contract — 防御机制契约
description: internal/prompt/mechanism 的 Contract 接口、registry、启动期 fail-fast 校验与 prompt/响应期落点
priority: HIGH
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ../architecture/prompt-architecture.md
  - ../architecture/oracle-mechanism-framework.md
  - ../guides/adding-a-defense-mechanism.md
---

# Mechanism Contract — 防御机制契约

`internal/prompt/mechanism/` 是 commit `a7307b6 refactor(prompt): bind strategies to mechanism contracts` 引入的薄层抽象：把"strategy ↔ oracle ↔ function template ↔ output marker"这条链路集中到 `Contract` 接口，让 `cfg.Strategy` 与 `cfg.Compiler.Oracle.Type` 不一致时启动期就失败，避免 fuzz 跑了 N 轮才发现 prompt 选错。

## 1. 接口

```go
// internal/prompt/mechanism/contract.go:11-38
type Contract interface {
    OracleType() string                    // 必须等于 cfg.Compiler.Oracle.Type
    FunctionTemplatePath(isa string) string // 派生 initial_seeds/<isa>/<strategy>/function_template.c
    PlaceholderFunctionName() string       // LLM 必须实现的函数名 (canary 是 "seed")
    RequiredMarkers() []string             // 合并模板后必须存在的字符串 (canary 是 ["SEED_RETURNED"])
    FuzzTimePromptExample() string         // ## CRITICAL OUTPUT REQUIREMENTS 块的完整内容
    CriticalRulesAddendum() string         // 追加到 critical-rules 块尾的机制特定规则
}
```

## 2. registry

```go
// internal/prompt/mechanism/registry.go
var registry = map[string]Contract{}

func Register(c Contract)         // 重复注册 panic
func Get(name string) (Contract, bool)
func MustGet(name string) Contract
```

注册发生在各 contract 文件的 `init()`：

| 文件 | 注册键 | 现状 |
| --- | --- | --- |
| `mechanism/canary.go:5-7` | `"canary"` | ✅ 已注册 (`canaryContract`) |
| (未来) `mechanism/fortify.go` | `"fortify"` | ❌ 未实现 (见 `_archive/oracles/fortify-oracle.md`) |

## 3. 校验落点

### 3.1 启动期 (fail-fast)

`@/home/yall/project/de-fuzz/cmd/defuzz/app/fuzz.go:250-264`：

```go
mechanismContract, ok := mechanism.Get(cfg.Strategy)
if !ok {
    return fmt.Errorf("no mechanism contract registered for strategy %q; register it in internal/prompt/mechanism/", cfg.Strategy)
}
if mechanismContract.OracleType() != cfg.Compiler.Oracle.Type {
    return fmt.Errorf(
        "strategy/oracle mismatch: strategy %q declares oracle type %q but cfg.Compiler.Oracle.Type is %q",
        cfg.Strategy, mechanismContract.OracleType(), cfg.Compiler.Oracle.Type,
    )
}

functionTemplate := mechanismContract.FunctionTemplatePath(cfg.ISA)
promptBuilder := prompt.NewBuilder(cfg.Compiler.Fuzz.MaxTestCases, functionTemplate, mechanismContract)
```

YAML 里的 `compiler.fuzz.function_template` 字段不再被读取（路径完全由 contract 决定）；YAML 里的 `compiler.oracle.type` 与顶层 `strategy` 必须互相一致。

### 3.2 响应期 (LLM 输出校验)

LLM 返回的 C 代码经 `ParseLLMResponse` 与模板合并后，`prompt.Builder` 会校验 `contract.RequiredMarkers()` 列表里的每个字符串都在最终 seed 文本中至少出现一次。canary 的唯一 marker 是 `"SEED_RETURNED"`——它必须在 `seed()` 函数体内、`return` 之前 `printf` 出来，否则被判为无效响应（参见 `@/home/yall/project/de-fuzz/docs/tech-docs/features/canary-oracle.md` §"假阳性修复"）。

### 3.3 Prompt 注入

`Builder` 在 constraint-solving prompt 里组装两段 contract 内容：

- `## CRITICAL OUTPUT REQUIREMENTS` ← `FuzzTimePromptExample()`
- 在 `## CRITICAL RULES` 末尾追加 ← `CriticalRulesAddendum()`

样例（canary）：

```
## CRITICAL OUTPUT REQUIREMENTS
**DO NOT include ANY explanations...**
Example of CORRECT output:
` ` ` c
void seed(int buf_size, int fill_size) {
    char buffer[64];
    memset(buffer, 'A', fill_size);
    printf("SEED_RETURNED\n");
    fflush(stdout);
}
` ` `

(critical rules 末尾追加)
- **You CAN add function attributes** like __attribute__((stack_protect)) if needed.
```

## 4. 当前已注册：canary

`internal/prompt/mechanism/canary.go` 全文 60 行；关键点：

| 方法 | 返回值 |
| --- | --- |
| `OracleType` | `"canary"` |
| `FunctionTemplatePath("aarch64")` | `"initial_seeds/aarch64/canary/function_template.c"` |
| `PlaceholderFunctionName` | `"seed"` |
| `RequiredMarkers` | `["SEED_RETURNED"]` |
| `FuzzTimePromptExample` | 一段含正例 + 带 `__attribute__((stack_protect))` 反例 + `// ||||| CFLAGS_START / END |||||` 标记的输出格式说明 |
| `CriticalRulesAddendum` | `"- **You CAN add function attributes** like __attribute__((stack_protect)) if needed."` |

要给 `EpilogueCanaryScrubChecker` (INV-SP-R03) 增加新 marker（例如 `CANARY_SCRUB_OK` / `GUARD_LEAKED`）需要谨慎：当前这些 marker 是 oracle **运行时**期望的输出，不是 prompt 合并 **时**必须存在的字符串。`RequiredMarkers` 校验在编译之前；`scrub` 模式的 marker 只在 oracle 跑 `<binary> scrub` 时由 binary 自行打印，不会影响 prompt 校验。

## 5. 扩展契约（最小步骤）

```go
// internal/prompt/mechanism/<name>.go
package mechanism

import "path/filepath"

func init() { Register(&fooContract{}) }

type fooContract struct{}

func (c *fooContract) OracleType() string                { return "foo" }
func (c *fooContract) FunctionTemplatePath(isa string) string {
    return filepath.Join("initial_seeds", isa, "foo", "function_template.c")
}
func (c *fooContract) PlaceholderFunctionName() string   { return "seed" }
func (c *fooContract) RequiredMarkers() []string         { return []string{"FOO_RETURNED"} }
func (c *fooContract) FuzzTimePromptExample() string     { return "..." }
func (c *fooContract) CriticalRulesAddendum() string     { return "" }
```

注册路径必须有 oracle factory 同名注册（`oracle.Register("foo", NewFooOracle)`）；否则启动期 oracle 创建会失败。完整端到端流程见 `@/home/yall/project/de-fuzz/docs/tech-docs/guides/adding-a-defense-mechanism.md`。

## 6. 故障域速查

| 现象 | 排查 |
| --- | --- |
| 启动报 `no mechanism contract registered for strategy "X"` | 没有 `internal/prompt/mechanism/X.go` 在 init 注册；新增 contract 后必须 import 该包以触发 init（package `mechanism` 已被 `cmd/defuzz/app/fuzz.go` 间接 import，所以同包 init 自动跑） |
| 启动报 `strategy/oracle mismatch` | YAML 里 `compiler.oracle.type` 与 `strategy` 不一致；改其中一个 |
| LLM 输出永远 `RequiredMarkers` 校验失败 | prompt 没有展示 marker 用法；检查 `FuzzTimePromptExample` 是否含示例，understanding.md 是否提及 marker 重要性 |
| 多个 contract 同名注册 | `Register` 会 panic；典型于复制粘贴新 contract 时忘改 `OracleType` |

代码：`@/home/yall/project/de-fuzz/internal/prompt/mechanism/`、装配：`@/home/yall/project/de-fuzz/cmd/defuzz/app/fuzz.go:250-275`。
