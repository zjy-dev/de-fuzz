# Contract: Coverage / Analyzer 接口（LLVM 实现须满足）

LLVM 实现复用 `internal/coverage` 既有抽象，引擎主循环不得为其改写。本契约固化 LLVM 实现对这些接口的语义承诺。

## C1. `coverage.Coverage`（必须实现，语义对齐 GCC）

```go
type Coverage interface {
    Clean() error
    Measure(s *seed.Seed) (Report, error)
    HasIncreased(newReport Report) (bool, error)
    GetIncrease(newReport Report) (*CoverageIncrease, error)
    Merge(newReport Report) error
    GetTotalReport() (Report, error)
    GetStats() (*CoverageStats, error)
}
```

| 方法 | LLVM 语义承诺 |
|---|---|
| `Clean` | 删除 profraw 目录下 `*.profraw` / 残留 `*.profdata`；保留 `.ll` 与构建期数据。幂等。 |
| `Measure` | `Clean → 编译 seed（设 LLVM_PROFILE_FILE）→ MeasureCompiled`。seed.Meta.ID==0 → error。 |
| `HasIncreased` | total 不存在 → `true`（首颗 seed）；否则比较经目标过滤的"已覆盖行集合"是否新增。须缓存增量供 `GetIncrease`。 |
| `GetIncrease` | 返回新覆盖行数/函数数摘要；首颗 seed 返回 baseline 文案（对齐 GCC）。 |
| `Merge` | total 不存在→以 new 初始化；否则按行集合取并集写回 total。失败须可恢复（不破坏旧 total）。 |
| `GetTotalReport` | total 不存在 → error；存在 → 校验 JSON 合法后返回 `*LLVMReport`。 |
| `GetStats` | total 不存在 → 零值 `CoverageStats`；否则返回覆盖行/总行/百分比/函数覆盖。 |

## C2. 可选接口（须实现以接入引擎现有路径）

```go
type PreCompileCoverage interface  { Prepare() error }         // = Clean
type PostCompileCoverage interface { MeasureCompiled(s *seed.Seed) (Report, error) }
```

- 引擎 `measureCoverage` 优先调用 `MeasureCompiled`（编译已在 `tryMutatedSeed` 内完成），LLVM 必须实现。

## C3. 过滤抽取入口（消除引擎对具体类型的硬断言）

现状：`engine.go:extractCoveredLines` 对 `*coverage.GCCCoverage` 做类型断言走 `ExtractCoveredLinesFiltered`。

**契约**：引入可选接口，GCC 与 LLVM 实现都满足；引擎改为按接口断言：

```go
type FilteredLineExtractor interface {
    ExtractCoveredLinesFiltered(report Report) ([]string, error)
}
```

- 返回值：经"目标文件+函数"过滤后的 `"file:line"` 列表（被测编译器源码行）。
- 引擎断言顺序：先 `FilteredLineExtractor`，否则回退包级 `ExtractCoveredLines`。
- **无回归约束**：`GCCCoverage` 已有同名方法，自动满足该接口；引擎改动对 GCC 行为透明。

## C4. `Report`

```go
type Report interface { ToBytes() ([]byte, error) }
```

- `LLVMReport.ToBytes`：读取 JSON 文件原文；空路径/文件缺失 → error。

## C5. Analyzer 复用约束

- LLVM CFG 解析结果填充 `CFGFunction`/`BasicBlock`，经 `NewAnalyzer(cfgPaths, targetFunctions, sourceDir, mappingPath, decay)` 或新增 `NewAnalyzerFromCFGFunctions(...)` 构造路径进入。
- **不复制**目标选择/权重/Mapping 算法；仅替换数据来源。
- 目标函数匹配须容忍 mangled/demangled 双形态（复用 `targetFunctionMatcher` 的 exact+simplified 策略）。
