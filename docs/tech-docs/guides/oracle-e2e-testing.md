---
title: Oracle End-to-End Testing (Bypassing the Fuzz Loop)
description: 如何为任意 Oracle 编写绕过 fuzz-loop / corpus-manager 的直达测试，快速验证 checker wiring 与后端行为
priority: HIGH
last_updated: 2026-05-24
status: IMPLEMENTED
related_docs:
  - ../architecture/oracle-mechanism-framework.md
  - ../features/canary-oracle.md
  - ../guides/adding-a-defense-mechanism.md
  - ../../reference/config-schema.md
---

# Oracle 端到端测试：绕过 Fuzz Loop

## 为什么要写直达测试

DeFuzz 的完整 fuzz 链路很长：

```
LLM → seed.Seed → CorpusManager → 编译 → VM/QEMU 执行 → Oracle → 结果聚合
```

在开发或调试一个 Oracle 时，如果每次验证都要跑完整链路，迭代成本极高。因此仓库维护一组**绕过 fuzz-loop 的直达 repro**：它们直接实例化 Oracle、提供编译产物、调用 `Analyze`，在几秒内给出 verdict。

这类测试的核心价值：

1. **快速验证 wiring**：确认新写的 `InvariantChecker` 被正确注册到 `MechanismOracle`。
2. **后端行为确认**：在真实交叉编译器 + QEMU 上观测输出，验证 checker 的解析逻辑与预期一致。
3. **回归保护**：CI 中单独跑这些 repro，确保 Oracle 不被后续重构破坏。
4. **调试聚焦**：当 fuzz 报出异常 verdict 时，用直达测试最小化复现场景。

## 适用场景

- 新增一个 `InvariantChecker` 后，需要确认它在真实后端上能 Pass / Fail / NA。
- 升级交叉编译器（GCC 主版本、backend 变化）后，重跑已有 invariant 的触发条件。
- 为长尾 ISA（LoongArch64、RISC-V、CSKY 等）首次验证 `MechanismOracle` 的跨 ISA 适配。
- 构造 CVE / 已知缺陷的正控组，供 fuzzer 的"种子质量评估"引用。

## 接口速览

### Oracle 接口

```go
// internal/oracle/oracle.go
type Oracle interface {
    Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error)
}
```

- `s`：种子元数据 + 源码内容。对直达测试来说，通常只需要填 `Meta` 和 `Content`。
- `ctx`：分析上下文，**必须**提供 `BinaryPath`；若 Oracle 是 *active*（需要自行执行二进制），还需提供 `Executor`。
- `results`：fuzz 引擎已经跑过的结果。直达测试通常传 `nil`，由 Oracle 自己驱动执行。

### AnalyzeContext

```go
type AnalyzeContext struct {
    BinaryPath string        // 编译产物路径
    Executor   Executor      // 二进制执行接口（QEMU / local）
}
```

### Executor 接口

```go
type Executor interface {
    ExecuteWithInput(binaryPath string, stdin string) (exitCode int, stdout string, stderr string, err error)
    ExecuteWithArgs(binaryPath string, args ...string) (exitCode int, stdout string, stderr string, err error)
}
```

实现者：

| 实现 | 路径 | 用途 |
| --- | --- | --- |
| `QEMUOracleExecutorAdapter` | `internal/seed_executor/executor.go:276-283` | 交叉编译产物在 QEMU user-mode 下执行 |
| `LocalExecutor` | `internal/seed_executor/executor.go:75-118` | 本机二进制直接执行 |

## 通用模板

以下是一个可复用的 Go 驱动模板，放在 `cmd/<mechanism>-repro/main.go`：

```go
package main

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"

    "github.com/zjy-dev/de-fuzz/internal/oracle"
    "github.com/zjy-dev/de-fuzz/internal/seed"
    executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
)

func main() {
    // 1) 编译种子（或复用已有编译产物）
    binaryPath := compileOrReuse()

    // 2) 根据场景选 Executor
    //    - 交叉编译 → QEMU
    //    - 本机编译 → LocalExecutor
    ex := executor.NewQEMUOracleExecutorAdapter("qemu-<ISA>", "", 30)

    // 3) 组装 AnalyzeContext
    ctx := &oracle.AnalyzeContext{
        BinaryPath: binaryPath,
        Executor:   ex,
    }

    // 4) 构造 seed.Seed（源码内容仅用于报告，不用于编译）
    src, _ := os.ReadFile("repro/<isa>/<mechanism>/source.c")
    s := &seed.Seed{
        Meta:    seed.Metadata{ID: 1, FilePath: "source.c"},
        Content: string(src),
    }

    // 5) 实例化目标 Oracle（此处以 canary 为例）
    o := &oracle.CanaryOracle{
        MaxBufferSize:  1024,
        DefaultBufSize: 64,
    }

    // 6) 调用 Analyze
    bug, err := o.Analyze(s, ctx, nil)
    if err != nil {
        fmt.Fprintf(os.Stderr, "oracle error: %v\n", err)
        os.Exit(2)
    }
    if bug == nil {
        fmt.Println("verdict: NO BUG")
        return
    }
    fmt.Println("verdict: BUG DETECTED")
    fmt.Println(bug.Description)
}

func compileOrReuse() string {
    // 若产物已存在则复用；否则调用交叉编译器
    dst := filepath.Join(os.TempDir(), "repro-binary")
    // exec.Command(cc, flags..., "-o", dst, "source.c").Run()
    return dst
}
```

## 分步说明

### Step 1 — 编译种子

种子可以是：

- **freestanding**：自带 `__stack_chk_*` / `_start` / syscall stub（如 LoongArch64 canary repro），用于 glibc 未就绪的场景。
- **glibc-linked**：标准模板 + 机制所需 flag（如 `-fstack-protector-strong`）。

直达测试的职责是把"编译"与"分析"解耦：

```go
// 编译
compileSeed(cc, src, dst, cflags)

// 分析（可多次调用同一 binary，换不同 Oracle 参数）
for _, param := range params {
    o := newOracleWithParam(param)
    bug, _ := o.Analyze(seed, ctx, nil)
    // 记录结果...
}
```

### Step 2 — 选 Executor

**QEMU 场景**（交叉编译）：

```go
ex := executor.NewQEMUOracleExecutorAdapter(
    "qemu-loongarch64",      // 或 qemu-aarch64, qemu-riscv64
    "/path/to/sysroot",      // 静态链接时可留空
    30,                      // timeout seconds
)
```

**本机场景**：

```go
ex := &executor.LocalExecutor{Timeout: 10 * time.Second}
```

### Step 3 — 组装 AnalyzeContext

```go
ctx := &oracle.AnalyzeContext{
    BinaryPath: binaryPath,   // 必须是绝对路径；QEMU 在 host 文件系统里找它
    Executor:   ex,
}
```

约束：

- `BinaryPath` 为空 → active Oracle（如 CanaryOracle）会返回 error。
- `Executor` 为 nil → 同上。
- Passive Oracle（只做静态分析，如反汇编扫描）理论上不需要 Executor，但 DeFuzz 的当前实现仍要求非 nil；传一个 no-op wrapper 即可。

### Step 4 — 构造 Seed

直达测试对 `seed.Seed` 的字段需求最小化：

```go
s := &seed.Seed{
    Meta: seed.Metadata{
        ID:       1,
        FilePath: srcPath,
    },
    Content: string(content),   // 用于 Bug.Seed 溯源
    // CFlags / FlagProfile 仅在 polarity / flag-profile 相关 checker 里用到
}
```

如果 checker 依赖 `NegativeCFlags`（如 canary 的 polarity），需要额外填充：

```go
s.AppliedLLMCFlags = []string{"-fno-stack-protector"}
s.LLMCFlagsApplied = true
```

### Step 5 — 调用 Analyze

```go
bug, err := o.Analyze(s, ctx, nil)
```

结果语义：

- `err != nil`：Oracle 自身出错（依赖缺失、执行超时、parse 失败等），**不是 bug 结论**。
- `bug == nil && err == nil`：所有 invariant 通过（Pass / NA），无 bug。
- `bug != nil`：至少一条 invariant 判 Fail，需要看 `bug.Description` 里的详细 verdict。

## 针对不同 Oracle 类型的适配

### 纯静态 Oracle（无运行期执行）

若你的 checker 只读 ELF / 反汇编 / 读源码，不需要 QEMU：

```go
ctx := &oracle.AnalyzeContext{
    BinaryPath: binaryPath,
    Executor:   &executor.LocalExecutor{}, // 占位，实际不会被调用
}
```

### 多阶段 Dynamic Oracle

若 checker 需要多轮执行（如 canary 的二分搜索），`MechanismOracle.Analyze` 内部会自行调度；你的驱动只需提供一次 `ctx`：

```go
// CanaryOracle 内部会执行数十次 binary（二分搜索）
// 驱动代码无需关心循环细节
bug, _ := canaryOracle.Analyze(s, ctx, nil)
```

### 需要特定 argv 的 Oracle

如 `EpilogueCanaryScrubChecker` 需要 `<binary> scrub` 参数，这由 checker 内部通过 `ExecuteWithArgs` 发起；驱动无需干预。

## 验证清单

写完直达测试后，按以下清单确认覆盖度：

| 检查项 | 方法 |
| --- | --- |
| 编译产物能独立运行 | `qemu-<ISA> <binary> <args>` 直接跑，看 stdout / exit code |
| Oracle 被正确实例化 | `go test ./cmd/<mechanism>-repro/...` 或 `go run ./cmd/<mechanism>-repro` |
| 所有 checker  verdict 路径被覆盖 | 至少观测到 Pass / Fail / NotApplicable 各一次 |
| Polarity 翻转正确 | 如果机制有负控，分别跑 `-f<mechanism>` 和 `-fno-<mechanism>` 两组，确认 verdict 翻转 |
| 错误路径不 panic | 模拟 BinaryPath 不存在 / QEMU 找不到 / 超时，确认返回 error 而非 panic |
| 报告格式可读 | `bug.Description` 包含 invariant ID、category、evidence 片段 |

## 参考实现

- **LoongArch64 canary leak**：`cmd/canary-repro/main.go` + `repro/loongarch64/canary_leak/source.c`
  - 覆盖 `CanaryOracle` + `QEMUOracleExecutorAdapter`
  - 验证 `INV-SP-R03` 在真实后端上的检出
- **CanaryOracle 本身**：`internal/oracle/canary_oracle.go:116-121`
  - 展示 `Analyze` → `mechanism().Analyze` 的委托模式
- **QEMU Executor 适配器**：`internal/seed_executor/executor.go:268-367`
  - `ExecuteWithInput` / `ExecuteWithArgs` 的实现与 QEMU exit code 解析

## 维护建议

1. **不要把 repro 源码放在 `cmd/` 里**：`cmd/` 只放 Go 驱动；C 种子放在 `repro/<isa>/<case>/`，与 `initial_seeds/` 的结构一致。
2. **在 feature doc 里回链**：每个 oracle 的 `docs/tech-docs/features/<mechanism>-oracle.md` 应包含"端到端复现"章节，指向对应的 `cmd/<mechanism>-repro`。
3. **在 invariant survey 里回链**：每条 invariant 的 `oracle_mapping` / `implementation` 字段应引用 repro 路径，方便从定义页直达可运行代码。
4. **CI 集成**：把 `go run ./cmd/<mechanism>-repro` 加入 `scripts/ci-test.sh` 或 GitHub Actions，作为"Oracle 冒烟测试"独立 job。
5. **编译产物不提交**：在 `.gitignore` 中忽略 repro 目录下的 `*.o`、`a.out`、二进制；只保留 `source.c`、`README.md`、Go 驱动源码。
