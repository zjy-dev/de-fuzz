# Phase 0 Research: LLVM 覆盖率驱动 Fuzz Engine 移植

本文解析 spec 与 Technical Context 中的未知项，给出落地决策。研究范围严格限定覆盖率模块。

## R1. 覆盖率采集工具链（对应 GCC gcov+gcovr）

**Decision**: 采用 LLVM 原生 source-based coverage：
- 编译/链接被测 Clang（项目外构建）时带 `-fprofile-instr-generate -fcoverage-mapping`，并用 `-fprofile-list=<list>` 把插桩限定到栈保护相关源文件（对齐 GCC `cfgexpand.o` 白名单）。
- 运行期：每编译一颗 seed 前设置 `LLVM_PROFILE_FILE="<dir>/seed-%p.profraw"`，运行被测 clang 编译 seed，落地 `.profraw`。
- 聚合：`llvm-profdata merge -sparse <dir>/*.profraw -o <seed>.profdata`。
- 导出：`llvm-cov export <clang-binary> -instr-profile=<seed>.profdata -format=text -ignore-filename-regex=... > <seed>.json`。

**Rationale**: 用户已澄清选用 llvm-cov 原生方案（精度高、region/line 级、JSON 稳定）。`-fprofile-list` 是 Clang 官方提供的"只插桩选定文件"机制，最贴近 GCC 白名单语义且可版本化。

**Alternatives considered**:
- clang `--coverage`（gcov 兼容，复用 gcovr）：被用户否决；gcov 兼容层精度/行号映射受限。
- 全量插桩：体积与噪音爆炸，违背"聚焦栈保护"。

## R2. llvm-cov export JSON 结构与"已覆盖行"抽取

**Decision**: 解析 `llvm-cov export -format=text` 输出的 JSON：
- 顶层 `{ version, type: "llvm.coverage.json.export", data: [...] }`。
- `data[].files[]`：`{ filename, segments[], summary, ... }`。
- **segment 元组（6 元素，定序）**：`[Line, Col, Count, HasCount, IsRegionEntry, IsGapRegion]`。
- `data[].functions[]`：`{ name(mangled), count, filenames[], regions[] }`；**region 元组（8 元素）**：`[LineStart, ColStart, LineEnd, ColEnd, ExecutionCount, FileID, ExpandedFileID, Kind]`。

抽取"已覆盖 file:line"：遍历 `files[].segments[]`，取 `HasCount==true && IsRegionEntry==true && Count>0` → `(filename, Line)`。这与 GCC 的 covered line+count 等价。

**目标过滤**：
- 文件级：`llvm-cov export` 支持位置参数 `-sources <file>...` 与 `-ignore-filename-regex=`。
- 函数级：`export` 无 `-name` 系列（那是 `show` 子命令的），需在 JSON 后处理按 `functions[].name` 过滤；或用 `functions[].regions[]`（含 `LineStart..LineEnd`）把 segment 行号归属到目标函数。本项目复用现有 `applyTargetFilter` 思路：在 Go 侧按"配置目标文件+函数"过滤。

**Rationale**: 字段顺序来自 llvm-cov 源码 `CoverageExporterJson.cpp`，稳定可解析；用 `IsRegionEntry && Count>0` 还原"覆盖行:计数"。

**Alternatives considered**: lcov 文本格式（`-format=lcov`）——可读性差、需额外解析器；JSON 更结构化。

## R3. LLVM 侧等价于 `.015t.cfg` 的 CFG 数据（BB + 后继 + 每块源码行）

**Decision**: 用 `clang -S -emit-llvm -g -O0 <compiler-source>.cc -o <out>.ll` 生成带调试信息的 IR，在 Go 侧解析 `.ll` 文本：
- 每个 `define ... @<mangled>(...) { ... }` 为一个函数。
- 函数体内以 `label:`（或入口隐式块）切分基本块；记录块的标识。
- 块的终结指令 `br` / `switch` / `invoke` 的目标 label 即**后继 BB**集合。
- 块内每条指令尾部的 `!dbg !N` → 反查 `!N = !DILocation(line: X, ...)`，对块内所有指令的 `line` 取并集，得到**该 BB 的源码行集合**；`scope`→`!DIFile` 回溯文件名。

产出与 `internal/coverage/analyzer.go` 的 `CFGFunction{Name, Blocks, SuccsMap}` / `BasicBlock{ID, File, Lines, Successors}` 等价的结构，直接喂给现有 `Analyzer`（或新增一个 `NewAnalyzerFromLLVM` 构造路径，复用其索引/选择/Mapping 逻辑）。

**BB 编号映射**：GCC `<bb N>` 用整数 ID。LLVM label 是字符串/数字混合，需在解析时给每个块分配稳定的整数 ID（按出现顺序，从 2 起以对齐 GCC "跳过 entry/virtual BB"的 `bbID>1` 约定，或显式保留 0/1 作为虚拟入口）。设计阶段在 data-model 固化。

**Rationale**: `-g` 的 `!DILocation` 是 LLVM 中唯一权威的 IR→源码行映射；后继来自终结指令。二者组合即可重建 `.015t.cfg` 三要素。直接解析 `.ll` 文本比解析 `opt -passes=dot-cfg` 的 `.dot` 更稳健（社区已知 `.dot` 中 `!dbg` 编号在部分版本显示不准，issue #120168）。

**Alternatives considered**:
- `opt -passes=dot-cfg` → `.dot`：适合可视化对照，但行号需二次解析且编号不稳。作为可选的交叉验证手段，不作主路径。
- 自写 LLVM C++ pass 遍历 `Function→BasicBlock→succ`：最干净但需链接 LLVM 库、引入 C++ 构建，超出"Go + 命令行工具"的现有工程形态，否决。
- `llvm-cov export` 的 `branches`：是覆盖率视角，不直接给 BB 拓扑，仅可作 count 补充。

**残留风险（移交实现阶段）**: `.ll` 文本解析需处理多行指令、PHI 节点、`switch` 的多目标、无名块（`%N` 编号块）等细节；以 fixture `.ll` 单测覆盖这些形态。CFG 数据获取方式（编译期 dump vs 离线生成）在 quickstart 中说明为"离线/启动期一次性生成 `.ll`，运行期解析"。

## R4. C++ 符号 demangle

**Decision**: `llvm-cov export` 的 `functions[].name` 为 mangled C++ 名。在 Go 侧抽取/过滤目标函数时，调用 `llvm-cxxfilt`（或 `c++filt`）对名字做 demangle 后再与配置的目标函数名匹配；或对配置目标函数同时按 mangled/简化名匹配（复用 GCC 路径已有的 `simplifyFunctionName` + exact/simple 双匹配思路）。

**Rationale**: 与现有 `targetFunctionMatcher`（exact + simplified）一致，最小改动；demangle 命令可经配置注入，缺失时退化为 mangled 精确匹配。

**Alternatives considered**: 纯 Go demangle 库——引入新依赖，非必要；优先复用现有匹配策略 + 可选外部 demangler。

## R5. 引擎集成与后端选择

**Decision**: 在 config 增加 `coverage_backend`（枚举 `gcc` | `llvm`，默认 `gcc` 以保持向后兼容）。`cmd/defuzz/app/fuzz.go` 据此装配 `GCCCoverage` 或 `LLVMCoverage`，并选择 CFG 数据来源（GCC `.015t.cfg` 路径 vs LLVM `.ll` 路径）。引擎中 `extractCoveredLines` 当前对 `*coverage.GCCCoverage` 做了类型断言以走过滤路径——需扩展为同时识别 `*coverage.LLVMCoverage`（或抽象出一个 `FilteredLineExtractor` 可选接口，二者都实现），避免 LLVM 路径退化为未过滤抽取。

**Rationale**: 默认 `gcc` 保证无回归（FR-017）；用可选接口替代具体类型断言，使引擎对后端无感（FR-019）。

**Alternatives considered**: 用配置文件名前缀（`clang-` vs `gcc-`）隐式推断后端——过于隐晦，显式 `coverage_backend` 更清晰。

## 未决项收敛

spec 中"控制流结构获取方式在实现规划阶段确定"已在 R3 收敛为：**离线/启动期用 `clang -S -emit-llvm -g` 生成目标源文件的 `.ll`，运行期由 Go 解析**。无剩余 NEEDS CLARIFICATION。
