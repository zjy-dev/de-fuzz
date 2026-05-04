# SanitizerCoverage (SanCov) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 LLVM/Clang / compiler-rt / libFuzzer / AFL++ 中与 **SanitizerCoverage (SanCov)** 直接相关的 invariants 抽取归类, 作为 DeFuzz fuzzer 集成与 oracle 的形式化依据.
>
> 机制简写与 survey: **SanCov** = SanitizerCoverage. 是 sanitizer 框架的覆盖率插桩接口, 不是检测漏洞的 sanitizer; 但 fuzzer 反馈协议 *依赖* 此机制.

## 0. 术语与坐标

- **edge / block / function / cmp coverage**: SanCov 提供多种粒度: `func`, `bb`, `edge`, `inline-8bit-counters`, `pc-table`, `trace-pc-guard`, `trace-cmp` 等.
- **trace-pc-guard**: 每个 BB 一个 32-bit guard (link-time 分配 ID), 进入 BB 时调 `__sanitizer_cov_trace_pc_guard(guard*)`.
- **inline-8bit-counters**: 每 BB 一个 8-bit counter, 命中时 `++counter` (饱和加 1). 由 fuzzer runtime 读取.
- **trace-cmp**: 每个 cmp 指令前调 `__sanitizer_cov_trace_const_cmpN` / `_cmpN`, 让 fuzzer 学习常数比较.
- **PC table**: BB ID -> PC 地址映射, 写入 `__sancov_pcs` 段.
- **指令插桩点**: 函数入口, 每 BB 入口, 每 cmp / div / gep / switch 等.

每条 invariant 字段同前.

## 1. 启用条件

### INV-SANCOV-E01 — `-fsanitize-coverage=...` 启用

- **statement**: Clang `-fsanitize-coverage=<feature1>,<feature2>,...`. 关键 feature:
  - `func`: 仅函数入口
  - `bb`: 每 BB
  - `edge`: 每 edge (含人造 split BB)
  - `trace-pc-guard`: BB + guard 调用 callback
  - `inline-8bit-counters`: 内联 BB counter
  - `pc-table`: 生成 PC 表
  - `trace-cmp`: 每 cmp 前 callback
  - `trace-div`, `trace-gep`, `indirect-calls`: 各类追踪
  - `no-prune`: 不优化掉冗余插桩
  fuzzer 模板组合: libFuzzer 用 `inline-8bit-counters,pc-table,trace-cmp`; AFL++ 用 `trace-pc-guard`.
- **compiler**: GCC (部分), LLVM/Clang (完整)
- **version**: Clang 5+
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/SanitizerCoverage.html ; `llvm/lib/Transforms/Instrumentation/SanitizerCoverage.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz fuzzer 集成 — 用 SanCov 提供 fuzzer feedback.

### INV-SANCOV-E02 — GCC 部分实现

- **statement**: GCC 11+ 实现部分 SanCov: `-fsanitize-coverage=trace-pc` (内核用), `=trace-pc-guard`. 但 `inline-8bit-counters`, `pc-table`, `trace-cmp` 等 LLVM 特定的不实现. 因此 GCC + libFuzzer 不可直接配合.
- **compiler**: GCC
- **version**: GCC 11+
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: 跨编译器 fuzzer 兼容性.

### INV-SANCOV-E03 — 与 sanitizers 共存友好

- **statement**: SanCov 不需要 shadow / runtime 内存, 不与任何 sanitizer 冲突. 通常与 ASan / MSan / UBSan 并用 (fuzzer + 漏洞检测).
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

## 2. ABI / 回调签名

### INV-SANCOV-B01 — `__sanitizer_cov_trace_pc_guard(uint32_t *guard)`

- **statement**: BB 入口插桩, 调用该函数, 传入 BB 对应 guard 指针. fuzzer runtime 实现该函数. ABI: 无参数其他, 无返回值, 必须可被 calleed 多次. 默认 weak link, 用户可重定义.
- **runtime**: libFuzzer / AFL++ / 用户实现
- **source_kind**: user-doc + source
- **source_url_or_path**: `compiler-rt/lib/fuzzer/FuzzerTracePC.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档级 ABI 契约.

### INV-SANCOV-B02 — `__sanitizer_cov_trace_pc_guard_init(uint32_t *start, uint32_t *stop)`

- **statement**: 进程启动时由 runtime 调用一次, 传入所有 guard 数组的起止 (在 `.text` 链接器生成的两个段间). fuzzer 在此分配 ID.
- **runtime**: libFuzzer / AFL++
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/SanitizerCoverage.html
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-SANCOV-B03 — `__sanitizer_cov_trace_const_cmp{1,2,4,8}` / `_cmp{1,2,4,8}`

- **statement**: `_const_cmpN(constant, value)` 在 cmp 指令前调用, 把 "constant 与 value 比较" 报告给 fuzzer; 用于 dictionary 自动学习. `_cmpN(value1, value2)` 是双向无常量版.
- **runtime**: libFuzzer
- **source_kind**: user-doc
- **source_url_or_path**: `compiler-rt/lib/fuzzer/FuzzerTracePC.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: fuzzer 字典学习.

### INV-SANCOV-B04 — `__sanitizer_cov_trace_pc_indir(uintptr_t callee)`

- **statement**: 间接 call 前调用, 传入 callee 地址, 让 fuzzer 学习间接分支目标.
- **runtime**: libFuzzer
- **source_kind**: user-doc
- **source_url_or_path**: 同上
- **version_sensitivity**: stable
- **oracle_mapping**: 间接分支 fuzz feedback.

### INV-SANCOV-B05 — `__sanitizer_cov_8bit_counters_init`, `__sanitizer_cov_pcs_init`

- **statement**: inline-8bit-counters 模式下 runtime 注册 counter array; pc-table 模式注册 PC 表. 由 SanCov compiler-rt runtime 在 ctor 中通过 dlsym / 链接器段 symbol 边界发现.
- **runtime**: libFuzzer
- **source_kind**: user-doc + source
- **source_url_or_path**: `compiler-rt/lib/fuzzer/FuzzerTracePC.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

## 3. 段布局 (Section Layout)

### INV-SANCOV-S01 — guard 数组在 `__sancov_guards`

- **statement**: 链接器把所有 SanCov guard 集中到 `__sancov_guards` (Linux: `.data`-级 section), runtime 通过 `__start___sancov_guards` / `__stop___sancov_guards` 符号边界访问. ELF binutils 自动支持 section start/stop symbols.
- **linker**: binutils, lld
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/Transforms/Instrumentation/SanitizerCoverage.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-SANCOV-S02 — PC 表在 `__sancov_pcs`

- **statement**: pc-table 模式下 PC 表段名 `__sancov_pcs`, 同样 start/stop 符号定位.
- **linker**: 同上
- **source_kind**: source
- **source_url_or_path**: 同上
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-SANCOV-S03 — counter 数组在 `__sancov_cntrs`

- **statement**: inline-8bit-counters 模式下 counter 段名 `__sancov_cntrs`. 三个段的命名是 ABI 级契约, fuzzer runtime 不可改.
- **linker**: 同上
- **source_kind**: source
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

## 4. 插入位置

### INV-SANCOV-P01 — `func` 模式: 仅函数入口

- **statement**: 最粗粒度, 只在每函数入口插一次 callback.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-SANCOV-P02 — `bb` / `edge` 模式

- **statement**: `bb` = 每 basic block; `edge` = 每 edge (在 critical edge 上 split BB 后), 提供更精确边覆盖.
- **compiler**: Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: fuzzer 选择.

### INV-SANCOV-P03 — `trace-cmp` 在每 cmp 指令前

- **statement**: 编译器在每个 `cmp` (整数 / 浮点 / 字符) 指令前插 callback. 用于学习常数. 性能开销大 (5-10×).
- **compiler**: Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 字典学习.

### INV-SANCOV-P04 — `no-prune` 关掉冗余插桩剪除

- **statement**: 默认 SanCov 优化器会合并 dominated BB 的插桩 (减少 callback). `no-prune` 关掉合并, 保留原始信息. 用于精确分析.
- **compiler**: Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

## 5. 属性

### INV-SANCOV-A01 — `no_sanitize("coverage")` 函数级关停

- **statement**: 函数属性关闭 SanCov 插桩. 用于性能敏感函数 / 不应被 fuzzer 探索的代码.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html
- **version_sensitivity**: stable
- **oracle_mapping**: 函数级 seed.

## 6. 运行时

### INV-SANCOV-R01 — Runtime 由用户 / fuzzer 提供

- **statement**: SanCov 仅生成 callback 调用, 不提供 runtime. 用户必须链接 libFuzzer / AFL++ / 自实现的 runtime, 否则 link error (undefined reference to `__sanitizer_cov_*`).
- **runtime**: libFuzzer 等
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-SANCOV-R02 — `__sanitizer_cov_dump_coverage()`

- **statement**: 用户可调用 `__sanitizer_cov_dump_coverage(...)` 获取当前 coverage 快照. 用于自定义 fuzzer logic.
- **runtime**: libFuzzer
- **source_kind**: user-doc
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: API 调用.

## 7. 与其他机制交互

### INV-SANCOV-I01 — SanCov + ASan 是 libFuzzer 标配

- **statement**: libFuzzer 推荐 `-fsanitize=address -fsanitize=fuzzer-no-link -fsanitize-coverage=...`, 让 ASan 检漏 + SanCov 喂 coverage. 几乎所有 OSS-Fuzz 目标如此构建.
- **compiler**: Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://llvm.org/docs/LibFuzzer.html
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 标配.

### INV-SANCOV-I02 — SanCov 与 LTO 兼容

- **statement**: `-flto` 启用下, SanCov 仍正常插桩, 但 BB 划分变化导致 guard ID 集合变化. 跨 build coverage 数据不可比.
- **compiler**: Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-SANCOV-I03 — Linux kernel `kcov`

- **statement**: Linux 内核 `kcov` 用 GCC `-fsanitize-coverage=trace-pc`, 把 PC 写到 per-task buffer, 用户态 fuzzer (`syzkaller`) 读取. 是 SanCov 在内核的具体应用.
- **runtime**: Linux kernel
- **source_kind**: source
- **source_url_or_path**: Linux `kernel/kcov.c`
- **version_sensitivity**: stable
- **oracle_mapping**: 内核 fuzz seed.

### INV-SANCOV-I04 — SanCov 与 KCFI / IBT

- **statement**: SanCov callback 是直接 call (不经函数指针), 不与 IBT/KCFI 冲突.
- **compiler**: Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

## 8. 验证与已知回归

### INV-SANCOV-VER-INLINE-CTR — inline-8bit-counters 引入

- **statement**: 早期 libFuzzer 用 trace-pc-guard, 后切到 inline-8bit-counters 提速. 切换后 guard 段名变化, 旧 fuzzer corpus 不可重用.
- **runtime**: libFuzzer
- **version**: 切换于 LLVM 6+
- **source_kind**: source
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-SANCOV-VER-D135422 — Clang trace-cmp 历史 ABI 变更

- **statement**: trace-cmp callback 签名在 Clang 多版本间微调 (参数顺序, 函数命名). 跨 Clang major 不兼容.
- **compiler**: LLVM/Clang
- **source_kind**: source
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 跨版本回归.

## 9. DeFuzz Oracle 与 SanCov 的关系

| 用途 | invariant | 说明 |
|---|---|---|
| Fuzzer feedback | INV-SANCOV-B01-B05 | callback ABI 稳定性是 fuzzer 工作前提 |
| Coverage report | INV-SANCOV-S01-S03 | 静态扫描 ELF section 验证插桩 |
| Dictionary 学习 | INV-SANCOV-B03 | trace-cmp callback |
| 内核 fuzz | INV-SANCOV-I03 | kcov 接口 |

## 10. 开放问题

- **GCC 与 Clang SanCov 互通性**: 二者对 trace-pc-guard 的实现是否字节兼容, 待确认.
- **AArch64 SanCov 边角**: AArch64 上 BB 划分不同 (无 conditional move 等), guard 数量与 x86 差异.
- **新加 callback 类型**: trace-load / trace-store 等是否被 fuzzer 使用?
- **JIT 代码 SanCov**: V8 / LuaJIT JIT 出的代码无 SanCov 插桩, fuzzer 看不到 JIT 内分支.

## 11. 使用建议

- libFuzzer 标配: `-fsanitize=address,fuzzer -fsanitize-coverage=inline-8bit-counters,pc-table,trace-cmp`.
- AFL++ 标配: `-fsanitize-coverage=trace-pc-guard`.
- DeFuzz 内部 fuzzer 选 inline-8bit-counters (低开销 + 富 feedback).
- 跨 Clang major 升级时检查 callback ABI.
- `likely-to-drift` invariant (INV-SANCOV-VER-D135422) 在版本升级 audit.
