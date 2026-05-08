# ShadowCallStack (SCS) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / LLVM/Clang / Arm AAPCS64 / RISC-V psABI / Linux kernel / compiler-rt 中与 **ShadowCallStack (SCS)** 直接相关的 invariants 抽取归类, 作为 DeFuzz SCS oracle 的形式化依据.
>
> 机制简写与 survey 一致: **SCS** = ShadowCallStack. AArch64 主推; RISC-V 软件实现 + 硬件 Zicfiss; x86_64 历史曾支持但 LLVM 9.0 移除. AArch64 GCS 是 SCS 的硬件版, 见 `@/home/yall/project/de-fuzz/docs/invariants/gcs.md`.

## 0. 术语与坐标

- **SCS**: 把每个函数的 *返回地址* 同时存到一块独立的 "影子栈" 内存; epilogue 从影子栈而不是 LR 还原返回地址. 缓冲区溢出污染常规 LR 不影响影子栈, 因为影子栈寄存器与栈布局解耦.
- **shadow stack pointer reg**:
  - AArch64: `x18` (platform reserved, AAPCS64 已规定可挪用)
  - RISC-V (软实现): `gp / x3` (要求链接器 `--no-relax-gp`)
  - RISC-V (硬实现 Zicfiss): 专用 `ssp`
  - x86_64: 历史曾用 `gs` 段, LLVM 9.0 移除该 backend
- **scs push / scs pop**:
  - AArch64 prologue: `str x30, [x18], #8` (post-index, 把 LR 写入 ssp 然后递增)
  - AArch64 epilogue: `ldr x30, [x18, #-8]!` (pre-index, 递减后读)
- **leaf 函数**: 不调用其他函数的函数, 不需保存 LR, SCS 不必入栈.
- **runtime guard region**: SCS 影子栈段必须由 runtime 在线程创建时分配, 通常 mmap'ed 16KB / 32KB, 前后用 PROT_NONE guard 防越界.

每条 invariant 字段同前.

## 1. 启用条件 (Enablement)

### INV-SCS-E01 — Clang `-fsanitize=shadow-call-stack`

- **statement**: Clang 选项 `-fsanitize=shadow-call-stack` 启用 SCS, 并自动追加 `-ffixed-x18` (AArch64) 或 `-ffixed-x3` (RISC-V 软). 缺少 `-ffixed-*` 时 Clang 编译期 *报错*, 因为 SCS 必须保留专用寄存器.
- **compiler**: LLVM/Clang
- **version**: Clang 7+
- **target**: aarch64, riscv32/64
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/ShadowCallStack.html ; `clang/lib/Driver/ToolChains/Arch/AArch64.cpp`
- **evidence_snippet**: Clang docs: *"SCS works by saving a function's return address ... in a separately allocated 'shadow call stack' ... AArch64 currently uses x18 as the shadow call stack pointer"*.
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz CFLAGS 矩阵正反控组; oracle 抓 `str x30, [x18], #8` / `ldr x30, [x18, #-8]!` 字节模式.

### INV-SCS-E02 — GCC `-fsanitize=shadow-call-stack` 与 `-ffixed-x18`

- **statement**: GCC 同样支持 `-fsanitize=shadow-call-stack`, 在 AArch64 上必须配 `-ffixed-x18` 否则报错: *"shadow call stack requires `-ffixed-x18`"*.
- **compiler**: GCC
- **version**: GCC 12+
- **target**: aarch64
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/AArch64-Options.html ; `gcc/config/aarch64/aarch64.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: GCC 编译反例: 不带 `-ffixed-x18` -> 编译失败.

### INV-SCS-E03 — x86_64 SCS 已被 LLVM 9.0 移除

- **statement**: 早期 LLVM (< 9) 在 x86_64 提供 SCS, 用 `gs:0x...` 段存储影子栈. 因性能与与 SHSTK 重叠, LLVM 9.0 删除该 backend. 当前 x86_64 路径不可用 SCS, 替代为 SHSTK.
- **compiler**: LLVM/Clang
- **version**: 移除于 LLVM 9.0
- **target**: x86_64 (历史)
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/ShadowCallStack.html (历史说明) ; LLVM commit log
- **version_sensitivity**: stable since removal
- **oracle_mapping**: x86_64 不应启 SCS; 反例编译应失败.

### INV-SCS-E04 — RISC-V 软 SCS 需 `--no-relax-gp`

- **statement**: RISC-V 软 SCS 用 `gp/x3` 当 ssp; 但 `gp` 历史用作 linker relaxation (PC-relative -> gp-relative 缩短指令). 启用 SCS 时必须告诉链接器 `--no-relax-gp`, 否则 link-time relaxation 会破坏 SCS 寄存器使用.
- **compiler + linker**: LLVM/Clang + lld / binutils
- **version**: Clang 11+
- **target**: riscv32/64
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/ShadowCallStack.html
- **version_sensitivity**: stable
- **oracle_mapping**: 反例: 无 `--no-relax-gp` 时构建失败或运行错误.

## 2. 指令编码 (Instruction Patterns)

### INV-SCS-B01 — AArch64 prologue: `str x30, [x18], #8`

- **statement**: 非 leaf 函数 prologue 在保存 LR 前发 `str x30, [x18], #8` (post-index, write LR 到 [x18], 再 x18 += 8). 字节编码 `0x9100xxxx` 形式, 具体 `0x91 0x40 0x00 0xF8` (LE).
- **compiler**: GCC, LLVM/Clang
- **target**: aarch64
- **source_kind**: source + test
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc` (`scs_push`) ; `llvm/lib/Target/AArch64/AArch64FrameLowering.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 静态扫描.

### INV-SCS-B02 — AArch64 epilogue: `ldr x30, [x18, #-8]!`

- **statement**: 非 leaf 函数 epilogue 发 `ldr x30, [x18, #-8]!` (pre-index, x18 -= 8, 再读 LR). 与 prologue 对称.
- **compiler**: GCC, LLVM/Clang
- **target**: aarch64
- **source_kind**: source + test
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc` (`scs_pop`) ; `llvm/lib/Target/AArch64/AArch64FrameLowering.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描.

### INV-SCS-B03 — RISC-V 软 SCS prologue/epilogue

- **statement**: 非 leaf 函数 prologue: `sd ra, 0(gp); addi gp, gp, 8`; epilogue: `addi gp, gp, -8; ld ra, 0(gp)`. 编码取决于 RV32/RV64. Clang 通过 `RISCVFrameLowering` 插入.
- **compiler**: LLVM/Clang
- **target**: riscv32/64
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/Target/RISCV/RISCVFrameLowering.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描.

## 3. 栈帧 / 寄存器约束

### INV-SCS-F01 — leaf 函数不入 SCS (LR 不保存)

- **statement**: leaf 函数不写 LR 到普通栈, 也不入 SCS. 缓冲区溢出仍可改 LR, 但 leaf 函数 `RET` 直接用 LR (寄存器), 没有"从内存还原"动作, 因此 *缓冲区溢出 LR 改不了 leaf 函数返回*. SCS 对 leaf 不增减安全保证.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: 设计 + Clang docs
- **source_url_or_path**: https://clang.llvm.org/docs/ShadowCallStack.html
- **version_sensitivity**: stable
- **oracle_mapping**: leaf 函数 + LR 覆盖 seed 应不被 SCS 拦截 (但因 leaf 不读 LR 也不会被攻击).

### INV-SCS-F02 — `x18` 在 AArch64 必须 reserved

- **statement**: SCS 启用下 GCC/Clang 通过 `-ffixed-x18` 把 `x18` 从分配池排除. 链接的 *所有* 静态库 / DSO 必须不使用 x18 (即都得带 `-ffixed-x18` 编译). Apple/Android 平台已默认 reserved x18, Linux 通用平台需显式. 用户库若汇编或 inline-asm 用 x18 -> SCS 内容损坏.
- **compiler**: GCC, LLVM/Clang
- **target**: aarch64
- **source_kind**: source + ABI
- **source_url_or_path**: AAPCS64 §5.1.1 ; Clang docs
- **version_sensitivity**: stable
- **oracle_mapping**: 反例: 含 inline asm 用 x18 的 seed -> SCS 损坏后 ret 错误.

### INV-SCS-F03 — RISC-V `gp` 在 SCS 启用下不可作 GOT base

- **statement**: 软 SCS 把 gp 占用; 链接器 relaxation 不可使用 gp 作为 PC-relative 缩短. 通过 `--no-relax-gp` 关.
- **linker**: lld, binutils
- **target**: riscv32/64
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/ShadowCallStack.html
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

### INV-SCS-F04 — `setjmp` / `longjmp` 与 SCS

- **statement**: `setjmp` 必须保存 ssp (x18 / gp 当前值), `longjmp` 恢复. glibc / musl 在 SCS-aware 构建时把 ssp 加入 `jmp_buf`. 否则 longjmp 后 ret 与影子栈不对齐.
- **runtime**: glibc, musl
- **source_kind**: source
- **source_url_or_path**: glibc `sysdeps/aarch64/setjmp.S` (SCS-aware 路径)
- **version_sensitivity**: target-specific
- **oracle_mapping**: setjmp/longjmp seed; 老 glibc 不支持 SCS 的 jmp_buf, oracle 跑前确认 glibc 版本.

### INV-SCS-F05 — 异常 unwind 跳过 SCS

- **statement**: C++ 异常 unwind 通过 DWARF / `__builtin_eh_return` 直接跳到 catch, 不走普通 epilogue, 因此 *不会* 自动 pop SCS. libgcc / libunwind 必须在 unwind 过程中调整 ssp. 若 unwinder 不感知 SCS, 影子栈会泄漏每异常一帧, 最终影子栈耗尽 -> guard page fault.
- **runtime**: libgcc, libunwind
- **source_kind**: source
- **source_url_or_path**: libgcc `unwind-dw2.c` (SCS-aware 补丁)
- **version_sensitivity**: target-specific
- **oracle_mapping**: 频繁 throw seed, 期望长跑后影子栈不耗尽.

## 4. 属性 / 局部控制

### INV-SCS-A01 — `no_sanitize("shadow-call-stack")` 函数级关停

- **statement**: Clang 函数属性 `__attribute__((no_sanitize("shadow-call-stack")))` 关闭该函数的 SCS 插桩. 但 *寄存器仍被 reserved*, 因此该函数即便不入栈也不能用 x18.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html
- **version_sensitivity**: stable
- **oracle_mapping**: 函数级测试 seed.

### INV-SCS-A02 — `naked` 函数不发 SCS

- **statement**: `naked` 函数无 prologue, 因此不入 SCS. 用户责任手写汇编保持 SCS 协议.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: naked + 间接调用 seed.

## 5. ELF 元数据

### INV-SCS-M01 — 无专用 GNU property bit

- **statement**: SCS 没有 ELF property bit (与 BTI / PAC 不同). 链接器无法自动检查所有对象都启用 SCS; 必须靠构建系统保证.
- **compiler + linker**: 不适用
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: CI 通过 grep `-ffixed-x18` 在 build flags 中强制.

## 6. 运行时语义

### INV-SCS-R01 — SCS / LR 不一致 -> 间接 SIGSEGV / 异常

- **statement**: SCS 启用下, 如果攻击者改了 *普通栈* 上的 LR 副本但没改影子栈, epilogue `ldr x30, [x18, #-8]!` 还原原始 LR, `ret` 跳回正常路径, *攻击失效但无信号*. 攻击者若改了影子栈而非常规 LR, 影子栈 ssp 越界访问 guard page -> `SIGSEGV`. 因此 SCS 的 oracle 信号 *弱*: 通常表现为 "攻击失败 + 程序正常运行", 不像 SHSTK 直接 `#CP`. 仅在影子栈被破坏时才能直接观察.
- **runtime**: Linux kernel
- **source_kind**: 设计
- **source_url_or_path**: https://clang.llvm.org/docs/ShadowCallStack.html
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 重点是 *差分*: 同一 seed 在 SCS / 无 SCS 下行为差异 (覆盖 LR 攻击成功率).

### INV-SCS-R02 — runtime 必须分配影子栈区域

- **statement**: SCS 假设 runtime (glibc / bionic / musl / pthread library) 在线程创建时为每线程分配影子栈, 把 ssp (x18/gp) 初始化为该区域起始. 若 runtime 不感知 SCS, x18 = 0 时第一次 `str x30, [x18], #8` 触发 SIGSEGV.
- **runtime**: glibc, bionic, musl
- **version**: bionic (Android) 默认; glibc 部分支持; musl 不支持
- **source_kind**: source
- **source_url_or_path**: bionic `bionic/pthread_create.cpp` ; glibc 提案
- **version_sensitivity**: target-specific
- **oracle_mapping**: oracle 启动前验证 runtime 已分配.

### INV-SCS-R03 — `jmp_buf` 仅存 ssp 低位以避影子栈地址泄漏

- **statement**: 因影子栈基址是攻击者 *不应* 知道的秘密, `jmp_buf` 只保存 ssp 的低 N 位 (offset within 影子栈), 高位由 runtime 填充. 这避免 `jmp_buf` 泄漏到只读位置 (e.g. core dump) 后影子栈位置暴露.
- **runtime**: glibc, bionic
- **source_kind**: 设计
- **source_url_or_path**: https://clang.llvm.org/docs/ShadowCallStack.html
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明; oracle 不直接验证.

### INV-SCS-R04 — 影子栈耗尽 -> guard page fault

- **statement**: 影子栈区域两端各有 PROT_NONE guard page, 越界 (递归过深 / unwind 失败) 触发 `SIGSEGV`. 这是 SCS 唯一的 hard fail 信号.
- **runtime**: pthread library
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 深递归 seed.

## 7. 与其他机制的交互

### INV-SCS-I01 — SCS 与 PAC ret-signing 互补

- **statement**: PAC 在 LR 上加密签, SCS 把 LR 复制到隔离区. 二者都是反向 CFI 但路径完全不同, 同时启用提供更深防御 (攻击者必须同时绕 PAC 验证 *和* 影子栈).
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/pointer-authentication.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-SCS-I02 — SCS 与 GCS (硬件) 同概念互斥

- **statement**: GCS 是 Armv9.4 硬件影子栈, 启用 GCS 后软件 SCS 多余. Linux 通常优先用 GCS (硬件加速); 软 SCS 仅在不支持 GCS 的旧 CPU 上.
- **compiler**: GCC 14+, Clang 18+
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/gcs.md`
- **version_sensitivity**: target-specific
- **oracle_mapping**: 矩阵.

### INV-SCS-I03 — SCS 与 BTI 完全相容

- **statement**: BTI 影响间接分支目标, 不改 LR 路径; 与 SCS 正交.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-SCS-I04 — Linux 内核 SCS

- **statement**: Linux 内核 AArch64 自 5.7+ 支持 `CONFIG_SHADOW_CALL_STACK`, 用 x18 作 ssp, 与用户态隔离. 内核 SCS 对应回归 invariant 由 Linux self-test 提供.
- **target-specific**: Linux kernel
- **version**: Linux ≥ 5.7
- **source_kind**: source
- **source_url_or_path**: Linux `Documentation/security/self-protection.rst` ; `arch/arm64/Kconfig`
- **version_sensitivity**: stable
- **oracle_mapping**: 内核 seed.

## 8. 验证与已知回归

### INV-SCS-VER-X18-INLINE-ASM — 用户 inline asm 误用 x18

- **statement**: 历史教训: Linux 内核早期某些 inline asm 误用 x18, 导致 SCS 损坏. 修复需在 `arch/arm64` 全面 audit. 用户态库同理.
- **target-specific**: 内核 + 用户库
- **source_kind**: source
- **source_url_or_path**: Linux git log `arch/arm64` SCS 主题
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描 inline asm 用 x18.

### INV-SCS-VER-LLVM-D44343 — Clang AArch64 SCS 引入

- **statement**: LLVM D44343 (Clang 7) 引入 AArch64 SCS, 早期实现存在 `-ffixed-x18` 默认未自动设置等回归.
- **compiler**: LLVM/Clang
- **version**: Clang 7+
- **source_kind**: mailing-list
- **source_url_or_path**: https://reviews.llvm.org/D44343
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 Clang seed.

### INV-SCS-VER-X86-REMOVAL — LLVM 9.0 移除 x86_64 SCS

- **statement**: LLVM 9.0 移除 x86_64 SCS backend; 之后所有 x86 安全保证转向 SHSTK. 跨版本 SCS oracle 必须按 LLVM 版本分支.
- **compiler**: LLVM/Clang
- **version**: 移除于 LLVM 9.0
- **source_kind**: source
- **source_url_or_path**: LLVM commit log
- **version_sensitivity**: stable since removal
- **oracle_mapping**: 文档说明.

## 9. DeFuzz SCS Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-SCS-R01 | 攻击成功率差分: SCS 关时改 LR 成功, SCS 开时无影响 | 缓冲区溢出 LR + 测量 RIP 控制 |
| INV-SCS-R04 | `SIGSEGV` (guard page) | 极深递归 |
| INV-SCS-F02 | 影子栈被破坏 -> ret 跳错 | inline asm 写 x18 |
| INV-SCS-F04 | longjmp 后 ret 错乱 | 老 glibc + setjmp/longjmp |
| INV-SCS-E01 | 编译失败 | Clang `-fsanitize=scs` 无 `-ffixed-x18` |

## 10. 开放问题

- **glibc 用户态 SCS 支持**: 主线 glibc 至今未默认支持 SCS; Android bionic 是事实上的标准. 待补 glibc 路线图.
- **跨 DSO SCS**: 一个 DSO 用 SCS, 另一不用, 调用 trampoline 如何处理 ssp?
- **debugger / coredump**: gdb 等调试器对影子栈的可见性, 是否会泄漏地址?
- **JIT**: V8 / LuaJIT 自管 LR 行为, 与 SCS 兼容性需逐引擎核对.
- **编译器异常 unwinder 的 SCS 感知**: libgcc / libunwind 在 SCS 启用下的 `_Unwind_RaiseException` 路径完整性, 待补回归 invariant.

## 11. 使用建议

- 优先 AArch64 + Android (bionic) 平台跑 SCS oracle, Linux glibc 路径不完整.
- CI 强制构建系统检 `-ffixed-x18` 全局存在.
- `likely-to-drift` invariant (INV-SCS-VER-X18-INLINE-ASM) 在每次内核 / 关键 C 库版本升级 audit.
- 跨编译器对照: GCC 12+ / Clang 7+ 行为应一致, 老版本预期不一致.
