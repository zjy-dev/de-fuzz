# Intel CET-IBT (`endbr32` / `endbr64`) Invariants — Silent-Bypass 视角

> 本文只关心**会让 IBT 静默失效或被静默削弱**的不变量, 即"`-fcf-protection=branch`/`full` 已生效, GNU property 已置位, 进程已在内核 enforce, 但攻击者控制的间接跳转仍能落到非 endbr 指令而不触发 `#CP`, 或落到 endbr 但是攻击者构造出来的伪 landing pad". 形如 "`-fcf-protection=none` 是否真不发 endbr"、"`-mibt` 是否单独发 endbr"、"`-fhardened` 是否覆盖 IBT" 等问题, 后果或是机制更强或是配置级失败, 不在本文档范围内.
>
> 机制简写: **IBT** = Indirect Branch Tracking, Intel CET 的前向 CFI 子特性. SHSTK (shadow stack) 另文.
>
> 来源覆盖: GCC 主线, LLVM/Clang 主线, binutils, glibc ld.so, Linux kernel `objtool`, Intel SDM Vol.1 §18, x86-64 psABI GNU property, GCC Bugzilla / LLVM issue tracker, RAID 2023 FineIBT 论文及 LWN 后续披露.

## 0. 术语与坐标

- **landing pad**: 合法的间接跳转目标, 即 4 字节指令 `endbr64` (`F3 0F 1E FA`, x86_64) 或 `endbr32` (`F3 0F 1E FB`, i386).
- **CET tracker**: CPU 内部状态机. 任何间接 `call*`/`jmp*` 把状态从 `IDLE` 切到 `WAIT_FOR_ENDBRANCH`; 下一条指令若不是 `endbr` 则触发 `#CP(ENDBRANCH)`, Linux 用户空间表现为 `SIGSEGV` + `si_code = SEGV_CPERR`.
- **NOTRACK 前缀**: 单字节 `3E`, 加在 `jmp*/call*` 前, 告诉 CPU 该间接分支已经过编译器类型化, 跳过 landing pad 检查. 仅在 `IA32_U_CET.NO_TRACK_EN = 1` 时生效.
- **GNU property**: ELF `.note.gnu.property` 中的 `GNU_PROPERTY_X86_FEATURE_1_AND` 位图. ld.so 对所有 loaded object 的该位做 *AND 归并*, 任一缺失即整个映像降级.
- **FineIBT**: 在 `endbr64` 之后追加签名 hash 校验, 把 IBT 由 "可达即合法" 加强为 "签名匹配才合法". 有两条 silent-bypass 路径: hash 在 32-bit 内截断 → 碰撞; 内核 `apply_fineibt` patch 时机若错过, 整段函数仍是粗 IBT.
- **silent bypass**: 攻击者构造的 `call *reg / jmp *reg / ret to gadget` 完成跳转, CPU 不触发 `#CP`, 进程不收到 `SIGSEGV(SEGV_CPERR)`, 控制流已被转移. 这是本文所有不变量的统一威胁模型.

每条不变量按 [`README.md` §2](./README.md#2-survey-字段约定) 的字段记录: `ID / statement / compiler / version / target / source_kind / source_url_or_path / evidence_snippet / version_sensitivity / observation`.

## 1. Landing pad 必须覆盖每条间接可达边

布局类不变量的共同威胁: 攻击者把间接分支瞄向某个**应当但实际没有** `endbr` 的指令地址, CPU 不进入 `#CP`, 跳转生效.

### INV-IBT-P01 — 间接可达的函数入口必须以 `endbr` 开头

- **statement**: 在 `-fcf-protection=branch` / `full` 下, 编译器对 (a) 所有外部可见 (`.globl` / `weak` / 非 hidden static) 函数, (b) 所有被取地址的 static 函数, (c) 通过函数指针 / vtable / 回调进入的函数, 必须把 `endbr64` (x86_64) 或 `endbr32` (i386) 作为入口的第一条指令. 仅被直接 `call rel32` 调用且能在 IPA 阶段证明无任何取地址点的 static 函数可省略.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: source + bug-disclosure
- **source_url_or_path**: `gcc/config/i386/i386.cc` (`ix86_function_needs_cet_endbr_p`) ; `llvm/lib/Target/X86/X86IndirectBranchTracking.cpp` (`isIRBlockAddressTaken`) ; https://gcc.gnu.org/bugzilla/show_bug.cgi?id=102953 (CET/ENDBR 跟踪 PR)
- **evidence_snippet**: LLVM `X86IndirectBranchTracking.cpp` 顶部注释: 在每个 function entry / address-taken BB 插入 `ENDBR64`.
- **version_sensitivity**: stable at doc level, likely-to-drift at impl level (address-taken 启发式因版本而异)
- **observation**: 通过函数指针调用一个 *应当* 是间接可达但二进制内首指令非 `endbr64` 的函数; 进程未收到 `SIGSEGV(SEGV_CPERR)` 而正常进入函数体 ⇒ 不变量被违反.

### INV-IBT-P02 — `setjmp` / `__builtin_setjmp` 调用点的返回地址处必须有 `endbr`

- **statement**: `longjmp` 经由 `ret` 或 `jmp *reg` 间接跳回 `setjmp` 调用点, 因此 `setjmp` 调用之后紧邻的指令必须是 `endbr`, 否则 `longjmp` 后 CPU 仍处 `WAIT_FOR_ENDBRANCH` 状态, 下一条业务指令触发 `#CP`. 实现侧由 GCC `ix86_setjmp_endbr` 路径与 LLVM `X86IndirectBranchTracking` 在 setjmp 调用 site 之后强制插桩保证.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: source + test
- **source_url_or_path**: `gcc/config/i386/i386.cc` ; `gcc/testsuite/gcc.target/i386/cet-sjlj-*.c` ; `llvm/lib/Target/X86/X86IndirectBranchTracking.cpp`
- **version_sensitivity**: stable
- **observation**: 含 `setjmp/longjmp` 的程序在 IBT enforce 下 `longjmp` 返回正常执行下一条指令 ⇒ 校核 `setjmp` 调用点之后是否有 `endbr64`; 缺失即不变量被违反 (silent bypass).

### INV-IBT-P03 — C++ 异常 / cleanup 的 landing pad 必须以 `endbr` 开头

- **statement**: unwinder 通过 `_Unwind_RaiseException` → `__cxa_throw` 路径以间接跳转进入 `landingpad` BB, 该 BB 首指令必须是 `endbr`. LLVM 早期实现 (Clang 9 / 10 时间窗) 在 `-fexceptions -fcf-protection=branch` 组合下漏插, GCC 同期已正确发射, 该差异是公开的 silent bypass 路径.
- **compiler**: LLVM/Clang (历史回归), GCC (参考实现)
- **version**: LLVM 修复见 issue #44527 后续 commit; Clang ≤ 修复版本暴露
- **target**: i386, x86_64
- **source_kind**: bug-disclosure + source
- **source_url_or_path**: https://github.com/llvm/llvm-project/issues/44527 ; `llvm/lib/Target/X86/X86IndirectBranchTracking.cpp` ; `gcc/except.cc`
- **evidence_snippet**: issue #44527: clang 在 `-fexceptions -fcf-protection` 下不在 EH landing pad 发射 `endbr64`, GCC 发射.
- **version_sensitivity**: target-specific (修复后稳定)
- **observation**: 抛出并捕获 C++ 异常的二进制, 反汇编 `.text` 中由 `.gcc_except_table` 指向的 landing pad label 首指令非 `endbr64` ⇒ 不变量被违反; 攻击者若能伪造异常表项即可命中.

### INV-IBT-P04 — IFUNC resolver 与 PLT 解析回填点必须以 `endbr` 开头

- **statement**: GNU IFUNC (`__attribute__((ifunc(...)))`) 的 resolver 在 ld.so 启动时以 `call *reg` 形式间接调用; glibc `_dl_runtime_resolve*` / `_dl_fixup` 把延迟解析的 PLT entry 改写为间接跳到目标函数的 `endbr64`. resolver 入口与 `_dl_runtime_resolve` 入口都必须以 `endbr64` 开头, 否则 ld.so 自身的间接跳转链触发 `#CP`. 编译器对带 `ifunc` 属性的 C 函数自动插; 手写汇编 resolver (glibc `sysdeps/x86_64/multiarch/*.S` 的 `ENTRY` 宏) 由作者显式放 `endbr64`.
- **compiler + runtime**: GCC / Clang + glibc
- **version**: glibc ≥ 2.28
- **target**: i386, x86_64
- **source_kind**: runtime + source
- **source_url_or_path**: glibc `sysdeps/x86_64/dl-machine.h` ; glibc `sysdeps/x86/sysdep.h` (`ENTRY` 宏内置 `_CET_ENDBR`)
- **version_sensitivity**: stable
- **observation**: 手写汇编 IFUNC resolver 缺 `_CET_ENDBR` 宏即首字节非 `F3 0F 1E FA`, 在 IBT enforce 下进程启动即 `SIGSEGV(SEGV_CPERR)`; 反之若 ld.so 在解析阶段落入攻击者控制的非 `endbr` 地址而不报 `#CP` ⇒ silent bypass.

### INV-IBT-P05 — GCC 嵌套函数 trampoline 必须以 `endbr` 开头

- **statement**: GCC 嵌套函数 (`__attribute__((nested))` / GNU C nested function) 的 trampoline 由 runtime 在栈或可执行 heap 上动态生成, 调用者通过函数指针间接进入. `x86_output_trampoline_template` 在 `TARGET_IBT` 为真时把 `endbr64`/`endbr32` 作为模板第一条指令; 若该路径漏插, 任何对嵌套函数的间接调用都触发 `#CP`, 反之若编译器在 IBT enabled 下生成的 trampoline 仍以普通指令开头则属 silent bypass.
- **compiler**: GCC
- **version**: GCC 8+
- **target**: i386, x86_64
- **source_kind**: source
- **source_url_or_path**: `gcc/config/i386/i386.cc` (`x86_output_trampoline_template`)
- **version_sensitivity**: stable
- **observation**: 含嵌套函数 + 函数指针调用的二进制, dump 运行时 trampoline 的字节模板, 首四字节非 `F3 0F 1E FA` ⇒ 不变量被违反.

### INV-IBT-P06 — 调度器 / 优化 pass 不得把 `endbr` 与紧邻的间接分支目标地址解耦

- **statement**: 后端调度器、机器级优化 (machine outlining, branch folding, pre-RA scheduler) 不得把 `endbr` 移动到指令组之外, 也不得在 `endbr` 与"作为分支目标的下一条指令"之间插入指令. LLVM 通过把 `ENDBR` 标记为 scheduling boundary (D84862) 阻止此类位移; GCC 在 `i386.md` 把 `endbr` 包成不可调度的 `UNSPECV_NOP_ENDBR`.
- **compiler**: LLVM/Clang (修复见 D84862), GCC
- **version**: LLVM 11+ (D84862 提交于 2020-08-03 `rGf208c659fb76`), GCC 全程
- **target**: i386, x86_64
- **source_kind**: source + bug-disclosure
- **source_url_or_path**: https://reviews.llvm.org/D84862 ; `llvm/lib/Target/X86/X86InstrControl.td` (`ENDBR64` `isMeta` / scheduling barrier 标记) ; `gcc/config/i386/i386.md`
- **evidence_snippet**: D84862: *"Make ENDBR instruction a scheduling boundary"* — 调度器不再把指令越过 `ENDBR` 重排.
- **version_sensitivity**: stable since fix (target-specific 修复路径)
- **observation**: 反汇编中函数入口顺序为 `endbr64; <other>` 应原子保留; 若优化 pass 移动后变为 `<other>; endbr64; ...` 或在两者之间插入指令, 间接调用者跳到旧地址即落入 `<other>` 触发 `#CP` (粗 IBT 失效) 或继续执行而 endbr 在新位置失去意义 ⇒ 不变量被违反.

## 2. 字节模式: `endbr` 序列不得被攻击者复用

### INV-IBT-B01 — `endbr` 4 字节序列不得在任意字节对齐位置作为立即数 / displacement 出现在 `.text`

- **statement**: 由于 CPU 在 `WAIT_FOR_ENDBRANCH` 状态下仅做字节级匹配, 普通指令 (`mov imm`, `push imm`, `cmp imm`, PIC `lea`, `movabsq`) 的立即数若含字节序列 `F3 0F 1E FA` (endbr64) 或 `F3 0F 1E FB` (endbr32), 攻击者可把间接分支瞄向该立即数的字节偏移构造伪 landing pad. 编译器必须在所有可能写出立即数到 `.text` 的路径上拒绝该常数 (强制走 `.rodata`), 且检查须覆盖 4 字节序列的 *任意 1-byte 对齐移位* (8 种位置). 任何跳过的常数路径都是潜在 silent bypass.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+ (引入), 历经多版本补齐覆盖面
- **target**: i386, x86_64
- **source_kind**: source + test
- **source_url_or_path**: `gcc/config/i386/predicates.md` (`ix86_endbr_immediate_operand`) ; `gcc/config/i386/i386.cc` (`legitimate_constant_p`, `legitimate_pic_constant_p` 引用点) ; `gcc/testsuite/gcc.target/i386/cet-pr*` 用例集
- **evidence_snippet**: `predicates.md`: *"Match (CONST_INT) that would be rejected as ENDBR immediate when `-fcf-protection=branch` is on"* — 字节级移位检查匹配 8 种对齐.
- **version_sensitivity**: likely-to-drift (新指令 / 新常数路径若漏判即回归)
- **observation**: 编译产物 `.text` 中含字节模式 `F3 0F 1E FA` / `F3 0F 1E FB` 出现在非 `endbr` 指令位置 (如 `mov $imm,%reg` / `cmp $imm,%reg` 操作数所在字节), 而源代码包含该常数 ⇒ 不变量被违反; 攻击者可控制间接分支瞄向该字节偏移构造伪 landing pad.

## 3. NOTRACK 前缀必须限于编译器证类型安全的间接分支

### INV-IBT-N01 — `NOTRACK` 前缀不得用于攻击者可影响目标的间接调用 / 跳转

- **statement**: `NOTRACK` 前缀解除间接分支的 landing pad 检查, 仅可用于编译器 *已经* 通过 type-id / jump table layout 自身证类型安全的场景 (例如 switch 展开的紧邻 jump table). 若编译器对 *任意* 函数指针调用、虚函数调用、或不能证类型安全的 switch 也输出 `NOTRACK`, 整条间接分支链对 IBT 透明, 攻击者无需绕过任何 landing pad 即完成跳转. GCC PR 104816 公开了 `-fcf-protection=branch` 默认在 switch jump table 路径走 `NOTRACK` 的 silently-permissive 行为, 修复方式是引入 `-mcet-switch` 让用户显式选择 "为每个 switch case 发 endbr 而非给跳转加 NOTRACK"; **该选项至今不是默认**, 因此本不变量在不显式开 `-mcet-switch` 时仍是潜在 silent bypass 入口.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 13+ (`-mcet-switch` 文档化于 r13-744-g2f4f7de787); Clang 对应路径未提供等价 opt-in
- **target**: i386, x86_64
- **source_kind**: bug-disclosure + source
- **source_url_or_path**: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=104816 ; `gcc/config/i386/i386.cc` (`ix86_output_call_insn`, `notrack` 输出路径)
- **evidence_snippet**: PR 104816 状态 WAITING + commit `r13-744`: 引入 `-mcet-switch` 但仍非 default; switch 默认仍发 NOTRACK 间接跳转, 单 jump table 内攻击者若能改写表项即直接命中任意目标.
- **version_sensitivity**: likely-to-drift (策略层面, 默认值未来可能变更)
- **observation**: 反汇编 `.text` 中 `3E FF 24` (`jmp *(...)` 带 notrack) / `3E FF D0` (`call *%rax` 带 notrack) 出现在指向函数指针 / vtable / 非紧邻 jump table 的间接分支前 ⇒ 不变量被违反.

### INV-IBT-N02 — FineIBT 函数签名 hash 不得对不同 ABI 类型碰撞

- **statement**: FineIBT 在 `endbr64` 之后插入一段 "比较固定 hash 立即数, 不等则 trap" 序列, 把 IBT 由 "可达即合法" 加强为 "签名匹配才合法". 该 hash 在 32-bit 立即数空间内, 若两个不同 ABI 签名的函数被映射到同一 hash, 攻击者把间接调用瞄准 hash 等价的另一函数即可在 FineIBT 启用下完成不被 trap 的跳转. 公开披露见 LWN "A hole in FineIBT protection" 与后续 LSS-EU 增强讨论.
- **compiler + runtime**: LLVM/Clang (用户态 RFC), Linux kernel (`CONFIG_FINEIBT`, `apply_fineibt`)
- **version**: Linux 6.2+ (内核侧 merged), LLVM 用户态仍在 RFC
- **target**: x86_64
- **source_kind**: paper + bug-disclosure
- **source_url_or_path**: https://cs.brown.edu/~vpk/papers/fineibt.raid23.pdf (RAID 2023 原文) ; https://lwn.net/Articles/1011680/ (hash 碰撞披露) ; `arch/x86/kernel/alternative.c` (`apply_fineibt`)
- **version_sensitivity**: likely-to-drift (策略持续演进)
- **observation**: 在 FineIBT enforce 的内核镜像中, 通过有意识构造的 ABI-equivalent 函数对做间接调用替换, 调用未被 `__cfi_*` trap 拦截 ⇒ 不变量被违反. 普通 IBT (无 FineIBT) 不适用本条.

## 4. 进程级 IBT enforcement 不得被静默 AND-merge 关掉

### INV-IBT-M01 — Loaded DSO 缺 IBT property 时 ld.so 必须不静默关闭进程 IBT

- **statement**: glibc ld.so 在加载每个 DSO 时把其 `GNU_PROPERTY_X86_FEATURE_1_IBT` 位与进程当前状态做 AND. 任一 DSO 不带 IBT 即整个进程 IBT 降级 (`_dl_cet_check` 默认走 `permissive` 路径). 在 permissive 模式下, 主进程虽以 `-fcf-protection=full` 编译并落了 endbr, 但 `dlopen` 一个无 IBT 的老 DSO 后, 内核 `IA32_U_CET.ENDBR_EN` 会被关掉, 此后所有 endbr 都是 NOP, 整段进程对攻击者透明 — 这正是 silent bypass: 二进制里 endbr 仍在, GNU property 仍宣告 IBT, 但运行时不 enforce.
- **runtime**: glibc ld.so + Linux kernel
- **version**: glibc ≥ 2.28
- **target**: x86_64 (i386 同语义但发行版几乎不启)
- **source_kind**: runtime
- **source_url_or_path**: glibc `sysdeps/x86/dl-cet.c` (`_dl_cet_check`, tunable `glibc.cpu.x86_ibt`)
- **evidence_snippet**: tunable 默认 `permissive`: 缺 IBT property 的 DSO 被 dlopen 时, ld.so 调用 `arch_prctl(ARCH_SHSTK_DISABLE | ARCH_IBT_DISABLE)` 关闭进程级 enforcement 而不是拒绝加载.
- **version_sensitivity**: stable (tunable 名随 glibc 微调)
- **observation**: 一进程以 `-fcf-protection=full -Wl,-z,force-ibt` 编译并 `readelf -n` 显示 IBT property 置位, dlopen 一个无 IBT property 的 DSO 后, 通过 `arch_prctl(ARCH_CET_STATUS)` 读取或在 endbr-less 函数上做间接调用观察, 进程 IBT 已被关闭 ⇒ 不变量被违反 (silent bypass). 显式 `glibc.cpu.x86_ibt=on` 可阻止该降级, 是不变量持守的运行时配置点.

## 5. 已知 silent-bypass 案例

| Case | 违反的不变量 | Toolchain / 系统 | 说明 |
| --- | --- | --- | --- |
| LLVM issue #44527 | INV-IBT-P03 | Clang ≤ 修复版本 | `-fexceptions -fcf-protection=branch` 下 EH landing pad 不发 `endbr64`, GCC 发 — 异常路径间接跳入 landing pad 时无 landing pad 检查. |
| LLVM D84862 之前 | INV-IBT-P06 | LLVM ≤ 11 | 后端调度器把 `ENDBR` 与紧邻的间接分支目标解耦, 修复后将 `ENDBR` 标记为 scheduling boundary. |
| GCC PR 104816 | INV-IBT-N01 | GCC ≥ 8 (默认配置) | `-fcf-protection=branch` 默认对 switch jump table 间接跳转加 `NOTRACK`, 单 jump table 即可在 IBT 下完整绕过. 修复需显式 `-mcet-switch`. |
| FineIBT hash 碰撞 (LWN 2024) | INV-IBT-N02 | Linux ≥ 6.2 + `CONFIG_FINEIBT` | 32-bit hash 空间不够, 不同 ABI 签名间存在可被利用的碰撞, FineIBT trap 不触发. |
| glibc dl-cet.c permissive | INV-IBT-M01 | glibc ≥ 2.28 默认 | dlopen 无 IBT 的老 DSO 后 ld.so 静默关闭进程 IBT enforcement, 主程序的 endbr 退化为 NOP. |
| GCC PR 102953 | INV-IBT-P01 | GCC ≤ 修复版本 | CET/ENDBR 跟踪 PR, 涉及 `nocf_check` 诊断、direct call 绕 endbr 的 sym+4 优化等 ENDBR 发射边界条件. |

## 6. 可程序化筛选结果

> 筛选维度与静/动态归属准则定义在 [`README.md` §3 可程序化筛选方法论](./README.md#3-可程序化筛选方法论). 本章只列结果. 所有不变量均直接对应某条 silent-bypass 路径, 因此本机制下不再单列"非 silent-bypass 排除项".

### 通过筛选

- **INV-IBT-P01** — 间接可达函数入口必须 endbr
  - 类别: 静态
  - 通过理由: ELF 符号表 (`.dynsym`) + `.text` 反汇编可枚举所有 global / address-taken 函数入口, 比对首四字节是否为 `F3 0F 1E FA` 是确定信号.

- **INV-IBT-P02** — `setjmp` 调用点之后必须 endbr
  - 类别: 静态
  - 通过理由: 调用 `setjmp` / `_setjmp` 的指令位置静态可识别, 校验下一条指令字节即可.

- **INV-IBT-P03** — EH landing pad 必须 endbr
  - 类别: 静态
  - 通过理由: `.gcc_except_table` 指向的 landing pad label 静态可解析, 比对其首字节. 历史 LLVM 9-10 版本提供已知违反样本.

- **INV-IBT-P04** — IFUNC resolver / `_dl_runtime_resolve` 必须 endbr
  - 类别: 静态
  - 通过理由: ELF `.rela.dyn` / `.dynamic` 中的 IFUNC 项指向的 resolver 入口静态可解析; ld.so 内置入口需对 glibc 二进制反汇编验证, 可作为发行版级一次性核对.

- **INV-IBT-P05** — 嵌套函数 trampoline 必须 endbr
  - 类别: 动态
  - 通过理由: trampoline 字节模板在运行时被 `__builtin_init_trampoline` 写入栈或 mmap 的可执行页, 必须从运行进程内 dump. 静态二进制内的模板字节也可校核但需识别 GCC libgcc 版本.

- **INV-IBT-P06** — 调度器不得位移 endbr
  - 类别: 静态
  - 通过理由: 反汇编每个间接分支目标地址, 验证该地址正好落在 `endbr` 指令首字节而非紧邻的其他指令. LLVM D84862 之前的回归是已知样本.

- **INV-IBT-B01** — endbr 字节序列不得作为立即数出现在 `.text`
  - 类别: 静态
  - 通过理由: 在 `.text` 节上做字节模式扫描, 排除 `endbr` 指令本身的位置后剩余命中即不变量被违反; 8 种字节对齐移位独立扫描.

- **INV-IBT-N01** — NOTRACK 仅限编译器证类型安全的间接分支
  - 类别: 静态
  - 通过理由: 反汇编 `.text` 中前缀字节 `3E` + `FF /2` (`call *`) / `FF /4` (`jmp *`) 可枚举所有 NOTRACK 分支; 与该分支目标是否落在紧邻 jump table 区段交叉判定. PR 104816 默认行为可用作正反对照.

- **INV-IBT-N02** — FineIBT 签名 hash 不得碰撞
  - 类别: 静态
  - 通过理由: 反汇编 `__cfi_*` thunk 序列提取每个目标的 hash 立即数, 在符号集合内做去重, 命中即碰撞. 仅对启用 FineIBT 的内核镜像有意义.

- **INV-IBT-M01** — ld.so 不得静默关闭进程 IBT
  - 类别: 动态
  - 通过理由: 必须运行二进制 + dlopen 无 IBT 的 DSO, 通过 `arch_prctl(ARCH_CET_STATUS)` 或在 endbr-less 函数上做间接调用观察是否仍触发 `SEGV_CPERR`. 静态分析无法判定.

### 未通过筛选

本文档已按 silent-bypass 视角剔除非相关项. 历史档案中曾列出的 `-fcf-protection` 三档语义、`-mibt`/`-mshstk` ISA 位、`-fhardened` 隐含项、`endbr` 在 legacy CPU 上的 NOP 兼容性、`-fcf-protection=check` 编译期失败、`#CP` 在 Linux 表现为 `SEGV_CPERR` 的具体值、CET 与 SHSTK 启停独立性、legacy bitmap 配置接口等条目, 后果或为机制更强、或为编译/链接/启动失败、或为只读规范陈述, 不构成 silent bypass, 因此不再列入本文档的筛选范围.

## 7. 开放问题

- **LLVM 用户态 FineIBT RFC**: 内核已 merge, 用户态 RFC 尚未敲定; 一旦 Clang trunk 启用, INV-IBT-N02 的 oracle 需扩展到用户态字节模式.
- **`X86IndirectBranchTracking` 与 `ix86_function_needs_cet_endbr_p` 启发式差异**: 两者对 "static 函数取地址后被 inline 消除" / "computed goto 目标 BB" 的判定不一一对应, 需逐 lit-test 对照.
- **AMD Zen4+ 的 IBT 实现**: 行为与 Intel 一致, 但 AMD SDM 措辞略异; 上述不变量在 AMD CPU 上的 enforce 路径需独立核对 (尤其 `IA32_U_CET` 模型 MSR 兼容性).
- **`legacy_bitmap` 路径与 JIT (V8 / LuaJIT)**: 启用 legacy bitmap 后页内 endbr 检查完全关闭, 该页里所有 INV-IBT-P* 类不变量失效; DeFuzz 自检阶段需读 `ARCH_CET_STATUS` 跳过这些页.
- **32-bit 用户空间 `endbr32`**: 当前 Linux 发行版几乎不启 i386 用户态 CET, 编译器仍发射 `endbr32`, 但运行时不 enforce — 属于 "代码层不变量成立, 运行时不触发" 的特殊组合, 不算 silent bypass 但也无验证手段.
- **`_dl_runtime_resolve` 路径下 `nocf_check` 与 `endbr` 共存**: glibc 内部部分 ASM 路径用 `_CET_NOTRACK` 宏放过, 该路径若被攻击者影响 `link_map` 即等同 IBT 透明; 需对 glibc 主线 ABI 兼容性矩阵做单独审计.
