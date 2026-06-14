# Contract: llvm-cov export JSON 解析

被测 Clang 编译每颗 seed 后，经 `llvm-profdata merge` + `llvm-cov export -format=text` 产出 JSON。本契约固化解析口径，确保"已覆盖行集合"语义与 GCC 一致。

## 输入命令（模板，路径经配置注入）

```bash
# 1. 运行期（编译 seed 前）
export LLVM_PROFILE_FILE="{profrawDir}/seed-%p.profraw"
{instrumented-clang} {seed flags} {seed.c} -o {seed-bin}   # 复用既有 compileFunc

# 2. 合并
{llvm_profdata_command} merge -sparse {profrawDir}/*.profraw -o {seed}.profdata

# 3. 导出
{llvm_cov_command} export {compilerBinary} -instr-profile={seed}.profdata \
    -format=text [-ignore-filename-regex=...] > {seedReportDir}/{ID}.json
```

## JSON 结构（仅解析所需字段）

```json
{
  "version": "3.1.0",
  "type": "llvm.coverage.json.export",
  "data": [
    {
      "files": [
        {
          "filename": "/abs/path/llvm/lib/.../StackProtector.cpp",
          "segments": [
            [Line, Col, Count, HasCount, IsRegionEntry, IsGapRegion]
          ]
        }
      ],
      "functions": [
        {
          "name": "<mangled>",
          "count": 12,
          "filenames": ["/abs/path/.../StackProtector.cpp"],
          "regions": [
            [LineStart, ColStart, LineEnd, ColEnd, ExecCount, FileID, ExpandedFileID, Kind]
          ]
        }
      ]
    }
  ]
}
```

## 解析规则

1. **类型校验**：`type` 必须等于 `"llvm.coverage.json.export"`；否则报错（防误喂 gcovr JSON）。
2. **已覆盖行抽取**（核心，等价 GCC covered line）：
   - 对每个 `data[].files[]` 的每个 `segments[]`：
     - `seg` 长度 ≥ 5；
     - `Count = seg[2]`、`HasCount = seg[3]`、`IsRegionEntry = seg[4]`；
     - 命中条件：`HasCount == true && IsRegionEntry == true && Count > 0`；
     - 产出 `"{filename}:{int(seg[0])}"`。
   - 去重后即该报告的"已覆盖行集合"。
3. **数值容错**：JSON number 在 Go 解析为 `float64`，转 int/int64 前做边界处理；`seg[3]`/`seg[4]` 可能是 bool 或 0/1，两种都接受。
4. **目标过滤**：在已覆盖行集合上，按配置 `targets`（文件路径 + 函数行号区间）过滤——
   - 文件匹配：规范化路径后比对（同 GCC `normalizeCoveragePath`）；
   - 函数匹配：用 `functions[]` 的 `name`（demangle 后）+ `regions[]` 的 `LineStart..LineEnd` 把行号归属到目标函数，仅保留落在目标函数区间内的行。

## total.json 规范形式（LLVM 自有，不复用 gcovr 合并）

LLVM 无 `gcovr -a` 式合并命令，故 `Merge` 在 Go 侧完成。`total.json` 以**已覆盖行集合的规范 JSON**存储（而非原始 llvm-cov 导出），便于增量比较与续跑：

```json
{
  "type": "defuzz.llvm.coverage.total",
  "covered_lines": { "/abs/.../StackProtector.cpp": [120, 121, 145, ...] }
}
```

- `Merge`：新报告"已覆盖行集合" ∪ total → 写回。
- `HasIncreased`：新报告集合 \ total 集合 非空即有增量。
- 首颗 seed（total 不存在）：直接以新报告集合初始化 total，返回 `true`。
- `GetStats`：覆盖行数 = total 集合大小；总行数来自 CFG（`Analyzer.GetTotalTargetLines`）或 llvm-cov summary，二者口径在实现时择一并保持稳定。

## demangle

- `functions[].name` 为 mangled；按配置 `llvm_demangler_command`（`llvm-cxxfilt`/`c++filt`）批量 demangle（stdin 行→stdout 行，等长）。
- demangler 缺失：退化为 mangled 名 + `simplifyFunctionName` 简化名双匹配（复用 GCC `targetFunctionMatcher`）。
