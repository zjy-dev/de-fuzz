# Stack Canary (Stack Protector) Invariants

> 本文从一手资料 (GCC / LLVM/Clang / glibc / 各 ISA ABI 文档 / CVE 通告) 抽取与 **stack canary** 直接相关的不变量, 形成 oracle 设计的形式化依据.
>
> 机制简写: **SP** = Stack Protector / canary.

## 0. 术语与坐标

- **canary slot**: 函数栈帧里存放 guard 值的槽.
- **guard value / `__stack_chk_guard`**: 进入函数时写入 canary slot 的秘密值.
- **saved registers**: 被 callee 保存的寄存器区域, 典型包括 LR/FP (AArch64) 或 return address (x86 的 retaddr 槽).
- **spill area**: 编译器为跨调用活跃值分配的栈槽.
- **vulnerable object**: 可被溢出写的对象 (字符数组、`alloca`、VLA、聚合体含数组、取地址的自动变量等).
- **phase 1/2 分类**: 见 GCC `cfgexpand.cc::stack_protect_decl_phase` —— 决定 canary 冲突图中哪些对象必须排在 canary 的溢出一侧.

每条不变量按 [`README.md` §2](./README.md#2-survey-字段约定) 的字段记录: `ID / statement / compiler / version / target / source_kind / source_url_or_path / evidence_snippet / version_sensitivity / observation`.

## 1. 静态前提 (Static Preconditions)

### INV-SP-E01 — 三档 flag 的保护面

- **statement**: `-fstack-protector` 只对含 `alloca` 或 ≥8 字节字符 buffer 的函数插 canary; `-fstack-protector-strong` 扩到 "取地址 local / 聚合体含数组 / 使用 `__builtin_frame_address`" 等更多启发式; `-fstack-protector-all` 无差别插桩; `-fstack-protector-explicit` 仅对带 `stack_protect` 属性的函数插桩.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 4.9+ (含 16.x), Clang 6+
- **target**: generic
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html ; `gcc/cfgexpand.cc` ; `llvm/lib/CodeGen/StackProtector.cpp`
- **evidence_snippet**: GCC 手册 "functions that call `alloca`, and functions with buffers larger than or equal to 8 bytes" / "Only variables that are actually allocated on the stack are considered, optimized away variables or variables allocated in registers don't count."
- **version_sensitivity**: stable (启发式细节 likely-to-drift)
- **observation**: 同一函数在不同 flag 档位下编译产物里是否引用 `__stack_chk_fail` / `__stack_chk_guard`, 与文档启发式应一致.

### INV-SP-E02 — `-fhardened` 隐式启用 SP-strong

- **statement**: GCC `-fhardened` (Linux) 隐式开启 `-D_FORTIFY_SOURCE=3` (glibc <2.35 时回退为 `=2`)、`-D_GLIBCXX_ASSERTIONS`、`-ftrivial-auto-var-init=zero`、`-fPIE -pie -Wl,-z,relro,-z,now`、`-fstack-protector-strong`、`-fstack-clash-protection`, x86 GNU/Linux 上还有 `-fcf-protection=full`. `-fhardened` **只对未在命令行显式给定**的选项生效, 因此显式 `-fstack-protector` (普通档) 会**抑制**升级为 strong.
- **compiler**: GCC
- **version**: GCC 14+
- **target**: generic (Linux)
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **observation**: `gcc -fhardened --help=hardened` 列出当前实际启用项; 编译产物中应能观测到 `__stack_chk_fail` 引用与 strong 档位的启发式行为一致.

### INV-SP-E03 — 属性可覆盖全局开关

- **statement**: 函数属性 `__attribute__((no_stack_protector))` 关闭该函数 SP; `stack_protect` 强制插 canary. Clang `safebuffers` 等价于前者. 这些属性与全局 flag 组合时函数级优先.
- **compiler**: GCC, Clang
- **version**: GCC 11+, Clang 4+
- **target**: generic
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html ; https://clang.llvm.org/docs/AttributeReference.html
- **version_sensitivity**: stable
- **observation**: 函数体反汇编里是否存在 guard load / check 序列, 应严格遵循属性优先.

## 2. 启发式 (Heuristic) — 哪些变量算 "vulnerable"

### INV-SP-H01 — GCC 启发式分类位

- **statement**: `cfgexpand.cc` 的 `stack_protect_classify_type` 给每个自动变量打分类位 (`SPCT_HAS_LARGE_CHAR_ARRAY` ≥8 字节字符数组、`SPCT_HAS_SMALL_CHAR_ARRAY`、`SPCT_HAS_ARRAY` 任意数组、`SPCT_HAS_AGGREGATE` 含数组聚合体、`SPCT_HAS_FN_FRAME_ADDRESS` 被 `__builtin_frame_address` 触及), 进入 canary 冲突图.
- **compiler**: GCC
- **version**: GCC 4.9+
- **target**: generic
- **source_kind**: source
- **source_url_or_path**: https://github.com/gcc-mirror/gcc/blob/master/gcc/cfgexpand.cc (`stack_protect_classify_type`, `stack_protect_decl_phase`, `add_stack_protection_conflicts`)
- **version_sensitivity**: likely-to-drift
- **observation**: 分类位本身没有二进制可观测形式; 但其结果 — 是否插桩、保护面对象顺序 — 能在反汇编里间接观测.

### INV-SP-H02 — `-fstack-protector-strong` 的额外触发条件

- **statement**: 满足下列任一即插 canary: 有地址被取的自动变量、函数直接调用 `alloca` 或声明 VLA、局部聚合体中含数组、使用 `__builtin_frame_address`. 寄存器分配成功的纯 scalar / 被优化掉的变量**不**触发.
- **compiler**: GCC, LLVM/Clang (等价但不一一对应)
- **version**: GCC 4.9+, Clang 6+
- **target**: generic
- **source_kind**: source + user-doc
- **source_url_or_path**: GCC 手册 Instrumentation Options; `gcc/cfgexpand.cc`; `llvm/lib/CodeGen/StackProtector.cpp::StackProtector::RequiresStackProtector`
- **evidence_snippet**: "optimized-away or register-allocated variables are not protected".
- **version_sensitivity**: stable at doc level, likely-to-drift at impl level
- **observation**: 同一源代码在 `-fstack-protector-strong` 与 `-fstack-protector` 之间编译, 是否插桩应严格匹配上述条件清单.

### INV-SP-H03 — VLA / `alloca` 必须触发 SP

- **statement**: 含 VLA 或 `__builtin_alloca` 的函数, 在 `-fstack-protector{,-strong,-all,-explicit}` 任一非 off 档位下都会被插 canary —— `alloca` 出现在所有四档启发式里.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 4.9+, Clang 6+
- **target**: generic
- **source_kind**: user-doc + test
- **source_url_or_path**: GCC 手册 Instrumentation Options; `gcc/testsuite/gcc.dg/ssp-*.c`, `fstack-protector-strong.c`
- **version_sensitivity**: stable
- **observation**: 含 VLA/`alloca` 的函数在 SP 启用档位下, 编译产物必须引用 `__stack_chk_fail`.

## 3. 栈帧布局 (Frame Layout)

### INV-SP-L01 — Canary slot 必须夹在 vulnerable locals 与 saved registers / return address 之间

- **statement**: 对栈下行架构, canary 位于所有 vulnerable 自动对象 与 所有 saved registers / return address 之间, 使得任何"从 vulnerable 对象顺序溢出到 saved regs / retaddr"的路径必然先覆盖 canary.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: generic (x86_64, aarch64 已验证; 其他栈下行 ISA 同理)
- **source_kind**: source + paper
- **source_url_or_path**: `gcc/cfgexpand.cc` (`add_stack_protection_conflicts`, `expand_stack_vars`); `gcc/config/aarch64/aarch64.cc` 注释; `llvm/lib/CodeGen/StackProtector.cpp` 顶部注释
- **evidence_snippet**: aarch64.cc: *"When using stack smash protection, make sure that the canary slot comes between the locals and the saved registers. Otherwise, it would be possible for a carefully sized smash attack to change the saved registers (particularly LR and FP) without reaching the canary."*
- **version_sensitivity**: stable
- **observation**: 顺序覆盖 vulnerable 对象的越界写: 若先触发 canary check 失败 (进程被 SP 运行时终止), 不变量满足; 若直接修改 saved regs / retaddr 而 canary 未触发, 不变量被违反.

### INV-SP-L02 — AArch64: 启用 SP 时 saved registers 必须置于 locals 之上

- **statement**: AArch64 backend 在 `crtl->stack_protect_guard` 为真时 `aarch64_save_regs_above_locals_p` 返回 true, 强制使用"saved regs / LR / FP / SVE 寄存器位于 locals 与 canary 之上"的布局, canary (作为最上方 local) 因此夹在 locals 与 saved regs 之间.
- **compiler**: GCC
- **version**: GCC 14+ (CVE-2023-4039 修复后; GCC ≤13.2 存在相反布局缺陷)
- **target**: aarch64
- **source_kind**: source + bug-disclosure
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc` `aarch64_save_regs_above_locals_p`; https://gcc.gnu.org/pipermail/gcc-patches/2023-September/630054.html
- **version_sensitivity**: stable since fix
- **observation**: 反汇编 prologue 观察 saved regs 的相对位置; GCC ≤13.2 与 14+ 在含 VLA 的函数上布局不同.

### INV-SP-L03 — 动态分配 (VLA/`alloca`) 与 canary 的相对位置

- **statement**: 动态分配区域必须位于 canary 的 "栈低端" 侧, 不能插在 canary 与 saved regs 之间; 否则可构造大小恰好的 dynamic alloc 越过 canary 直达 LR/FP (CVE-2023-4039 模型).
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 14+, LLVM 17+
- **target**: aarch64 (主要), x86_64 (对称)
- **source_kind**: bug-disclosure + source
- **source_url_or_path**: https://rtx.meta.security/mitigation/2023/09/12/CVE-2023-4039.html ; https://gcc.gnu.org/pipermail/gcc-patches/2023-September/630054.html ; `gcc/config/aarch64/aarch64.cc`
- **version_sensitivity**: stable since fix
- **observation**: 在 VLA/`alloca` 函数里通过越界写, 若 canary 未先被覆盖即触达 LR/FP, 不变量被违反.

### INV-SP-L04 — Spill / 参数副本不得落在 canary 保护范围外

- **statement**: 跨调用活跃的参数副本、寄存器 spill 槽, 若被放在 "VLA/`alloca` 之上且在 canary 之下", 则小规模 VLA 溢出可先破坏这些副本, 再被后续 `memset`/`memcpy` 放大成覆盖 retaddr 的大溢出, 且崩溃可能发生在 canary check 之前. 注意此条属于 hardening-ideal —— 文档将其与硬安全契约区分开, 不直接当成经典 canary 绕过.
- **compiler**: GCC, LLVM/Clang
- **version**: optimization-dependent
- **target**: generic (aarch64 上最易观察)
- **source_kind**: internals + ABI-spec
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gccint/Stack-and-Calling.html ; `gcc/config/aarch64/aarch64.cc`
- **version_sensitivity**: likely-to-drift (依赖 RA 策略)
- **observation**: VLA 函数的越界写可能引发非"返回时"的间接崩溃 (函数体内 size 参数被污染). 现象与 L01 重叠, 区分需要更细的崩溃位置信息.

### INV-SP-L05 — 多个 vulnerable 对象共享同一 canary 保护面

- **statement**: `add_stack_protection_conflicts` 让所有 `phase==1/2` 的对象与 canary slot 形成冲突图, 强制它们都排在 canary 的 "溢出源" 一侧; 因此一个函数内多个 char array / `alloca` / VLA 仍由同一 canary 保护.
- **compiler**: GCC
- **version**: GCC 4.9+
- **target**: generic
- **source_kind**: source
- **source_url_or_path**: `gcc/cfgexpand.cc::add_stack_protection_conflicts`
- **version_sensitivity**: stable
- **observation**: 含混合 vulnerable 对象 (fixed buffer + VLA + ...) 的函数, 任一对象的越界写均应触发同一 canary check.

## 4. 寄存器与调用约定 (Register / Calling Convention)

### INV-SP-R01 — Canary guard 不驻留 caller-saved 寄存器 (跨调用区间)

- **statement**: guard 值在 prologue 从 guard 来源加载后, 若函数中存在可能破坏该寄存器的调用, 则必须立即写入 canary slot; epilogue 校验时重新从 guard 来源加载. 不允许在跨调用区间把 guard 保留在 caller-saved 寄存器中.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: generic
- **source_kind**: internals
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gccint/Stack-and-Calling.html ; `llvm/lib/CodeGen/StackProtector.cpp`
- **version_sensitivity**: stable
- **observation**: 编译器内部约束, 二进制层面没有"跨调用瞬时寄存器值"的直接观测点.

### INV-SP-R02 — 跨调用参数副本应优先 callee-saved 寄存器

- **statement**: 对于 VLA/`alloca` 函数, 若参数 (尤其是 `memset/memcpy` 的 size 参数) 跨调用活跃, 将其保存到 callee-saved 寄存器 (AArch64 `x19-x28`; x86_64 `rbx/r12-r15`) 比溢出到紧邻动态分配区的栈 spill 更安全. 这是 hardening-ideal, **不是**强不变量.
- **compiler**: GCC, LLVM/Clang
- **version**: optimization-dependent
- **target**: generic
- **source_kind**: internals + ABI-spec
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gccint/Stack-and-Calling.html ; https://github.com/ARM-software/abi-aa
- **version_sensitivity**: likely-to-drift
- **observation**: 反汇编可见参数 spill 落点; 是否构成"安全"问题依赖具体函数语境, 难自动化判定.

### INV-SP-R03 — Epilogue 必须在 return 前 clobber 持有过 canary 的寄存器

- **statement**: SP-protected 函数的 epilogue 中, 从 guard 来源 (`__stack_chk_guard` 全局符号 / `%fs:0x28` / `TPIDR_ELn + offset` 等) 加载并完成校验后、到返回指令 (`ret` / `jr $ra` / `jirl $zero, $ra, 0` 等) 之前, 任何 transient 持有过 canary 值 (从 guard 加载得到, 或 XOR 中间值) 的 GPR 必须被显式覆写. 否则函数返回后 caller-saved 寄存器中残留的 guard 值, 可被随后的寄存器暴露原语 (caller 的内联汇编 / 编译器注入的 spill / signal handler `mcontext_t` / side-channel) 读出, 攻击者据此伪造任意函数的 canary 静默绕过整个进程的 SP 保护.
  - 与 INV-SP-R01 区别: R01 约束**函数体内跨调用**期间; R03 约束**校验完成到返回**之间.
- **compiler**: GCC (主要), LLVM/Clang (对应路径需独立审计)
- **version**: 历史已修 — PR 96191 (2020) 在 aarch64/arm/thumb1 backend 引入 `stack_protect_test` define_insn 完成 scrub; GCC ≤16.1 (主线) `gcc/cfgexpand.cc::stack_protect_prologue` 与 `gcc/function.cc::stack_protect_epilogue` 的 generic fallback 仍未修, 落到 fallback 的 backend (mips / mips64 / loongarch64 / xtensa / csky / or1k / hppa / m68k / alpha / arc / nds32 / microblaze 等) 全部命中.
- **target**: 任意未提供 `targetm.have_stack_protect_set` / `targetm.have_stack_protect_test` 的 backend.
- **source_kind**: source + bug-disclosure
- **source_url_or_path**: `gcc/cfgexpand.cc::stack_protect_prologue` (generic fallback 用 `emit_move_insn`, 未 clobber source 寄存器); `gcc/function.cc::stack_protect_epilogue` (用 `emit_cmp_and_jump_insns`, 跳转后未 scrub 任何 GPR); `gcc/config/aarch64/aarch64.md` `stack_protect_test_<mode>` (参考实现, 显式清洗 temp 寄存器); https://gcc.gnu.org/bugzilla/show_bug.cgi?id=96191 ; https://gcc.gnu.org/bugzilla/show_bug.cgi?id=125045 (meta-bug)
- **evidence_snippet**: PR 96191 报告 "the secret canary value is left in registers after the function returns"; aarch64.md `stack_protect_test_<mode>` 注释把 temp 寄存器清洗标为安全关键.
- **version_sensitivity**: likely-to-drift (随每个 backend 是否新增 `stack_protect_{set,test}` define_insn 而变).
- **observation**: SP-protected 函数返回后, caller 上下文中可见的 GPR 内若仍存在 guard 值或 guard XOR 中间值, 不变量被违反. 该现象在普通越界路径下不可见 (越界仍触发 canary check), 必须独立观察寄存器残留.

## 5. Guard 值来源 (Guard Source)

### INV-SP-G01 — 默认 guard 符号

- **statement**: 默认 guard 是外部变量 `__stack_chk_guard` (类型 `ptr_type_node`); 失败处理为调用 `__stack_chk_fail` (必须 `noreturn`). 三件事由三个 target hook 决定: `TARGET_STACK_PROTECT_GUARD`, `TARGET_STACK_PROTECT_FAIL`, `TARGET_STACK_PROTECT_RUNTIME_ENABLED_P`.
- **compiler**: GCC, LLVM/Clang (符号名相同)
- **version**: all
- **target**: generic
- **source_kind**: internals
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gccint/Stack-Smashing-Protection.html ; `gcc/doc/tm.texi`
- **version_sensitivity**: stable
- **observation**: 二进制符号表中是否引用 `__stack_chk_guard` / `__stack_chk_fail`.

### INV-SP-G02 — x86_64 Linux 从 TLS 读 guard

- **statement**: x86_64 SysV: guard 从 `%fs:0x28` (TLS) 读取, 不经过 GOT. Windows x86_64: 从 TEB 偏移读取.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: x86_64 (Linux / Windows)
- **source_kind**: ABI-spec + source
- **source_url_or_path**: https://gitlab.com/x86-psABIs/x86-64-ABI ; `gcc/config/i386/i386.cc`
- **version_sensitivity**: stable
- **observation**: prologue 反汇编中是否出现 `mov %fs:0x28,%rax` (Linux) 或对应 TEB 加载.

### INV-SP-G03 — AArch64: `-mstack-protector-guard=sysreg` 使用 `TPIDR_ELn + offset`

- **statement**: AArch64 backend 的 `aarch64_stack_protect_canary_mem` 根据 `-mstack-protector-guard={global,sysreg}` 决定 guard 来自外部变量还是 `TPIDR_EL0/EL1/EL2/EL3` + 指定偏移; 布局由 `-mstack-protector-guard-reg=` / `-mstack-protector-guard-offset=` 精确指定.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 9+, Clang 8+
- **target**: aarch64
- **source_kind**: source + user-doc
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc::aarch64_stack_protect_canary_mem` ; GCC AArch64 Options 手册
- **version_sensitivity**: stable
- **observation**: prologue 反汇编里 guard 来源指令模式 (`adrp+ldr` 全局符号 vs `mrs` 系统寄存器读).

### INV-SP-G04 — `_RUNTIME_ENABLED_P` hook 的延迟判定

- **statement**: 当 `TARGET_STACK_PROTECT_RUNTIME_ENABLED_P` 返回 false, 即便启用了 `-fstack-protector*`, 也不会发射 canary —— 用于某些 freestanding / kernel 变体. 这是 SP 可被 target 层延迟关掉的唯一合法路径.
- **compiler**: GCC
- **version**: all
- **target**: target-specific
- **source_kind**: internals
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gccint/Stack-Smashing-Protection.html
- **version_sensitivity**: stable
- **observation**: freestanding 二进制中即便命令行带 `-fstack-protector*` 也可能没有 `__stack_chk_fail` 引用, 这是该 hook 合法生效的现象.

## 6. 运行时与 libc 契约 (Runtime Contract)

### INV-SP-F01 — `__stack_chk_fail` 语义

- **statement**: `__stack_chk_fail` 必须是 `noreturn`; glibc 实现通过 `__fortify_fail` / `__libc_message` 输出 "stack smashing detected" 后 `abort()`.
- **compiler + runtime**: GCC + glibc, Clang + glibc / compiler-rt
- **version**: all
- **target**: generic (Linux/POSIX)
- **source_kind**: runtime
- **source_url_or_path**: https://sourceware.org/glibc/manual/latest/html_node/Source-Fortification.html ; glibc `debug/stack_chk_fail.c`
- **version_sensitivity**: stable
- **observation**: 越界写到 canary 槽后, 进程因 `SIGABRT` 终止, stderr 含 "stack smashing detected".

### INV-SP-F02 — `__stack_chk_fail_local` 的静态链接 + PIC 要求

- **statement**: 静态链接到不导出 `__stack_chk_fail` 的 libc 或 `-fpic` 的场景下, 每个 DSO 必须内嵌 `__stack_chk_fail_local` thunk (由 `libgcc2.c` 提供), 否则链接失败.
- **compiler + runtime**: GCC + libgcc
- **version**: all
- **target**: generic
- **source_kind**: runtime
- **source_url_or_path**: `libgcc/libgcc2.c`
- **version_sensitivity**: stable
- **observation**: 链接器报 `__stack_chk_fail_local` 未定义错误是合法的配置失败信号, 不是运行时绕过.

### INV-SP-F03 — libc 无 SP 支持时必须链 libssp

- **statement**: 若目标 libc 不提供 `__stack_chk_guard` / `__stack_chk_fail` 符号, 链接器必须显式连入 GCC 的 `libssp.a` / `libssp_nonshared.a`, 否则 `-fstack-protector*` 在链接期出现未定义引用.
- **compiler + runtime**: GCC + libssp
- **version**: all
- **target**: generic (主要嵌入式 / 裸 libc 场景)
- **source_kind**: runtime
- **source_url_or_path**: https://github.com/gcc-mirror/gcc/tree/master/libssp
- **version_sensitivity**: stable
- **observation**: 链接器对 SP 符号的未定义引用错误.

### INV-SP-F04 — guard 值在 `execve` 后重新初始化

- **statement**: glibc 在每次 `execve` (及 dl 解释器初始化) 阶段重新写入 `__stack_chk_guard` (通常取 `AT_RANDOM` 的 16 字节); fork 子进程继承同一 guard 值, 因此"fork 后爆破 guard"的攻击只能依赖父进程暴露.
- **runtime**: glibc
- **version**: glibc 2.10+
- **target**: generic (Linux)
- **source_kind**: runtime
- **source_url_or_path**: glibc `csu/libc-start.c`, `sysdeps/unix/sysv/linux/dl-osinfo.h`
- **version_sensitivity**: stable
- **observation**: 同一进程内 `__stack_chk_guard` 的值在生命周期内稳定; 跨 `execve` 改变.

## 7. 属性与局部禁用 (Attributes)

### INV-SP-A01 — `no_stack_protector` 必须在函数级别彻底关闭 SP

- **statement**: `__attribute__((no_stack_protector))` 覆盖所有 `-fstack-protector{,-strong,-all}`, 该函数不插 canary, 不调 `__stack_chk_fail`.
- **compiler**: GCC, Clang
- **version**: GCC 11+, Clang 4+
- **target**: generic
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: stable
- **observation**: 反汇编中目标函数体应不含 guard load / check 序列, 也无对 `__stack_chk_fail` 的调用边.

### INV-SP-A02 — `stack_protect` 属性在 `-fstack-protector-explicit` 下必生效

- **statement**: 带 `__attribute__((stack_protect))` 的函数在 `-fstack-protector-explicit` (GCC) / `-fstack-protector` + 无 `no_stack_protector` (Clang) 下保证插入 canary, 独立于启发式.
- **compiler**: GCC, Clang
- **version**: GCC 4.9+, Clang 6+
- **target**: generic
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: stable
- **observation**: 即使函数没有任何启发式触发条件, 反汇编里仍出现完整的 guard load + check 序列.

### INV-SP-A03 — 属性与 `-fno-stack-protector` 的互斥解析

- **statement**: 全局 `-fno-stack-protector` + 函数级 `stack_protect` 属性: GCC / Clang 均保留函数级强制插桩; 全局 `-fstack-protector-*` + 函数级 `no_stack_protector`: 函数级关闭优先.
- **compiler**: GCC, Clang
- **version**: GCC 11+, Clang 4+
- **target**: generic
- **source_kind**: test
- **source_url_or_path**: `gcc/testsuite/g++.dg/no-stack-protector-attr*.C`
- **version_sensitivity**: stable
- **observation**: flag × attribute 的笛卡尔组合下, 函数体反汇编里 guard 序列的有/无应严格遵循属性优先规则.

## 8. 链接与 DSO (Linking / Cross-DSO)

### INV-SP-X01 — `__stack_chk_guard` 跨 DSO 一致

- **statement**: 所有加载到同一进程的 DSO 共享同一个 `__stack_chk_guard` (glibc 在 dl startup 写入), 因此函数在 DSO A 中设置 canary 后跨 DSO 调用返回仍能校验.
- **runtime**: glibc + ld.so
- **version**: glibc 2.10+
- **target**: generic
- **source_kind**: runtime
- **source_url_or_path**: glibc `sysdeps/unix/sysv/linux/dl-osinfo.h`
- **version_sensitivity**: stable
- **observation**: 多 DSO 进程内, 任一 DSO 读取 `__stack_chk_guard` 得到相同的值.

### INV-SP-X02 — `-fstack-protector-strong` 与 LTO 的联动

- **statement**: SP 的启发式决策发生在 IR 生成阶段, LTO 不改变 "该函数是否有 canary" 的结果 (与 CFI 不同). 但 inline 后内联进来的 vulnerable 对象必须被合并到 caller 的冲突图, 以决定 caller 的 canary 保护面.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: generic
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/CodeGen/StackProtector.cpp` ; `gcc/cfgexpand.cc`
- **version_sensitivity**: stable
- **observation**: 同一函数在 `-flto` / 非 LTO 下的插桩决策应一致 (假设源码 vulnerability 集合不变).

## 9. 已知回归 / CVE 关联 (Known Regressions)

### INV-SP-CVE-2023-4039 — AArch64 GCC ≤13.2 的 L01 历史违反

- **statement**: CVE-2023-4039 修复前, AArch64 `-fstack-protector*` 在存在动态分配 (VLA/`alloca`) 时, saved regs 布局位于 locals 之下, 使得 canary 位于 LR/FP 之上但 "栈下方" 的溢出源 (dynamic alloc) 可直接越过 canary 触及 LR —— INV-SP-L01 / L03 的违反实例.
- **compiler**: GCC ≤13.2
- **target**: aarch64
- **source_kind**: bug-disclosure
- **source_url_or_path**: https://rtx.meta.security/mitigation/2023/09/12/CVE-2023-4039.html
- **observation**: 同 INV-SP-L01: VLA 越界路径下 canary 未先触发即修改 LR.

## 10. 可程序化 invariants

> 筛选维度与静/动态归属准则定义在 [`README.md` §3 可程序化筛选方法论](./README.md#3-可程序化筛选方法论). 本章只列结果.

### 通过筛选

- **INV-SP-G01** — 默认 guard 符号 `__stack_chk_guard` / `__stack_chk_fail` 进入二进制
  - 类别: 静态
  - 通过理由: 现象在 ELF 动态符号表上直接可读, 判定确定 (有/无).

- **INV-SP-A01** — `no_stack_protector` 函数应不含 guard 序列
  - 类别: 静态
  - 通过理由: 函数级反汇编中 guard load / check 序列的有/无是确定信号.

- **INV-SP-A02** — `stack_protect` 属性在 `-fstack-protector-explicit` 下必生效
  - 类别: 静态
  - 通过理由: 极简函数 + 属性即可构造无启发式干扰的正控样本, 反汇编现象确定.

- **INV-SP-H03** — VLA / `alloca` 函数在 SP 启用档位下必须插桩
  - 类别: 静态
  - 通过理由: 文档级强保证, 现象等价于 G01 在该函数上必须出现 SP 引用.

- **INV-SP-L01** — Canary 必须夹在 vulnerable locals 与 saved regs / retaddr 之间
  - 类别: 动态
  - 通过理由: 顺序越界写的崩溃路径直接区分"先撞 canary"与"先改 retaddr"两种行为.

- **INV-SP-L03** — 动态分配区域必须位于 canary 栈低端侧
  - 类别: 动态
  - 通过理由: L01 在 VLA/`alloca` 上的特化形态, 同一观测通道.

- **INV-SP-L05** — 多个 vulnerable 对象共享同一 canary 保护面
  - 类别: 动态
  - 通过理由: 混合 vulnerable 对象的越界写仍走 L01 通道, 现象等价.

- **INV-SP-F01** — 越界写到 canary 后进程被 SP 运行时终止
  - 类别: 动态
  - 通过理由: glibc 行为完全确定 (`SIGABRT` + "stack smashing detected"), 与 L01 互为正反对照.

- **INV-SP-R03** — Epilogue 必须在 return 前 clobber 持有过 canary 的寄存器
  - 类别: 动态
  - 通过理由: guard 的 64-bit 随机值与 caller 上下文寄存器值碰撞概率 ~2⁻⁶⁴, 比对结果确定; 现象在普通越界路径不可见, 必须独立通道.

- **INV-SP-CVE-2023-4039** — AArch64 GCC ≤13.2 的 L01 历史违反
  - 类别: 动态
  - 通过理由: L01 的 (GCC, ISA) 实例, 复用同一观测通道, 历史版本可作为正反对照.

### 未通过筛选

- **INV-SP-E01** — 三档 flag 的保护面启发式
  - 排除理由: 判定确定性不足 — 启发式细节随 GCC 小版本飘移 (`version_sensitivity = likely-to-drift`), 任何固定真值表都会变成误报源.

- **INV-SP-E02** — `-fhardened` 隐式启用 SP-strong
  - 排除理由: 实现成本不对等 — 这是命令行级前提, 通过 `--help=hardened` 与构建配置校验更直接, 不构成"静默失效".

- **INV-SP-E03** — 属性可覆盖全局开关
  - 排除理由: 实现成本 — 与 A01 / A02 在判定通道上重合, 单独抽不出新现象.

- **INV-SP-H01** — GCC 启发式分类位
  - 排除理由: 可观测性不足 — 分类位仅存在于 `cfgexpand.cc` 的编译期中间状态, 二进制层无残留.

- **INV-SP-H02** — `-fstack-protector-strong` 的额外触发条件
  - 排除理由: 判定确定性不足 — 除已被 H03 覆盖的 VLA/`alloca` 子集外, "addr-taken / 聚合体含数组"等条件易被寄存器分配优化掉, 真值条件不稳定.

- **INV-SP-L02** — AArch64 saved regs 位置
  - 排除理由: 实现成本 — 与 L01 是同一现象的不同表述, 直接验证布局需要 AArch64 prologue 反汇编与 saved-reg 偏移解析.

- **INV-SP-L04** — Spill / 参数副本不得落在 canary 保护范围外
  - 排除理由: 判定确定性不足 — 一手资料明确归为 hardening-ideal, 自动判定会与 L01 现象混淆.

- **INV-SP-R01** — Canary guard 不驻留 caller-saved 寄存器 (跨调用区间)
  - 排除理由: 可观测性不足 — 约束的是函数体内瞬时寄存器状态, 二进制特征与运行时行为均无直接观测点.

- **INV-SP-R02** — 跨调用参数副本应优先 callee-saved 寄存器
  - 排除理由: 判定确定性不足 — 一手资料显式标记为 hardening-ideal, 不构成硬安全契约.

- **INV-SP-G02** — x86_64 Linux 从 `%fs:0x28` 读 guard
  - 排除理由: 实现成本 — 需要 ISA-specific prologue 反汇编模式匹配, 而 guard 来源不影响"canary 是否生效"的安全判定.

- **INV-SP-G03** — AArch64 `-mstack-protector-guard=sysreg`
  - 排除理由: 实现成本 — 同 G02, 还叠加 flag-conditional 解析, 默认配置不命中此路径.

- **INV-SP-G04** — `_RUNTIME_ENABLED_P` hook
  - 排除理由: 可观测性不足 — 仅在 freestanding / kernel 变体生效, user-space 二进制触发不到.

- **INV-SP-F02** — `__stack_chk_fail_local` thunk 静态链接 + PIC 要求
  - 排除理由: 实现成本不对等 — 链接器契约, 违反即链接失败, 不属于"静默失效"领域.

- **INV-SP-F03** — libc 无 SP 支持时必须链 libssp
  - 排除理由: 同 F02, 链接失败前置.

- **INV-SP-F04** — guard 值在 `execve` 后重新初始化
  - 排除理由: 可观测性不足 — 这是动态观察通道所**依赖**的前提, 不是检测目标.

- **INV-SP-A03** — 属性与 `-fno-stack-protector` 的互斥解析
  - 排除理由: 实现成本 — 与 A01 / A02 在判定通道上等价, 信息增益低.

- **INV-SP-X01** — `__stack_chk_guard` 跨 DSO 一致
  - 排除理由: 可观测性不足 — 验证需要构造多 DSO 场景, 不是单二进制不变量.

- **INV-SP-X02** — SP 与 LTO 联动
  - 排除理由: 判定确定性不足 — 是 LTO/非 LTO 双编译的等价性属性, 不是单二进制 invariant.

## 11. 开放问题 / 未覆盖 invariants

- **LLVM `StackProtector.cpp` 启发式 vs GCC `cfgexpand.cc` 的精确差异**: 已知"等价但不一一对应", 8 字节边界、取址判定等细节需要对照 lit tests 逐条列出.
- **RISC-V / LoongArch64 的 canary 布局**: psABI 入口已知 (`riscv-non-isa/riscv-elf-psABI-doc` 等), 但缺少各自后端的栈帧专门归纳.
- **Clang `-mstack-protector-guard=tls` 系列 Linux kernel 专属 flag**: 内核场景下 guard 由 per-CPU 变量提供, 不变量与 user-space 不同.
- **`-fstrub` / `-fharden-control-flow-redundancy` 与 SP 的叠加效应**: SP 与 strub 边界上的栈擦除顺序尚未单独抽不变量.
- **多线程下的 guard 重置**: 子线程是否共享 `__stack_chk_guard` 与 TLS `%fs:0x28` 的关系, 需要查 glibc `__pthread_initialize_minimal`.
- **`setjmp/longjmp` / C++ 异常通过 canary 边界时的校验路径**: LLVM SafeStack / SCS 文档已指出异常是已知泄漏点, SP 侧需核.
- **GCC 16.1 S/390 新增 `-mstack-protector-guard=global` / `-mstack-protector-guard-record`**: 用于内核运行时 patch canary 加载地址, 需补 S/390 专门条目.
