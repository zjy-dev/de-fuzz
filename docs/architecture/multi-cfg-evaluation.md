# 多 CFG 支持：意义与 PR 实现评估

> **配套阅读**：`@/home/yall/project/de-fuzz/docs/architecture/gcc-pipeline.md`。本文假设读者已理解 "构建期 GCC 插桩 → 运行期 CFG 解析 + 目标选择" 的整体链路。
>
> **评估目标**：分支 `more_architecture` 上 BIGSMATER 的 commit `b907c01` "Multi-File CFG and Fortify Update"（未合入 main），这是本项目目前唯一专门围绕多 CFG 改动的 PR。

## 1. 为什么需要多 CFG

### 1.1 单 CFG 的结构性瓶颈

项目最初只抓 `cfgexpand.cc.015t.cfg` 一份 dump，`SelectTarget` 的候选空间天然被限制在 `cfgexpand.cc` 里的函数。但实际的编译器防御逻辑**从一开始就是跨文件的**：

| 防御策略 | 核心文件 | 主要函数 |
| --- | --- | --- |
| Stack canary | `gcc/cfgexpand.cc` | `stack_protect_classify_type`, `expand_used_vars`, `stack_protect_prologue` |
| Stack canary | `gcc/function.cc` | `stack_protect_epilogue`, `assign_parm_adjust_stack_rtl` |
| Stack canary | `gcc/targhooks.cc` | `default_stack_protect_guard`, `default_external_stack_protect_fail` |
| Stack canary | `gcc/config/aarch64/aarch64.cc` 等 | `aarch64_stack_protect_guard`（架构特定） |
| `_FORTIFY_SOURCE` | `gcc/c-family/c-opts.cc` | `c_finish_options` |
| `_FORTIFY_SOURCE` | `gcc/builtins.cc` | `fold_builtin_object_size`, `expand_builtin_memory_chk` |
| `_FORTIFY_SOURCE` | `gcc/gimple-fold.cc` | `gimple_fold_builtin_memory_chk` 等系列 |
| `_FORTIFY_SOURCE` | `gcc/targhooks.cc` / `gcc/config/linux.cc` | `default_fortify_source_default_level`, `linux_fortify_source_default_level` |

单 CFG 模式下，fuzzer 要么只能针对 `cfgexpand.cc` 里的目标，要么手工换 cfg 文件；跨文件的 target functions 会在 `cmd/defuzz/app/fuzz.go` 的"单 CFG filter"里被静默跳过，形成覆盖盲区。

### 1.2 多 CFG 带来的三类收益

1. **目标空间扩展**：`SelectTarget`（`@/home/yall/project/de-fuzz/internal/coverage/analyzer.go:386-531`）以 `bbWeights` 排序候选 BB，多 CFG 意味着候选集跨越 epilogue、target hook、架构后端；高 branching-factor 的 BB 往往恰恰出现在 `function.cc` 或 `builtins.cc`，从而让权重排序更能挑到"信息量高"的目标。
2. **发散分析更准确**：`UftraceAnalyzer`（`@/home/yall/project/de-fuzz/internal/coverage/divergence.go:97-156`）观察到的函数名来自整个 `cc1` 调用栈，而不限于某一文件。若 analyzer 只装载了 `cfgexpand.cc` 的 CFG，发散点落在 `function.cc::stack_protect_epilogue` 时，prompt 就无法附带对应的函数源码，LLM 只能盲写；多 CFG 让 `BuildRefinedPrompt`（`@/home/yall/project/de-fuzz/internal/prompt/constraint.go:236-383`）有能力在两端源码里都做"标注 + 引用"。
3. **策略可拓展性**：Fortify / FORTIFY / CFI 等将来要接入的防御几乎都位于 `builtins.cc` + `gimple-fold.cc` + `c-family/c-opts.cc` 组合。没有多 CFG，这类策略没办法纳入"CFG-guided constraint solving" 的主循环，只能退化为盲测。

## 2. 现状：main 上已有的基础实现

这部分来自 commit `1d4a3ea`（"Add riscv64 canary baseline analysis and seed updates"，2026-03-13，`crypto-analyzer`），也是当前 HEAD 唯一在用的多 CFG 代码路径：

- **配置字段**：`FuzzConfig.CFGFilePaths []string`（`@/home/yall/project/de-fuzz/internal/config/config.go:68-72`），保留 `CFGFilePath string` 向后兼容。
- **合并入口**：`runFuzz` 把两者合并进局部切片再交给 analyzer（`@/home/yall/project/de-fuzz/cmd/defuzz/app/fuzz.go:314-320`）。
- **Analyzer 改造**：`NewAnalyzer` 接受 `cfgPaths []string`，`for _, path := range cfgPaths { c.parseCFGFile(path) }`，所有 functions 合并进同一张 `functions` map，然后统一 `buildPredecessorMaps`（`@/home/yall/project/de-fuzz/internal/coverage/analyzer.go:116-162`）。
- **目标选择**：通过 `inferCFGSourceBase` + `filepath.Base(target.File)` 做粗粒度匹配，单 CFG 时过滤"非当前文件"的目标（`@/home/yall/project/de-fuzz/cmd/defuzz/app/fuzz.go:325-348`）。

当前真实启用多 CFG 的配置：

| 配置文件 | cfg_file_paths 数量 | 用途 |
| --- | --- | --- |
| `@/home/yall/project/de-fuzz/configs/gcc-v15.2.0-riscv64-canary.yaml:33-37` | 4（cfgexpand.cc / function.cc / targhooks.cc / toplev.cc） | 首个真正启用的多 CFG 案例 |
| 其它 canary 配置（x64、aarch64、loongarch64） | 1（仅 cfgexpand.cc） | 仍为单 CFG |

**定性结论**：main 的多 CFG 只完成了"能跑"，缺少防御性校验和测试。

## 3. PR `b907c01` 的改动清单

原始 stat（`git show --stat b907c01`）显示 38 个文件、+2169/-93，但其中大部分是 Fortify 策略（初始 seeds、oracle、prompt 扩展）。**纯粹归属多 CFG 的改动**如下：

### 3.1 `cmd/defuzz/app/fuzz.go` 新增 `collectTargetFunctionsForCFGPaths`

把原先直接 `targetFunctions := ...` 的内联逻辑抽成一个可测试 helper：

- 输入：CFG 路径切片 + `[]config.TargetFunction`。
- 处理：把所有 cfg 的 basename 收进 set；逐个 target 按 `filepath.Base(target.File)` 查集合，不命中时累加 `skippedTargets` 并记录 `missingTargetFiles`。
- 输出：`(targetFunctions, skippedTargets, missingTargetFiles)` 三元组，便于 `logger.Warn` 输出高信噪比提示。

### 3.2 单/多 CFG 分支打日志

- 单 CFG 分支：`Single CFG dump mode: only functions from %s can be actively targeted; skipped %d target functions from other files (%v)`。
- 多 CFG 分支：`Multi-CFG mode: skipped %d target functions because matching CFG dumps are missing for source files %v`。

比 main 版的"静默跳过"有明显诊断价值。

### 3.3 Config 重置

在 `loadCompilerConfigInto` 里加了一行：

```go
cfg.Compiler = CompilerConfig{}
```

针对 Viper 在 `UnmarshalKey` 到**已有结构体**时不会清空切片字段的行为——对多 CFG 尤其关键，因为 `cfg_file_paths` 是切片，跨 ISA/strategy 切换 config 时残留旧路径会让 analyzer 默默"把别的策略的目标也装进来"。

### 3.4 Compiler CFlags 统一入口

新增 `resolveCompilerCFlags`，把原来 `runFuzz` 里三处"没有 cflags 就塞 `-O0` / `-fstack-protector-strong -O0`"的分支统一为一个中性 baseline。这与多 CFG 正交，但同属该 PR。

### 3.5 `cmd/defuzz/app/fuzz_test.go`

6 个单元测试，按内容分两组：

- helper 级别：`TestCollectTargetFunctionsForCFGPaths_SingleCFGFiltersOtherFiles`、`..._MultiCFGSkipsMissingFiles` —— 纯字符串级验证。
- 端到端：`..._AArch64CanaryConfigHasNoMissingTargets`、`..._AArch64FortifyConfigHasNoMissingTargets` —— 加载实际 YAML，用 `LoadConfigWithOverrides("aarch64", "canary"|"fortify")` 验真。

### 3.6 配置切换为多 CFG

- `configs/gcc-v15.2.0-aarch64-canary.yaml`：`cfg_file_path` → `cfg_file_paths`，囊括 cfgexpand/function/calls/targhooks/aarch64 共 5 份；同时关掉 `flag_strategy.enabled`，Canary flag 由 LLM 的 CFLAGS 段接管。
- 新增 `configs/gcc-v15.2.0-{aarch64,loongarch64,riscv64}-fortify.yaml`，每份 5 个 cfg 路径，与 Fortify oracle 配合。

## 4. 设计评估

### 4.1 做得好的点

1. **向后兼容干净**：既不破坏 `CFGFilePath` 单值写法，也让 `CFGFilePaths` 能独立使用；`cmd/defuzz/app/fuzz.go:314-320` 的合并逻辑是 4 行，理解成本低。
2. **可测试的 helper 抽象**：`collectTargetFunctionsForCFGPaths` 把文件系统无关的逻辑单独拎出来，使得 `fuzz_test.go` 里的前两个 case 能纯内存断言，不需要 disk fixture。
3. **诊断信息显著改善**：`skippedTargets` + `missingTargetFiles` 在 fuzz 启动时一次性暴露配置错配，比 main 版本少踩大量"跑了半小时才发现没覆盖到"的坑。
4. **Config 重置是真 bug 修复**：Viper 语义坑，PR 作者显然踩过一次。建议即便 PR 不合入，也应把这一行单独 cherry-pick 到 main。
5. **跨文件 Fortify 是多 CFG 的真实动机**：Fortify 在 GCC 里的逻辑天然跨 `builtins.cc` / `gimple-fold.cc` / `c-opts.cc` / `targhooks.cc` / `config/linux.cc`，新建的 `configs/gcc-v15.2.0-*-fortify.yaml` 真的把这 5 份 CFG 都拉进来了——这正是多 CFG 的"杀手级用例"。

### 4.2 问题与风险（按严重度从高到低）

#### 高：同名函数静默覆盖

`@/home/yall/project/de-fuzz/internal/coverage/analyzer.go:192-196` 与 `:280-282`：

```go
if currentFunc != nil {
    c.functions[currentFunc.Name] = currentFunc
    c.indexFunction(currentFunc)
}
```

**问题**：`c.functions` 以**函数名**为 key。若两份 CFG dump 含同名函数（典型场景：跨 TU 的 `static` helper、匿名命名空间里的 `execute`、模板实例化），后 parse 的直接覆盖前者，没有任何警告。

**对多 CFG 的放大**：单 CFG 时不会触发；一旦开启 5 份 dump，碰撞概率显著上升。PR 加入的 `configs/gcc-v15.2.0-aarch64-canary.yaml` 里，`cfgexpand.cc` 和 `function.cc` 都各自有 `{anonymous}::pass_expand::execute` 之类 pass wrapper 的风险需要手动确认。

**建议修复**（可小改）：

```go
if existing, ok := c.functions[currentFunc.Name]; ok {
    logger.Warn("CFG function %q already parsed from %s, overriding with %s",
        currentFunc.Name, existing.SourceFile, cfgPath)
}
```
或者更彻底：改 key 为 `file + "::" + name`，`targetFunctions` 改为 `[]TargetFunctionRef{File, Function}`。后者是主干改动，可作为 follow-up。

#### 中：`collectTargetFunctionsForCFGPaths` 只按 basename 匹配

```go
targetBase := filepath.Base(target.File)
if len(cfgSourceBases) > 0 && !cfgSourceBases[targetBase] {
    skippedTargets += len(target.Functions)
    ...
}
```

**问题**：`gcc/config/i386/i386.cc` 和 `gcc/config/aarch64/aarch64.cc` 不同，但换成 `gcc/foo/calls.cc` 与 `gcc/cp/calls.cc` 这种真实存在的情况就会误判匹配。当前 GCC 源码里没有完全同名的两份，但这是"依赖外部约定"而非显式保证。

**建议**：先用完整 path suffix 匹配（`strings.HasSuffix(target.File, "/" + cfgBaseFull)` 或比较 `filepath.Clean`），找不到再降级到 basename；debug 日志列出每条 (target.File → cfgPath) 的匹配关系。

#### 中：`fuzz_test.go` 与 main 配置已脱节（若合并会失败）

测试 `TestCollectTargetFunctionsForCFGPaths_AArch64CanaryConfigHasNoMissingTargets` 只读取 `cfg.Compiler.Fuzz.CFGFilePaths`，**不包含** `CFGFilePath` 单值：

```go
targetFunctions, skippedTargets, missingFiles := collectTargetFunctionsForCFGPaths(
    cfg.Compiler.Fuzz.CFGFilePaths, // <- 只传这个
    cfg.Compiler.Targets,
)
require.NotEmpty(t, targetFunctions)
```

而 HEAD 上 `configs/gcc-v15.2.0-aarch64-canary.yaml` 仍用单值 `cfg_file_path`（`@/home/yall/project/de-fuzz/configs/gcc-v15.2.0-aarch64-canary.yaml:56`）。若把 PR 合进 main 而不同步改配置，这个 case 会看到空切片 → 直接短路 → `require.NotEmpty` 失败。

**建议**：helper 或 helper 的"生产侧"入口（`buildCfgPaths(cfg)`）收敛"合并 `CFGFilePath` + `CFGFilePaths`"的逻辑，测试和生产共用；否则永远会出现"测试里一个合并逻辑、fuzz.go 里另一个合并逻辑"的双重真相。

#### 中：与 `b907c01` 同一 PR 里混入了大量 Fortify 改动

从 `git show --stat b907c01` 看，38 个改动文件里至少 20 个是 Fortify 相关（oracle、template、understanding、initial seeds、3 套 fortify.yaml）。这让评审难度、合入风险都被放大，也让"多 CFG 这个机制对 canary 是否真的有收益"难以单独度量。

**建议**：把 PR 拆成两半：
1. *feat: tighten multi-CFG orchestration*（只含 `collectTargetFunctionsForCFGPaths` + config reset + fuzz_test.go + canary config 迁移 + resolveCompilerCFlags）。
2. *feat: add Fortify strategy*（fortify oracle + prompt + configs + seeds）。

这样 canary 侧的多 CFG 能独立先行，Fortify 作为独立策略 follow-up。

#### 低：去重缺失

`collectTargetFunctionsForCFGPaths` 遇到"同一函数出现在多个 target 条目"时会重复追加。下游 `NewAnalyzer` 的 validation loop（`@/home/yall/project/de-fuzz/internal/coverage/analyzer.go:148-152`）不会报错，但 `SelectTarget` 内部用该 slice 的长度推断"候选域"时会有轻微偏差。建议 helper 内 `sort.Strings` + `slices.Compact`。

#### 低：多 CFG 解析未并行

启动阶段对 5 份 cfg 做顺序 `parseCFGFile`；每份百 KB~MB 的 dump 约几百 ms，总时间秒级。当前不是瓶颈，但将来若继续扩到 10+ 份（比如把 `config/*/*.cc` 全拉进来做架构对比实验）会变成启动体感延迟。PR 的设计没有为并行打下基础（`c.functions`、`c.bbWeights` 的并发写入都需要 `sync.Mutex`）。**不是 PR 的问题**，但应在做类似架构文档时记下。

#### 低：路径规范化潜在不一致（已有问题，被多 CFG 放大）

`normalizeFilePath`（`@/home/yall/project/de-fuzz/internal/coverage/analyzer.go:95-107`）基于 `source_parent_path` 把相对路径扩展成"`<source_parent>/xxx.cc`"，而 target.File 在 YAML 里写的是 `"gcc/gcc/cfgexpand.cc"` 这种相对 `source_parent` 的路径，同时 gcovr 的 `-r ..` 又会给出另一种形态的路径。单 CFG 时大家都是同一个文件，路径对齐错了也工作；多 CFG 把 5 个文件一起塞进来，就更容易暴露"命中判定失败 / 渲染源码片段指向错误目录"的 corner case。

**建议**：在 `Analyzer` 层加一个 debug dump（第一次 parse 完之后 log 一份 `function → file` 映射），fuzz 启动时人工肉眼核对一次。

## 5. 对 main 的推荐动作

不论 PR 是否整体合入，以下三项值得单独 cherry-pick 或重新实现：

1. **`CompilerConfig{}` reset**：1 行修复，影响所有多 CFG 配置切换场景。
2. **`collectTargetFunctionsForCFGPaths` + 合并 helper**：把 main 当前版本 `cmd/defuzz/app/fuzz.go:314-348` 的内联逻辑替换成可测函数，同时修掉"basename 匹配的盲区"。
3. **Warn 级别日志**：单/多 CFG 模式下的"被跳过的 target"都应该显式打印，不要默认静默。

## 6. 参考

- PR commit：`b907c01`（branch `more_architecture`，尚未进入 main）；follow-up：`cb776e0 fix fortify fp & update docs`、`88907d0 fix canary_oracle`。
- main 上首次落地多 CFG：`1d4a3ea Add riscv64 canary baseline analysis and seed updates`（2026-03-13）。
- 相关代码区：
  - `@/home/yall/project/de-fuzz/internal/config/config.go:63-72`
  - `@/home/yall/project/de-fuzz/cmd/defuzz/app/fuzz.go:312-420`
  - `@/home/yall/project/de-fuzz/internal/coverage/analyzer.go:113-162`、`174-283`
- 整体工作流请看 `@/home/yall/project/de-fuzz/docs/architecture/gcc-pipeline.md`。
