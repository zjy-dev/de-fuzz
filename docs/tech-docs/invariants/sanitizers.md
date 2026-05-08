# Sanitizers (ASan / HWASan / MSan / TSan / UBSan / DFSan) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 LLVM/Clang / GCC libsanitizer / compiler-rt / Linux kernel 中与 **各 sanitizer** 直接相关的 invariants 抽取归类, 作为 DeFuzz sanitizer oracle 的形式化依据.
>
> 机制简写与 survey: ASan, HWASan, MSan, TSan, UBSan, DFSan. 每 sanitizer 自成一节. KASAN (内核 ASan) 在 ASan 节末单独说明. SanCov 见 `@/home/yall/project/de-fuzz/docs/invariants/sancov.md`.

## 0. 共同术语

- **shadow memory**: 用每 N 字节内存对应 1 字节 / 1 bit 的元数据, 描述该内存当前 *合法 / poisoned / tagged*. ASan: 1:8 比例; MSan: 1:1 (定义) + 1:1 (origin); TSan: 复杂 metadata; HWASan: 16:1.
- **interceptor / runtime**: sanitizer 在 libc 函数 (`malloc/memcpy/strcmp/...`) 之前安装 wrapper, 用于检查 / 更新 shadow.
- **redzone / poison**: ASan 在 heap/stack 对象周围分配的"哨兵字节", 检查越界用.
- **trap mode / handler mode**: 共享的二态: trap 直接 `ud2`/`SIGILL`, handler 通过 ubsan / asan runtime 输出报告.
- **`-fsanitize=...`**: 编译器选项, 启用对应 sanitizer. 多个 sanitizer 通常 *互斥* (不可同启), 除少数组合 (UBSan + ASan ok).

每条 invariant 字段同前.

---

# 一. AddressSanitizer (ASan)

## 1.1 启用条件

### INV-ASAN-E01 — `-fsanitize=address` 启用 ASan

- **statement**: GCC / Clang `-fsanitize=address` 启用 ASan; 链接时自动加 ASan runtime (compiler-rt `libclang_rt.asan-*.so` 或 GCC `libasan.so`).
- **compiler**: GCC 4.8+, LLVM/Clang 3.1+
- **target**: x86_64, aarch64, riscv64, ppc64, mips, etc.
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/AddressSanitizer.html ; https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html ; `compiler-rt/lib/asan/`
- **version_sensitivity**: stable
- **oracle_mapping**: ASan oracle 是 DeFuzz 标配; 期望 fuzz seed 触发 use-after-free / heap-OOB / stack-OOB 时 runtime 报告.

### INV-ASAN-E02 — ASan + MSan / TSan 互斥

- **statement**: ASan 与 MSan / TSan 不可同启, 因 shadow 布局冲突. ASan + UBSan 可同启 (UBSan 无 shadow).
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/AddressSanitizer.html
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

## 1.2 字节模式 / shadow

### INV-ASAN-B01 — Shadow scale = 1:8

- **statement**: ASan shadow 比例 1:8, 即每 8 字节对齐区域用 1 字节 shadow. shadow byte 取值范围: 0 = all 8 bytes addressable; 1-7 = 前 N 字节 ok; 0x80+ = poisoned, 各值对应不同区域 (heap/stack/global redzone, freed memory 等).
- **runtime**: compiler-rt asan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/asan/asan_mapping.h`
- **evidence_snippet**: ASan algo paper.
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明; oracle 不直接读 shadow.

### INV-ASAN-B02 — Shadow offset 平台依赖

- **statement**: shadow base 地址平台依赖:
  - x86_64 Linux: `0x00007fff8000`
  - aarch64 (39-bit VA): `0x1ffff8000`
  - aarch64 (42-bit VA): `0xffffffff8000`
  - i386 Linux: `0x20000000`
  shadow_address(addr) = (addr >> 3) + offset.
- **runtime**: compiler-rt asan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/asan/asan_mapping.h`
- **version_sensitivity**: target-specific
- **oracle_mapping**: oracle 跨平台时 shadow 地址不同, 但语义相同.

## 1.3 内存对象保护

### INV-ASAN-P01 — Heap: redzone + delayed free

- **statement**: ASan `malloc` 分配 size + redzone (前后各 ≥16 字节); `free` 后对象进入 quarantine (默认 256MB), 期间访问触发 use-after-free 报告.
- **runtime**: compiler-rt asan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/asan/asan_allocator.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: heap UAF / OOB seed.

### INV-ASAN-P02 — Stack: 局部对象 redzone

- **statement**: ASan 编译器把每个 alloca 替换为 "alloca(size + redzone)", 用 instruction 在 prologue/epilogue poison/unpoison shadow. 每次进入函数, stack frame 有红区; 退出时全部 poison 防 stale-pointer use.
- **compiler**: GCC, Clang
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/Transforms/Instrumentation/AddressSanitizer.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: stack OOB / use-after-return seed.

### INV-ASAN-P03 — Global: redzone 链接期补齐

- **statement**: ASan 编译器把全局变量后追加 redzone, 链接期 ASan ctor 注册全局 metadata 数组到 runtime, 由 runtime poison shadow.
- **compiler + linker**: GCC, Clang + ld
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/Transforms/Instrumentation/AddressSanitizer.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: global OOB seed.

### INV-ASAN-P04 — Use-after-return 启用 fake stack

- **statement**: `ASAN_OPTIONS=detect_stack_use_after_return=1` (默认 0 在某些版本) 让 ASan 在每次 stack alloca 走 *fake stack* (heap 上分配), 函数返回后该 fake frame 进 quarantine. 启用后能检测返回地址的 dangling pointer.
- **runtime**: compiler-rt asan
- **source_kind**: user-doc
- **source_url_or_path**: https://github.com/google/sanitizers/wiki/AddressSanitizerUseAfterReturn
- **version_sensitivity**: stable
- **oracle_mapping**: UAR seed.

## 1.4 属性

### INV-ASAN-A01 — `no_sanitize("address")` 函数级关停

- **statement**: 函数属性关闭 ASan 插桩. 该函数无 redzone, 不参与 shadow 检查. 仍走 ASan malloc.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html
- **version_sensitivity**: stable
- **oracle_mapping**: 函数级 seed.

### INV-ASAN-A02 — `__attribute__((address_safe))`/Clang `safebuffers`

- **statement**: 等价含义, 关闭 ASan 检查.
- **compiler**: Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 同 A01.

## 1.5 运行时

### INV-ASAN-R01 — ASan 触发 abort + 报告

- **statement**: 命中违例时 ASan runtime 打印彩色报告到 stderr, 然后 `abort()` (`ASAN_OPTIONS=abort_on_error=1`) 或 `_exit(1)` (默认). 退出码默认 1.
- **runtime**: compiler-rt asan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/asan/asan_report.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 看 stderr 内 "ERROR: AddressSanitizer:" + 退出码.

### INV-ASAN-R02 — `ASAN_OPTIONS` 控制行为

- **statement**: 关键 options:
  - `halt_on_error=1` 命中即停 (默认)
  - `abort_on_error=0/1` 选 abort vs exit
  - `detect_stack_use_after_return=0/1`
  - `detect_leaks=0/1` (LSan 内嵌)
  - `quarantine_size_mb=N`
- **runtime**: compiler-rt asan
- **source_kind**: user-doc
- **source_url_or_path**: https://github.com/google/sanitizers/wiki/AddressSanitizerFlags
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 配置 ASAN_OPTIONS 控制信号语义.

## 1.6 KASAN (内核 ASan)

### INV-KASAN-E01 — Linux kernel `CONFIG_KASAN`

- **statement**: 内核 ASan, 三种实现: `KASAN_GENERIC` (与用户态等价 inline shadow), `KASAN_SW_TAGS` (HWASan 风格软标记), `KASAN_HW_TAGS` (Arm MTE 硬件标记). 报告 → `kasan_report` → printk.
- **runtime**: Linux kernel
- **version**: Linux ≥ 4.0 (基础)
- **source_kind**: source
- **source_url_or_path**: Linux `mm/kasan/`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 内核 seed.

---

# 二. HWAddressSanitizer (HWASan)

## 2.1 启用与原理

### INV-HWASAN-E01 — `-fsanitize=hwaddress` 启用 (aarch64 主)

- **statement**: HWASan 用 AArch64 Top Byte Ignore (TBI) 把每个指针的高 8 位用作 tag, malloc / alloca 给对象 + 红区分配 *相同 tag*, 错位访问检测. 不需 shadow 的 1:8 比例 (减少内存); 但需要 aarch64 TBI / 最近 x86 LAM.
- **compiler**: GCC 11+, LLVM/Clang 9+
- **target**: aarch64 (主), x86_64 with LAM (实验)
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/HardwareAssistedAddressSanitizerDesign.html ; `compiler-rt/lib/hwasan/`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: aarch64 主, oracle 信号同 ASan.

### INV-HWASAN-E02 — 与 ASan 互斥

- **statement**: HWASan + ASan 不可同启.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

## 2.2 字节模式

### INV-HWASAN-B01 — Tag 在指针高 8 位

- **statement**: AArch64 上指针的 bit 56-63 是 tag, CPU 通过 TBI 忽略, 访问按 bit 0-55 进行. HWASan 维护 shadow 表: 每 16 字节内存对应 1 字节 shadow tag; 访问前指令插桩比对.
- **runtime**: compiler-rt hwasan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/hwasan/hwasan_mapping.h`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-HWASAN-B02 — Shadow scale 1:16

- **statement**: HWASan shadow 1:16, 比 ASan 省内存. shadow byte 即对应 16 字节 region 的 tag; 0 = unspecified/ok, 非 0 = 必须匹配指针 tag.
- **runtime**: compiler-rt hwasan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/hwasan/hwasan_mapping.h`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

## 2.3 运行时

### INV-HWASAN-R01 — Tag mismatch 触发 trap

- **statement**: 访问指令前插桩比对 tag. 不匹配走 `__hwasan_check` runtime 入口, 输出报告 + abort. AArch64 用 `brk` 指令带特定 immediate 区分种类 (read/write/size).
- **runtime**: compiler-rt hwasan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/hwasan/hwasan.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: 命中信号 `SIGTRAP` 或 `SIGABRT`.

### INV-HWASAN-R02 — Tag 来源是 PRNG

- **statement**: 每次分配 HWASan 给对象生成新 tag (8-bit 随机), 写入 shadow + 指针高 8 位. 攻击者无法预测 tag, 这是 HWASan 防御的根基.
- **runtime**: compiler-rt hwasan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/hwasan/hwasan_allocator.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

---

# 三. MemorySanitizer (MSan)

## 3.1 启用

### INV-MSAN-E01 — `-fsanitize=memory` 启用 MSan

- **statement**: 检测 *未初始化内存读取*. 编译器在每次 store 写 shadow 标记 "initialized", 每次 load 检查 shadow; 若 load 后值传播到分支 / syscall, 触发报告.
- **compiler**: LLVM/Clang (GCC 不实现)
- **version**: Clang 4+
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/MemorySanitizer.html ; `compiler-rt/lib/msan/`
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 用未初始化内存读取 seed.

### INV-MSAN-E02 — 必须重新编译所有依赖

- **statement**: MSan 假设所有 load/store 都被插桩; 任何未插桩的代码 (包括 libc) 在写时不更新 shadow, MSan 误报. 因此必须用 MSan-instrumented libc++ / glibc + 所有依赖. 通常用 `--gcc-toolchain=...` 和 `compiler-rt` 的 `MSAN_NO_LIBC=...`.
- **compiler + runtime**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/MemorySanitizer.html
- **version_sensitivity**: stable
- **oracle_mapping**: 工程上 oracle 跑前必须确认整 stack 已 MSan build.

## 3.2 Shadow

### INV-MSAN-B01 — Shadow 1:1 + Origin 1:1

- **statement**: MSan shadow 与目标 1:1 (同字节). bit 1 = uninitialized. 此外 *origin* shadow 同样 1:1, 记录每 4 字节的 origin (调用栈 hash), 用于报告时回溯首次未初始化点.
- **runtime**: compiler-rt msan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/msan/msan_mapping.h`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

## 3.3 运行时

### INV-MSAN-R01 — Use of uninitialized value 触发报告

- **statement**: load 未初始化内存到寄存器无副作用; 但若该值后续用于 *条件分支* 或 *作为 syscall 参数*, MSan 报告 "use of uninitialized value". 仅在 *使用* 才报, 不在 *load* 报.
- **runtime**: compiler-rt msan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/msan/msan_report.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 看 stderr "WARNING: MemorySanitizer".

### INV-MSAN-R02 — `MSAN_OPTIONS` 控制

- **statement**: `halt_on_error`, `print_stats`, `track_origins=2` 等. `track_origins` 提高调试体验但慢 1.5×.
- **runtime**: compiler-rt msan
- **source_kind**: user-doc
- **source_url_or_path**: https://github.com/google/sanitizers/wiki/MemorySanitizer
- **version_sensitivity**: stable
- **oracle_mapping**: 选项配置.

---

# 四. ThreadSanitizer (TSan)

## 4.1 启用

### INV-TSAN-E01 — `-fsanitize=thread` 启用 TSan

- **statement**: 检测数据竞争. 编译器为每次 memory access 插桩, runtime 维护 happens-before 图.
- **compiler**: GCC 4.8+, LLVM/Clang 3.2+
- **target**: x86_64, aarch64, ppc64
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/ThreadSanitizer.html ; `compiler-rt/lib/tsan/`
- **version_sensitivity**: stable
- **oracle_mapping**: 多线程竞争 seed.

### INV-TSAN-E02 — 与 ASan / MSan 互斥

- **statement**: 不可同启.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

## 4.2 Shadow

### INV-TSAN-B01 — Shadow 8:1 (复杂结构)

- **statement**: TSan shadow 比例 1:8 但每 shadow 字段是 16 字节 (`Shadow Word`), 即每 8 字节内存对应 16 字节 metadata. metadata 含 thread id + epoch + access type (read/write/atomic) + size. shadow 总开销 ~5-15× 内存.
- **runtime**: compiler-rt tsan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/tsan/rtl/tsan_platform.h`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 内存开销大.

## 4.3 运行时

### INV-TSAN-R01 — 数据竞争报告

- **statement**: 检测到 W↔W / W↔R 跨线程无同步访问时, 输出报告 + 默认 exit 66 (`TSAN_OPTIONS=exitcode=66`).
- **runtime**: compiler-rt tsan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/tsan/rtl/tsan_report.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 看 stderr "WARNING: ThreadSanitizer" + 退出码.

### INV-TSAN-R02 — `TSAN_OPTIONS`

- **statement**: `halt_on_error`, `exitcode=N`, `report_thread_leaks`, `history_size=N` 等.
- **runtime**: compiler-rt tsan
- **source_kind**: user-doc
- **source_url_or_path**: https://github.com/google/sanitizers/wiki/ThreadSanitizerFlags
- **version_sensitivity**: stable
- **oracle_mapping**: 配置.

---

# 五. UndefinedBehaviorSanitizer (UBSan)

## 5.1 启用

### INV-UBSAN-E01 — `-fsanitize=undefined` 启用 UBSan 全家

- **statement**: UBSan 是 ~25 个检查的集合: 整数溢出, 移位越界, 浮点 NaN, vptr 校验, 对齐, 空指针解引用, ... `-fsanitize=undefined` 启用大部分; 单独 `-fsanitize=integer`, `-fsanitize=null`, `-fsanitize=alignment` 可细控.
- **compiler**: GCC 4.9+, LLVM/Clang 3.3+
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/UndefinedBehaviorSanitizer.html
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 检 ub 类问题.

### INV-UBSAN-E02 — 可与 ASan / MSan / TSan 同启

- **statement**: UBSan 不需 shadow, 与其他 sanitizer 兼容.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵 友好.

## 5.2 trap / handler 模式

### INV-UBSAN-B01 — `-fsanitize-trap=undefined` -> `ud2`

- **statement**: 默认 UBSan handler 调 `__ubsan_handle_*` runtime 输出报告; `-fsanitize-trap=undefined` 让违例直接 `ud2`/`brk`, 无 runtime 依赖. 信号: handler 模式 `SIGABRT` (or 警告继续), trap 模式 `SIGILL`.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/UndefinedBehaviorSanitizer.html
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 区分两模式.

### INV-UBSAN-B02 — `-fsanitize-recover=...` 让违例可继续

- **statement**: 默认很多 UBSan 检查报告后 *继续* 执行 (recover). `-fno-sanitize-recover=undefined` 让首次违例 abort. 这影响 oracle: 默认配置可能错过 *后续* 被掩盖的 bug.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/UndefinedBehaviorSanitizer.html
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 强制 `-fno-sanitize-recover`.

## 5.3 关键检查

### INV-UBSAN-P01 — 整数溢出 (`-fsanitize=signed-integer-overflow`)

- **statement**: 检查带符号整数加减乘溢出. 触发 -> handler.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 整数 seed.

### INV-UBSAN-P02 — 空指针 (`-fsanitize=null`)

- **statement**: 解引用空指针前插检查.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: NULL seed.

### INV-UBSAN-P03 — vptr (`-fsanitize=vptr`)

- **statement**: 虚函数调用前检查 vptr 类型一致 (与 CFI vcall 类似但不需 LTO). 用于 C++ 类型混乱检测.
- **compiler**: Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 虚函数 seed.

## 5.4 运行时

### INV-UBSAN-R01 — handler 函数名 ABI

- **statement**: 各 UBSan 检查对应 runtime handler 函数名 (`__ubsan_handle_signed_overflow`, `__ubsan_handle_type_mismatch_v1`, ...) 是 ABI 级契约, GCC libubsan / compiler-rt ubsan 必须保持一致.
- **runtime**: GCC libubsan / compiler-rt ubsan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/ubsan/ubsan_handlers.h`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

---

# 六. DataFlowSanitizer (DFSan)

## 6.1 启用

### INV-DFSAN-E01 — `-fsanitize=dataflow` 启用 (Clang only)

- **statement**: DFSan 对每字节内存赋一个 *label* (16-bit), 编译器把 load/store 指令插桩传播 label, 用于通用 taint 分析. 用户通过 `__dfsan_create_label`, `__dfsan_set_label` 等 API 控制.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/DataFlowSanitizer.html ; `compiler-rt/lib/dfsan/`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: taint 测试 seed; 也可用作 fuzzer feedback.

### INV-DFSAN-E02 — 仅 Clang 支持

- **statement**: GCC 不实现 DFSan.
- **compiler**: GCC (无)
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 仅 Clang 路径.

## 6.2 Shadow

### INV-DFSAN-B01 — Shadow 1:2 (16-bit label per byte)

- **statement**: 早期 DFSan shadow 1:1 (每字节 1 字节 label, 8-bit), 后改 16-bit label 即 shadow 1:2. label 0 = "无标记".
- **runtime**: compiler-rt dfsan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/dfsan/dfsan_platform.h`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 文档.

## 6.3 运行时

### INV-DFSAN-R01 — Label 通过 union 传播

- **statement**: 当两个 tainted 值参与运算 (e.g. add), 结果 label = `dfsan_union(l1, l2)` (创建 / 复用一个新 label 表示来自两源). 这是 DFSan 数据流追踪的核心.
- **runtime**: compiler-rt dfsan
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/dfsan/dfsan.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: taint propagation seed.

---

# 七. 共用约束

## 7.1 frame pointer / sibling call

### INV-SAN-FP01 — 推荐 `-fno-omit-frame-pointer`

- **statement**: 所有 sanitizer 报告依赖准确栈回溯, 推荐 `-fno-omit-frame-pointer -fno-optimize-sibling-calls`. 否则 stack trace 不完整.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **source_url_or_path**: 各 sanitizer docs
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 强制配置.

## 7.2 ELF 元数据

### INV-SAN-M01 — sanitizer ctor 在 `.preinit_array`

- **statement**: 各 sanitizer runtime 通过 `.preinit_array` 注册 ctor (`__asan_init`, `__msan_init`, `__tsan_init`, `__ubsan_init`), 需在 main 之前运行. ld.so 按 section 顺序调用.
- **runtime**: compiler-rt
- **source_kind**: source
- **source_url_or_path**: 各 runtime `*_init.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-SAN-M02 — GCC libsanitizer 与 LLVM compiler-rt 同步

- **statement**: GCC `libsanitizer/` 大部分代码从 LLVM `compiler-rt` 同步 (有 `README.gcc` 注释源 commit). runtime 行为以 LLVM 为准, GCC 滞后数月.
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: `gcc/libsanitizer/README.gcc`
- **version_sensitivity**: target-specific (GCC vs Clang 微差异)
- **oracle_mapping**: 跨编译器 oracle 时考虑滞后.

## 7.3 运行时通用

### INV-SAN-R01 — `[SAN]_OPTIONS` 环境变量

- **statement**: 每 sanitizer 有自己的环境变量 (`ASAN_OPTIONS`, `MSAN_OPTIONS`, ...) 控制运行时. 几乎所有 sanitizer 都支持 `halt_on_error`, `abort_on_error`, `log_path`, `external_symbolizer_path`. 这是 oracle 控制 sanitizer 行为的标准接口.
- **runtime**: compiler-rt
- **source_kind**: user-doc
- **source_url_or_path**: https://github.com/google/sanitizers/wiki
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 必须显式设环境变量.

# 八. 已知回归 / CVE

### INV-SAN-VER-LIBC-INTERCEPTOR — Glibc 升级破 sanitizer interceptor

- **statement**: 历史多次 glibc 升级 (`fortify` / IFUNC strcmp / pthread 实现变更) 破 sanitizer interceptor, 表现为 false positive / 静默无效. 修复滞后到 compiler-rt 新版本.
- **runtime**: glibc + compiler-rt
- **source_kind**: source
- **source_url_or_path**: LLVM bug tracker, glibc Bugzilla
- **version_sensitivity**: target-specific
- **oracle_mapping**: oracle 跑前 audit 版本组合.

### INV-SAN-VER-PR47009 — GCC libsanitizer 滞后

- **statement**: GCC PR47009 系: GCC libsanitizer 落后 LLVM 数月, 部分新 sanitizer (HWASan) 在某些 GCC 版本不可用.
- **compiler**: GCC
- **source_kind**: mailing-list
- **source_url_or_path**: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=47009
- **version_sensitivity**: stable
- **oracle_mapping**: 跨编译器对照.

# 九. DeFuzz Oracle 映射总表

| Sanitizer | Oracle 信号 | 触发 seed |
|---|---|---|
| ASan | "AddressSanitizer:" + abort | UAF / OOB |
| HWASan | "HWAddressSanitizer:" + abort | 同 ASan, aarch64 优先 |
| MSan | "MemorySanitizer:" + abort | 未初始化读取 |
| TSan | "ThreadSanitizer:" + exit 66 | 多线程竞争 |
| UBSan | "runtime error:" + abort/continue | 整数溢出 / NULL / 移位 |
| DFSan | API 查询 label | taint propagation |

# 十. 开放问题

- **GCC vs Clang 语义微差异**: 各 sanitizer 在 GCC 与 Clang 间有轻微行为差异 (检查覆盖率, redzone 大小), 待逐项整理.
- **musl / bionic 上 sanitizer**: musl 不官方支持 ASan; bionic 仅有 HWASan 完整支持. 跨 libc 行为待补.
- **多 sanitizer 同启**: 实验性 fork 已有 ASan + UBSan 同启; 目前 ASan + MSan 不可同启的根本原因 (shadow 重叠).
- **Sanitizers 与 LTO**: LTO 启用下 sanitizer 行为变化, 待补.
- **SanitizerCoverage 与 sanitizers 的耦合**: 见 `@/home/yall/project/de-fuzz/docs/invariants/sancov.md`.

# 十一. 使用建议

- DeFuzz oracle 默认套餐: `-fsanitize=address,undefined -fno-sanitize-recover=all -fno-omit-frame-pointer -O1`.
- 多线程 seed 用 TSan 单独构建.
- aarch64 上 ASan 可改 HWASan 节省内存 (12.5% vs 200%).
- MSan 仅在能完整 MSan-rebuild libc 的平台上 useful.
- UBSan 永远 `-fno-sanitize-recover` 避免漏报.
- 跨编译器 sanitizer oracle 需对照 GCC libsanitizer 版本与 compiler-rt commit hash.
