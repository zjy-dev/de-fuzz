# Stack Canary (Stack Protector) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / LLVM/Clang / libc / ABI / CVE 中与 **stack canary** 直接相关的 invariants 统一抽取、归类, 作为 DeFuzz canary oracle 的形式化依据.
>
> 机制简写与 survey 一致: **SP** = Stack Protector / canary.

## 0. 术语与坐标

- **canary slot**: 函数栈帧里存放 guard 值的槽.
- **guard value / `__stack_chk_guard`**: 进入函数时写入 canary slot 的秘密值.
- **saved registers**: 被 callee 保存的寄存器区域, 典型包括 LR/FP (AArch64) 或 return address (x86 的 retaddr 槽).
- **spill area**: 编译器为跨调用活跃值 (如参数副本) 分配的栈槽.
- **vulnerable object**: 可被溢出写的对象 (字符数组, `alloca`, VLA, 结构体中含字符数组, 取地址的自动变量等).
- **phase 1/2 分类**: 见 `gcc/cfgexpand.cc` 的 `stack_protect_decl_phase` — 决定 canary 冲突图中哪些对象必须排在 canary 的溢出一侧.

每条 invariant 采用 survey 推荐的字段: `ID / statement / compiler / version / target / source_kind / source_url_or_path / evidence_snippet / version_sensitivity / oracle_mapping`.

## 1. 静态前提 (Static Preconditions)

### INV-SP-E01 — 三档 flag 的保护面

- **statement**: `-fstack-protector` 只对含 `alloca` 或 ≥8 字节字符 buffer 的函数插 canary; `-fstack-protector-strong` 扩到 "取地址 local / 聚合体含数组 / 使用 `__builtin_frame_address`" 等更多启发式; `-fstack-protector-all` 无差别插桩; `-fstack-protector-explicit` 仅对带 `stack_protect` 属性的函数插桩.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 4.9+, Clang 6+
- **target**: generic
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html ; `gcc/cfgexpand.cc` ; `llvm/lib/CodeGen/StackProtector.cpp`
- **evidence_snippet**: "Includes functions with `alloca`, and functions with buffers larger than or equal to 8 bytes" (GCC manual).
- **version_sensitivity**: stable (启发式细节 likely-to-drift)
- **oracle_mapping**: 生成不同 buffer 尺寸 / 取址模式 / alloca 模式的 seed, 断言同一 flag 下该函数是否插入 canary 与启发式决策一致.

### INV-SP-E02 — `-fhardened` 隐式启用 `-fstack-protector-strong`

- **statement**: GCC `-fhardened` (GNU/Linux) 隐式开启 `-D_FORTIFY_SOURCE=3 -ftrivial-auto-var-init=zero -fPIE -pie -Wl,-z,relro,-z,now -fstack-protector-strong -fstack-clash-protection -fcf-protection=full`.
- **compiler**: GCC
- **version**: GCC 14+
- **target**: generic (Linux)
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: 在 CFLAGS 变种里把 `-fhardened` 作为一个配置, 用 canary oracle 验证它等价于启用了 SP-strong 的结果.

### INV-SP-E03 — 属性可覆盖全局开关

- **statement**: 函数属性 `no_stack_protector` / `__attribute__((no_stack_protector))` 关闭该函数 SP; `stack_protect` 强制插 canary. `safebuffers` (Clang) 等价于前者的子集语义. 这些属性与全局 flag 组合时函数级优先.
- **compiler**: GCC, Clang
- **version**: GCC 11+, Clang 4+
- **target**: generic
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html ; https://clang.llvm.org/docs/AttributeReference.html
- **version_sensitivity**: stable
- **oracle_mapping**: canary oracle 模板中 `main` 使用 `no_stack_protector` 保证 main 无 canary, 避免 "caller 本身保护" 掩盖 callee 的绕过 (见 `@/home/yall/project/de-fuzz/docs/oracles/canary-oracle.md:99-108`).

## 2. 启发式 (Heuristic) — 哪些变量属于 "vulnerable"

### INV-SP-H01 — GCC 启发式分类位

- **statement**: `cfgexpand.cc` 的 `stack_protect_classify_type` 给每个自动变量打以下分类位, 进入 canary 冲突图: `SPCT_HAS_LARGE_CHAR_ARRAY` (≥8 字节字符数组), `SPCT_HAS_SMALL_CHAR_ARRAY` (<8 字节字符数组), `SPCT_HAS_ARRAY` (任意数组), `SPCT_HAS_AGGREGATE` (含数组的聚合体), `SPCT_HAS_FN_FRAME_ADDRESS` (被 `__builtin_frame_address` 触及).
- **compiler**: GCC
- **version**: GCC 4.9+
- **target**: generic
- **source_kind**: source
- **source_url_or_path**: https://github.com/gcc-mirror/gcc/blob/master/gcc/cfgexpand.cc (`stack_protect_classify_type`, `stack_protect_decl_phase`, `add_stack_protection_conflicts`)
- **version_sensitivity**: likely-to-drift (分类细节随启发式微调而变)
- **oracle_mapping**: 生成"刚好 7 字节 vs 刚好 8 字节字符数组" 边界 seed, 断言 SP 决策翻转.

### INV-SP-H02 — `-fstack-protector-strong` 额外触发条件

- **statement**: 满足下列任一即插 canary: 有地址被取的自动变量 (`addr-taken alloca`), 函数直接调用 `alloca` 或声明 VLA, 局部聚合体中含数组, 使用 `__builtin_frame_address`. 不含寄存器分配成功的纯 scalar / 被优化掉的变量.
- **compiler**: GCC, LLVM/Clang (等价但不一一对应)
- **version**: GCC 4.9+, Clang 6+
- **target**: generic
- **source_kind**: source + internals
- **source_url_or_path**: `gcc/cfgexpand.cc` ; `llvm/lib/CodeGen/StackProtector.cpp` (`StackProtector::RequiresStackProtector`)
- **evidence_snippet**: GCC 手册: "optimized-away or register-allocated variables are not protected".
- **version_sensitivity**: stable at doc level, likely-to-drift at impl level
- **oracle_mapping**: 在 seed 模板里引入 "只有 scalar + 被编译器提升为寄存器" 的负例, 期望 SP 不生效; 引入 alloca/VLA 正例, 期望 SP 生效.

### INV-SP-H03 — VLA / alloca 必须触发 SP

- **statement**: 含 VLA 或 `__builtin_alloca` 的函数, 在 `-fstack-protector{,-strong,-all,-explicit}` 任一非 `off` 档位下都会被插 canary (这是 `alloca` 出现在所有四档启发式里的原因).
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 4.9+, Clang 6+
- **target**: generic
- **source_kind**: user-doc + test
- **source_url_or_path**: GCC manual Instrumentation Options; `gcc/testsuite/gcc.dg/ssp-*.c`, `fstack-protector-strong.c`
- **version_sensitivity**: stable
- **oracle_mapping**: canary oracle 的模板 3 (`alloca`) / 模板 2 (VLA) 专门覆盖此条 (`@/home/yall/project/de-fuzz/docs/oracles/canary-oracle.md:181-196`).

## 3. 栈帧布局 (Frame Layout)

### INV-SP-L01 — Canary slot 必须夹在 vulnerable locals 与 saved registers / return address 之间

- **statement**: 保护模型要求对栈顶→栈底溢出方向 (栈下行架构), canary 位于所有 vulnerable 自动对象 与 所有 saved registers / return address 之间, 使得任何"从 vulnerable 对象顺序溢出到 saved regs / retaddr"的路径必然先覆盖 canary.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: generic (x86_64, aarch64 已验证; 其他栈下行 ISA 同理)
- **source_kind**: source + internals + paper
- **source_url_or_path**: `gcc/cfgexpand.cc` (`add_stack_protection_conflicts`, `expand_stack_vars`); `gcc/config/aarch64/aarch64.cc` 注释; `llvm/lib/CodeGen/StackProtector.cpp` 顶部注释
- **evidence_snippet**: aarch64.cc: *"When using stack smash protection, make sure that the canary slot comes between the locals and the saved registers. Otherwise, it would be possible for a carefully sized smash attack to change the saved registers (particularly LR and FP) without reaching the canary."*
- **version_sensitivity**: stable
- **oracle_mapping**: canary oracle 中 3 者关系分析 (case 3: `canary -> ret -> buf` 是 CVE-2023-4039 的反例, 违反此条; 见 `@/home/yall/project/de-fuzz/docs/oracles/canary-oracle.md:33-37`).

### INV-SP-L02 — AArch64: 启用 SP 时 saved registers 必须置于 locals 之上 (布局 2)

- **statement**: AArch64 backend 在 `crtl->stack_protect_guard` 为真时返回 `aarch64_save_regs_above_locals_p = true`, 强制使用"布局 (2)": saved regs / LR / FP / SVE 寄存器 **位于 locals 与 canary 之上**, 这样 canary (作为最上方 local) 在 locals 与 saved regs 之间.
- **compiler**: GCC
- **version**: GCC 14+ (CVE-2023-4039 修复后; 此前 GCC ≤13.2 存在相反布局缺陷)
- **target**: aarch64
- **source_kind**: source
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc` `aarch64_save_regs_above_locals_p`
- **evidence_snippet**: 同上注释
- **version_sensitivity**: stable since fix; historical CVE 对 GCC ≤13.2 存在
- **oracle_mapping**: 在 GCC ≤13.2 的受控版本里可作为正控组验证 oracle 能检出 CVE-2023-4039 模式; GCC 14+ 应当不再触发.

### INV-SP-L03 — 动态分配 (VLA/alloca) 与 canary 的相对位置

- **statement**: 动态分配区域 (VLA/alloca) 必须位于 canary 的 "栈低端" 侧, 不能插在 canary 与 saved regs 之间; 否则可构造大小恰好的 dynamic alloc 越过 canary 直达 LR/FP (CVE-2023-4039 模型).
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 14+, LLVM 17+
- **target**: aarch64 (主要), x86_64 (对称)
- **source_kind**: bug-disclosure + source + mailing-list
- **source_url_or_path**: https://rtx.meta.security/mitigation/2023/09/12/CVE-2023-4039.html ; https://gcc.gnu.org/pipermail/gcc-patches/2023-September/630054.html ; `gcc/config/aarch64/aarch64.cc`
- **evidence_snippet**: patch 标题 "[PATCH 00/19] aarch64: Fix -fstack-protector issue" 以及 `aarch64.cc` 栈帧 ASCII 图.
- **version_sensitivity**: stable since fix
- **oracle_mapping**: canary oracle case 3 (`canary -> ret -> buf`) 即此条违反形式 (`@/home/yall/project/de-fuzz/docs/oracles/canary-oracle.md:33-37`).

### INV-SP-L04 — Spill area / 参数副本不得落在 canary 保护范围外

- **statement**: 跨调用活跃的参数副本、寄存器 spill 槽, 若被放在 "VLA/alloca 之上且在 canary 之下" (布局 (2) 的 "local variables (2)" 或 padding 区), 则小规模 VLA 溢出可先破坏这些副本, 再被后续 `memset` / `memcpy` 放大成覆盖 retaddr 的大溢出, 且 SIGSEGV 可能发生在 canary check 之前. 对 DeFuzz 而言, 这类现象应归入 hardening 缺陷 / oracle 假阳性边界, 不能直接当成经典 canary bypass.
- **compiler**: GCC, LLVM/Clang
- **version**: optimization-dependent
- **target**: generic (aarch64 上最易观察)
- **source_kind**: internals + calling-convention
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gccint/Stack-and-Calling.html ; `gcc/config/aarch64/aarch64.cc`
- **version_sensitivity**: likely-to-drift (依赖 RA 策略)
- **oracle_mapping**: 这是当前 oracle 已知 false positive / hardening bug 边界. 需要 sentinel (`SEED_RETURNED`) 区分 "函数内部间接崩溃 vs 返回时崩溃" (`@/home/yall/project/de-fuzz/docs/oracles/canary-oracle.md:245-285`).

### INV-SP-L05 — 多个 vulnerable 对象共享同一 canary 保护面

- **statement**: `add_stack_protection_conflicts` 让所有 `phase==1/2` 的对象与 canary slot 形成冲突图, 强制它们都排在 canary 的 "溢出源" 一侧; 因此一个函数内多个 char array / alloca / VLA 仍由同一 canary 保护.
- **compiler**: GCC
- **version**: GCC 4.9+
- **target**: generic
- **source_kind**: source
- **source_url_or_path**: `gcc/cfgexpand.cc` `add_stack_protection_conflicts`
- **version_sensitivity**: stable
- **oracle_mapping**: seed 模板 4 (mixed: fixed + VLA + ...) 覆盖此条 (`@/home/yall/project/de-fuzz/docs/oracles/canary-oracle.md:198-205`).

## 4. 寄存器与调用约定 (Register / Calling Convention)

### INV-SP-R01 — Canary guard 不驻留 caller-saved 寄存器

- **statement**: guard 值在 prologue 从 guard 来源加载后, 若函数中存在可能破坏该寄存器的调用, 则必须立即写入 canary slot; epilogue 校验时重新从 guard 来源加载做比较. 不允许在跨调用区间把 guard 保留在 caller-saved 寄存器中.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: generic
- **source_kind**: internals
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gccint/Stack-and-Calling.html ; `llvm/lib/CodeGen/StackProtector.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 此 invariant 通常在编译器内自动成立; oracle 主要通过 "end-to-end 是否触发 `__stack_chk_fail`" 间接覆盖.

### INV-SP-R02 — 跨调用参数副本应优先 callee-saved 寄存器

- **statement**: 对于 VLA/alloca 函数, 若参数 (尤其是用于后续 `memset/memcpy` 的 size 参数) 跨调用活跃, 将其保存到 callee-saved 寄存器 (AArch64 `x19-x28`; x86_64 `rbx/r12-r15`) 比溢出到紧邻动态分配区的栈 spill 更安全; 否则小溢出可能先污染参数副本并触发函数内部间接崩溃.
- **compiler**: GCC, LLVM/Clang
- **version**: optimization-dependent
- **target**: generic
- **source_kind**: internals + ABI-spec
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gccint/Stack-and-Calling.html ; https://github.com/ARM-software/abi-aa
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 当前不是强 invariant, 属 hardening-ideal; DeFuzz 把违反者标为 "security hardening 缺陷", 而非经典 canary 绕过.

### INV-SP-R03 — Epilogue 必须在 return 前 clobber canary 寄存器

- **statement**: 在 SP-protected 函数的 epilogue 中, 从 guard load (`__stack_chk_guard` 全局符号 / `%fs:0x28` / `TPIDR_ELn + offset` 等) 与 frame-saved canary 的 XOR / 比较得出 "校验通过" 之后, 到返回指令 (`ret` / `jr $ra` / `jirl $zero, $ra, 0` / `rts` 等) 之前, 任何 transiently 持有过 canary 值 (从 guard 加载得到, 或 XOR 中间值) 的 GPR 必须被显式覆写 (零写 / move-from-zero / 加载无关值). 否则函数返回后 caller-saved 寄存器中残留的 guard 值, 可被随后的寄存器暴露 primitive (caller 中的寄存器 dump 内联汇编 / 编译器注入的 spill-before-call / signal handler `mcontext_t` / side-channel / 调试 probe) 读出, 攻击者据此伪造任意函数的 canary, 静默绕过整个进程生命期内 `-fstack-protector{,-strong,-all}` 的保护. 注意: 此 invariant 与 INV-SP-R01 的区别是作用域 — R01 约束 *函数体内跨调用* 期间 guard 不得驻留 caller-saved 寄存器; R03 约束 *校验通过到返回指令* 之间持有过 canary 的寄存器必须 clobber.
- **compiler**: GCC (主要), LLVM/Clang (对应路径需独立审计)
- **version**: 历史已修 — PR-96191 (2020) 在 aarch64 / arm / thumb1 backend 引入 `stack_protect_test` define_insn 完成 scrub; GCC ≤ 16.1 仍未修复 `gcc/cfgexpand.cc::stack_protect_prologue` 与 `gcc/function.cc::stack_protect_epilogue` 的 generic fallback, 因此 mips / mips64 / loongarch64 / xtensa / csky 以及一长串未提供 backend template 的目标 (or1k / hppa / m68k / alpha / arc / nds32 / microblaze / visium / mcore / mmix / v850 / rx / rl78 / mn10300 / m32r / m32c / lm32 / cris / h8300 / frv / fr30 / ft32 / epiphany / stormy16 / iq2000 / moxie 等) 全部命中此条违反.
- **target**: 任意 backend 未提供 `targetm.have_stack_protect_set` / `targetm.have_stack_protect_test`, 落到 generic fallback 的 ISA.
- **source_kind**: source + bug-disclosure
- **source_url_or_path**: `gcc/cfgexpand.cc::stack_protect_prologue` (line 6951, generic fallback 用 `emit_move_insn`, 未 clobber source 寄存器) ; `gcc/function.cc::stack_protect_epilogue` (line 5070, 用 `emit_cmp_and_jump_insns`, 跳转后未 scrub 任何 GPR) ; `gcc/config/aarch64/aarch64.md` `stack_protect_test_<mode>` (line 8086, 参考实现, 显式清洗 temp 寄存器) ; https://gcc.gnu.org/bugzilla/show_bug.cgi?id=96191 (AArch64/Arm/Thumb1 历史修复) ; https://gcc.gnu.org/bugzilla/show_bug.cgi?id=125045 (meta-bug, per-target 子 bug 待提) ; `@/home/yall/project/defend-reviewer/findings/DREV-2026-001/README.md`
- **evidence_snippet**: PR-96191 报告 "the secret canary value is left in registers after the function returns"; aarch64.md `stack_protect_test_<mode>` 注释明确把 temp 寄存器清洗标为安全关键; DREV-2026-001 在本地 `gcc-16.1.0-RC-20260424` `loongarch64-linux-gnu` cross 上观测到 `$r12` 从 `__stack_chk_guard` 加载, 经 `bne $r13,$r12,.L5` 比较, 直到 `jr $r1` 之间无任何零写.
- **version_sensitivity**: likely-to-drift (随每个 backend 是否新增 `stack_protect_{set,test}` define_insn 而变; 长尾 backend 升级或新增 backend 都会改变命中范围). 对应到 oracle: 每次 GCC 主版本升级或新 ISA 接入都要重跑此条.
- **oracle_mapping**: **运行时 caller-saved 寄存器快照** (不走静态反汇编路径). 流程:
  (1) seed 模板对 `seed()` 加 `__attribute__((noinline, used))`, 强制保留独立 epilogue, 否则被内联后 caller-saved 残留无从观察;
  (2) `main` 标 `__attribute__((no_stack_protector))` (沿用现模板的 `NO_CANARY`), 整个函数体改写为单段 inline asm: 调用 `seed(...)`, 紧接着把 ISA-specific 的全部 caller-saved GPR `store` 到栈 buffer, 再调 `_exit` — 用 inline asm 包裹是为避免 C 编译器在 call 与快照之间插入任何 spill / reload / 临时寄存器使用;
  (3) C-side 在 inline asm 之外读 `extern uintptr_t __stack_chk_guard;` 取 ground truth, 逐一比对 buffer 内每个寄存器值; 任一相等即沿 stdout 输出 sentinel `GUARD_LEAKED reg=<idx>` 并非零退出, 全部不等输出 `CANARY_SCRUB_OK` 正常退出;
  (4) Oracle 仅解析 stdout: `GUARD_LEAKED` ⇒ `VerdictFail` (Detail 含 reg idx 与 ISA-specific 寄存器名); `CANARY_SCRUB_OK` ⇒ `VerdictPass`; 二者皆无 ⇒ `VerdictError`.
  每 ISA 仅需提供两件事: (a) caller-saved GPR 列表 (x86_64 SysV: `rax,rcx,rdx,rsi,rdi,r8-r11`; AArch64 AAPCS64: `x0-x18`; MIPS O32/N64: `$v0-$v1,$a0-$a3,$t0-$t9`; LoongArch64 LP64: `$r4-$r20` 等); (b) "把这些 GPR 写入栈 buffer 并调 `_exit`" 的 inline asm 序列. 跨 ISA 检测逻辑统一.
  已知 `VerdictNotApplicable`:
  - `seed()` 被内联进 `main` (用 `noinline` 抑制即可);
  - guard 被分配到 callee-saved 寄存器 — ABI 上在 ret 点已 restore, 不构成残留, 也即非本 invariant 所约束;
  - `-mstack-protector-guard=sysreg` (AArch64 / LoongArch sysreg 变体) 下 guard 不来自 `__stack_chk_guard` 符号, 需把 ground truth 改为对应 sysreg 直读, 第一版标 NA.
  不选静态反汇编路径的原因: 受影响 ISA 含 xtensa / csky / 多数长尾 backend, Capstone / `golang.org/x/arch` 覆盖不全, 等价于必须 shell-out `objdump` 文本解析, pattern 表跨 GCC peephole 易碎. 运行时方案直接观测安全契约 ("guard 是否残留进 caller-saved 寄存器"), 64-bit 随机 guard 偶然碰撞概率 ~2⁻⁶⁴, FP 可忽略.
  此条**不能**通过现有 `DynamicBufferSearchChecker` 间接覆盖 — 违反此条时 overflow 路径上 `__stack_chk_fail` 仍正确触发, 退出码仍为 134 (SIGABRT), 二分搜索会判 PASS, 形成 false-negative; 必须由独立的 dynamic checker 配合 seed 模板的 scrub mode 来发现.
- **implementation**: `@/home/yall/project/de-fuzz/internal/oracle/checker_dynamic_scrub.go` (`EpilogueCanaryScrubChecker`, 注册于 `(*CanaryOracle).mechanism()` 的 dynamic phase). seed 模板 scrub helper 见 `@/home/yall/project/de-fuzz/initial_seeds/x64/canary/function_template.c` `run_scrub_probe` (其余三个 ISA 同名函数). 触发方式: 同一份编译产物用 argv `scrub` 单独跑一次, 与 INV-SP-L01 的 `<buf_size> <fill_size>` 共用 binary. x86_64 用 `mov %fs:0x28` 直接读 TLS canary; aarch64 / riscv64 / loongarch64 通过弱声明的 `__stack_chk_guard` 全局符号读取 (sysreg 模式下符号缺失则发 `CANARY_SCRUB_NA reason=no_guard_symbol` → NA). Polarity-insensitive: `-fno-stack-protector` 下无 canary 加载即无可泄露, "无泄露" 仍是正确结论.

## 5. Guard 值来源 (Guard Source)

### INV-SP-G01 — 默认 guard 符号

- **statement**: 默认 guard 是外部变量 `__stack_chk_guard` (类型 `ptr_type_node`); 失败处理为调用 `__stack_chk_fail` (必须 `noreturn`). 这三件事由三个 target hook 决定: `TARGET_STACK_PROTECT_GUARD`, `TARGET_STACK_PROTECT_FAIL`, `TARGET_STACK_PROTECT_RUNTIME_ENABLED_P`.
- **compiler**: GCC, LLVM/Clang (符号名相同)
- **version**: all
- **target**: generic
- **source_kind**: internals
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gccint/Stack-Smashing-Protection.html ; `gcc/doc/tm.texi`
- **version_sensitivity**: stable
- **oracle_mapping**: `nm` / `objdump` 扫描 target 二进制, 存在 `__stack_chk_guard` / `__stack_chk_fail` 外部引用即基本确认 SP 被启用到链接期.

### INV-SP-G02 — x86_64 Linux 从 TLS 读 guard

- **statement**: x86_64 SysV: guard 从 `%fs:0x28` (TLS) 读取, 不经过 GOT. Windows x86_64: 从 TEB 偏移读取.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: x86_64 (Linux / Windows)
- **source_kind**: ABI-spec + source
- **source_url_or_path**: https://gitlab.com/x86-psABIs/x86-64-ABI ; `gcc/config/i386/i386.cc` (guard expand path)
- **version_sensitivity**: stable
- **oracle_mapping**: 反汇编 prologue 匹配 `mov %fs:0x28,%rax` 指令模式可确认 guard 来源.

### INV-SP-G03 — AArch64: `-mstack-protector-guard=sysreg` 使用 `TPIDR_ELn + offset`

- **statement**: AArch64 backend 的 `aarch64_stack_protect_canary_mem` 根据 `-mstack-protector-guard={global,sysreg}` 决定 guard 来自外部变量还是 `TPIDR_EL0 / TPIDR_EL1 / TPIDR_EL2 / TPIDR_EL3` + 指定偏移; 布局由 `-mstack-protector-guard-reg=` 与 `-mstack-protector-guard-offset=` 精确指定.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 9+, Clang 8+
- **target**: aarch64
- **source_kind**: source + user-doc
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc` `aarch64_stack_protect_canary_mem` ; GCC AArch64 Options 手册
- **version_sensitivity**: stable
- **oracle_mapping**: Linux kernel 构建 + `sysreg` 模式下, guard 来源在 `TPIDR_EL1 + off`; DeFuzz 仅需 case 化该选项, 不额外插桩.

### INV-SP-G04 — `_RUNTIME_ENABLED_P` hook 的延迟判定

- **statement**: 当 `TARGET_STACK_PROTECT_RUNTIME_ENABLED_P` 返回 false, 即便启用了 `-fstack-protector*`, 也不会发射 canary (用于某些 freestanding / kernel 变体). 这是 SP 可被 target 层延迟关掉的唯一合法路径.
- **compiler**: GCC
- **version**: all
- **target**: target-specific
- **source_kind**: internals
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gccint/Stack-Smashing-Protection.html
- **version_sensitivity**: stable
- **oracle_mapping**: 对 freestanding / `-ffreestanding` 配置, 不应假设 SP 生效.

## 6. 运行时与 libc 契约 (Runtime Contract)

### INV-SP-F01 — `__stack_chk_fail` 语义

- **statement**: `__stack_chk_fail` 必须是 `noreturn`; glibc 实现通过 `__fortify_fail` / `__libc_message` 输出 "stack smashing detected" 后 `abort()`, 导致进程 exit code = 128+SIGABRT = 134.
- **compiler + runtime**: GCC + glibc, Clang + glibc / compiler-rt
- **version**: all
- **target**: generic (Linux/POSIX)
- **source_kind**: runtime
- **source_url_or_path**: https://sourceware.org/glibc/manual/latest/html_node/Source-Fortification.html ; glibc `debug/stack_chk_fail.c`
- **version_sensitivity**: stable
- **oracle_mapping**: canary oracle 以 `exit_code == 134` 作为 "canary 成功拦截" 的正向信号 (`@/home/yall/project/de-fuzz/docs/oracles/canary-oracle.md:275-285`).

### INV-SP-F02 — `__stack_chk_fail_local` 的静态链接 + PIC 要求

- **statement**: 静态链接到不导出 `__stack_chk_fail` 的 libc 或 `-fpic` 的场景下, 每个 DSO 必须内嵌一个 `__stack_chk_fail_local` thunk (由 `libgcc2.c` 提供), 否则链接失败.
- **compiler + runtime**: GCC + libgcc
- **version**: all
- **target**: generic
- **source_kind**: runtime
- **source_url_or_path**: `libgcc/libgcc2.c`
- **version_sensitivity**: stable
- **oracle_mapping**: 链接失败 / `__stack_chk_fail_local` 未定义错误视为配置级 bug, 不是 canary 绕过.

### INV-SP-F03 — libc 无 SP 支持时必须链 libssp

- **statement**: 若目标 libc 不提供 `__stack_chk_guard` / `__stack_chk_fail` 符号, 链接器必须显式连入 GCC 的 `libssp.a` / `libssp_nonshared.a`, 否则 `-fstack-protector*` 在链接期出现未定义引用.
- **compiler + runtime**: GCC + libssp
- **version**: all
- **target**: generic (主要是嵌入式 / 裸 libc 场景)
- **source_kind**: runtime
- **source_url_or_path**: https://github.com/gcc-mirror/gcc/tree/master/libssp
- **version_sensitivity**: stable
- **oracle_mapping**: 当 DeFuzz 用非 glibc target (e.g., musl 旧版, newlib) 时需配置 `-lssp`.

### INV-SP-F04 — guard 值在 fork / exec 后重新初始化

- **statement**: glibc 在每次 `execve` (及 dl 解释器初始化) 阶段重新写入 `__stack_chk_guard` (通常取 `AT_RANDOM` 的 16 字节); fork 子进程继承同一 guard 值 (与父进程相同), 因此"fork 后尝试爆破 guard" 的攻击假设只能依赖父进程暴露.
- **runtime**: glibc
- **version**: glibc 2.10+
- **target**: generic (Linux)
- **source_kind**: runtime
- **source_url_or_path**: glibc `csu/libc-start.c`, `sysdeps/unix/sysv/linux/dl-osinfo.h`
- **version_sensitivity**: stable
- **oracle_mapping**: canary oracle 的二分搜索假设 "同一进程内 guard 值稳定" — 依赖此条.

## 7. 属性与局部禁用 (Attributes)

### INV-SP-A01 — `no_stack_protector` 必须在函数级别彻底关闭 SP

- **statement**: `__attribute__((no_stack_protector))` 覆盖所有 `-fstack-protector{,-strong,-all}`, 该函数不插 canary, 不调 `__stack_chk_fail`.
- **compiler**: GCC, Clang
- **version**: GCC 11+, Clang 4+
- **target**: generic
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: stable
- **oracle_mapping**: canary oracle 的 `main` 用 `NO_CANARY` 宏保证 "caller 无 canary, 恶意最大化" (`@/home/yall/project/de-fuzz/docs/oracles/canary-oracle.md:99-108`).

### INV-SP-A02 — `stack_protect` 属性在 `-fstack-protector-explicit` 下必生效

- **statement**: 带 `__attribute__((stack_protect))` 的函数在 `-fstack-protector-explicit` (GCC) / `-fstack-protector` + 无 `no_stack_protector` (Clang) 下保证插入 canary, 独立于启发式.
- **compiler**: GCC, Clang
- **version**: GCC 4.9+, Clang 6+
- **target**: generic
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: stable
- **oracle_mapping**: 用于构造 "极简函数 + 属性" 的正控组.

### INV-SP-A03 — 属性与 `-fno-stack-protector` 的互斥解析

- **statement**: 全局 `-fno-stack-protector` + 函数级 `stack_protect` 属性: GCC / Clang 均保留函数级强制插桩; 全局 `-fstack-protector-*` + 函数级 `no_stack_protector`: 函数级关闭优先.
- **compiler**: GCC, Clang
- **version**: GCC 11+, Clang 4+
- **target**: generic
- **source_kind**: test
- **source_url_or_path**: `gcc/testsuite/g++.dg/no-stack-protector-attr*.C`
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz flag × attribute 笛卡尔组合的测试面由此条定义.

## 8. 链接与 DSO (Linking / Cross-DSO)

### INV-SP-X01 — `__stack_chk_guard` 跨 DSO 一致

- **statement**: 所有加载到同一进程的 DSO 共享同一个 `__stack_chk_guard` (glibc 在 dl startup 写入), 因此函数在 DSO A 中设置 canary 后跨 DSO 调用返回仍能校验.
- **runtime**: glibc + ld.so
- **version**: glibc 2.10+
- **target**: generic
- **source_kind**: runtime
- **source_url_or_path**: glibc `sysdeps/unix/sysv/linux/dl-osinfo.h`
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 不需要单独测试, 但当 seed 涉及 `dlopen` 时依赖此条.

### INV-SP-X02 — `-fstack-protector-strong` 与 LTO 的联动

- **statement**: SP 的启发式决策发生在 IR 生成阶段, LTO 不改变 "该函数是否有 canary" 的结果 (与 CFI 不同). 但 inline 后内联进来的 vulnerable 对象必须被合并到 caller 的冲突图, 以决定 caller 的 canary 保护面.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: generic
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/CodeGen/StackProtector.cpp` ; `gcc/cfgexpand.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: `-flto` 作为独立配置纳入 CFLAGS 矩阵; 预期行为与非 LTO 一致.

## 9. 已知回归 / CVE 关联 (Known Regressions)

### INV-SP-CVE-2023-4039 (AArch64, GCC ≤13.2)

- **statement**: CVE-2023-4039 修复前, AArch64 `-fstack-protector*` 在存在动态分配 (VLA/alloca) 时, saved regs 布局位于 locals 之下 (布局 1), 使得 canary 位于 LR/FP 之上但 "栈下方" 的溢出源 (dynamic alloc) 可直接越过 canary 触及 LR — 即上文 INV-SP-L01 的违反.
- **compiler**: GCC ≤13.2
- **target**: aarch64
- **source_kind**: bug-disclosure
- **source_url_or_path**: https://rtx.meta.security/mitigation/2023/09/12/CVE-2023-4039.html
- **oracle_mapping**: canary oracle case 3 `canary -> ret -> buf` 即 CVE 的观察形式 (`@/home/yall/project/de-fuzz/docs/oracles/canary-oracle.md:33-37`).

## 10. DeFuzz Canary Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-SP-L01 | `exit_code == 139` 且 sentinel 已打印 | fixed/VLA/alloca + memset 越界 |
| INV-SP-L02 | 同上 (AArch64) | VLA + memset, 对比 GCC ≤13.2 vs ≥14 |
| INV-SP-L03 | 同上 | alloca / VLA 大小可控 |
| INV-SP-L04 | `exit_code == 139` 但 sentinel 未打印 (假阳性 / hardening) | VLA + `memset(buf, _, fill_size)` 其中 `fill_size` 通过栈传递 |
| INV-SP-F01 | `exit_code == 134` | 任意溢出到 canary 槽 |
| INV-SP-H03 | 静态判定插桩存在 + `exit_code == 134` | alloca / VLA 小溢出 |
| INV-SP-A01 | main 不插 canary | `main` 标注 `no_stack_protector` |
| INV-SP-R03 | `main` 在 `seed()` 返回后立即用 inline asm 快照所有 caller-saved GPR; 任一等于 guard 运行时值则 stdout 含 `GUARD_LEAKED reg=<idx> name=<reg-name>` | `seed()` 经模板内 redeclaration 加 `noinline,used`; `main` 标 `no_stack_protector` 并多一条 argv `scrub` 分支调 `run_scrub_probe` (call→snapshot→C 比对→`_exit`). 实现: `EpilogueCanaryScrubChecker` (`@/home/yall/project/de-fuzz/internal/oracle/checker_dynamic_scrub.go`) + 4 ISA 模板 `run_scrub_probe`. ISA 覆盖: x64 (TLS `%fs:0x28`) / aarch64 / riscv64 / loongarch64. 受影响 ISA 中其余长尾 backend (mips / xtensa / csky / ...) 待 follow-up. 退出码 134 不可由动态二分发现, 必须 leak-snapshot 通道 |

## 11. 开放问题 / 未覆盖 invariants (Follow-ups)

- **LLVM `StackProtector.cpp` 启发式 vs GCC `cfgexpand.cc` 的精确差异**: 已知"等价但不一一对应", 但 8 字节边界、取址判定等细节需要对照 lit tests 逐条列出.
- **RISC-V / LoongArch64 的 canary 布局**: survey 给出 psABI 入口 (`riscv-non-isa/riscv-elf-psABI-doc`), 但当前还缺少各自后端的专门栈帧归纳, 需要后续补齐.
- **Clang `-mstack-protector-guard=tls` 系列 Linux kernel 专属 flag**: 内核场景下 guard 由 per-CPU 变量提供, invariant 与 user-space 不同.
- **`-fstrub` / `-fharden-control-flow-redundancy` 与 SP 的叠加效应**: survey 给出了入口, 但 SP 与 strub 边界上的栈擦除顺序尚未单独抽 invariant.
- **多线程下的 guard 重置**: 子线程是否共享 `__stack_chk_guard` 与 TLS `%fs:0x28` 的关系, 需要查 glibc `__pthread_initialize_minimal`.
- **`setjmp/longjmp` / C++ 异常通过 canary 边界时的 guard 校验路径**: LLVM SafeStack / SCS 文档已指出异常是已知泄漏点, SP 侧的对应行为需核.

## 12. 使用建议

- 新增配置 / 升级编译器版本时, 对 §1-§8 逐条回归; §9 的 CVE 作为正控组; §10 决定 oracle 报 bug 的阈值.
- 遇到 `exit_code == 139` 时必须先查 sentinel, 区分 INV-SP-L01 (真绕过) vs INV-SP-L04 (hardening 缺陷, 非经典 canary 绕过).
- 在 CI 里把每条 invariant 的 `version_sensitivity` 字段作为"该条多久回看一次"的依据: `likely-to-drift` 的每次升级都需要人工确认.
