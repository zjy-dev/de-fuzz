# `-fharden-control-flow-redundancy` (HCFR) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC `gcc/passes.def` / `libgcc/hardcfr.c` 中与 **HCFR** 直接相关的 invariants 抽取归类.
>
> 机制简写与 survey: **HCFR** = `-fharden-control-flow-redundancy`. GCC 13+ 引入的"控制流冗余校验": 在每个 BB 执行时记 bitmap, return 前对比 bitmap 与 CFG 期望集合, 检测控制流被劫持后到达不应到达的 return.

## 0. 术语与坐标

- **CFR / CFG redundancy**: 编译器为每函数生成 *bitmap* (每 BB 1 bit), 函数运行时把 *实际执行过的 BB* 写到 bitmap; return 前对 bitmap 与 *合法 return path 的 BB 集合* 做集合比对. 不等则 trap.
- **`__hardcfr_check`**: libgcc runtime 入口, 接受 bitmap 与期望集合, 不一致则 abort.
- **`-fhardcfr-check-returning-calls`**: 默认开, 在 noreturn 调用之前也校验.
- **`-fhardcfr-check-noreturn-calls=...`**: 控制对 noreturn 调用前的校验数量, 选项 `nothrow` (默认), `always`, `never`.
- **`-fhardcfr-check-exceptions`**: 默认开, 异常出口前校验.

每条 invariant 字段同前.

## 1. 启用条件

### INV-HCFR-E01 — `-fharden-control-flow-redundancy`

- **statement**: GCC 13+ 选项, 启用 HCFR. 对每个函数生成 bitmap + return 前 check.
- **compiler**: GCC
- **version**: GCC 13+
- **target**: 通用
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Optimize-Options.html ; `gcc/gimple-harden-control-flow.cc` ; `libgcc/hardcfr.c`
- **evidence_snippet**: GCC manual: *"`-fharden-control-flow-redundancy`: emit extra code to check the integrity of the control flow path"*.
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: oracle 抓 return / noreturn 前的 `__hardcfr_check` 调用.

### INV-HCFR-E02 — Clang 不实现

- **statement**: Clang 无 HCFR 等价机制. KCFI 是 indirect call 检查, 不同维度.
- **compiler**: Clang (无)
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-HCFR-E03 — 函数级 `__attribute__((hardcfr))` / `nohardcfr`

- **statement**: 函数级属性细控. 关键函数加 `hardcfr`, 关闭单个函数加 `nohardcfr`.
- **compiler**: GCC
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 函数级 seed.

## 2. 编译期插桩

### INV-HCFR-C01 — 每 BB 入口 set bitmap[bb_id]

- **statement**: 编译器在每个 BB 入口插入 `bitmap[bb_id >> 6] |= (1ULL << (bb_id & 63))` 或类似位操作 store. bitmap 是函数局部数组, 大小 = ⌈num_BB / 8⌉ 字节.
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: `gcc/gimple-harden-control-flow.cc`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描.

### INV-HCFR-C02 — return 前调 `__hardcfr_check`

- **statement**: 编译器在每个 return 前发 `__hardcfr_check(bitmap, expected_set)`. expected_set 是编译期常量数组, 表示从 entry 到该 return 的合法 BB 集合.
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: `gcc/gimple-harden-control-flow.cc`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描.

### INV-HCFR-C03 — noreturn / 异常路径多次校验

- **statement**: noreturn 函数调用 (例 `abort`, `exit`, `__builtin_unreachable`) 前也校验; 异常 throw 前同理. 因此一函数可能多次调 `__hardcfr_check`. `-fhardcfr-check-noreturn-calls=` 控制.
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: 同上
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 文档.

### INV-HCFR-C04 — `no-xthrow` 默认: 不校验 throw 抛出路径

- **statement**: 默认不在每次 *被动* 抛异常前校验 (因开销大), 仅 *主动* throw 校验. 通过 `-fhardcfr-check-exceptions` 全开.
- **compiler**: GCC
- **source_kind**: user-doc
- **source_url_or_path**: GCC manual
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 文档.

## 3. Runtime

### INV-HCFR-R01 — `__hardcfr_check` 调用 abort

- **statement**: libgcc `__hardcfr_check(map, expected)`: 对 map 与 expected 做集合比对 (allowable subsets), 不匹配 -> `__builtin_trap()` -> SIGILL/SIGABRT. 报告由编译器侧的 trap 处理.
- **runtime**: libgcc
- **source_kind**: source
- **source_url_or_path**: `libgcc/hardcfr.c`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: oracle 看 SIGILL / SIGABRT.

### INV-HCFR-R02 — 同函数多次 trap 不重复报告

- **statement**: 一旦命中, 程序终止; 不存在"重复报告". 这是 trap 模式的标准行为.
- **runtime**: libgcc
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

## 4. ELF 元数据

### INV-HCFR-M01 — 无 ELF property

- **statement**: HCFR 是纯 codegen + libgcc runtime, 无 ELF 标识. 链接器不感知; 若混编, 仅插桩函数受保护.
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描函数级.

## 5. 与其他机制交互

### INV-HCFR-I01 — HCFR 与 SP / SHSTK / PAC 正交

- **statement**: HCFR 检测控制流劫持后到达"非法 return", 与 canary / SHSTK / PAC 检测返回地址被改是不同维度. 同启提供深度防御.
- **compiler**: GCC
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-HCFR-I02 — HCFR 与 CFI / IBT/BTI 正交

- **statement**: HCFR 检测 *函数内* 控制流; CFI / IBT/BTI 检测 *函数间* 间接调用. 二者覆盖不同窗口.
- **compiler**: GCC vs Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-HCFR-I03 — HCFR 与 LTO

- **statement**: LTO 启用下 BB 划分变化, bitmap 大小 / expected_set 可能变. 同函数跨 build 的 bitmap 不可比.
- **compiler**: GCC
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: LTO 矩阵.

### INV-HCFR-I04 — HCFR 与 setjmp / longjmp

- **statement**: longjmp 跳过 bitmap 写入, 跳到 setjmp 后下一 return 时 bitmap 不完整 -> `__hardcfr_check` fail. 已知缺陷; HCFR docs 标记 setjmp 路径需 `nohardcfr`.
- **compiler**: GCC
- **source_kind**: source + 文档
- **source_url_or_path**: `libgcc/hardcfr.c` 注释
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: setjmp/longjmp seed (反例).

## 6. 验证与已知回归

### INV-HCFR-VER-EARLY — GCC 13 早期边角

- **statement**: GCC 13.0 实现存在多边角 (大函数 bitmap 性能, 异常路径漏校), 在 13.x 子版本逐步修复. 老 GCC 13 seed 可能假阳 / 假阴.
- **compiler**: GCC
- **version**: 持续修复 GCC 13.x
- **source_kind**: mailing-list
- **source_url_or_path**: gcc-patches "hardcfr"
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 老 GCC 13 seed.

## 7. DeFuzz Oracle 映射总表

| Invariant | Oracle 信号 | seed |
|---|---|---|
| INV-HCFR-C02 | 静态扫描 return 前 `__hardcfr_check` | 任意函数 |
| INV-HCFR-R01 | `SIGILL` / `SIGABRT` | 间接控制流劫持 (gadget 跳到非法 BB) |
| INV-HCFR-I04 | 反例: setjmp/longjmp 触发假阳 | longjmp seed |

## 8. 开放问题

- **bitmap 内存开销**: 大函数 bitmap 可能上 KB; 性能 / cache 影响待量化.
- **inline 后 BB 合并**: 编译器 inline 函数后 BB 合并, expected_set 是否更新? 需 audit.
- **Clang 路线图**: Clang 是否未来实现, 待跟踪.
- **`-fhardcfr-check-noreturn-calls=always` 性能**: 全开模式下开销待补.

## 9. 使用建议

- 关键 parser / dispatcher 函数加 `__attribute__((hardcfr))`.
- 涉 setjmp/longjmp 的函数加 `__attribute__((nohardcfr))` 避免假阳.
- 与 SP / SHSTK / CFI 共用提供深度防御.
- 限 GCC 13+, 跨 GCC 13 子版本 audit.
