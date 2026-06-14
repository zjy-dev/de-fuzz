# Phase 1 Data Model: LLVM 覆盖率驱动 Fuzz Engine 移植

本文定义 LLVM 覆盖率方案的数据实体。复用既有结构时明确标注"复用"，新增结构给出字段与约束。语义须与 GCC 路径对齐。

## 1. 复用的既有结构（不改语义）

### CFGFunction / BasicBlock（`internal/coverage/analyzer.go`，复用）
LLVM CFG 解析的产物**必须**填充为这些既有结构，以便复用 `Analyzer` 的索引、目标选择、权重衰减、`CoverageMapping`：

- `CFGFunction{ Name, MangledName, Blocks map[int]*BasicBlock, SuccsMap map[int][]int, PredsMap map[int][]int }`
- `BasicBlock{ ID int, Function, File string, Lines []int, Successors []int, Predecessors []int }`

**LLVM 填充约定**：
- `Name`：demangle 后的函数名（与配置 `targets[].functions` 匹配）；`MangledName`：原始 mangled 名。
- `Blocks` 的 `ID`：解析 `.ll` 时按基本块出现顺序分配整数，**从 2 起**（保留 0/1 语义空位，对齐 `Analyzer.selectTargetBB` 中 `bbID<=1` 跳过约定）。
- `File`：该块指令 `!DILocation`→`!DIFile` 解析出的源码文件路径，规范化后须能与配置 `targets[].file` 匹配（同 GCC 的 `normalizeFilePath`/`applyTargetFilter` 口径）。
- `Lines`：块内所有指令 `!DILocation.line` 的去重集合。
- `Successors`：终结指令（`br`/`switch`/`invoke`）目标 label 映射到的 BB ID 集合；同时写入 `SuccsMap[ID]`。
- `PredsMap`/`Predecessors`：由 `Analyzer.buildPredecessorMaps()` 统一反转生成，解析阶段不填。

### CoverageMapping（`internal/coverage/analyzer.go`，复用）
`line_to_seeds` 语义、持久化路径（`{state}/coverage_mapping.json`）不变。LLVM 路径产出的 covered-lines（`file:line` 字符串）经 `Analyzer.RecordCoverage` 写入，与 GCC 完全一致。

### Coverage / Report 接口（`internal/coverage/coverage.go`，复用）
LLVM 实现满足 `Coverage`、可选 `PreCompileCoverage`、`PostCompileCoverage`。`CoverageStats`、`CoverageIncrease` 结构复用。

## 2. 新增结构

### LLVMReport（实现 `Report`）
单 seed 或累计覆盖报告的载体。

| 字段 | 类型 | 说明 |
|---|---|---|
| `path` | string | llvm-cov export JSON 文件路径（与 `GcovrReport` 一致，只存路径） |

- `ToBytes()`：读取并返回 JSON 原文。
- 约束：`path` 非空且文件存在；否则返回错误（对齐 `GcovrReport.ToBytes`）。

### llvmCovExport（JSON 解析中间模型，内部）
对应 `llvm-cov export -format=text` 输出，仅解析所需字段：

```
llvmCovExport{
  Version string            `json:"version"`
  Type    string            `json:"type"`     // 须为 "llvm.coverage.json.export"
  Data    []llvmCovDatum    `json:"data"`
}
llvmCovDatum{
  Files     []llvmCovFile     `json:"files"`
  Functions []llvmCovFunction `json:"functions"`
}
llvmCovFile{
  Filename string      `json:"filename"`
  Segments [][]any     `json:"segments"`   // 6 元组 [Line,Col,Count,HasCount,IsRegionEntry,IsGapRegion]
}
llvmCovFunction{
  Name      string    `json:"name"`        // mangled
  Count     int64     `json:"count"`
  Filenames []string  `json:"filenames"`
  Regions   [][]any   `json:"regions"`     // 8 元组 [LStart,CStart,LEnd,CEnd,ExecCount,FileID,ExpFileID,Kind]
}
```

**校验规则**：
- `Type != "llvm.coverage.json.export"` → 报错（防止误喂 gcovr JSON）。
- segment 元素数 < 5 → 跳过该 segment（容错不同 llvm 版本的尾随字段）。
- 提取已覆盖行：`HasCount(seg[3]) && IsRegionEntry(seg[4]) && Count(seg[2])>0` → `(Filename, int(seg[0]))`。

### LLVMCoverage（实现 `Coverage` + Pre/PostCompile）
对齐 `GCCCoverage` 的职责，字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `executor` | `exec.Executor` | 注入，便于单测 |
| `compileFunc` | `func(*seed.Seed) error` | 编译回调（被插桩 clang 编译 seed，设 `LLVM_PROFILE_FILE`） |
| `compilerBinary` | string | 被测 clang 二进制路径（`llvm-cov export` 的 `<binary>` 参数） |
| `profrawDir` | string | profraw 落地目录（每轮 Clean 清理） |
| `profdataMergeCmd` | string | `llvm-profdata merge` 命令模板（不含输入/输出） |
| `covExportCmd` | string | `llvm-cov export` 命令模板（不含 `-instr-profile`/输出重定向） |
| `totalReportPath` | string | 累计 `total.json`（LLVM 格式） |
| `seedReportDir` | string | 单 seed JSON 目录 |
| `targetFilter` | 目标过滤配置 | 文件+函数白名单（同 GCC `filterConfig` 思路） |
| `demanglerCmd` | string | 可选；`llvm-cxxfilt`/`c++filt`，空则按 mangled+simplified 匹配 |
| `lastIncrease` | 增量缓存 | `HasIncreased` 计算后供 `GetIncrease` 复用 |

**方法语义（对齐 GCC）**：
- `Clean()/Prepare()`：删除 `profrawDir` 下 `*.profraw` 与残留 `*.profdata`；**不删** `.ll`/构建期结构数据。
- `Measure(s)`：`Clean` → `compileFunc(s)` → `MeasureCompiled(s)`。
- `MeasureCompiled(s)`：`profdata merge` → `llvm-cov export` 生成 `{seedReportDir}/{ID}.json` → 返回 `*LLVMReport`。seed ID 为 0 时报错（对齐 GCC）。
- `HasIncreased(new)`：解析 total 与 new 的"已覆盖行集合（经目标过滤）"，比较是否有新行；total 不存在 → true（首颗）。缓存增量。
- `Merge(new)`：total 不存在则拷贝 new 为 total；否则对两个 JSON 的"已覆盖行集合"取并集后写回 total（LLVM 无 gcovr 的 `-a` 合并命令，故在 Go 侧按行集合并；total.json 以"行集合"规范形式存储，见契约）。
- `GetTotalReport()/GetStats()`：解析 total，统计覆盖行/总行/百分比。
- `ExtractCoveredLinesFiltered(report)`：经目标过滤的 `file:line` 列表（引擎消费入口）。

### LLVM 配置字段（`internal/config`，新增）
扩展 `CompilerConfig` / `FuzzConfig`（保持 GCC 字段不动，新增项默认空/默认值以无回归）：

| 字段（mapstructure） | 归属 | 说明 |
|---|---|---|
| `coverage_backend` | CompilerConfig | `"gcc"`(默认) \| `"llvm"`；选择后端 |
| `llvm_profile_dir` | CompilerConfig | profraw 目录 |
| `llvm_profdata_command` | CompilerConfig | profdata merge 命令模板 |
| `llvm_cov_command` | CompilerConfig | llvm-cov export 命令模板 |
| `llvm_demangler_command` | CompilerConfig | 可选 demangler |
| `llvm_ir_paths` | FuzzConfig | 目标源文件对应的 `.ll` 文件路径列表（CFG 数据来源，等价 `cfg_file_paths`） |

**约束**：当 `coverage_backend == "llvm"` 时，`llvm_cov_command`、`compiler.path`（被测 clang）、至少一个 `llvm_ir_paths` 必填；缺失则启动期报错（对齐 GCC 对 `gcovr_command` 的强制校验）。

## 3. 实体关系

```
LLVMCoverage ──produces──▶ LLVMReport(JSON) ──parsed──▶ covered "file:line" 集合
                                                  │
                                                  ▼
                                   Analyzer.RecordCoverage ──▶ CoverageMapping(line→seeds)
                                                  ▲
.ll 文件 ──llvm_cfg 解析──▶ CFGFunction/BasicBlock ──NewAnalyzer──┘
                                                  │
                                                  ▼
                                   Analyzer.SelectTarget ──▶ TargetInfo(目标 BB + base seed)
```

## 4. 状态/持久化

| 文件 | 内容 | 生命周期 |
|---|---|---|
| `{state}/total.json` | LLVM 累计覆盖（行集合规范形式） | 跨运行累积，支持续跑 |
| `{seedReportDir}/{ID}.json` | 单 seed llvm-cov 导出 | 每 seed 一份 |
| `{profrawDir}/*.profraw` | 运行期原始覆盖 | 每轮 Clean 清理 |
| `{state}/coverage_mapping.json` | line→seeds（复用） | 跨运行累积 |
| `{...}/*.ll` | 目标源文件 IR（CFG 来源） | 启动期/离线生成，运行期只读 |
