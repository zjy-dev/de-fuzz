# Kernel Control Flow Integrity (KCFI) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 LLVM/Clang / Linux kernel / Arm A-profile FineIBT 等 中与 **KCFI** 直接相关的 invariants 抽取归类, 作为 DeFuzz KCFI oracle 的形式化依据.
>
> 机制简写与 survey: **KCFI** = `-fsanitize=kcfi`. 内核场景设计的 **无 LTO** 间接调用 CFI. 与 Clang `-fsanitize=cfi` 的关系见 `@/home/yall/project/de-fuzz/docs/invariants/cfi.md`.

## 0. 术语与坐标

- **KCFI**: Linux 内核 (主线 6.1+ 在 x86_64, 6.2+ 在 aarch64) 启用 `CONFIG_CFI_CLANG`, 即 KCFI. 思路: 编译期把每个函数的 *type-id hash* 写入函数 prologue 前若干字节; 间接 call 前从函数指针所指目标读出该 hash 比对, 不匹配则 trap. 不需要 LTO, 不破坏跨 DSO 函数指针等价.
- **type-id hash**: `__builtin_kcfi_typeid(T)` 计算的 32-bit 值, 由函数签名 mangled name 经稳定 hash 得到.
- **prologue prefix**: 函数实际入口前 4 字节 (x86_64 / aarch64) 是 `mov $TYPEID, %eax` 或类似的"伪指令"实际只用作 type-id 标记的常量. CPU 不执行该字节 (因为 call 跳到 +4 偏移).
- **call site check**: `__cfi_check_failed(typeid, addr)` runtime 入口 (内核内为 `__cfi_check_failed`); 用户态 KCFI 默认 `ud2` 触发 trap.
- **`-fsanitize=kcfi`**: Clang 选项. 与 `-fsanitize=cfi-icall` 等价但不需 LTO.

每条 invariant 字段同前.

## 1. 启用条件 (Enablement)

### INV-KCFI-E01 — `-fsanitize=kcfi` 在 Clang 16+

- **statement**: Clang 选项 `-fsanitize=kcfi` (Clang 16+) 启用 KCFI 风格的 CFI 仅对 *间接函数调用* (icall). 不需 `-flto`, 不需要 `-fvisibility=hidden`. 对 vtable / member function pointer 不做检查.
- **compiler**: LLVM/Clang
- **version**: Clang 16+
- **target**: x86_64, aarch64, riscv64 (实验)
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/ControlFlowIntegrity.html#kernel-cfi ; `clang/lib/CodeGen/CGExpr.cpp` (`EmitKCFICheck`)
- **evidence_snippet**: Clang docs: *"`kcfi`: Indirect call checking, intended for the kernel"*.
- **version_sensitivity**: stable
- **oracle_mapping**: 内核构建标志 `CONFIG_CFI_CLANG=y` 引发 `-fsanitize=kcfi`.

### INV-KCFI-E02 — Linux kernel `CONFIG_CFI_CLANG`

- **statement**: Linux 内核 6.1+ 在 x86_64, 6.2+ 在 aarch64 添加 `CONFIG_CFI_CLANG`. 启用后所有内核间接调用经 KCFI 检查. KCFI 失败由 `do_cfi_check_failed` 调度到 `panic` (默认) 或可配置 oops.
- **runtime**: Linux kernel
- **version**: Linux ≥ 6.1
- **target**: x86_64, aarch64, riscv64
- **source_kind**: source
- **source_url_or_path**: Linux `arch/x86/include/asm/cfi.h` ; `kernel/cfi.c`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 内核 seed.

### INV-KCFI-E03 — 用户态 KCFI

- **statement**: KCFI 也可在用户态使用, 用作 *不依赖 LTO* 的 CFI 替代. 但失去 vtable / cross-cast 检查, 仅 icall. DeFuzz 用户态可作 CFI 弱化版 oracle.
- **compiler**: LLVM/Clang
- **target**: 通用
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/ControlFlowIntegrity.html
- **version_sensitivity**: stable
- **oracle_mapping**: 用户态 seed.

## 2. 字节模式

### INV-KCFI-B01 — 函数 prologue 前 4 字节是 type-id

- **statement**: x86_64 KCFI: 函数实际入口前 4 字节是 `mov $0xXXXXXXXX, %eax` 编码 (`B8 XX XX XX XX`), 但实际入口在 `+5` 偏移 (跳过 `B8` + 4 字节). aarch64 KCFI: 函数前 4 字节 `BTI c` + 紧接的 4 字节 type-id literal.
- **compiler**: LLVM/Clang
- **target**: x86_64, aarch64
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/Target/X86/X86AsmPrinter.cpp` ; `llvm/lib/Target/AArch64/AArch64AsmPrinter.cpp`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描 `.text` 函数前缀 type-id 模式.

### INV-KCFI-B02 — 间接调用前的检查序列

- **statement**: x86_64 间接 call 前 lowering 为:
  ```
  cmpl $TYPEID, -0x4(%target)
  jne  1f
  call *%target
  1: ud2
  ```
  即"读目标函数前 4 字节作 type-id 比对, 不匹配则 ud2".
- **compiler**: LLVM/Clang
- **target**: x86_64
- **source_kind**: source + lit test
- **source_url_or_path**: `llvm/lib/Target/X86/X86KCFIPass.cpp` ; `llvm/test/CodeGen/X86/kcfi.ll`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描.

### INV-KCFI-B03 — aarch64 检查序列

- **statement**: aarch64 lowering: `ldur w16, [<target>, #-4]; ldr w17, =TYPEID; cmp w16, w17; b.ne 1f; blr <target>; 1: brk #0x8220` (等价 `ud2`). brk immediate 决定 si_code.
- **compiler**: LLVM/Clang
- **target**: aarch64
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/Target/AArch64/AArch64KCFIPass.cpp`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描.

## 3. 插入位置

### INV-KCFI-P01 — 所有 address-taken 函数前置 type-id

- **statement**: 编译器在每个 address-taken 函数 (含全局符号) 前插 type-id. 仅被直接 call 的 static 函数无 type-id 前缀.
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/CodeGen/AsmPrinter/AsmPrinter.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描.

### INV-KCFI-P02 — 间接 call 前必须 type check

- **statement**: 所有间接 call/blr 必须前置检查序列. 编译器视所有 call expression 为 indirect 当 callee 不是 const-known function. `cfi_unchecked_callee` 属性可豁免.
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `clang/lib/CodeGen/CGCall.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 函数指针调用 seed.

## 4. 属性

### INV-KCFI-A01 — `__nocfi` (内核) / `no_sanitize("kcfi")` (用户态)

- **statement**: Linux 内核宏 `__nocfi` (`__attribute__((no_sanitize("kcfi")))`) 关闭函数级 KCFI. 用于必须接受 non-CFI 函数指针的回调路径 (boot 早期 / firmware shim).
- **compiler**: LLVM/Clang
- **source_kind**: user-doc + source
- **source_url_or_path**: Linux `include/linux/compiler-clang.h` (`__nocfi`)
- **version_sensitivity**: stable
- **oracle_mapping**: 内核 / 用户函数级测试.

### INV-KCFI-A02 — `cfi_unchecked_callee` 函数指针豁免

- **statement**: 与 CFI 同, 函数指针属性 `cfi_unchecked_callee` 让间接 call 不做 KCFI 检查.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html
- **version_sensitivity**: stable
- **oracle_mapping**: seed.

## 5. ELF 元数据

### INV-KCFI-M01 — 无标准 GNU property bit

- **statement**: KCFI 不引入 ELF property bit; 所有信息以函数 prologue 内的 type-id 字节 + 调用点检查序列直接编码到 `.text`. 无需 ld.so 协调.
- **runtime**: 不适用
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描即可.

## 6. 运行时语义

### INV-KCFI-R01 — KCFI 失败信号

- **statement**: 用户态 KCFI 失败 -> `ud2` -> `SIGILL` + `si_code = ILL_ILLOPN`. 内核 KCFI 失败由 fault handler `do_cfi_check_failed` 接管, 默认 panic (`bug_at_cfi_check_failed`); 可配置 oops.
- **runtime**: Linux kernel + ud2 trap
- **source_kind**: source
- **source_url_or_path**: Linux `kernel/cfi.c` ; `arch/x86/kernel/traps.c`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 用户态 oracle 看 `SIGILL`; 内核 oracle 看 `oops/panic` 输出.

### INV-KCFI-R02 — FineIBT (x86) 与 KCFI 的合作

- **statement**: x86_64 FineIBT (Linux 6.2+) 在 IBT `endbr` 后追加 KCFI 风格的 type-id 检查. 实际 prologue 是 `endbr64; cmp $TYPEID, ...; jne ud2;`. KCFI 在 FineIBT 启用下与 IBT 合并, 提供 *硬件 + 类型化* 的双层防御.
- **runtime**: Linux kernel
- **version**: Linux ≥ 6.2 (x86 FineIBT)
- **source_kind**: source + RFC
- **source_url_or_path**: Linux `arch/x86/kernel/alternative.c` (`apply_fineibt`) ; LLVM Discourse FineIBT
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 内核 FineIBT 启用下行为变化.

## 7. 与其他机制交互

### INV-KCFI-I01 — KCFI 与 IBT/BTI 不互斥

- **statement**: KCFI 是软件类型检查, IBT/BTI 硬件 landing pad. 同启提供深度防御. FineIBT 是合并方案.
- **compiler**: LLVM/Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-KCFI-I02 — KCFI 与 PAC ret-signing / SHSTK 正交

- **statement**: KCFI 仅检查前向边间接调用, 不影响反向边. PAC / SHSTK / GCS 仍负责返回边.
- **compiler**: LLVM/Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-KCFI-I03 — KCFI 与 LTO

- **statement**: KCFI 不需 LTO, 但 LTO 启用下编译器可移除部分冗余 type check. 二者兼容.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/ControlFlowIntegrity.html
- **version_sensitivity**: stable
- **oracle_mapping**: LTO 矩阵.

### INV-KCFI-I04 — KCFI 在 Linux 与 user-mode 一致

- **statement**: 用户态 KCFI 与 Linux 内核 KCFI 字节模式一致, type-id hash 算法相同. 这意味着相同 seed (cross-build) 在用户态可调试, 部署到内核行为一致.
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: Linux + Clang 文档
- **version_sensitivity**: stable
- **oracle_mapping**: 用户态 oracle 作内核 oracle 的代理.

## 8. 验证与已知回归

### INV-KCFI-VER-EARLY-CLANG-15 — 早期 Clang 实现差异

- **statement**: Clang 15 的 `-fsanitize=kcfi` 是早期实验, ABI 与 Clang 16+ 不兼容 (字节布局变更). 内核构建必须用 Clang 16+.
- **compiler**: LLVM/Clang
- **version**: 稳定于 Clang 16+
- **source_kind**: source
- **source_url_or_path**: LLVM commit log
- **version_sensitivity**: stable since 16
- **oracle_mapping**: 老 Clang 反例.

### INV-KCFI-VER-FINEIBT-MERGE — Linux 6.2 FineIBT merge 历史

- **statement**: Linux 6.2 合并 FineIBT, 把 IBT + KCFI 合并为单一硬化路径. 6.2 之前 KCFI 与 IBT 是独立检查序列, 性能开销大.
- **runtime**: Linux kernel
- **version**: Linux ≥ 6.2
- **source_kind**: mailing-list
- **source_url_or_path**: Linux LKML "FineIBT" 主题
- **version_sensitivity**: stable
- **oracle_mapping**: 老内核 vs 新内核行为差异.

## 9. DeFuzz KCFI Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-KCFI-P02 | `SIGILL` (用户态) / oops (内核) | 函数指针指向类型不匹配函数 |
| INV-KCFI-A01 | 不报错 | `__nocfi` 函数指针 |
| INV-KCFI-R02 | endbr + 同时 type 检查 | FineIBT 启用下间接调用 |
| INV-KCFI-VER-EARLY-CLANG-15 | 老 Clang 不兼容 | Clang 15 vs 16 ABI |

## 10. 开放问题

- **type-id hash 算法稳定性**: Clang 主版本间 hash 计算细节是否不变? 跨版本混编是否破坏?
- **JIT / kernel module 加载**: 动态加载 .ko 时 KCFI 字节是否仍生效? (内核 `do_init_module` 路径需保留 type-id.)
- **KCFI 与 Rust kernel modules**: Rust 函数的 KCFI type-id 计算细节, 待补.
- **AArch64 KCFI prologue 与 BTI/PAC 顺序**: type-id, BTI c, PACIASP 三者排列, 待文档化.
- **RISC-V KCFI**: 是否已上游 mainline kernel? 待跟踪.

## 11. 使用建议

- 内核构建用 Clang 16+ + 6.1+ 内核 + `CONFIG_CFI_CLANG=y`. x86 加 FineIBT 提升性能.
- 用户态 KCFI 适合 *不能 LTO* 的项目 (例如插件架构), 作为弱化 CFI.
- `likely-to-drift` invariant (INV-KCFI-B01-B03 字节模式, INV-KCFI-R02 FineIBT) 在每次 Clang/内核 major 升级 audit.
- 静态扫描函数前缀 type-id 字节 + 调用点 type check 可作 CI 前置.
