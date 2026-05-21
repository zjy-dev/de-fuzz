# Clang Control Flow Integrity (CFI) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 LLVM/Clang / Itanium C++ ABI / lld / compiler-rt 中与 **Clang CFI** 直接相关的 invariants 抽取归类, 作为 DeFuzz CFI oracle 的形式化依据.
>
> 机制简写与 survey: **CFI** = Clang Control Flow Integrity (一组 sanitizer 形式的前向 CFI 检查). 与 KCFI (内核 CFI) 解耦, 见 `@/home/yall/project/de-fuzz/docs/invariants/kcfi.md`. GCC 等价是 `-fvtable-verify` (libvtv), 行为与 Clang CFI 不同.

## 0. 术语与坐标

- **CFI scheme**: Clang CFI 由若干 `-fsanitize=cfi-*` 组成:
  - `cfi-vcall`: 虚函数调用 (this->vptr->method)
  - `cfi-nvcall`: 非虚成员函数调用
  - `cfi-derived-cast`: 基类向派生类强制转换
  - `cfi-unrelated-cast`: 不相关类型间转换
  - `cfi-icall`: 间接函数调用 (函数指针)
  - `cfi-mfcall`: member function pointer 调用
  - `cfi`: 全部上述 (alias)
- **type id / type metadata**: 编译器为每个间接调用目标 (函数 / vtable) 计算一个 *type ID*, 通过 LLVM IR `!type` metadata 传到 LTO 阶段, 在 link 时合成 *bit set* 或 *jump table*.
- **bit set / jump table**: CFI lowering 后端: 直接 bit set 用 mod/and 检查; jump table 把所有同类型函数集中到连续地址区, indirect call 校验 PC 落在该区.
- **cross-DSO CFI**: 跨 DSO 函数指针调用; 由 ld.so + compiler-rt `libclang_rt.cfi.so` 维护进程级 type-id shadow 表.
- **`-flto` 必需**: 大多数 CFI 方案依赖 LTO 让 type metadata 跨 module 可见; 例外是 `cfi-icall` 的 `non-canonical-jump-tables` 模式不一定需要 LTO.
- **`-fvisibility=hidden`**: 严格依赖, 因 type ID 比较仅在同一 linkage unit 内有效, public 符号无法精确 type 化.
- **CFI failure handler**: 默认调用 `__ubsan_handle_cfi_check_fail` (有 trap 模式) 或 `ud2` (no-runtime trap 模式).

每条 invariant 字段同前.

## 1. 静态前提 (Static Preconditions)

### INV-CFI-E01 — `-fsanitize=cfi` 全家启用

- **statement**: Clang `-fsanitize=cfi` 启用所有 CFI 子方案; 可单独 `-fsanitize=cfi-vcall` 等. 必须配 `-flto` (除 `cfi-icall` 部分模式) 与 `-fvisibility=hidden` (除 `cfi-icall`).
- **compiler**: LLVM/Clang
- **version**: Clang 3.7+ (基础), 现代功能 Clang 7+
- **target**: 通用
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/ControlFlowIntegrity.html ; `clang/include/clang/Basic/Sanitizers.def`
- **evidence_snippet**: Clang docs: *"To use CFI, pass `-fsanitize=cfi` ... You must also enable LTO with `-flto`, and use `-fvisibility=hidden`."*.
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz CFLAGS 矩阵; oracle 抓 type metadata 是否生成, 以及间接调用前的检查序列.

### INV-CFI-E02 — `-fsanitize=cfi-icall` 不强制 LTO 但收益缩小

- **statement**: `cfi-icall` 在不开 LTO 时仍可工作 (本 TU 内的间接调用), 但失去跨 module 类型一致性. `-fno-sanitize-cfi-cross-dso` 即此模式. 默认行为是开 LTO + cross-DSO.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/ControlFlowIntegrity.html
- **version_sensitivity**: stable
- **oracle_mapping**: 反例 seed: 不开 LTO 时跨 DSO 函数指针调用不被 CFI 拦截.

### INV-CFI-E03 — Cross-DSO CFI 需 `-fsanitize-cfi-cross-dso`

- **statement**: 跨 DSO CFI (e.g. dlopen 加载的库) 启用需 `-fsanitize-cfi-cross-dso` (Clang 3.9+). 实现要求所有 DSO 都启用 CFI, 并链接 `libclang_rt.cfi.so` runtime; 后者维护 *进程级 shadow 表* 把每个函数地址映射到 type ID.
- **compiler + runtime**: LLVM/Clang + compiler-rt
- **version**: Clang 3.9+
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/ControlFlowIntegrityDesign.html ; `compiler-rt/lib/cfi/cfi.cc`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: dlopen + 跨 DSO 间接调用 seed.

### INV-CFI-E04 — GCC 不实现 Clang CFI

- **statement**: GCC 没有等价的 `-fsanitize=cfi`. GCC 提供 `-fvtable-verify` (libvtv), 仅覆盖虚函数, 设计与 Clang CFI 不同 — Clang CFI 用 type ID + bit set, vtv 用全程序 vtable 闭包.
- **compiler**: GCC (无)
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/wiki/cxx-vtv
- **version_sensitivity**: stable
- **oracle_mapping**: GCC 路径不应启 CFI; oracle 跨编译器对比时单列.

## 2. 字节模式 / IR 元数据

### INV-CFI-B01 — IR `!type` metadata 是 CFI 的传递载体

- **statement**: 编译器在每个间接调用目标 (vtable / 函数定义) 上挂 LLVM IR `!type !N` metadata, type ID 是该类型 mangled name 的 hash. LTO 阶段把所有 module 的 metadata 合并, 决定 bitset / jump table 布局. 没有 `!type` 的目标视为非 CFI 安全, 间接调用拦截.
- **compiler**: LLVM/Clang
- **source_kind**: source + LLVM docs
- **source_url_or_path**: https://llvm.org/docs/TypeMetadata.html
- **version_sensitivity**: stable
- **oracle_mapping**: 编译期生成 IR 检查 metadata; 可通过 `clang -emit-llvm -S` 验证.

### INV-CFI-B02 — bit set 检查的指令模式

- **statement**: lowering 后, 间接调用前的检查通常是 `mov`+`and`+`cmp`+`jne` 序列, 验证目标地址落在某个 bit set 内. 失败跳到 trap (`ud2`) 或 runtime handler.
- **compiler**: LLVM/Clang
- **source_kind**: source + lit test
- **source_url_or_path**: `llvm/test/Transforms/LowerTypeTests/`
- **version_sensitivity**: likely-to-drift (lowering 优化变化)
- **oracle_mapping**: 静态扫描 `.text` 中检查序列字节模式.

### INV-CFI-B03 — jump table lowering 模式

- **statement**: `cfi-icall` 在 jump table 模式 (默认) 下生成连续 `jmp <real_func>` 表, 间接调用变成 "scale + add + jmp" 形式; `cfi_canonical_jump_table` 属性可让函数地址直接是其 jump table entry, 保持兼容性.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html ; `llvm/lib/Transforms/IPO/LowerTypeTests.cpp`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描 jump table 字节模式.

## 3. 检查插入位置

### INV-CFI-P01 — 虚函数调用前发 type check

- **statement**: `cfi-vcall` 在每次 `this->vptr->method()` 之前插入 `llvm.type.test(vptr, !id)` intrinsic, lowering 为 bit set 检查. vptr 不在该 type id 集合中即 trap.
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `clang/lib/CodeGen/CGCXXABI.cpp` ; `clang/lib/CodeGen/CGClass.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 虚函数 + 篡改 vptr seed.

### INV-CFI-P02 — 间接函数调用前发 type check

- **statement**: `cfi-icall` 在每个间接 call 前插 `llvm.type.test(funcptr, !type)`. 函数定义必须有匹配 `!type` metadata, 否则函数指针指向它会失败.
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `clang/lib/CodeGen/CGCall.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 函数指针签名不匹配 seed.

### INV-CFI-P03 — `unrelated-cast` 在 cast 表达式插入

- **statement**: `cfi-unrelated-cast` 在 `static_cast<T*>(ptr)` 等基类向"完全无关"类型 cast 时插入 type check. 合法的 derived-to-base 不报.
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `clang/lib/Sema/SemaCast.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 错误 cast seed.

### INV-CFI-P04 — `mfcall` 检查 member function pointer

- **statement**: `cfi-mfcall` 在通过 member function pointer 调用前检查 *指针的 this-adjustment 字段* 一致性. 涉及 Itanium C++ ABI 的 member fn pointer `{ptr, this-adjust}` 表示.
- **compiler**: LLVM/Clang
- **source_kind**: source + ABI
- **source_url_or_path**: https://itanium-cxx-abi.github.io/cxx-abi/abi.html ; `clang/lib/CodeGen/MicrosoftCXXABI.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 错误 mfp seed.

## 4. 属性 / 局部控制

### INV-CFI-A01 — `no_sanitize("cfi")` 函数级关停

- **statement**: `__attribute__((no_sanitize("cfi")))` 关闭该函数的所有 CFI 检查; 可细到 `no_sanitize("cfi-vcall")`.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html
- **version_sensitivity**: stable
- **oracle_mapping**: 函数级测试.

### INV-CFI-A02 — `cfi_unchecked_callee` 让指针接受非 CFI 目标

- **statement**: 函数指针属性 `__attribute__((cfi_unchecked_callee))` 让 *该指针* 在间接调用时不做 CFI 检查. 用于必须接受 non-CFI 函数 (signal handler / dlsym 取得的函数) 的场景.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html
- **version_sensitivity**: stable
- **oracle_mapping**: 函数指针属性 seed.

### INV-CFI-A03 — `cfi_canonical_jump_table` 保持地址兼容

- **statement**: `__attribute__((cfi_canonical_jump_table))` 让 `&function` 返回该函数的 jump table entry (非真实函数体), 这样在 cross-DSO 场景, 两个 DSO 取地址结果相等. 默认 ON; 关掉用 `__attribute__((nocf_check))` 风格的 `no_canonical_jump_table` 属性.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html
- **version_sensitivity**: stable
- **oracle_mapping**: 跨 DSO 函数地址等同性 seed.

### INV-CFI-A04 — `lto_visibility_public` (Windows COM 等)

- **statement**: 当 ABI 允许跨 linkage unit 抽象基类继承 (Windows COM, 部分插件框架) 时, 该基类必须显式标 `__attribute__((lto_visibility_public))`, 否则 CFI 假设其封闭, 拒绝合法跨 DSO 派生.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/LTOVisibility.html
- **version_sensitivity**: stable
- **oracle_mapping**: COM 跨 DSO seed.

## 5. ELF 元数据

### INV-CFI-M01 — Cross-DSO CFI 通过 runtime + dynamic tag

- **statement**: cross-DSO CFI 不直接用 GNU property; 而通过 compiler-rt runtime (`libclang_rt.cfi.so`) 在 ld.so 启动后做 *all DSO 函数地址 -> type id* 的 shadow 表构建. 每个 DSO 在 `.cfi_check` 段提供 type-id 列表; runtime 注册.
- **runtime**: compiler-rt
- **source_kind**: source + 设计
- **source_url_or_path**: https://clang.llvm.org/docs/ControlFlowIntegrityDesign.html ; `compiler-rt/lib/cfi/cfi.cc`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: dlopen seed; oracle 检查 `.cfi_check` 段存在.

### INV-CFI-M02 — `-fsanitize-trap=cfi` 让违例直接 `ud2`

- **statement**: 默认 CFI 失败调用 ubsan handler 输出报告; `-fsanitize-trap=cfi` 改为直接 `ud2` (`#UD` -> `SIGILL`), 用于 release 构建避免 runtime 依赖. 这是 oracle 关键差异: 信号是 `SIGILL` (trap) vs `SIGABRT` (handler abort).
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/UndefinedBehaviorSanitizer.html
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 区分两种信号.

## 6. 运行时语义

### INV-CFI-R01 — CFI 失败信号

- **statement**: CFI 失败默认调用 `__ubsan_handle_cfi_check_fail` -> `abort()` -> `SIGABRT` (退出码 134); trap 模式下 `ud2` -> `SIGILL` (`si_code = ILL_ILLOPN`); recover 模式下打印警告但继续 (用于诊断).
- **runtime**: compiler-rt ubsan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/ubsan/ubsan_handlers_cxx.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 看 `SIGABRT` / `SIGILL` 区分模式.

### INV-CFI-R02 — Cross-DSO failure 由 `__cfi_slowpath` 处理

- **statement**: cross-DSO 模式下间接调用先在本地 bitset 检查; 不命中走 `__cfi_slowpath(type_id, ptr)` runtime 入口, 在 shadow 表中查 ptr 所属 DSO 的 `__cfi_check` 函数, 由该函数最终 trap.
- **runtime**: compiler-rt
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/cfi/cfi.cc` (`__cfi_slowpath`)
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 跨 DSO 错误调用 seed.

## 7. 与其他机制的交互

### INV-CFI-I01 — CFI vs IBT/BTI

- **statement**: CFI 是 *软件 + 类型化* 的前向 CFI, IBT/BTI 是 *硬件 + 通用* 的前向 CFI. CFI 拦截的范围严格更窄但精确 (类型签名), IBT/BTI 拦截"任何非 endbr/BTI 目标" (粗粒度). 同启提供深度防御.
- **compiler**: LLVM/Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/endbr-ibt.md` ; `@/home/yall/project/de-fuzz/docs/invariants/bti.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-CFI-I02 — CFI vs `-fvtable-verify` (GCC libvtv)

- **statement**: GCC libvtv 通过维护"全程序 vtable 闭包" + 信号保护页校验; Clang CFI 通过 type ID + LTO bit set. 不互通, 不可混链.
- **compiler**: GCC vs Clang
- **source_kind**: 设计
- **source_url_or_path**: `gcc/libvtv/`
- **version_sensitivity**: stable
- **oracle_mapping**: 编译器分支.

### INV-CFI-I03 — CFI 与 LTO visibility

- **statement**: CFI 对每个 class 推断"是否可被外部 linkage unit 继承". 默认假设否 (即不可); 仅 ABI 强制公开 (Windows COM std::exception) 或显式 `lto_visibility_public` 才允许. 见 INV-CFI-A04.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/LTOVisibility.html
- **version_sensitivity**: stable
- **oracle_mapping**: 跨 DSO 派生类 seed.

### INV-CFI-I04 — KCFI 是 CFI 的 *无 LTO* 简化版

- **statement**: KCFI 把 type-id hash 直接编码到函数 prologue (前 4 字节常量), 间接 call 前对比该常量. 不需 LTO. 仅检查 `cfi-icall` 子集. 见 `@/home/yall/project/de-fuzz/docs/invariants/kcfi.md`.
- **compiler**: LLVM/Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/kcfi.md`
- **version_sensitivity**: stable
- **oracle_mapping**: KCFI 与 CFI 解耦 oracle.

### INV-CFI-I05 — 与 sanitizers 共用 ubsan handler

- **statement**: CFI 失败 handler 在 ubsan runtime 中, 若同时 `-fsanitize=cfi,undefined`, 错误会先经 ubsan; 兼容良好. trap 模式可单独 per-check.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/UndefinedBehaviorSanitizer.html
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

## 8. 验证与已知回归

### INV-CFI-VER-DSO-VISIBILITY — 早期 CFI 强制 -fvisibility=hidden

- **statement**: 早期 (Clang 3.7-4.0) CFI 严格要求 `-fvisibility=hidden`, 否则编译期报错. 后续放松, 部分子方案允许默认 visibility.
- **compiler**: LLVM/Clang
- **version**: 修复散布 Clang 3.9-4.0
- **source_kind**: source
- **source_url_or_path**: LLVM bug tracker
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 Clang 反例.

### INV-CFI-VER-CROSS-DSO-PERF — Cross-DSO CFI 性能问题

- **statement**: 早期 cross-DSO CFI 在每次间接 call 走 slowpath, 性能下降 30%+. 通过 fast-path bitset 优化在 Clang 8+ 改善.
- **compiler**: LLVM/Clang
- **version**: 优化于 Clang 8+
- **source_kind**: mailing-list
- **source_url_or_path**: LLVM Discourse
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 Clang seed.

### INV-CFI-VER-VTABLE-INTERLEAVE — vtable interleaving 算法变化

- **statement**: CFI vtable 布局优化 (interleaving) 让多类共享同一区段, 减小 bitset. 算法在 Clang 5-7 演进; 升级时若不重构整 LTO 单元会报奇怪 link error.
- **compiler**: LLVM/Clang
- **version**: 演进 Clang 5+
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/Transforms/IPO/LowerTypeTests.cpp`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: vtable 大量 + LTO seed.

## 9. DeFuzz CFI Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-CFI-P01 | `SIGABRT` (handler) / `SIGILL` (trap) | 篡改 vptr |
| INV-CFI-P02 | 同上 | 函数指针指向类型不匹配函数 |
| INV-CFI-P03 | 同上 | static_cast 错误类型 |
| INV-CFI-P04 | 同上 | member function pointer 错配 |
| INV-CFI-A02 | 不报错 (反例豁免) | `cfi_unchecked_callee` 函数指针 |
| INV-CFI-R02 | 跨 DSO trap 信号 | dlopen + 间接调用 |
| INV-CFI-M02 | `SIGILL` 取代 `SIGABRT` | `-fsanitize-trap=cfi` |

## 10. 开放问题

- **C++ exception 与 CFI**: 异常 unwind 调用 destructor (虚函数), CFI 在 unwind 路径下的检查覆盖. 待补.
- **JIT 生成代码与 CFI cross-DSO**: JIT 函数地址不在 shadow 表, 调用前需 runtime 注册. V8 等如何配合?
- **C 反射 / FFI**: dlsym 取得的函数指针类型为 `void*`, CFI 必须靠 `cfi_unchecked_callee` 豁免, 否则统一 trap.
- **不同 LLVM 版本间 CFI ABI 兼容**: cross-DSO CFI runtime ABI 在 Clang 主版本间如何兼容, 待补 invariant.
- **GCC 对应方案**: GCC `-fvtable-verify` 完全不同设计; 是否值得单写 invariant? (libvtv 在 survey 中已列, 此处不重复.)

## 11. 使用建议

- 必开 `-flto=thin` + `-fvisibility=hidden` + `-fsanitize=cfi`. Cross-DSO 加 `-fsanitize-cfi-cross-dso`.
- release 构建用 `-fsanitize-trap=cfi` 减少 runtime 依赖.
- DSO 间共享类型 (Windows COM, plugin) 必须 `lto_visibility_public`.
- `likely-to-drift` invariant (INV-CFI-B02 lowering 字节, INV-CFI-VER-VTABLE-INTERLEAVE) 在每次 Clang major 升级 audit.
- DeFuzz oracle 跨 DSO 路径必须 dlopen 真实 .so 测试, 不只是单可执行内.
