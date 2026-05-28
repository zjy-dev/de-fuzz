# Stack Canary (Stack Protector) Invariants — Silent-Bypass 视角

> 本文只关心**会让 stack canary 静默失效或被静默削弱**的不变量, 即"机制看起来在运行, 实际无法阻止 control-data 被改写, 或者 guard 已可被攻击者预测". 形如"`-fno-stack-protector` 是否生效"、"`no_stack_protector` 属性是否被尊重"、"libssp 是否被链入"等问题, 后果或是机制更强或是链接/编译失败, 不在本文档范围内.
>
> 机制简写: **SP** = Stack Protector / canary.
>
> 来源覆盖: GCC 主线 (含 16.x), LLVM/Clang 主线, glibc, AAPCS64, x86_64 SysV, GCC Bugzilla / LLVM Issue tracker / CERT/CC, CVE-2023-4039 公开分析.

## 0. 术语与坐标

- **canary slot**: 函数栈帧里存放 guard 值的槽.
- **guard value / `__stack_chk_guard`**: 进入函数时写入 canary slot 的秘密值.
- **saved registers**: callee 保存的寄存器区域, 含 LR/FP (AArch64) 或 return address (x86 retaddr 槽).
- **vulnerable object**: 字符数组、`alloca`、VLA、聚合体含数组、取地址的自动变量等.
- **silent bypass**: 攻击者构造的越界写发生后, 函数仍然正常返回, 进程不被 `__stack_chk_fail` 终止, 控制流已被转移. 这是本文所有不变量的统一威胁模型.

每条不变量按 [`README.md` §2](./README.md#2-survey-字段约定) 的字段记录: `ID / statement / compiler / version / target / source_kind / source_url_or_path / evidence_snippet / version_sensitivity / observation`.

## 1. 栈帧布局必须强制覆盖序

布局类不变量的共同威胁: 攻击者从某个 vulnerable 对象顺序写出, 不经过 canary 即触达 saved regs / retaddr, 函数返回时 canary 仍然完整, 校验通过, 进程静默地被劫持.

### INV-SP-L01 — Canary slot 夹在 vulnerable locals 与 saved registers / return address 之间

- **statement**: 对栈下行架构, canary 位于所有 vulnerable 自动对象与所有 saved registers / return address 之间, 任何"从 vulnerable 对象顺序溢出到 saved regs / retaddr"的写路径必先覆盖 canary.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: generic (x86_64, aarch64 已验证; 其他栈下行 ISA 同理)
- **source_kind**: source + paper
- **source_url_or_path**: `gcc/cfgexpand.cc` (`add_stack_protection_conflicts`, `expand_stack_vars`); `gcc/config/aarch64/aarch64.cc` 注释; `llvm/lib/CodeGen/StackProtector.cpp` 顶部注释
- **evidence_snippet**: aarch64.cc: *"When using stack smash protection, make sure that the canary slot comes between the locals and the saved registers. Otherwise, it would be possible for a carefully sized smash attack to change the saved registers (particularly LR and FP) without reaching the canary."*
- **version_sensitivity**: stable
- **observation**: 顺序覆盖 vulnerable 对象的越界写, 若先触发 canary check 失败, 不变量满足; 若直接修改 saved regs / retaddr 而 canary 未触发, 不变量被违反 (silent bypass).

### INV-SP-L02 — 动态分配 (VLA / `alloca`) 必须位于 canary 的栈低端侧

- **statement**: VLA / `alloca` 区域必须位于 canary 的栈低端 (即更远离 saved regs) 一侧, 不能插在 canary 与 saved regs 之间; 否则可构造大小恰好的 dynamic alloc 越过 canary 直达 LR / FP.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC ≥ 14 (CVE-2023-4039 修复后), LLVM ≥ 17
- **target**: aarch64 (主要), x86_64 (对称)
- **source_kind**: bug-disclosure + source
- **source_url_or_path**: https://rtx.meta.security/mitigation/2023/09/12/CVE-2023-4039.html ; https://gcc.gnu.org/pipermail/gcc-patches/2023-September/630054.html ; `gcc/config/aarch64/aarch64.cc::aarch64_save_regs_above_locals_p`
- **version_sensitivity**: stable since fix
- **observation**: 含 VLA/`alloca` 的函数被越界写穿越 canary 直接修改 LR/FP, 函数返回未触发 `__stack_chk_fail` ⇒ silent bypass; GCC ≤ 13.2 在 AArch64 上对所有此类函数均违反.

### INV-SP-L03 — 同函数内多个 vulnerable 对象共享同一 canary 保护面

- **statement**: `add_stack_protection_conflicts` 让所有 vulnerable 对象 (`SPCT_HAS_*` 任一) 与 canary slot 形成冲突图, 强制它们都排在 canary 的"溢出源"一侧; 因此一个函数内的混合 vulnerable 对象 (固定 buffer + VLA + `alloca` + 含数组聚合体) 仍由同一 canary 保护. 若任一对象被排到 canary 之上, 该对象的越界写就是 silent bypass.
- **compiler**: GCC
- **version**: GCC ≥ 4.9
- **target**: generic
- **source_kind**: source
- **source_url_or_path**: `gcc/cfgexpand.cc::add_stack_protection_conflicts`
- **version_sensitivity**: stable
- **observation**: 含混合 vulnerable 对象的函数, 任一对象的越界写均应触发同一 canary check; 若部分对象的越界路径绕过 canary, 不变量被违反.

### INV-SP-L04 — Stack protector slot 不得在初次定位后被重新分配到 locals 之后

- **statement**: 后端 / RA / frame elimination 任何阶段都不得把 protector slot 的实际偏移移动到 vulnerable locals 之后. CERT/CC VU#129209 (LLVM Arm backend) 即此类违反: protector 在 `LocalStackSlotAllocation` 之后被重新分配, 偏移落到 locals 之上, canary 失去拦截作用.
- **compiler**: LLVM/Clang (GCC 也存在同类风险)
- **version**: LLVM 修复见 D64757 / D64759; Arm Compiler 6 受影响版本: 6.12 (Arm Compiler for Linux 19.0–19.2). 6.13+ 与 19.3+ 已修.
- **target**: arm (Aarch32, 主要); 修复后 LLVM 把 protector frame access 保持在 frame-index 形式, 由 PEI 直接解析为 sp/fp/bp 偏移
- **source_kind**: bug-disclosure + source
- **source_url_or_path**: https://kb.cert.org/vuls/id/129209/ ; https://reviews.llvm.org/D64759
- **evidence_snippet**: D64759: *"forces PEI to address it using fp/sp/bp"*; VU#129209: *"a new stack protector can be allocated later in the process ... the stack cookie pointer to spill to the stack and potentially be overwritten."*
- **version_sensitivity**: target-specific
- **observation**: 受影响 toolchain 编译的函数, 越界写绕过 canary 修改 retaddr 而 canary 校验仍通过 ⇒ silent bypass.

## 2. 校验逻辑必须真校 guard 值

### INV-SP-V01 — Epilogue 比较 guard **值**, 不得比较 guard **地址**

- **statement**: Stack-protector epilogue 必须把 canary slot 中存储的 guard 值与从 guard 来源 (`__stack_chk_guard` 全局符号 / `%fs:0x28` / `TPIDR_ELn + offset`) 重新加载得到的 guard 值进行比较. 不得错误地把 guard 来源的地址与 canary slot 中的地址进行比较, 否则比较结果与栈是否被覆盖无关, `__stack_chk_fail` 永远不会被调用.
- **compiler**: GCC
- **version**: GCC 9.x (Cortex-M4 bare-metal toolchain 已观察到该 codegen) — 早于此与之后的版本未观察到, 视为回归窗口
- **target**: arm (Cortex-M, `--specs=nano.specs`/`nosys.specs`)
- **source_kind**: bug-disclosure
- **source_url_or_path**: https://www.systemonchips.com/gcc-9-stack-protector-comparing-stack-guard-address-instead-of-value-on-cortex-m4/ ; 关联 GCC PR 85434
- **evidence_snippet**: 实测 prologue `str r3, [sp, #68]` 保存的是 `__stack_chk_guard` 的链接期常量地址而非其内容, epilogue `eors r2, r3` XOR 两个常量地址, 总是相等.
- **version_sensitivity**: likely-to-drift
- **observation**: 任何越界写覆盖 canary slot 后, 函数仍正常返回, 即便覆盖值与 guard 真实值显著不同, `__stack_chk_fail` 不被调用 ⇒ silent bypass. 与 INV-SP-L01 区分: L01 假设比较逻辑正确, V01 直接质疑比较逻辑本身.

### INV-SP-V02 — `__stack_chk_fail` 必须是 `noreturn`

- **statement**: GCC `TARGET_STACK_PROTECT_FAIL` hook 与 LLVM 都把 `__stack_chk_fail` 视为 `noreturn`, 据此不在调用之后插入正常返回路径. glibc / libssp 实现也保证调用结束于 `abort()`. 若被覆盖的 `__stack_chk_fail` 实现 (例如 freestanding / 嵌入式 stub) 返回, 函数返回路径将继续执行污染过的 retaddr ⇒ silent bypass.
- **runtime + compiler**: GCC + glibc, Clang + glibc / compiler-rt, libssp; 嵌入式自实现 stub 风险点
- **version**: all
- **target**: generic
- **source_kind**: internals + runtime
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gccint/Stack-Smashing-Protection.html ; glibc `debug/stack_chk_fail.c`
- **version_sensitivity**: stable
- **observation**: canary check 失败后函数没有 `abort` 而是正常返回, 即视为不变量被违反.

## 3. Guard 保密性必须维持到函数返回

任何让攻击者可以读到 guard 值或 guard 地址中转副本的路径, 都允许 forge canary, 进而 silent bypass 任意 SP 保护函数 — 不需要破坏栈帧布局.

### INV-SP-S01 — Guard 值 / guard 地址不得 spill 到攻击者可写的栈区

- **statement**: 后端不得让"加载 guard 值"或"加载 guard 地址"中间结果的寄存器 spill 到与 vulnerable 对象同帧的普通 spill 槽. 攻击者若能改写 spill 副本 (覆盖地址 ⇒ 校验时从攻击者控制内存读取假 guard; 覆盖值 ⇒ 直接喂入伪 guard), 则 canary 校验失去意义.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 已修 PR85434 (ARM/Aarch64 在 `stack_protect_set/check` 模式中加 scheduler barrier + volatile MEM); LLVM D64759 把 protector frame access 锁在 frame-index 而非虚寄存器
- **target**: arm/aarch32 (PIC 路径风险最高), aarch64, x86_64
- **source_kind**: bug-disclosure + source
- **source_url_or_path**: https://gcc.gnu.org/legacy-ml/gcc-patches/2018-04/msg01272.html (PR85434 讨论) ; https://reviews.llvm.org/D64759
- **evidence_snippet**: PR85434 讨论: *"Between these two steps, the register holding the address can be spilled to the stack."* D64759: *"we may end up spilling this pointer to the stack, which can be a security concern."*
- **version_sensitivity**: likely-to-drift (随 RA / scheduler 启发式漂移)
- **observation**: 反汇编中 `__stack_chk_guard` 的地址加载结果在 prologue 与 epilogue 之间被存入栈再读出, 且 spill 槽与 vulnerable 对象同帧 ⇒ 不变量被违反.

### INV-SP-S02 — Epilogue 在 return 前必须 clobber 持有过 canary 的寄存器

- **statement**: SP-protected 函数的 epilogue 中, 从 guard 来源加载并完成校验后、到返回指令 (`ret` / `jr $ra` / `jirl $zero, $ra, 0` 等) 之前, 任何 transient 持有过 canary 值或 XOR 中间值的 GPR 必须被显式覆写. 否则函数返回后 caller-saved 寄存器中残留的 guard 值, 可被随后的寄存器暴露原语 (caller 内联汇编 / 编译器注入的 spill / signal handler `mcontext_t` / micro-arch side channel) 读出, 攻击者据此伪造任意 SP 保护函数的 canary.
- **compiler**: GCC (主要), LLVM/Clang (对应路径需独立审计)
- **version**: GCC ≤ 16.x — `gcc/cfgexpand.cc::stack_protect_prologue` 与 `gcc/function.cc::stack_protect_epilogue` 的 generic fallback 仍未修. AArch64 / Arm / Thumb1 backend 通过 PR 96191 在 `stack_protect_test_<mode>` define_insn 中显式 scrub temp 寄存器; 落到 fallback 的 backend (mips / mips64 / loongarch64 / xtensa / csky / or1k / hppa / m68k / alpha / arc / nds32 / microblaze 等) 全部命中.
- **target**: 任意未提供 `targetm.have_stack_protect_set` / `targetm.have_stack_protect_test` 的 backend
- **source_kind**: source + bug-disclosure
- **source_url_or_path**: `gcc/cfgexpand.cc::stack_protect_prologue` (generic fallback 用 `emit_move_insn`, 未 clobber source 寄存器); `gcc/function.cc::stack_protect_epilogue` (用 `emit_cmp_and_jump_insns`, 跳转后未 scrub 任何 GPR); `gcc/config/aarch64/aarch64.md` `stack_protect_test_<mode>` (参考实现); https://gcc.gnu.org/bugzilla/show_bug.cgi?id=96191 ; https://gcc.gnu.org/bugzilla/show_bug.cgi?id=125045 (meta-bug)
- **evidence_snippet**: PR 96191 报告 *"the secret canary value is left in registers after the function returns"*; aarch64.md `stack_protect_test_<mode>` 注释把 temp 寄存器清洗标为安全关键.
- **version_sensitivity**: likely-to-drift (随每个 backend 是否新增 `stack_protect_{set,test}` define_insn 而变)
- **observation**: SP-protected 函数返回后 caller 上下文中可见的 GPR 内若仍存在 guard 值或 XOR 中间值, 不变量被违反. 该现象在普通越界路径下不可见, 必须通过寄存器残留独立观察.

## 4. 启发式必须真覆盖高风险路径

启发式细节本身随版本飘移 (likely-to-drift, 不是不变量), 但有一个底线: 含 `alloca` / VLA 的函数必须在所有非 off 档位下被插桩. 这是一个文档级强保证, 违反它意味着进程在最危险的栈分配模式下"以为有 canary, 其实没有", 等价于全函数级 silent bypass.

### INV-SP-H01 — VLA / `alloca` 函数在 SP 启用档位下必须插桩

- **statement**: 含 VLA 或 `__builtin_alloca` 的函数, 在 `-fstack-protector{,-strong,-all,-explicit}` 任一非 off 档位下都会被插 canary.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC ≥ 4.9, Clang ≥ 6
- **target**: generic
- **source_kind**: user-doc + test
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html ; `gcc/testsuite/gcc.dg/ssp-*.c`, `fstack-protector-strong.c`
- **evidence_snippet**: GCC 手册: *"functions that call `alloca`, and functions with buffers larger than or equal to 8 bytes"* — `alloca` 出现在所有四档启发式里.
- **version_sensitivity**: stable
- **observation**: 含 VLA / `alloca` 的函数在 SP 启用档位下, 编译产物必须引用 `__stack_chk_fail`; 否则该函数对应一类整段 silent bypass.

## 5. 已知 silent-bypass 案例

| Case | 违反的不变量 | Toolchain | 说明 |
| --- | --- | --- | --- |
| CVE-2023-4039 | INV-SP-L01 / INV-SP-L02 | GCC ≤ 13.2 (AArch64) | 含 VLA/`alloca` 的函数布局把 saved regs 排在 locals 之下, 动态分配溢出可越过 canary 直达 LR. 修复: `aarch64_save_regs_above_locals_p`. |
| CERT VU#129209 | INV-SP-L04 | LLVM Arm 9.x; Arm Compiler 6.12, ACfL 19.0–19.2 | `LocalStackSlotAllocation` 之后 protector slot 被重新分配到 locals 之上, 同时 protector 指针可 spill 到栈. 修复: D64757 / D64759. |
| GCC 9 Cortex-M4 codegen | INV-SP-V01 | GCC 9.3.1 arm-none-eabi (PR85434 邻域) | epilogue 比较两个 `__stack_chk_guard` 链接期常量地址, 与栈状态无关, canary 永远 pass. |
| GCC PR 85434 (ARM PIC) | INV-SP-S01 | GCC ≤ 修复版 (ARM/AArch32 PIC) | guard 地址 (GOT 计算结果) 在 `stack_protect_set/check` 之间被 spill, 攻击者可改写 spill 后篡改校验源. |
| GCC PR 96191 + meta-bug 125045 | INV-SP-S02 | GCC ≤ 16.x 在 generic fallback backend (mips, loongarch64, xtensa, csky, or1k, m68k, alpha, arc, nds32, microblaze, ...) | epilogue 校验完到 `ret` 之间不 clobber 持有过 guard 的 GPR, 函数返回后 caller 上下文寄存器中残留 guard. |

## 6. 可程序化筛选结果

> 筛选维度与静/动态归属准则定义在 [`README.md` §3 可程序化筛选方法论](./README.md#3-可程序化筛选方法论). 本章只列结果. 所有不变量均直接对应某条 silent-bypass 路径, 因此本机制下不再单列"非 silent-bypass 排除项".

### 通过筛选

- **INV-SP-L01** — Canary 必须夹在 vulnerable locals 与 saved regs / retaddr 之间
  - 类别: 动态
  - 通过理由: 顺序越界的崩溃路径直接区分"先撞 canary"与"先改 retaddr"两种行为, 与 silent bypass 一一对应.

- **INV-SP-L02** — 动态分配区域必须位于 canary 栈低端侧
  - 类别: 动态
  - 通过理由: L01 在 VLA/`alloca` 上的特化形态, 同一观测通道, 历史版本 (GCC ≤13.2 AArch64) 可作为正反对照.

- **INV-SP-L03** — 多 vulnerable 对象共享同一 canary 保护面
  - 类别: 动态
  - 通过理由: 混合 vulnerable 对象的越界写仍走 L01 通道, 现象等价.

- **INV-SP-L04** — Protector slot 不得被重新分配到 locals 之后
  - 类别: 动态
  - 通过理由: 后端 RA / frame elimination 决定, 二进制布局难直接判定; 但其 silent-bypass 现象等同 L01, 复用同一动态通道. 历史版本 (LLVM Arm 9.x / Arm Compiler 6.12) 可作为已知违反样本.

- **INV-SP-V01** — Epilogue 比较 guard 值而非 guard 地址
  - 类别: 静态
  - 通过理由: 反汇编 epilogue 模式可判定 — 比较的两个操作数若均来自 `&__stack_chk_guard` 而非 `*(&__stack_chk_guard)`, 即违反. 现象在 ELF 反汇编层确定.

- **INV-SP-V02** — `__stack_chk_fail` 必须 `noreturn`
  - 类别: 动态
  - 通过理由: 触发 canary 校验失败后, 进程是否被 `SIGABRT` 终止是确定信号; 自实现 stub 返回则不变量被违反.

- **INV-SP-S01** — Guard 值 / 地址不得 spill 到攻击者可写栈区
  - 类别: 静态
  - 通过理由: 反汇编 prologue ↔ epilogue 之间是否存在 `__stack_chk_guard` 中间结果 spill 是确定信号; 历史 PR85434 / D64759 提供已知违反样本.

- **INV-SP-S02** — Epilogue 在 return 前 clobber 持有过 canary 的寄存器
  - 类别: 动态
  - 通过理由: guard 的 64-bit 随机值与 caller 上下文寄存器值碰撞概率 ~2⁻⁶⁴, 比对结果确定; 现象在普通越界路径不可见, 必须独立通道观察.

- **INV-SP-H01** — VLA / `alloca` 函数在 SP 启用档位下必须插桩
  - 类别: 静态
  - 通过理由: 文档级强保证; 现象等价于该函数符号引用 `__stack_chk_fail`, ELF 静态可判.

### 未通过筛选

本文档已按 silent-bypass 视角剔除非相关项. 历史档案中曾列出的"启发式细节"、"链接器契约"、"`no_stack_protector` 属性是否生效"、"guard 来源 ISA 选择"、"`-fhardened` 元 flag 隐含项"等不变量, 后果或为机制更强、或为编译/链接失败, 不构成 silent bypass, 因此不再列入本文档的筛选范围.

## 7. 开放问题

- **LLVM `StackProtector.cpp` 启发式 vs GCC `cfgexpand.cc`**: H01 在 LLVM 侧是否同等强保证, 需对 lit tests 逐条比对.
- **RISC-V Zicfiss / LoongArch64 后端的 INV-SP-L01 / S02 状态**: 两者均尚未走完 `stack_protect_{set,test}` define_insn, 大概率继承 generic fallback ⇒ 落入 PR 125045 影响面.
- **GCC 16.1 S/390 新增 `-mstack-protector-guard=global` / `-mstack-protector-guard-record`**: 用于内核运行时 patch canary 加载地址, 属于 guard 来源选择, 不直接影响布局/校验逻辑, 但运行时 patch 时机若与函数 prologue 交错有可能制造新的 V01/S01 子情形, 需后续核实.
- **多线程 + `setjmp`/`longjmp` + C++ 异常路径下的 epilogue scrub**: 异常路径不走普通 epilogue, INV-SP-S02 在异常出口的覆盖度尚需对 `_Unwind_Resume` 路径的 register state 单独审计.
- **kernel SP (`-mstack-protector-guard=tls` 系列, per-CPU guard)**: 内核场景 guard 由 per-CPU 变量提供, 上述不变量在内核语境下需独立改写.
