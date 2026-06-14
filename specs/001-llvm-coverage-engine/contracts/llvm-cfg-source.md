# Contract: LLVM CFG 源（.ll → CFGFunction）

LLVM 侧等价于 GCC `.015t.cfg` 的控制流结构来源。从 `clang -S -emit-llvm -g` 产出的 `.ll` 解析出 BB + 后继 + 每块源码行，填充既有 `CFGFunction`/`BasicBlock`。

## 输入生成（离线/启动期一次性）

```bash
clang -S -emit-llvm -g -O0 {compiler-source}.cc -o {out}.ll
```

- `-g` 必需：使每条 IR 指令带 `!dbg !N`（源码行映射的唯一权威来源）。
- `-O0` 推荐：避免优化破坏 BB 与源码行的对应关系，贴近 GCC `.015t.cfg`（tree 级、未优化）。
- 每个目标源文件生成一个 `.ll`，路径列入配置 `llvm_ir_paths`（等价 `cfg_file_paths`）。

## .ll 结构与解析规则

### 函数
```
define {attrs} {ret} @{mangled}({params}) {attrs} !dbg !{N} {
entry:
  ...
{label}:
  ...
}
```
- 每个 `define ... @name(...) { ... }` → 一个 `CFGFunction`。
- `Name`：`@` 后的 mangled 名；demangle 后用于匹配配置目标函数。
- 仅对配置 `targets[].functions` 中的函数构造 `CFGFunction`（其余可跳过以省内存）。

### 基本块
- 入口块为隐式（函数体首行起，至首个终结指令）；其后每个 `{label}:` 起一个新块。
- **BB ID 分配**：按出现顺序分配整数，**从 2 起**（对齐 `Analyzer` 跳过 `bbID<=1` 的虚拟入口约定）。
- 维护 `label → BB ID` 映射用于后继解析。

### 后继（终结指令）
块的最后一条指令决定后继：
- `br label %{L}` → 后继 `{L}`。
- `br i1 %c, label %{T}, label %{F}` → 后继 `{T}, {F}`。
- `switch ... [ i32 v, label %{L1} ... ], label %{default}` → 所有 case label + default。
- `invoke ... to label %{normal} unwind label %{unwind}` → `{normal}, {unwind}`。
- `ret` / `unreachable` / `resume` → 无后继。
- 后继 label 经 `label → BB ID` 映射写入 `BasicBlock.Successors` 与 `CFGFunction.SuccsMap[ID]`。

### 源码行（!dbg → !DILocation）
- 块内每条指令尾部 `!dbg !{N}`；元数据区有 `!{N} = !DILocation(line: X, column: Y, scope: !{S})`。
- 对块内所有指令的 `line` 去重 → `BasicBlock.Lines`。
- `scope` 链回溯 `!DISubprogram`→`!DIFile`（`filename`/`directory`）得到 `BasicBlock.File`；规范化后须能匹配配置 `targets[].file`。
- 无 `!dbg` 的指令（如部分 PHI / 编译器生成）跳过，不贡献行号。

## 产出（填充既有结构）

```go
CFGFunction{
  Name: "<demangled>", MangledName: "<mangled>",
  Blocks: { 2: &BasicBlock{ID:2, File:"...StackProtector.cpp", Lines:[120,121], Successors:[3,4]}, ... },
  SuccsMap: { 2:[3,4], 3:[5], ... },
}
```
- `PredsMap`/`Predecessors` 不在解析阶段填，由 `Analyzer.buildPredecessorMaps()` 统一生成。
- 多个 `.ll` 的函数合并进同一 `Analyzer`（等价 GCC 多 CFG 合并）。

## 进入 Analyzer

新增构造路径（择一）：
- `coverage.NewAnalyzerFromCFGFunctions(funcs []*CFGFunction, targetFunctions []string, sourceDir, mappingPath string, decay float64) (*Analyzer, error)`，内部复用既有索引/校验逻辑；或
- 让 `llvm_cfg` 产出 `map[string]*CFGFunction` 后，由 `Analyzer` 既有的 `indexFunction`/`buildPredecessorMaps` 消费。

校验：配置目标函数若在解析结果中找不到 → 报错或告警（FR-013），与 GCC `NewAnalyzer` 对 `targetFunctions` 的校验一致。

## 解析边界（实现注意）

- 多行指令、`phi` 节点、无名块（LLVM 用 `%N` 数字编号块）、`;` 注释、属性组 `#0`、metadata-only 行——均需正确切分。
- 以 fixture `.ll`（含条件分支、switch、循环、invoke）单测覆盖。
- `.dot`（`opt -passes=dot-cfg`）仅作可视化交叉验证，不作为运行期数据源（`.dot` 中 `!dbg` 编号在部分 LLVM 版本不准，issue #120168）。
