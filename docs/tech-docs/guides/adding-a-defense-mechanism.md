---
title: Adding a Defense Mechanism (End-to-End Checklist)
description: 从 invariant survey 到上线运行：新增一个防御机制需要触动的所有代码与文档点
priority: HIGH
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ../architecture/oracle-mechanism-framework.md
  - ../features/mechanism-contract.md
  - ../features/canary-oracle.md
  - ../reference/config-schema.md
---

# Adding a Defense Mechanism (End-to-End Checklist)

本指南把"在 DeFuzz 里支持一个新防御机制"拆成 8 个串行步骤。每步都有"如何验证"小节；按顺序做完一遍，就拿到一个能跑 `defuzz fuzz` 的端到端机制。

> **触发场景**：新增"扩展不变量"型机制（如 fortify、shadow-call-stack、IBT）；在已有机制（如 canary）下加一条新 invariant 时只需要 §3 + §4 子集。

## 1. 在 invariants survey 中记账

**位置**：`docs/tech-docs/invariants/<mechanism>.md`

按现有 24 份 survey 的格式（参考 `stack-canary.md` / `fortify-source.md`）落一份新文件：

- 列出所有 invariant 行，命名 `INV-<MECH>-<CATEGORY><NN>`（如 `INV-FORT-L01`）。
- 每条至少写：`statement` / `oracle_mapping` / `source_url_or_path` / `version_sensitivity`。
- 该文件之后会被 `oracle.InvariantResult.SourceURL` 反链回来，是 reproducibility 的根。

> **如何验证**：在新文件里 grep `INV-<MECH>` 看 ID 唯一；和 `gcc-llvm-defense-invariant-source-survey.md` 对照，确认这个机制不与已有 ID 冲突。

## 2. 实现 InvariantChecker

**位置**：`internal/oracle/checker_<...>.go`

按 `checker_dynamic_buffer.go` / `checker_static_canary.go` 模板写：

```go
type FooStaticChecker struct{ /* params */ }

func (c *FooStaticChecker) ID() string                       { return "INV-FOO-S01" }
func (c *FooStaticChecker) Category() oracle.InvariantCategory { return oracle.CategoryStatic }
func (c *FooStaticChecker) Check(ctx *oracle.CheckContext) oracle.InvariantResult {
    r := oracle.InvariantResult{ID: c.ID(), Category: oracle.CategoryStatic, SourceURL: "...", Sensitivity: "stable"}
    // ... 设置 Verdict / Evidence / Detail / Reason
    // 若机制未启用（例如缺少必需符号），返回 VerdictNotApplicable + Reason="mechanism not active"
    return r
}
```

约束：

- Category 只在 `CategoryStatic` / `CategoryDynamic` 之间二选一，对应故事线 §4 的"静态属性 / 动态属性"分类。
- 不要在 `Check` 里 panic；缺前置条件返回 `VerdictNotApplicable + Reason`。**机制未启用也走这条路径**——绝不应返回 `Fail`（那是漏洞），而是用 NA 让聚合器自然跳过。
- 复用 `BinaryInspector` (`ctx.Inspector`) 而不是自己 shell 出 `nm` / `objdump`。
- 共享 dynamic search 结果通过 `ctx.CacheGet` / `CacheSet`，命名空间 `oracle.<purpose>`。
- 如果 verdict 在 polarity 翻转下应改变，加 `r.Detail["polarity_sensitive"] = true`。

写测试 `checker_<...>_test.go`，覆盖 Pass / Fail / NotApplicable / Error 四条路径；mock 一个 `oracle.Executor` 与 `BinaryInspector`（`fake_test_helper.go` 模式）。

> **如何验证**：`go test ./internal/oracle/...` 全绿。

## 3. 用 MechanismOracle 装配 Oracle

**位置**：`internal/oracle/<mechanism>_oracle.go`

参考 `canary_oracle.go:41-156`：

```go
func init() { Register("foo", NewFooOracle) }

type FooOracle struct{ /* config */ }

func NewFooOracle(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error) {
    return &FooOracle{...}, nil
}

func (o *FooOracle) Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error) {
    if ctx == nil || ctx.Executor == nil || ctx.BinaryPath == "" {
        return nil, fmt.Errorf("foo oracle requires AnalyzeContext with Executor and BinaryPath")
    }
    return o.mechanism().Analyze(s, ctx, results)
}

func (o *FooOracle) mechanism() *MechanismOracle {
    return &MechanismOracle{
        Name: "foo defense",
        Checkers: []InvariantChecker{
            &FooStaticChecker{},       // Phase 1: 静态属性（汇编 / 二进制特征）
            &FooDynamicChecker{},      // Phase 2: 动态属性（运行时行为）
        },
        Polarizer: PolarizerFunc(o.polarityFor),
    }
}

func (o *FooOracle) polarityFor(s *seed.Seed) Polarity { /* ... */ }
```

> **如何验证**：在 `oracle_test.go` / `mechanism_test.go` 模式下写 `TestFooOracle_BasicAggregation`；`go test ./internal/oracle/...`。

## 4. 注册 Mechanism Contract

**位置**：`internal/prompt/mechanism/<name>.go`

```go
package mechanism

import "path/filepath"

func init() { Register(&fooContract{}) }

type fooContract struct{}

func (c *fooContract) OracleType() string { return "foo" }
func (c *fooContract) FunctionTemplatePath(isa string) string {
    return filepath.Join("initial_seeds", isa, "foo", "function_template.c")
}
func (c *fooContract) PlaceholderFunctionName() string { return "seed" }
func (c *fooContract) RequiredMarkers() []string       { return []string{"FOO_RETURNED"} }
func (c *fooContract) FuzzTimePromptExample() string   { /* 一段 ## CRITICAL OUTPUT REQUIREMENTS */ return "..." }
func (c *fooContract) CriticalRulesAddendum() string   { return "" }
```

`OracleType()` 返回值必须严格等于 §3 中 `oracle.Register` 的第一个参数，否则启动期校验拒绝。

> **如何验证**：`go test ./internal/prompt/mechanism/...`；写 `TestFooContract_OracleTypeMatchesOracleRegistry`。

## 5. 提供 initial seeds + understanding

**位置**：`initial_seeds/<isa>/foo/`

至少 3 个文件：

- `function_template.c` —— 含 `FUNCTION_PLACEHOLDER: seed` 注释 + 模板骨架，必须在 seed body 内打印 `FOO_RETURNED` 的位置由模板示例引导。
- `understanding.md` —— 编译器实现细节、目标函数的语义、机制特定知识（VLA/alloca、`__chk_fail` 系列、prologue 模式等）。
- 至少 1 颗初始 seed (任一裸 C 文件)，用作 corpus 起点。

参考 `initial_seeds/aarch64/canary/`。

> **如何验证**：`defuzz generate --strategy foo --isa <isa>` 能跑通模板加载；`seed.LoadUnderstanding(basePath)` 不报错。

## 6. 写 YAML 配置

**位置**：`configs/gcc-vX.Y.Z-<isa>-foo.yaml`

骨架（参考 `configs/gcc-v15.2.0-aarch64-canary.yaml`）：

```yaml
isa: "<isa>"
strategy: "foo"
compiler:
  path: "/path/to/xgcc"
  gcovr_exec_path: "..."
  source_parent_path: "..."
  gcovr_command: "gcovr ..."
  cflags: ["-O0", "-D_FORTIFY_SOURCE=2"]   # 机制启用所需
  fuzz:
    output_root_dir: "fuzz_out"
    max_iterations: 256
    cfg_file_paths: ["/path/to/cfgexpand.cc.015t.cfg", "/path/to/builtins.cc.015t.cfg"]
    use_qemu: true
    qemu_path: "qemu-aarch64"
  oracle:
    type: "foo"                            # 必须等于 fooContract.OracleType()
    options:
      max_buffer_size: 4096
      negative_cflags: ["-D_FORTIFY_SOURCE=0"]
targets:
  - file: "gcc/gcc/builtins.cc"
    functions: ["fold_builtin_object_size", "expand_builtin_memory_chk"]
```

注意：

- **不要**写 `compiler.fuzz.function_template`——这条字段在 a7307b6 之后被 contract 接管。
- `cfg_file_paths` 应包含所有 target 函数所在源文件的 dump（多 CFG 模式，见 ADR-002）。

> **如何验证**：`config.LoadConfig(...)` 不报错；`mechanism.Get(strategy).OracleType() == cfg.Compiler.Oracle.Type`。

## 7. 写文档

**位置**：

- `docs/tech-docs/features/foo-oracle.md` —— 新机制 oracle 的"实现现状"参考（参考 `canary-oracle.md` 的"实现现状"段落格式：列 invariant ↔ checker 表、Polarity sensitivity 表、报告样例）。
- 在 `docs/tech-docs/README.md` 主索引追加该 feature 的链接行。
- 如果 axes 与 canary 的 FlagScheduler 共用，在 `docs/tech-docs/features/flag-scheduler.md` §"当前限制"上补一句"也支持 foo 策略 (ISA: ...)"。

> **如何验证**：在 `docs/tech-docs/` 全文 grep `foo`，确认所有引用都更新了。

## 8. 端到端冒烟

### 8.1 快速直达测试（推荐先跑）

在启动完整 fuzz loop 之前，先写一个绕过 fuzz-loop 的直达 repro，验证 Oracle wiring 与后端行为：

```bash
go run ./cmd/foo-repro
```

通用写法参考 `@/home/yall/project/de-fuzz/docs/tech-docs/guides/oracle-e2e-testing.md` §"通用模板"与 §"验证清单"。

### 8.2 完整 fuzz loop

```bash
# 1) 单元 + 集成测试
go test ./...

# 2) 模板与 understanding 装配
defuzz generate --strategy foo --isa <isa>

# 3) 一轮 fuzz（限制 1 个 target，便于人眼审日志）
defuzz fuzz --strategy foo --limit 1 --use-qemu --log-dir logs/

# 4) 看产物
ls fuzz_out/<isa>/foo/corpus/
cat fuzz_out/<isa>/foo/state/coverage_mapping.json | jq '.line_to_seeds | length'
```

期待行为：

- 启动期看到 `Target: <isa> / foo` 与 `Oracle using QEMU executor` 日志；
- 主循环至少完成 1 轮 `solveConstraint`，无 `strategy/oracle mismatch` 错误；
- 若 oracle 报 bug，`corpus.Add` 日志含 `reason: bug` + `BugDescription` 多行结构化内容。

> **注意**：直达 repro（§8.1）与 fuzz loop（§8.2）是互补的。repro 聚焦 Oracle 逻辑本身，fuzz loop 验证 prompt / contract / seed template / coverage 的集成。两者缺一不可。

## 9. 扩展现有机制（只加 invariant，不新增机制）

子集流程：

1. §1：在已有 invariants/<mechanism>.md 追加新行；
2. §2：在 `internal/oracle/checker_<existing>.go` 内追加新 type，或新建 `checker_<descriptive>.go`；
3. §3：在该机制的 `mechanism()` 函数 `Checkers: []InvariantChecker{...}` 列表里追加；
4. §7：在对应 `features/<mechanism>-oracle.md` 的 "实现现状" 表里追加一行。

不需要动 contract / config / initial seeds。

## 10. 反向 checklist（删除一个机制）

如果要彻底移除一个机制（参考 fortify 的处理 commit `a7307b6`）：

1. 删 `internal/oracle/<mechanism>_oracle*.go` 与 `internal/prompt/mechanism/<name>.go`；
2. 把 `docs/tech-docs/features/<mechanism>-oracle.md` 移到 `docs/tech-docs/_archive/oracles/<mechanism>-oracle.md`，加 deprecation 头；
3. 在 `docs/tech-docs/README.md` 的索引里把对应行删除或移到 _archive 区；
4. 删除 `configs/*<strategy>*.yaml`（或保留并清理为 disabled 状态）。

不删 `docs/tech-docs/invariants/<mechanism>.md` —— survey 是研究产物，独立于实现存在。
