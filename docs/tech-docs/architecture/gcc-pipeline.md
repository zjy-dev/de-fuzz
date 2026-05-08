---
title: GCC 相关工作流架构总览
description: 构建期插桩 GCC 与运行期 fuzz 主循环之间数据流的端到端拆解
priority: HIGH
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ./overview.md
  - ./fuzz-engine-loop.md
  - ../guides/building-instrumented-gcc.md
---

# GCC 相关工作流架构总览

> 本文梳理 **de-fuzz** 项目中所有与 GCC 直接交互的组件：从"构建一个带插桩的 GCC"开始，到 fuzz 循环中如何用它产生覆盖率、消费 CFG、通过 `uftrace` 比对执行轨迹。读完此文，你应当能说清楚 **每个 `.gcno`/`.gcda`/`.cfg`/`cc1` 来自哪里、被谁消费、推动了哪一步决策**。

## 1. 宏观流程（两阶段）

de-fuzz 的 GCC 工作流分成完全独立的两个时间尺度：

- **构建期（一次性）**：按补丁改造 GCC 源码，编出一个"自带覆盖率插桩 + 自带 CFG dump"的 `xgcc`。这一步在项目外执行，产物是被 fuzzer 当作**被测编译器**使用的二进制。
- **运行期（每次模糊）**：fuzzer 反复驱动该 `xgcc` 编译 seed，`xgcc` 在编译自身行为时落下 `.gcda`；fuzzer 用 `gcovr` 聚合成 JSON 覆盖率，再结合构建期就已经存在的 `.cfg` 文件做 CFG-guided 目标选择，最后用 `uftrace` 做路径发散分析指导 LLM 变异。

```
  ┌──────────────────────────── 构建期（一次） ─────────────────────────────┐
  │                                                                         │
  │   gcc source  ──patch Makefile.in──▶  configure/build                   │
  │                                         │                               │
  │                                         ▼                               │
  │             cfgexpand.gcno  +  cfgexpand.cc.015t.cfg  +  xgcc           │
  │                                                                         │
  └─────────────────────────────────────────────────────────────────────────┘
                                         │
                                         ▼
  ┌──────────────────────────── 运行期（每次模糊迭代） ─────────────────────┐
  │                                                                         │
  │  Analyzer 解析 .cfg  ──▶  SelectTarget  ──▶  PromptService 构 Prompt    │
  │        ▲                                              │                 │
  │        │                                              ▼                 │
  │   gcovr JSON ◀── xgcc 编译 seed（产 .gcda）◀── LLM 生成/变异 seed        │
  │        │                                              │                 │
  │        └─── 无增量/未命中 ───▶ uftrace 比对执行路径 ──┘                 │
  │                                                                         │
  └─────────────────────────────────────────────────────────────────────────┘
```

关键约定：**插桩只对 GCC 自己的 `cfgexpand.cc` 打开**。不是 fuzz 产物的可执行文件被插桩，而是**编译器本身**被插桩——也就是"测编译器"，不是"测程序"。

## 2. 构建期：让 GCC 自带覆盖率和 CFG dump

相关资产全部在 `@/home/yall/project/de-fuzz/docs/gcc-instrumentation/`：

- **补丁描述**：`@/home/yall/project/de-fuzz/docs/gcc-instrumentation/Makefile.in.patch`
- **构建脚本**：`@/home/yall/project/de-fuzz/docs/gcc-instrumentation/x64/build-gcc-instrumented.sh` 与 `aarch64/build-gcc-instrumented.sh`
- **说明**：各 `BUILD-GUIDE.md`

### 2.1 为什么做"选择性插桩"

若对整个 GCC 全量加 `-fprofile-arcs -ftest-coverage`，构建会显著变慢、`.gcno/.gcda` 体积巨大、分析也被噪音淹没。项目只挑 `cfgexpand.cc` 作为靶点——它是 GIMPLE → RTL 转换中栈变量展开、栈保护决策的核心模块，也是我们 **stack canary** 策略真正关心的代码。

### 2.2 补丁做的三件事

见 `@/home/yall/project/de-fuzz/docs/gcc-instrumentation/Makefile.in.patch:12-100`：

1. **白名单**（§1）：新增 `COVERAGE_WHITELIST = cfgexpand.o`、`CFG_DUMP_WHITELIST = cfgexpand.o`，并用 per-target 变量 `CFLAGS-cfgexpand.o` 注入两类 flag：
   - `$(COVERAGE_FLAGS)`：即 `-fprofile-arcs -ftest-coverage`，产出 `.gcno`（编译期结构）和后来的 `.gcda`（运行期命中）。
   - `-fdump-tree-cfg-lineno`：让 GCC 在 tree 级 pass `cfg` 停顿时把 **每个函数的基本块、后继集合、源码行号**转储成 `cfgexpand.cc.015t.cfg`。

2. **CFLAGS 路由**（§2）：把 `ALL_CFLAGS` / `ALL_CXXFLAGS` 里写死的 `$(COVERAGE_FLAGS)` 换成 `$(CFLAGS-$@)`，于是白名单之外的文件一条额外 flag 都拿不到——**零额外开销**。

3. **链接 flag**（§3）：`ALL_LINKERFLAGS += $(COVERAGE_LDFLAGS)`，把 `-lgcov --coverage` 引进来，否则带 `-fprofile-arcs` 的 object 链接会报 `undefined reference to __gcov_*`。

### 2.3 构建脚本的关键步骤

以 `@/home/yall/project/de-fuzz/docs/gcc-instrumentation/x64/build-gcc-instrumented.sh` 为例：

- 先用 `grep -q "FUZZ-COVERAGE-INSTRUMENTATION"` 卡住，拒绝在未打补丁的源码上跑（`@/home/yall/project/de-fuzz/docs/gcc-instrumentation/x64/build-gcc-instrumented.sh:51-57`）。
- `configure --enable-coverage=noopt --disable-bootstrap`，禁 bootstrap 避免被第二阶段覆盖掉插桩。
- `make all-gcc` + `make all-target-libgcc`，后者是"链接 seed 二进制所需要的运行时"。
- 结束时自动校验 `.cfg` 和 `-fdump-tree-cfg-lineno` 是否真的在构建日志里出现。

AArch64 变体（`@/home/yall/project/de-fuzz/docs/gcc-instrumentation/aarch64/build-gcc-instrumented.sh`）是 8 阶段的交叉编译链（host-tools → binutils → linux headers → gcc stage1 → glibc headers → gcc stage2 → full glibc → final gcc），**插桩仅作用于最后一个 stage 的 host 侧编译器**——我们测的还是 x86 主机上跑的 `aarch64-none-linux-gnu-gcc` 自身的 `cfgexpand.cc`。

### 2.4 构建产物与 fuzz 配置的对应

以 `@/home/yall/project/de-fuzz/configs/gcc-v12.2.0-x64-canary.yaml` 为锚：

| 构建产物 | 配置字段 | 作用 |
| --- | --- | --- |
| `gcc-build/gcc/xgcc` | `compiler.path` | 运行期被 fuzzer 拉起来编 seed |
| `gcc-build/gcc/` 目录 | `compiler.gcovr_exec_path` | `gcovr` 在此执行，收集 `.gcda` |
| `gcc-build/gcc/cfgexpand.cc.015t.cfg` | `compiler.fuzz.cfg_file_path` | CFG analyzer 的输入 |
| `cfgexpand.gcno` | 被 `gcov` 隐式使用 | 结构数据，每次运行不变 |
| `cfgexpand.gcda` | 每次运行被写；每轮开始清除 | 覆盖率计数 |

## 3. 运行期：GCC 如何被 fuzzer 驱动

本节按"数据流"拆成四个环节。所有 Go 代码都在 `@/home/yall/project/de-fuzz/internal/`。

### 3.1 编译 seed —— `internal/compiler`

入口：`@/home/yall/project/de-fuzz/internal/compiler/compiler.go:78-200`。

`GCCCompiler.Compile` 做的事情很薄：

1. 把 seed 内容写到 `{workDir}/seed_{ID}.c`。
2. 组装 argv：`-B{prefixPath}` → 配置里的 `cflags` → 当前 FlagProfile 的 flags → LLM 追加的 `s.CFlags`（会被 `filterLLMCFlags` 剔掉冲突项） → `<src> -o <bin>`。
3. 调用 `exec.Executor` 跑 `xgcc`，把完整 argv、`ProfileName`、`IsNegativeControl` 等保存进 `CompileResult`，便于后面复现与审计。

这里需要强调两个设计：

- **`--coverage` 不加在这一层**。fuzz 对象是编译器自身，seed 二进制要不要被插桩无关紧要。真正的覆盖率由"被编译器用到的 `cfgexpand.o`"自带的 `.gcno` 逻辑产生——也就是说 `xgcc` 每跑一次 seed，它自己运行到的 BB 都会写进 `.gcda`。
- **FlagProfile 机制**（`@/home/yall/project/de-fuzz/internal/compiler/compiler.go:274-322`）：某些策略（如 canary）要求按矩阵扫组合，`filterLLMCFlags` 会把 LLM 建议的 `-fstack-protector*`、`-fpic` 等"与 profile 冲突"的 flag 踢掉，保证本轮编译语义由 profile 掌控。

### 3.2 覆盖率采集 —— `internal/coverage/gcc.go`

核心类型 `GCCCoverage`（`@/home/yall/project/de-fuzz/internal/coverage/gcc.go:36-94`），实现 `Coverage` 接口（`@/home/yall/project/de-fuzz/internal/coverage/coverage.go:42-65`）。

每测一粒 seed 的标准动作（`@/home/yall/project/de-fuzz/internal/coverage/gcc.go:203-294`）：

1. **`Clean` / `Prepare`**：`find <gcovrExecPath> -name '*.gcda' -delete` 以及 `.gcov` 清扫。**`.gcno` 保留**——它随编译器一起发布、反映源码结构，不需要每轮重算。
2. **`Compile`**：调用编译回调，`xgcc` 运行时写出新的 `.gcda`。
3. **`MeasureCompiled`**：拼一条 `cd <gcovrExecPath> && <gcovrCommand> --json-pretty --json <seedReportDir>/<seedID>.json` 的 shell，把 `.gcda` 汇成 gcovr JSON。`gcovrCommand` 来自配置，例如 `gcovr --exclude ".*\.(h|hpp|hxx)$" --gcov-executable "gcov-14 --demangled-names" -r ..`。
4. **`HasIncreased` + `Merge`**：借 `github.com/zjy-dev/gcovr-json-util/v2/pkg/gcovr` 做增量计算与合并。`Merge` 等价于 `mv total.json tmp && gcovr -a tmp -a seed.json -o total.json`。
5. **过滤**：`applyTargetFilter`（`@/home/yall/project/de-fuzz/internal/coverage/gcc.go:142-201`）按配置里的 `targets:` 段把报告裁到"我们关心的 file + function"。`ExtractCoveredLinesFiltered` 是 fuzz engine 读回的专用入口，确保"line 集合"只包含 target 函数（例如 `stack_protect_classify_type` 系列）的行号。

**关键事实**：得到的 "covered lines" 是 **GCC 源码**（如 `cfgexpand.cc`）里的行号，不是 seed C 代码的行号。这一点贯穿后续所有步骤。

### 3.3 CFG 生成与解析

#### 3.3.1 `.cfg` 文件是什么

构建期 `-fdump-tree-cfg-lineno` 让 GCC 在 pass `cfg` 之后打印出所有函数的 GIMPLE 级控制流图，命名约定 `<源文件>.015t.cfg`。其形态大致是：

```
;; Function stack_protect_prologue (stack_protect_prologue, funcdef_no=...)
;; 2 succs { 3 4 }
;; 3 succs { 5 }
...
stack_protect_prologue ()
{
  <bb 2>:
    [cfgexpand.cc:7112:3] ...
    [cfgexpand.cc:7115:5] ...
    goto <bb 3> (<L0>);
  ...
}
```

正则抓手就定义在 `@/home/yall/project/de-fuzz/internal/coverage/analyzer.go:164-171`：

- `reFunctionHeader`：`^;; Function ([^\s(]+) \(([^,]+),`
- `reSuccSummary`：`^;; (\d+) succs \{ ([^}]*) \}`
- `reBBStart`：`^\s*<bb (\d+)>\s*:?`
- `reLineInfo`：`\[([^:\]]+):(\d+):\d+(?:\s+discrim\s+\d+)?\]`

#### 3.3.2 `internal/coverage/analyzer.go` —— 解析与索引

`NewAnalyzer`（`@/home/yall/project/de-fuzz/internal/coverage/analyzer.go:116-162`）接收一组 `.cfg` 路径（`cfg_file_path` 或 `cfg_file_paths`）以及 `targetFunctions`，顺序做：

1. `parseCFGFile`（`@/home/yall/project/de-fuzz/internal/coverage/analyzer.go:174-290`）流式读文件，按状态机切出 `CFGFunction`；每遇到 `<bb N>:` 新建 `BasicBlock`，后续行里所有 `[file:line:col]` 被收集进 `bb.Lines`；`;; N succs { ... }` 行填 `SuccsMap`。
2. `indexFunction`（`@/home/yall/project/de-fuzz/internal/coverage/analyzer.go:320-333`）构建三份索引：`lineToBB`、`bbToSuccCount`、`bbWeights`（初始权重 = 后继数）。
3. `buildPredecessorMaps` 反转 `SuccsMap` 得到 `PredsMap`——目标选择阶段要用。
4. 校验 `targetFunctions` 是否都在解析结果里，否则直接报错退出。

`CoverageMapping`（`@/home/yall/project/de-fuzz/internal/coverage/analyzer.go:926-1100`）是另一份与 `.cfg` 配套的持久化：`line_to_seeds` 记录"哪条 GCC 源码行由哪些 seed 先后覆盖过"，落在 `state/coverage_mapping.json`。

#### 3.3.3 目标选择算法

`SelectTarget`（`@/home/yall/project/de-fuzz/internal/coverage/analyzer.go:386-531`）按以下规则挑一个"值得打"的 BB：

- 候选条件：位于 `targetFunctions` 中；BB ID > 1（跳过 entry/virtual BB）；至少一行未覆盖；**可达**（无前驱 ∨ 至少一个前驱已被某 seed 覆盖）。
- 排序键：`Weight` 优先；平分时随机。初始权重为后继数（branching factor 越大信息收益越大），失败一次后 `DecayBBWeight` 按 `weightDecayFactor`（配置默认 0.8）衰减，防止死磕同一个不可达目标。
- 副产物：挑出 "base seed" —— 找该 BB 的已覆盖前驱，再从 `CoverageMapping` 里随机回一个曾经覆盖该前驱行的 seed ID，作为后面给 LLM 的"锚点"。

### 3.4 Prompt 构建：把 GCC 源码塞给 LLM

`BuildTargetContextFromCFG`（`@/home/yall/project/de-fuzz/internal/prompt/constraint.go:620-678`）把 analyzer 的输出翻译成 LLM 看得懂的材料：

- `ctx.FunctionCode` 由 `GenerateAnnotatedFunctionCode`（`@/home/yall/project/de-fuzz/internal/prompt/constraint.go:578-618`）从 **GCC 源码文件**里切出 target 行 ±20 行，对每行打 `[✓]`（已覆盖）/ `[✗]`（未覆盖）/ `[→]`（目标）。
- `ctx.BaseSeedCode` 是上面 `SelectTarget` 挑出的那粒历史 seed 的完整 C 源码。
- `BuildConstraintSolvingPrompt`（`@/home/yall/project/de-fuzz/internal/prompt/constraint.go:67-232`）把 target 元信息、标注后的 GCC 函数、base seed、profile 段拼成一个完整 prompt，强令 LLM "基于 base seed 修改"而非另起炉灶。

这个 prompt 是**整个"LLM-based constraint solving"的核心工艺品**——它让 LLM 看着编译器内部的 C++ 代码的行级覆盖情况，去猜"应当写什么样的 C seed 才能让编译器跑到 `[→]` 这几行"。

### 3.5 发散分析：`uftrace` 比对 `cc1` 调用序列

当 LLM 生成的 seed 没有命中目标 BB 时，`Engine.solveConstraint`（`@/home/yall/project/de-fuzz/internal/fuzz/engine.go:290-480`）启动重试循环，关键帮手是 `UftraceAnalyzer`（`@/home/yall/project/de-fuzz/internal/coverage/divergence.go`）。

流程（`@/home/yall/project/de-fuzz/internal/coverage/divergence.go:97-156`）：

1. 对 base seed 和 mutated seed 各跑一次 `uftrace record -P '.*' -d <dir> xgcc -c <seed.c> -o /dev/null`，`-P '.*'` 做全量动态 patching。
2. `extractCC1PID` 从 `<dir>/task.txt` 拿到 `cc1` 进程的 PID（`xgcc` 会 fork `cc1`，真正的编译逻辑在 `cc1`）。
3. `uftrace replay --no-libcall` 导出调用序列，按 PID + 缩进深度解析成 `FunctionCall{Name, Depth}` 数组。
4. `findParserStart` 跳过初始化噪音，从第一个 `c_parser*` / `*parse*` 开始对齐。
5. `findDivergence` 线性扫描两序列首个名字不一致的位置，记录 `CommonPrefix` / `Path1` / `Path2`（各 `contextSize=5` 个函数）。

得到的 `DivergencePoint` 通过 `BuildRefinedPrompt`（`@/home/yall/project/de-fuzz/internal/prompt/constraint.go:236-383`）变成下一轮 prompt 的"错题解析"：**把发散的 GCC 函数源码也贴出来**（analyzer 能按函数名反查 BB 行号区间，再走 `coverage.ReadSourceLines`），让 LLM 看清"在这个条件这里分叉了，你该怎么改 seed 才能走上和 base 一样的路"。

如果发散分析失败或 uftrace 未启用，重试仍会进行——不过 `divergentFunc` 退化为 target function 本身。若上一轮是编译失败，则切换到 `BuildCompileErrorRetryPrompt`（`@/home/yall/project/de-fuzz/internal/prompt/constraint.go:387-507`），拿编译器 stderr 让 LLM 修错。

## 4. 组件关系图（代码视角）

```
cmd/defuzz/app/fuzz.go  ── 组装所有依赖
        │
        ├─ compiler.NewGCCCompiler           ── 拉起 xgcc
        │       └── internal/compiler/compiler.go
        │
        ├─ coverage.NewGCCCoverage           ── .gcda → JSON 报告
        │       └── internal/coverage/gcc.go
        │
        ├─ coverage.NewAnalyzer              ── 解析 .015t.cfg
        │       └── internal/coverage/analyzer.go
        │
        ├─ coverage.UftraceAnalyzer          ── 发散分析（可选）
        │       └── internal/coverage/divergence.go
        │
        ├─ prompt.NewBuilder / PromptService ── 构造 LLM prompt
        │       └── internal/prompt/constraint.go
        │
        └─ fuzz.NewEngine                    ── 主循环编排
                └── internal/fuzz/engine.go
```

主循环 `Engine.Run`（`@/home/yall/project/de-fuzz/internal/fuzz/engine.go:126-196`）就是把上述所有部件串起来的"指挥棒"：

1. `processInitialSeeds` 用 analyzer 吃下初始 seeds 的覆盖数据，建好 `coverage_mapping.json`。
2. 每轮 `SelectTarget` → `BuildTargetContextFromCFG` → `GetConstraintPrompt` → LLM → `tryMutatedSeed`（内部包 `Compile` + `MeasureCompiled` + target 命中判定 + Oracle 运行）。
3. 未命中则进入重试，视情况启用 `DivergenceAnalyzer`，直到命中或达到 `MaxConstraintRetries`。
4. 每 10 轮保存一次 `CoverageMapping` + corpus 状态；全部完成后 `finalizeState` 固化为止。

## 5. 常见问题速查

| 现象 | 根因 / 排查入口 |
| --- | --- |
| `.cfg` 文件不存在 | 补丁未生效：检查 `Makefile.in` 里 `FUZZ-COVERAGE-INSTRUMENTATION` 标记，再看构建日志 `grep "cfgexpand.cc"` 是否含 `-fdump-tree-cfg-lineno` |
| `total.json` 无法生成 | `compiler.gcovr_exec_path` 指的目录里没有 `.gcda`，多半是 `xgcc` 没跑起来或 `--coverage` 链接 flag 缺失 |
| analyzer 启动时报 `target function not found` | 目标函数名（配置 `targets[].functions`）在 `.015t.cfg` 里无匹配；注意 `{anonymous}::pass_expand::execute` 之类 C++ 名称 |
| 发散分析总是失败 | 宿主未装 `uftrace`，或 `task.txt` 里没出现 `cc1`（可能是 `xgcc` 直接把工作交给 `cc1plus`/其他前端，正则需要调整） |
| LLM 反复跑偏 | 检查 `ctx.FunctionCode` 是否真的带 `[→]` 标记，以及 `BaseSeedCode` 是否非空——base seed 缺失时 prompt 质量显著下降 |

## 6. 延伸阅读

- 插桩补丁细节：`@/home/yall/project/de-fuzz/docs/gcc-instrumentation/README.md`、`@/home/yall/project/de-fuzz/docs/gcc-instrumentation/Makefile.in.patch`
- 配置字段对照：`@/home/yall/project/de-fuzz/configs/gcc-v12.2.0-x64-canary.yaml`（最简单的原生场景）、`@/home/yall/project/de-fuzz/configs/gcc-v12.2.0-aarch64-canary.yaml`（交叉编译 + QEMU）
- Prompt 架构详解：`@/home/yall/project/de-fuzz/docs/prompt-architecture.md`
- 项目顶层思想：`@/home/yall/project/de-fuzz/README.md` 的 "Algorithm Flowchart" 一节
