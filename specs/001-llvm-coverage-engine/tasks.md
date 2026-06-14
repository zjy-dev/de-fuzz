---

description: "Task list for LLVM 覆盖率驱动 Fuzz Engine 移植"
---

# Tasks: LLVM 覆盖率驱动 Fuzz Engine 移植

**Input**: Design documents from `/specs/001-llvm-coverage-engine/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: 本特性 spec 的 SC-007 明确要求"核心能力均有可独立运行的测试覆盖"，故包含测试任务（单元测试不依赖真实工具链，集成测试可跳过）。

**Organization**: 按用户故事分组，每个故事可独立实现与验证。

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 可并行（不同文件、无未完成依赖）
- **[Story]**: 所属用户故事（US1/US2/US3）

## Path Conventions

单体 Go 项目：源码在 `internal/`，测试与源码同包同目录（`*_test.go`），配置在 `configs/`，装配在 `cmd/defuzz/app/`。

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: 准备 LLVM 后端的接线骨架与测试夹具目录，不改动现有 GCC 行为。

- [X] T001 在 `internal/config/config.go` 的 `CompilerConfig` 新增字段 `CoverageBackend`（mapstructure `coverage_backend`，默认 `"gcc"`）、`LLVMProfileDir`、`LLVMProfdataCommand`、`LLVMCovCommand`、`LLVMDemanglerCommand`；在 `FuzzConfig` 新增 `LLVMIRPaths []string`（mapstructure `llvm_ir_paths`）。保持所有 GCC 字段不变，新增项默认空。
- [X] T002 [P] 在 `internal/config/config.go` 的 `LoadConfig()` 默认值段为 `CoverageBackend` 设默认 `"gcc"`；当 `CoverageBackend=="llvm"` 时校验 `compiler.path`、`llvm_cov_command`、至少一个 `llvm_ir_paths` 必填，缺失则返回明确错误（对齐 GCC 对 `gcovr_command` 的强制校验）。
- [X] T003 [P] 在 `internal/coverage/artifacts/` 新增测试夹具：一份样例 `llvm-cov export` JSON（含 `type`、`data[].files[].segments`、`data[].functions[].regions`，覆盖 count>0 与 count==0 两类行）命名 `llvm_cov_sample.json`；一份带 `!dbg`/`!DILocation` 的样例 `.ll`（含条件分支、switch、循环）命名 `stackprotector_sample.ll`。

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: 建立 LLVM 与 GCC 共享的覆盖率抽象解耦点，使引擎不再硬绑定具体类型。**所有用户故事都依赖本阶段。**

**⚠️ CRITICAL**: 本阶段未完成前，任何用户故事不可开始。

- [X] T004 在 `internal/coverage/coverage.go` 新增可选接口 `FilteredLineExtractor { ExtractCoveredLinesFiltered(report Report) ([]string, error) }`（契约 C3）。
- [X] T005 在 `internal/fuzz/engine.go` 的 `extractCoveredLines` 把对 `*coverage.GCCCoverage` 的硬类型断言改为对 `coverage.FilteredLineExtractor` 接口断言，回退仍为包级 `coverage.ExtractCoveredLines`；确认 `*coverage.GCCCoverage` 已天然满足该接口（不改其方法签名，保证 GCC 无回归）。
- [X] T006 [P] 在 `internal/coverage/llvm_report.go` 新增 `LLVMReport{ path string }` 并实现 `Report.ToBytes()`（空路径/文件缺失返回 error，对齐 `GcovrReport.ToBytes`）。

**Checkpoint**: 抽象解耦完成，引擎对后端无感；可并行开展 US1/US2。

---

## Phase 3: User Story 1 - 用 LLVM 覆盖率测量驱动 fuzz 主循环 (Priority: P1) 🎯 MVP

**Goal**: 提供 `LLVMCoverage`，对单 seed 采集被测 Clang/LLVM 自身源码覆盖，判定增量、合并入累计覆盖、支持续跑，满足 `Coverage` 契约（C1/C2/C3/C4）。

**Independent Test**: 注入 fake `exec.Executor` + 夹具 JSON，验证 measure/HasIncreased/Merge/GetStats/ExtractCoveredLinesFiltered 全链路；无需 CFG 目标选择或 oracle 参与。

### Tests for User Story 1 ⚠️

> 先写测试并确保失败，再实现。

- [X] T007 [P] [US1] 在 `internal/coverage/llvm_report_test.go` 写 JSON 解析测试：用 `artifacts/llvm_cov_sample.json` 验证 `type` 校验、segment 元组 `[Line,Col,Count,HasCount,IsRegionEntry,IsGapRegion]` 解析、按 `HasCount&&IsRegionEntry&&Count>0` 抽取 `file:line`、数值容错（bool 或 0/1），断言覆盖行集合正确（契约 `llvm-cov-json.md`）。
- [X] T008 [P] [US1] 在 `internal/coverage/llvm_test.go` 写 `LLVMCoverage` 行为测试（注入 fake executor）：`Clean` 只删 profraw、`Measure`→`MeasureCompiled` 产单 seed JSON、`HasIncreased`（首颗返回 true / 无新行返回 false）、`Merge`（首颗初始化 total、后续并集）、`GetStats`、续跑（total 已存在时不归零）、seed.ID==0 报错。

### Implementation for User Story 1

- [X] T009 [US1] 在 `internal/coverage/llvm_report.go` 实现 llvm-cov JSON 解析中间模型 `llvmCovExport`/`llvmCovDatum`/`llvmCovFile`/`llvmCovFunction` 及函数：解析 JSON、校验 `type=="llvm.coverage.json.export"`、抽取已覆盖 `file:line` 集合、按 `targets`（文件+函数 region 区间）过滤（data-model §2 / 契约 `llvm-cov-json.md`）。
- [X] T010 [US1] 在 `internal/coverage/llvm_report.go` 实现 `total.json` 规范形式（`{type:"defuzz.llvm.coverage.total", covered_lines:{file:[lines]}}`）的读写、集合并集合并、集合差判定增量（契约 `llvm-cov-json.md` total 段）。
- [X] T011 [US1] 在 `internal/coverage/llvm.go` 定义 `LLVMCoverage` 结构与 `NewLLVMCoverage(...)`（字段见 data-model §2：executor、compileFunc、compilerBinary、profrawDir、profdataMergeCmd、covExportCmd、totalReportPath、seedReportDir、targetFilter、demanglerCmd、lastIncrease）。
- [X] T012 [US1] 在 `internal/coverage/llvm.go` 实现 `Clean()`/`Prepare()`：删除 `profrawDir` 下 `*.profraw`/残留 `*.profdata`，保留 `.ll` 与结构数据（契约 C1）。
- [X] T013 [US1] 在 `internal/coverage/llvm.go` 实现 `Measure(s)`（Clean→compileFunc→MeasureCompiled，ID==0 报错）与 `MeasureCompiled(s)`（profdata merge → llvm-cov export 生成 `{seedReportDir}/{ID}.json` → 返回 `*LLVMReport`），实现 `PreCompileCoverage`/`PostCompileCoverage`（契约 C1/C2）。
- [X] T014 [US1] 在 `internal/coverage/llvm.go` 实现 `HasIncreased`/`GetIncrease`（缓存增量、首颗 baseline 文案）、`Merge`（失败可恢复，不破坏旧 total）、`GetTotalReport`、`GetStats`（覆盖行/总行/百分比/函数覆盖）（契约 C1）。
- [X] T015 [US1] 在 `internal/coverage/llvm.go` 实现 `ExtractCoveredLinesFiltered(report)`（经目标过滤的 `file:line` 列表），使 `LLVMCoverage` 满足 `FilteredLineExtractor`（契约 C3）；含 demangle 处理（`demanglerCmd` 存在则批量 demangle，缺失则 mangled+simplified 双匹配，复用思路同 GCC `targetFunctionMatcher`）。

**Checkpoint**: US1 完成——LLVM 覆盖率测量全链路可独立测试通过（SC-001/002/003）。

---

## Phase 4: User Story 2 - LLVM 下的 CFG 引导目标选择 (Priority: P2)

**Goal**: 从 `.ll` 解析目标函数的 BB+后继+源码行，填充既有 `CFGFunction`/`BasicBlock`，复用 `Analyzer` 做目标 BB 选择与 `CoverageMapping`。

**Independent Test**: 用夹具 `.ll` + 一份已覆盖行集合，验证解析出 BB/后继/行号，并经 `Analyzer` 选出满足"目标函数内、含未覆盖行、可达"的 BB。

### Tests for User Story 2 ⚠️

- [X] T016 [P] [US2] 在 `internal/coverage/llvm_cfg_test.go` 写 `.ll`→CFG 解析测试：用 `artifacts/stackprotector_sample.ll` 断言函数切分、BB ID 从 2 起、终结指令（`br`/条件 `br`/`switch`/`invoke`）后继正确、`!dbg`→`!DILocation` 行号去重、`scope`→`!DIFile` 文件名、无 `!dbg` 指令跳过（契约 `llvm-cfg-source.md`）。
- [X] T017 [P] [US2] 在 `internal/coverage/llvm_cfg_test.go` 写"解析结果进入 Analyzer"测试：用解析出的 `CFGFunction` 经新构造路径建 `Analyzer`，给定已覆盖行集合后 `SelectTarget` 返回的 BB 满足三约束；目标函数全覆盖返回 nil；目标函数缺失时报错/告警（FR-013/FR-014）。

### Implementation for User Story 2

- [X] T018 [US2] 在 `internal/coverage/llvm_cfg.go` 实现 `.ll` 解析：按 `define ... @mangled(...) { ... }` 切函数（仅对配置 targetFunctions 构造）、按 `label:`/入口切 BB 并从 2 起编号、维护 `label→BBID`、解析终结指令得后继，填充 `BasicBlock.Successors` 与 `CFGFunction.SuccsMap`（契约 `llvm-cfg-source.md`）。
- [X] T019 [US2] 在 `internal/coverage/llvm_cfg.go` 实现 `!dbg !N`→`!DILocation(line)` 反查与 `scope`→`!DIFile` 文件名解析，对每个 BB 收集去重行号填 `BasicBlock.Lines`、设 `BasicBlock.File`（规范化路径以匹配 `targets[].file`）；处理多行指令/PHI/无名块/注释/属性组等边界。
- [X] T020 [US2] 在 `internal/coverage/analyzer.go` 新增构造路径 `NewAnalyzerFromCFGFunctions(funcs map[string]*CFGFunction, targetFunctions []string, sourceDir, mappingPath string, decay float64) (*Analyzer, error)`，复用既有 `indexFunction`/`buildPredecessorMaps`/目标函数校验/`CoverageMapping` 加载，不复制选择算法（契约 C5）。
- [X] T021 [US2] 在 `internal/coverage/llvm_cfg.go` 提供入口函数：给定 `llvm_ir_paths` + targetFunctions，解析多个 `.ll` 合并函数并调用 `NewAnalyzerFromCFGFunctions` 返回 `*Analyzer`（等价 GCC 多 CFG 合并）。

**Checkpoint**: US2 完成——LLVM CFG 目标选择可独立测试通过（SC-004）。

---

## Phase 5: User Story 3 - 引擎按配置在 GCC / LLVM 间切换 (Priority: P3)

**Goal**: 引擎按 `coverage_backend` 装配 GCC 或 LLVM 后端，主循环/oracle/LLM/prompt 零改动；提供 LLVM canary 示例配置。

**Independent Test**: 分别用 GCC 配置与 LLVM 配置启动，两者都进入主循环完成至少一轮"测量+目标选择"，GCC 路径无回归。

### Tests for User Story 3 ⚠️

- [X] T022 [P] [US3] 在 `internal/config/config_test.go` 新增用例：`coverage_backend` 默认 `gcc`、设为 `llvm` 时缺 `llvm_cov_command`/`llvm_ir_paths`/`path` 报错、合法 LLVM 配置解析成功（覆盖 T001/T002）。
- [X] T023 [P] [US3] 在 `internal/coverage/llvm_integration_test.go` 写端到端集成测试：环境无 `clang`/`llvm-cov`/`llvm-profdata` 则 `t.Skip`，否则对一颗真实 seed 跑完整 measure→export→抽取行链路（quickstart 集成测试段）。

### Implementation for User Story 3

- [X] T024 [US3] 在 `cmd/defuzz/app/fuzz.go` 的 `runFuzz` 按 `cfg.Compiler.CoverageBackend` 分支：`llvm` 时用 `coverage.NewLLVMCoverage(...)` 替代 `NewGCCCoverage`，并用 `llvm_ir_paths` + targetFunctions 经 `coverage` 的 LLVM CFG 入口构造 `Analyzer`；`gcc`（默认）保持现有装配完全不变（FR-015/FR-017）。
- [X] T025 [US3] 在 `cmd/defuzz/app/fuzz.go` 提取/复用编译回调，使 LLVM 路径在编译 seed 前设置 `LLVM_PROFILE_FILE`（指向 `llvm_profile_dir`）；确保不影响 GCC 编译回调。
- [X] T026 [P] [US3] 新增 `configs/clang-vX.Y.Z-x64-canary.yaml` LLVM canary 示例配置（quickstart 配置段：coverage_backend、被测 clang path、profile/profdata/cov 命令、llvm_ir_paths、targets 对齐栈保护函数、oracle 仍为 canary）（FR-020）。

**Checkpoint**: US3 完成——两后端按配置切换，GCC 无回归（SC-005）。

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: 验收与回归收尾。

- [X] T027 运行 `go test ./internal/coverage/...` 确认 LLVM 新测试通过且 GCC 既有测试全过（SC-005/SC-007）。
- [X] T028 [P] 运行 `go test ./internal/oracle/... ./internal/llm/... ./internal/prompt/...` 确认覆盖率以外子系统无回归（SC-006/FR-018）。
- [X] T029 [P] 按 `specs/001-llvm-coverage-engine/quickstart.md` 的验收对照表逐项核对（SC-001~006）。
- [X] T030 [P] 文档更新（可选，仅当需要文档时）：本次未额外创建 `docs/tech-docs/` 文档；LLVM 后端用法已记录在 `specs/001-llvm-coverage-engine/quickstart.md`，`configs/clang-v18.1.0-x64-canary.yaml` 提供可参照样例。

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 无依赖，立即开始。
- **Foundational (Phase 2)**: 依赖 Setup；阻塞所有用户故事。
- **User Stories (Phase 3+)**: 均依赖 Foundational。US1 与 US2 相互独立，可并行；US3 依赖 US1（装配 LLVMCoverage）与 US2（装配 LLVM Analyzer）。
- **Polish (Phase 6)**: 依赖所有目标故事完成。

### User Story Dependencies

- **US1 (P1)**: 依赖 Foundational（T004/T006）。独立可测（MVP）。
- **US2 (P2)**: 依赖 Foundational。与 US1 独立可测。
- **US3 (P3)**: 依赖 US1（LLVMCoverage）+ US2（LLVM Analyzer 构造路径）方可完整装配。

### Within Each User Story

- 测试先写并失败 → 实现。
- 报告/解析层（model）先于 `LLVMCoverage`（service）。
- `.ll` 解析先于 `NewAnalyzerFromCFGFunctions` 接入。

### Parallel Opportunities

- Setup：T002、T003 可并行（T001 完成后）。
- Foundational：T006 与 T004/T005 可并行（T006 不依赖引擎改动）。
- US1 测试 T007、T008 可并行；US2 测试 T016、T017 可并行。
- US1 与 US2 整体可由不同开发者并行推进。
- Polish：T028、T029、T030 可并行。

---

## Parallel Example: User Story 1

```bash
# 先并行写测试（应失败）：
Task: "T007 JSON 解析测试 in internal/coverage/llvm_report_test.go"
Task: "T008 LLVMCoverage 行为测试 in internal/coverage/llvm_test.go"

# 实现按依赖顺序：T009→T010（report 层）→ T011→T012→T013→T014→T015（coverage 层）
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. 完成 Phase 1 Setup。
2. 完成 Phase 2 Foundational（解耦引擎类型断言，关键）。
3. 完成 Phase 3 US1（LLVM 覆盖率测量）。
4. **停下验证**：`go test ./internal/coverage/ -run LLVM` 全过 + GCC 无回归。
5. 此时 LLVM 覆盖率测量已可独立交付。

### Incremental Delivery

1. Setup + Foundational → 基础就绪。
2. US1 → 独立验证 → 交付（MVP：LLVM 测量）。
3. US2 → 独立验证 → 交付（LLVM CFG 目标选择）。
4. US3 → 装配切换 + 示例配置 → 端到端可跑。

### Parallel Team Strategy

Foundational 完成后：开发者 A 做 US1，开发者 B 做 US2，二者并行；US3 待 A/B 完成后由任一人整合装配。

---

## Notes

- [P] = 不同文件、无未完成依赖。
- 严格保持 GCC 路径行为不变（`coverage_backend` 默认 `gcc`；GCC 代码与测试零改动）。
- 改动范围限定 `internal/coverage/`、`internal/config/config.go`、`internal/fuzz/engine.go`（仅类型断言解耦）、`cmd/defuzz/app/fuzz.go`、`configs/`；**不得触碰 oracle/LLM/prompt 语义**。
- 单元测试不依赖真实 clang/llvm 工具链；集成测试在缺工具链时 `t.Skip`。
- 每个任务或逻辑组完成后提交。
