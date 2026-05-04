# GCC / LLVM 防御机制 Invariant 信息源调研

## 目标与边界

本文回答一个问题: **如果要系统抽取 GCC 与 LLVM/Clang 中各类软件防御机制的 invariants, 应该到哪些一手信息源里看, 它们分别能回答什么.**

这里的 invariant 不只是"打开某个 flag 启用某个机制"这种显式语义, 还包括很容易漏掉的隐式约束:

- 栈帧布局约束 (canary / 返回地址 / saved register / VLA / spill area / dynamic alloc 之间的相对顺序).
- 不能跨调用驻留在 caller-saved 寄存器中的值 (canary 自身, 跨调用使用的参数副本).
- 某机制依赖的专用寄存器必须被保留 (SCS x18 / x3 / ssp, BTI/PAC keys).
- LTO / visibility / 跨 DSO / 异常处理 / unwind / setjmp / longjmp 等成立条件.
- `_FORTIFY_SOURCE` 依赖 `__builtin_object_size` / `__builtin_dynamic_object_size` 的退化条件.
- 编译器与 libc / runtime / linker / loader 之间的契约 (符号、节区、dynamic tag).
- ISA / ABI / 字节序列层面的约束 (例如 IBT `endbr` 字节模式不能出现在普通指令立即数中).

机制覆盖范围: stack canary, `_FORTIFY_SOURCE` / object size, `-fstack-clash-protection`, `-fstack-check`, `-fcf-protection` (Intel CET IBT/SHSTK), AArch64 BTI, AArch64 / arm64e Pointer Authentication, ShadowCallStack, CFI / KCFI, SafeStack, Sanitizers (ASan/HWASan/MSan/TSan/UBSan/DFSan/KASAN), `SanitizerCoverage`, `-fhardened`, `-fzero-call-used-regs`, `-fharden-control-flow-redundancy`, `-fstrub` 系列, `-ftrivial-auto-var-init`, Bounds Safety (`-fbounds-safety`), Structure Protection, RISC-V Zicfilp / Zicfiss.

## 表格列定义

| 列 | 含义 |
|---|---|
| 来源 | 来源标题或仓库内路径 |
| 类型 | user-doc / internals / source / runtime / test / mailing-list / RFC / paper / ABI-spec |
| 入口 | URL 或仓库路径 (源码以 GitHub mirror 主分支为基准) |
| 简介 | 1-2 句说明此来源是什么 |
| 覆盖机制 | 能给出 invariants 的机制名 (简写) |
| 可提供的 invariants 示例 | 该来源能直接抽取的 invariant 文字化举例 |

机制简写: SP=stack protector / canary; FORT=`_FORTIFY_SOURCE`; SCP=stack-clash protection; SCK=`-fstack-check`; CET=Intel CET (IBT+SHSTK); BTI=AArch64 BTI; PAC=Pointer Authentication; SCS=ShadowCallStack; CFI=Clang CFI; KCFI=Kernel CFI; SS=SafeStack; ASan/HWASan/MSan/TSan/UBSan/DFSan; SanCov=SanitizerCoverage; HARD=`-fhardened`; ZCUR=`-fzero-call-used-regs`; HCFR=`-fharden-control-flow-redundancy`; STRUB=`-fstrub`; AVI=`-ftrivial-auto-var-init`; BS=Bounds Safety; STRP=Structure Protection.

## GCC 信息源表

| 来源 | 类型 | 入口 | 简介 | 覆盖机制 | 可提供的 invariants 示例 |
|---|---|---|---|---|---|
| GCC 用户手册: Program Instrumentation Options | user-doc | https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html | 所有插桩/硬化 flag 的官方语义入口 | SP, FORT, SCP, SCK, CET, SCS, HCFR, ZCUR, STRUB, AVI, sanitizers | `-fstack-protector` 仅保护 alloca / >=8 字节 buffer 函数; "optimized-away 或寄存器分配的变量不被保护"; `-fhardened` 在 GNU/Linux 隐式开 `-D_FORTIFY_SOURCE=3 -ftrivial-auto-var-init=zero -fPIE -pie -Wl,-z,relro,-z,now -fstack-protector-strong -fstack-clash-protection -fcf-protection=full`; `-fstack-clash-protection` 一次只分配一页且立即触碰 |
| GCC 用户手册: Object Size Checking | user-doc | https://gcc.gnu.org/onlinedocs/gcc/Object-Size-Checking.html | `_FORTIFY_SOURCE` 与 `__builtin_object_size` / `__builtin_dynamic_object_size` 的语义 | FORT | 当 builtin 返回 `(size_t)-1` 时 fortify 检查退化为原版调用; level 1/2/3 与 builtin 选择对应; FORTIFY 不会拦截手写循环或非 libc 函数 |
| GCC 用户手册: x86 Options | user-doc | https://gcc.gnu.org/onlinedocs/gcc/x86-Options.html | x86/x86_64 target option, 含 `-fcf-protection`/`-mshstk`/`-mibt` | CET, ZCUR | `-fcf-protection` 必须在所有 TU/DSO 一致; `-mshstk` 需 CET-SS 硬件; `nocf_check` 属性可在函数级关 IBT |
| GCC 用户手册: AArch64 Options | user-doc | https://gcc.gnu.org/onlinedocs/gcc/AArch64-Options.html | AArch64 option, 含 `-mbranch-protection={bti,pac-ret,pac-ret+leaf,pac-ret+b-key,gcs}` | BTI, PAC, SCS | `pac-ret` 必须与所有调用方一致才安全; `bti` 对所有间接跳转目标加 `BTI c/j` landing pad; `-fsanitize=shadow-call-stack` 必须配 `-ffixed-x18` |
| GCC 用户手册: Common Function Attributes | user-doc | https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html | 局部启停防御机制的属性 | SP, CET, ZCUR, sanitizers, STRUB | `no_stack_protector` / `stack_protect` / `nocf_check` / `zero_call_used_regs` / `no_sanitize_*` / `strub(...)` / `no_split_stack` / `no_stack_limit` 的语义与互相冲突 |
| GCC Internals: Stack Smashing Protection | internals | https://gcc.gnu.org/onlinedocs/gccint/Stack-Smashing-Protection.html | `TARGET_STACK_PROTECT_GUARD` / `_FAIL` / `_RUNTIME_ENABLED_P` 三 hook 契约 | SP | 默认 guard 是外部变量 `__stack_chk_guard`, 类型必须是 `ptr_type_node`; 默认 fail 调用 `__stack_chk_fail`, 必须是 `noreturn`; target 可改 guard 来源 (例如 AArch64 SSP_SYSREG) |
| GCC Internals: Frame Layout | internals | https://gcc.gnu.org/onlinedocs/gccint/Frame-Layout.html | 通用栈帧 hook (`STARTING_FRAME_OFFSET`, `FRAME_GROWS_DOWNWARD` 等) | SP, SCP, SCS, PAC | 栈生长方向; locals / saved regs / args 的相对顺序; 所有 target-neutral 布局推理的基础 |
| GCC Internals: Stack and Calling | internals | https://gcc.gnu.org/onlinedocs/gccint/Stack-and-Calling.html | calling convention 总章 | SP, PAC, SCS, BTI, CET | "callee-saved vs caller-saved"; 跨调用值不能放 caller-saved 寄存器的根据 |
| `gcc/cfgexpand.cc` (canary slot 决策) | source | https://github.com/gcc-mirror/gcc/blob/master/gcc/cfgexpand.cc | RTL 展开时栈对象排序与 canary slot 插入: `stack_protect_classify_type`, `stack_protect_decl_phase`, `add_stack_protection_conflicts`, `expand_stack_vars` | SP | "canary 与所有 phase=1/2 的 vulnerable 对象冲突, 因此被强制紧靠保存寄存器" 的实现根据; `SPCT_HAS_LARGE_CHAR_ARRAY` / `HAS_SMALL_CHAR_ARRAY` / `HAS_AGGREGATE` / `HAS_FN_FRAME_ADDRESS` 启发式 |
| `gcc/explow.cc` (stack clash probing) | source | https://github.com/gcc-mirror/gcc/blob/master/gcc/explow.cc | `probe_stack_range`, `anti_adjust_stack_and_probe_stack_clash`, `compute_stack_clash_protection_loop_data` | SCP, SCK | "若两次相邻栈分配跨过守护页则视为 clash"; `STACK_CLASH_PROTECTION_PROBE_INTERVAL` 与 `STACK_CLASH_MIN_BYTES_OUTGOING_ARGS` 精确语义 |
| `gcc/config/aarch64/aarch64.cc` (canary 布局注释) | source | https://github.com/gcc-mirror/gcc/blob/master/gcc/config/aarch64/aarch64.cc | AArch64 backend, 含 prologue/epilogue 与 frame layout, 注释直接写出 invariant | SP, BTI, PAC, SCS, SCP | "When using stack smash protection, make sure that the canary slot comes between the locals and the saved registers. Otherwise, it would be possible for a carefully sized smash attack to change the saved registers (particularly LR and FP) without reaching the canary."; `aarch64_stack_protect_canary_mem` 决定 guard 来自 `__stack_chk_guard` 还是 sysreg + offset; SCS 需要 `-ffixed-x18` 否则报错 |
| `gcc/config/aarch64/aarch64.cc` (BTI/PAC) | source | 同上 | `aarch64_return_address_signing_enabled`, `aarch64_handle_*_branch_protection`, `gen_paciasp` / `gen_autiasp` / `gen_bti_c` / `gen_bti_j` | BTI, PAC | `bti c` 必须出现在所有间接调用目标 (函数头, switch 间接 jump 目标); `paciasp` 与对称 `autiasp` 必须同处 prologue/epilogue, 中间 LR 不可被覆写; `pac-ret+b-key` 与 `pac-ret` key 不可混链 |
| `gcc/config/aarch64/aarch64.cc` (SCS prologue/epilogue) | source | 同上 | `scs_push` / `scs_pop` 在 prologue/epilogue 的插入点 | SCS | "SCS 路径下 LR 的恢复路径与普通路径不同, 必须避开普通 epilogue 的 LR 恢复"; SCS 与 `x18` reserved 的硬性绑定 |
| `gcc/config/i386/i386.cc` (CET endbr/notrack) | source | https://github.com/gcc-mirror/gcc/blob/master/gcc/config/i386/i386.cc | `gen_nop_endbr`, IBT 入口插入逻辑, vararg/tailcall 与 IBT 交互 | CET, ZCUR | `-fcf-protection=branch` 在所有可被间接调用的函数入口和 setjmp 返回点插 `endbr64`; `notrack` 前缀仅用于已经过类型检查的间接调用; 直接调用不加 `endbr` |
| `gcc/config/i386/predicates.md` (`ix86_endbr_immediate_operand`) | source | https://github.com/gcc-mirror/gcc/blob/master/gcc/config/i386/predicates.md | 谓词识别立即数中是否含 `0xfa1e0ff3` (endbr64) / `0xfb1e0ff3` (endbr32) 字节序列, 含字节级移位匹配 | CET | **`endbr` 字节序列在 IBT 启用时不允许出现在指令流的常数立即数中, 必须强制走 constant pool / `.rodata`**; `legitimate_constant_p` / `legitimate_pic_constant_p` 都引用此谓词以拒绝该常数 |
| `gcc/config/i386/cet.h` | source | https://github.com/gcc-mirror/gcc/blob/master/gcc/config/i386/cet.h | CET 相关宏与 ELF note (`GNU_PROPERTY_X86_FEATURE_1_IBT`, `_SHSTK`) 发射 | CET | 输出对象必须带 `GNU_PROPERTY_X86_FEATURE_1_IBT` / `_SHSTK` 才会被 loader 启用 IBT/SHSTK; 缺一即整个映像降级 |
| `gcc/builtins.cc` + `gcc/tree-object-size.cc` | source | https://github.com/gcc-mirror/gcc/blob/master/gcc/tree-object-size.cc | `__builtin_object_size`, `__builtin_dynamic_object_size`, `expand_builtin___memcpy_chk` 等 `_chk` 路径 | FORT | `BOS_OBJECT_SIZE_TYPE` (0/1/2/3) 决定取最大/最小估计; 别名 / 复杂指针 / 外部返回值导致退化为 `-1` 或 `0`; `_chk` 变体在编译期可证安全时回写为非 `_chk` 调用 |
| `gcc/ipa-strub.cc` + `libgcc/strub.c` | source + runtime | https://github.com/gcc-mirror/gcc/blob/master/gcc/ipa-strub.cc | 栈擦除: IPA pass 拆函数为 wrapper + body, runtime 提供 `__strub_enter` / `__strub_leave` / `__strub_update` | STRUB | `at-calls` 改 ABI; `internal` 不改 ABI 但增加 wrapper; 模式间 callee-callable 关系决定哪些组合非法; 异常 unwind 经过 strub 边界时必须重新初始化 |
| `libgcc/hardcfr.c` | runtime | https://github.com/gcc-mirror/gcc/blob/master/libgcc/hardcfr.c | `__hardcfr_check` runtime, 与 `-fharden-control-flow-redundancy` 配合 | HCFR | 每个 BB 在执行时记录到 bitmap; 在 return / noreturn / 异常出口前对 CFG 进行集合一致性校验; `noreturn` 路径可能多次校验, `no-xthrow` 是默认 |
| `libgcc/libgcc2.c` (`__stack_chk_guard`, `__stack_chk_fail_local`) | runtime | https://github.com/gcc-mirror/gcc/blob/master/libgcc/libgcc2.c | 默认 SP runtime | SP | 用户态默认 guard 来自 libc / libgcc; 静态链接 + `-fpic` 时 thunk 必须定义在每个 DSO 内; aarch64 / 部分目标改由 sysreg/TLS 提供 guard 时 libgcc 不参与 |
| `libssp/` (备用 SP runtime) | runtime | https://github.com/gcc-mirror/gcc/tree/master/libssp | `libssp.a`/`libssp_nonshared.a` 给没有 fortify/SP 的 libc | SP, FORT | 若 libc 不提供 `__stack_chk_*` 与 `*_chk` 函数, 链接器必须连入 `libssp` 才能让 SP/FORTIFY 工作 |
| `libsanitizer/` (含 `README.gcc`) | runtime | https://github.com/gcc-mirror/gcc/tree/master/libsanitizer | GCC sanitizer runtime, 大部分从 LLVM `compiler-rt` 同步 | sanitizers | runtime 行为以 LLVM `compiler-rt` 为准; ASan shadow scale/offset、TSan trace buffer、UBSan handler 函数名都是来自 LLVM 的隐式契约 |
| `libvtv/` (vtable verify runtime) | runtime | https://github.com/gcc-mirror/gcc/tree/master/libvtv | `-fvtable-verify` runtime | CFI(GCC 特色) | `-fvtable-verify=std` / `=preinit` 决定校验时机; 全程序闭包要求所有 DSO 一同 vtv-build; 缺一即任意 vcall 失败 |
| `gcc/testsuite/gcc.dg/ssp-*.c`, `gcc.dg/fstack-protector-strong.c`, `g++.dg/no-stack-protector-attr*.C` | test | https://github.com/gcc-mirror/gcc/tree/master/gcc/testsuite/gcc.dg | 通用 SP 行为回归 | SP | 哪些 alloca / VLA / 数组 / 取址模式必须触发 protector; `no_stack_protector` 在哪些组合下生效 |
| `gcc/testsuite/gcc.target/aarch64/stack-protector-*.c` | test | https://github.com/gcc-mirror/gcc/tree/master/gcc/testsuite/gcc.target/aarch64 | AArch64 SP/SCS/PAC/BTI 回归 | SP, SCS, PAC, BTI | "guard 来自 sysreg + offset 的指令序列固定"; SCS 启用时 prologue/epilogue 必须含 `str x30,[x18],#8` / `ldr x30,[x18,#-8]!` |
| `gcc/testsuite/gcc.target/i386/cet-*.c` (含 `cet-notrack-*`, `cet-pr124366.c`) | test | https://github.com/gcc-mirror/gcc/tree/master/gcc/testsuite/gcc.target/i386 | x86 CET 回归 | CET | 可被间接调用函数入口必须 `endbr64`; `notrack` 仅出现在 ICF / 类型化间接调用; ICF 不得合并带 `nocf_check` 与不带的函数; cet-pr124366 等回归: 含 endbr 字节常数禁止入指令立即数 |
| `gcc/testsuite/c-c++-common/strub-*.c`, `c-c++-common/hardcfr-*.c` | test | https://github.com/gcc-mirror/gcc/tree/master/gcc/testsuite/c-c++-common | strub 与 HCFR 行为回归 | STRUB, HCFR | 不同 strub 模式下 wrapper 数量、ABI 与可调用性约束; HCFR 在异常路径与 returning calls 上的 check 数量 |
| `gcc/doc/tm.texi` (Stack Smashing Protection / strub hooks) | internals | https://github.com/gcc-mirror/gcc/blob/master/gcc/doc/tm.texi | target macro/hook 文档源 | SP, STRUB | guard / fail / runtime_enabled 钩子契约 (与 gccint 文档同步); strub 对 dynamic alloc 的 target 协助点 |
| GCC Bugzilla | mailing-list | https://gcc.gnu.org/bugzilla/ | 安全 bug 与回归讨论 | 全部 | 例: PR111703 / CVE-2023-4039 (AArch64 SP 与动态分配) 完整披露 canary 与 LR / saved regs 关系 invariant |
| `gcc-patches` 邮件列表 | mailing-list | https://gcc.gnu.org/pipermail/gcc-patches/ | patch + reviewer 讨论 | 全部 | "[PATCH 00/19] aarch64: Fix -fstack-protector issue" (2023-09) 明确写出 canary 与 LR 相对位置 invariant; 同类线程是 invariant 最强一手证据 |
| GCC ChangeLog (`gcc/ChangeLog-2020..2025`) | source | https://github.com/gcc-mirror/gcc/blob/master/gcc/ChangeLog-2024 | 列出每次提交动到的文件与 PR ID | 全部 | 用于"某 invariant 是何时引入"的反向追溯, 配合 `git blame` |
| `gcc.gnu.org/projects/cxx-abi/` (Itanium 镜像) | user-doc | https://gcc.gnu.org/projects/cxx-abi/ | C++ ABI 与 vtable 文档入口 | CFI/STRP/vtv | vtable 布局、address point 选择、member function pointer 表示, GCC/Clang 共用 |

## LLVM / Clang 信息源表

| 来源 | 类型 | 入口 | 简介 | 覆盖机制 | 可提供的 invariants 示例 |
|---|---|---|---|---|---|
| Clang Command Line Reference | user-doc | https://clang.llvm.org/docs/ClangCommandLineReference.html | 所有 driver flag 的官方语义入口 | 全部 | `-fsanitize=*`, `-fcf-protection=*`, `-mbranch-protection=*`, `-fstack-protector*`, `-fbounds-safety`, `-fzero-call-used-regs`, `-fstrict-flex-arrays` 的精确 spelling 与依赖关系 |
| Clang Attribute Reference | user-doc | https://clang.llvm.org/docs/AttributeReference.html | 函数/变量属性官方文档 | 全部 | `no_stack_protector`, `safebuffers`, `nocf_check`, `cfi_canonical_jump_table`, `zero_call_used_regs`, `no_sanitize(...)`, `__counted_by`, `__sized_by`, `lto_visibility_public`, `cfi_unchecked_callee` |
| Clang Language Extensions | user-doc | https://clang.llvm.org/docs/LanguageExtensions.html | builtin / 类型限定符 / feature-test | FORT, BS, PAC | `__builtin_object_size`, `__builtin_dynamic_object_size`, `pass_object_size`, `__ptrauth(...)` 限定符, `__has_feature(bounds_safety)`, `__builtin_get_vtable_pointer` |
| Clang docs: Control Flow Integrity | user-doc | https://clang.llvm.org/docs/ControlFlowIntegrity.html | CFI 用户文档 | CFI, KCFI | "除 `kcfi` 外所有 CFI 方案需要 `-flto` / `-flto=thin`"; `-fsanitize=cfi-{vcall,nvcall,derived-cast,unrelated-cast}` 必须配 `-fvisibility=hidden`; KCFI 不要求 LTO 但仅检查 ICall, 不破坏跨 DSO 函数指针等价 |
| Clang docs: CFI Design | internals | https://clang.llvm.org/docs/ControlFlowIntegrityDesign.html | CFI 内部设计文档 (位图、jump table、cross-DSO shadow) | CFI | type ID 计算; jump table 布局; cross-DSO shadow 在 loader 中维护; vtable interleaving 的对齐与 address point 约束 |
| Clang docs: LTO Visibility | user-doc | https://clang.llvm.org/docs/LTOVisibility.html | LTO visibility 推断规则 | CFI, STRP | 当 ABI 允许跨 linkage unit 抽象基类继承 (如 Windows COM) 时, 该基类必须显式标 `[[clang::lto_visibility_public]]`; clang-cl `/MT`/`/MTd` 隐式给 std/stdext 公开 LTO 可见性 |
| Clang docs: SafeStack | user-doc + internals | https://clang.llvm.org/docs/SafeStack.html | safe/unsafe stack 分离方案 | SS | safe stack 仅放返回地址、spill、寿命安全的局部; safe stack 指针不得逃出 (setjmp/longjmp / swapcontext / `__builtin_frame_address` / 异常处理 是已知泄漏点); 单独 SafeStack 不能阻止任意写攻击 |
| Clang docs: ShadowCallStack | user-doc + internals | https://clang.llvm.org/docs/ShadowCallStack.html | aarch64/RISC-V SCS 文档 | SCS | "AArch64 用 `x18`, RISC-V 软实现用 `x3 (gp)` 且需链接器 `--no-relax-gp`, 硬件实现用专用 `ssp`"; SCS 不保护 leaf 函数 (返回直接走 LR/RA); jmp_buf 仅存储 SCSReg 低位避免泄漏完整地址; runtime 必须配合 guard 区域 |
| Clang docs: Pointer Authentication | user-doc + internals + ABI | https://clang.llvm.org/docs/PointerAuthentication.html | PAC / arm64e 全套语义、ABI、攻击模型 | PAC | C function pointer 默认共享同一 signing schema, 易受 substitution attack, 应使用 `__ptrauth` + 常量 discriminator + address diversity; `memcpy` 兼容性禁止函数指针使用 address diversity; vtable / dynamic_cast / member function pointer 的 PAC schema; 攻击模型: substitution / signing oracle / authentication oracle |
| Clang docs: Bounds Safety | user-doc + internals | https://clang.llvm.org/docs/BoundsSafety.html | `-fbounds-safety` 编程模型 | BS | ABI-visible 指针默认 `__single`, 局部默认 `__bidi_indexable`; `__counted_by` 关联指针与 count 必须 paired assignment, 中间不可有副作用; `__sized_by` 上界推导依赖运行时 trap 保证 `ptr+size` 不溢出; `-fbounds-safety` 不允许"隐式宽指针逃逸到 ABI" |
| Clang docs: Bounds Safety Impl Plans | RFC | https://clang.llvm.org/docs/BoundsSafetyImplPlans.html | 实现计划与上游路线 | BS | 哪些约束在 Sema 阶段强制 (paired assignment, abi visibility); 哪些 trap 在 IR/CFG 优化中可被消除; 默认 trap 路径 |
| Clang docs: Structure Protection | RFC + user-doc | https://clang.llvm.org/docs/StructureProtection.html | 实验性 UAF / type-safety 缓解 | STRP | 类型字段 reordering 与 deactivation symbols; 与 LTO visibility / dynamic loading 兼容矩阵 |
| Clang docs: AddressSanitizer 等系列 | user-doc + internals | https://clang.llvm.org/docs/AddressSanitizer.html | 各 sanitizer 用户与设计文档 | sanitizers | shadow 倍率/偏移/poison 颜色; `__asan_*` runtime 入口符号; UBSan trap mode (`-fsanitize-trap=...`) 与 handler 模式区别; 与 `-fno-omit-frame-pointer` / `-fno-optimize-sibling-calls` 的交互 |
| Clang docs: SanitizerCoverage | user-doc + internals | https://clang.llvm.org/docs/SanitizerCoverage.html | 覆盖率插桩接口 | SanCov | `__sanitizer_cov_trace_*` 系列回调签名是 ABI 级契约; 与 fuzzer (libFuzzer / AFL++) 的 coverage feedback 协议 |
| Clang docs: SafeBuffers | user-doc | https://clang.llvm.org/docs/SafeBuffers.html | C++ Safe Buffers Hardening 模型 | BS-like | `[[clang::unsafe_buffer_usage]]` 注解; 容器替换 (`std::array` / `std::span`) 自动建议; opt-out 边界 |
| LLVM docs: Type Metadata | internals | https://llvm.org/docs/TypeMetadata.html | CFI / WPD / virtual call 的 type-id 元数据 | CFI, KCFI, devirt | "type id" 在 LTO 时跨 module 唯一; vtable 的 type metadata 必须保留至 link 时, 否则 CFI 失败 |
| LLVM docs: Stack Maps + Patchpoints | internals | https://llvm.org/docs/StackMaps.html | stackmap intrinsic | SP, CFI, KCFI | 在 IR 层标记必须保留的活变量信息, 影响 SP slot 选择 |
| `llvm/lib/CodeGen/StackProtector.cpp` | source | https://github.com/llvm/llvm-project/blob/main/llvm/lib/CodeGen/StackProtector.cpp | 插入 protector 的 IR pass: 启发式 (alloca 大小、struct含数组、addr-taken alloca)、与 PrologEpilogInserter 接口 | SP | "stored value is checked upon exiting the block; if changed, abort" (头部注释); alloca 启发式与 GCC `cfgexpand` 不一一对应但等价; 决定 canary slot 紧贴保存寄存器 |
| `llvm/lib/Transforms/Instrumentation/{AddressSanitizer,HWAddressSanitizer,MemorySanitizer,ThreadSanitizer,DataFlowSanitizer,SanitizerCoverage,BoundsChecking,KCFI,CFGuard}.cpp` | source | https://github.com/llvm/llvm-project/tree/main/llvm/lib/Transforms/Instrumentation | 各 sanitizer / KCFI / BoundsChecking / SanCov 的 IR pass | sanitizers, KCFI, SanCov | 哪些访问被插桩 (例如跳过 `safebuffers` 函数); 哪些指令保留为 trap; KCFI 元数据写入 `kcfi_type` 与 prologue 校验序列 |
| `llvm/lib/Target/AArch64/` (`AArch64FrameLowering.cpp`, `AArch64MachineFunctionInfo.h`, `AArch64BranchTargets.cpp`, `AArch64PointerAuth.cpp`) | source | https://github.com/llvm/llvm-project/tree/main/llvm/lib/Target/AArch64 | AArch64 后端 BTI/PAC/SCS lowering | BTI, PAC, SCS, SP | "PAC `paciasp/autiasp` 必须在 LR 仍可信的窗口内匹配"; "SCS push/pop 与 unwind 需要 `.cfi_*` 注册"; "BTI landing pad 在所有 indirect target / setjmp 返回点" |
| `llvm/lib/Target/X86/` (`X86IndirectBranchTracking.cpp`, etc.) | source | https://github.com/llvm/llvm-project/tree/main/llvm/lib/Target/X86 | x86 CET / SHSTK / IBT lowering | CET | `endbr` 插入位置 (函数入口、`setjmp` 回点、可被间接调用 BB); SHSTK 与 alloca / `__builtin_setjmp` 交互 |
| `llvm/lib/Target/RISCV/` (Zicfilp / Zicfiss / SCS) | source | https://github.com/llvm/llvm-project/tree/main/llvm/lib/Target/RISCV | RISC-V 控制流强化 lowering | SCS, CFI(硬件) | Zicfilp landing pad (`lpad`) 插入规则; Zicfiss `sspush/sspop` 与 `ssp` 寄存器规则; SCS 软实现使用 `gp/x3`, 链接器需 `--no-relax-gp` |
| `clang/include/clang/Driver/Options.td` | source | https://github.com/llvm/llvm-project/blob/main/clang/include/clang/Driver/Options.td | 所有 driver option 的声明源 (含 group / alias / dependency) | 全部 | option spelling 与默认值; 哪些 flag 互斥; target-specific 限制 |
| `clang/include/clang/Basic/{Sanitizers.def,Attr.td,AttrDocs.td}` | source | https://github.com/llvm/llvm-project/tree/main/clang/include/clang/Basic | sanitizer kind 表与属性表 | sanitizers, 全部 | 各 sanitizer flag 的 group 关系; 属性参数与适用 decl 类别 |
| `clang/lib/CodeGen/{CGStackProtector.cpp,CGCall.cpp,CGCXXABI.cpp,CGPointerAuth.cpp}` | source | https://github.com/llvm/llvm-project/tree/main/clang/lib/CodeGen | 前端 IR 生成: SP 调度、call 插桩、C++ ABI lowering、PAC 限定符下沉 | SP, CFI, PAC | 前端如何把 `__attribute__((no_stack_protector))` 翻成 IR `nossp`; vtable 与 `cfi-vcall` 元数据生成; PAC 限定符下到 IR ptrauth intrinsic |
| `clang/lib/Sema/SemaBoundsSafety*.cpp` | source | https://github.com/llvm/llvm-project/tree/main/clang/lib/Sema | Bounds Safety 前端检查 | BS | paired assignment 检查; ABI-visibility 与 `__counted_by` 的兼容性; cast 规则 |
| `compiler-rt/lib/{asan,hwasan,msan,tsan,ubsan,cfi,safestack,shadowcallstack,sanitizer_common}` | runtime | https://github.com/llvm/llvm-project/tree/main/compiler-rt/lib | 各 sanitizer / cfi / safestack / scs runtime | sanitizers, CFI, SS, SCS | shadow memory 布局; trap handler 接口; cross-DSO CFI shadow 由 loader 协助维护; SCS guard region allocation 与 thread exit 释放 |
| `llvm/test/Transforms/{StackProtector,SafeStack,KCFI,CrossDSOCFI,BoundsChecking}` | test | https://github.com/llvm/llvm-project/tree/main/llvm/test/Transforms | IR 级 lit tests | SP, SS, KCFI, CFI, BS-runtime | 哪些 IR 构造触发 protector / SafeStack 拷贝; KCFI hash 在 prologue 的字节序列; cross-DSO CFI slow path |
| `clang/test/CodeGen/`, `clang/test/Sema/` | test | https://github.com/llvm/llvm-project/tree/main/clang/test | 前端到 IR 的 lit tests | 全部 | Sema 阶段对 `__counted_by` 与 paired assignment 的报错; 属性继承; `safebuffers` 关闭 sanitizers 的位置 |
| `compiler-rt/test/{cfi,safestack,shadowcallstack,asan,hwasan,ubsan}` | test | https://github.com/llvm/llvm-project/tree/main/compiler-rt/test | runtime 行为 lit tests | CFI, SS, SCS, sanitizers | "X 应触发 X-trap" 的正例; "在 Y 配置下不报"的反例; cross-DSO 行为 |
| LLVM Discourse RFC: `-fbounds-safety` | RFC | https://discourse.llvm.org/t/rfc-enforcing-bounds-safety-in-c-fbounds-safety/70854 | Bounds Safety RFC 主线程 | BS | 设计动机; ABI visibility 默认值; 与 Checked C 的差异; trap policy |
| LLVM Discourse RFC: KCFI / Structure Protection / arity-aware FineIBT | RFC | https://discourse.llvm.org/c/clang/6 | LLVM RFC 索引 | KCFI, STRP, CET | KCFI 设计选型; FineIBT arity 编码协议; Structure Protection 阶段化引入策略 |
| LLVM GitHub Issues / PRs | mailing-list | https://github.com/llvm/llvm-project/issues | 缺陷与设计争议 | 全部 | 用于追溯某 invariant 何时引入或被弱化; 例: SCS x86_64 因性能/安全缺陷在 LLVM 9.0 移除 (`Clang docs: ShadowCallStack` 引文) |
| LLVM `git blame` / Phabricator 历史 | source | GitHub blame UI | 老特性的设计讨论 | 全部 | 老 CFI / SafeStack / PAC 设计仍可在 Phabricator 归档定位 |

## 跨编译器外部规范 / 论文 / libc 信息源

下表中的来源是 GCC 与 LLVM/Clang 共同的一手依赖, 通常决定了"机制为什么必须满足这个 invariant", 编译器自身的文档无法替代.

| 来源 | 类型 | 入口 | 简介 | 覆盖机制 | 可提供的 invariants 示例 |
|---|---|---|---|---|---|
| Itanium C++ ABI | ABI-spec | https://itanium-cxx-abi.github.io/cxx-abi/abi.html | C++ ABI 规范, vtable / RTTI / member function pointer / `dynamic_cast` / 虚继承 | CFI, vtv, STRP, devirt | vtable layout、address point 与 secondary vtable 的精确偏移; member function pointer 是 `{ptr, this-adjustment}`; cross-DSO 类型识别依赖 mangled type name |
| AAPCS64 + AArch64 PAuth ABI | ABI-spec | https://github.com/ARM-software/abi-aa | AArch64 通用 ABI 与 PAuth/BTI/SCS 扩展 | PAC, BTI, SCS | `x18` 为 platform-reserved (Apple/Android 已用作 SCS 寄存器); `IA/IB/DA/DB/GA` 五把 PAC key 的语义; `paciasp/autiasp` 与 LR 的安全窗口规则; `BTI c/j/jc` 的指令地址分类 |
| Arm A-profile ARM (PAC, BTI, GCS) | ABI-spec | https://developer.arm.com/documentation/ddi0487/latest/ | Armv8/v9 architecture reference manual | PAC, BTI, GCS | PAC 的 hash 截断/key 数量/与 PAN 的关系; BTI 指令对间接跳转目标的硬件检查; GCS (Guarded Control Stack) 与 SCS 的硬件版本 |
| Intel CET specification | ABI-spec | https://software.intel.com/sites/default/files/managed/4d/2a/control-flow-enforcement-technology-preview.pdf | Intel CET (IBT + Shadow Stack) 架构 | CET | `endbr64`/`endbr32` 字节序列 (`F3 0F 1E FA` / `F3 0F 1E FB`) 与 NOP 兼容性; SHSTK 的 wrss/saveprev 语义; ELF GNU property (`GNU_PROPERTY_X86_FEATURE_1_IBT/_SHSTK`) 才是 loader 启用 CET 的触发条件 |
| RISC-V Zicfilp + Zicfiss | ABI-spec | https://github.com/riscv/riscv-cfi | RISC-V Control Flow Integrity 扩展 | SCS, CFI(硬件) | `lpad` 必须出现在所有间接跳转目标 (含 `jalr`); `sspush/sspop` 使用专用 `ssp` 寄存器, 不可被普通指令读写; 软 SCS 用 `gp/x3` 时链接器禁用 GP relaxation |
| RISC-V psABI | ABI-spec | https://github.com/riscv-non-isa/riscv-elf-psABI-doc | RISC-V ELF psABI | SCS, SP, sanitizers | `gp/x3` 与 `tp/x4` 的保留语义; `__stack_chk_guard` TLS 偏移; ELF 安全相关 dynamic tags |
| x86_64 psABI | ABI-spec | https://gitlab.com/x86-psABIs/x86-64-ABI | x86_64 SysV psABI | SP, CET, sanitizers | TLS 段中 `__stack_chk_guard` (Linux: `%fs:0x28` / Windows: TEB 偏移); `GNU_PROPERTY_X86_*` note 集合; red zone 与 SP/CET 交互 |
| GLIBC: Source Fortification | runtime + user-doc | https://sourceware.org/glibc/manual/latest/html_node/Source-Fortification.html | `_FORTIFY_SOURCE` 在 glibc 侧的实现契约 | FORT | 列出所有被 fortified 的 libc 函数 (`memcpy/strcpy/printf/...`); 命名约定 `__<name>_chk` / `__<name>_chk_warn` / `__<name>_2`; runtime 失败一律 `SIGABRT`; level 1/2/3 与 `__builtin_(dynamic_)object_size` 的对应 |
| GLIBC: `bits/string_fortified.h`, `bits/stdio2.h` 等头 | runtime | glibc 源码树 `string/bits/`, `stdio-common/bits/` | fortify wrapper 实际定义 | FORT | 每个 wrapper 的退化条件 (`__bos`/`__bos0` 计算结果); `__warn_or_error_decl` 何时触发编译期警告而非 runtime |
| GLIBC: `_FORTIFY_SOURCE` runtime (`debug/*_chk.c`) | runtime | glibc `debug/` 子目录 | `__chk_fail` 与各 `_chk` 实体 | FORT | `__chk_fail` 通过 `abort` 触发 SIGABRT; 错误信息格式 ("buffer overflow detected"); 与 `__libc_message` 的耦合 |
| Linux kernel: `Documentation/security/self-protection.rst`, `Documentation/x86/cet.rst` 等 | user-doc | https://www.kernel.org/doc/html/latest/security/self-protection.html | 内核侧防御机制清单 | KCFI, SCS, CET, SP | KCFI 在内核的 thunk 协议; CET 仅启用于 user-space 时的影响; 内核 SCS 与 PCPU 的耦合 |
| Linux kernel: `objtool` | source | https://github.com/torvalds/linux/tree/master/tools/objtool | 内核构建期校验 IBT/SHSTK/SCS/`__noreturn` 等 | CET, SCS, KCFI | 校验所有间接跳转目标存在 `endbr` 标记; 检查 `__nocfi` 函数被正确豁免; 这是"endbr 字节不能误入指令" invariant 的另一执行点 |
| CVE-2023-4039 公开分析 | bug-disclosure | https://rtx.meta.security/mitigation/2023/09/12/CVE-2023-4039.html, https://gcc.gnu.org/pipermail/gcc-patches/2023-September/630054.html | AArch64 SP 对动态分配 (VLA/alloca) 失效 | SP | "canary 必须比所有 vulnerable allocations 更靠近 saved registers"; "动态分配区不能放在 saved regs 与 canary 之间"; 这条 invariant 在补丁里被显式补齐 |
| Abadi 等: "Control-Flow Integrity" (CCS 2005) | paper | https://users.soe.ucsc.edu/~abadi/Papers/cfi-tissec-revised.pdf | CFI 原理 | CFI, KCFI | 指出"等价类"匹配是最弱的 CFI; 给出"两 ID 编码"上限性结论 |
| Tice 等: "Enforcing Forward-Edge CFI in GCC & LLVM" (USENIX Sec 2014) | paper | https://www.usenix.org/system/files/conference/usenixsecurity14/sec14-paper-tice.pdf | LLVM/GCC 早期前向边 CFI 设计 | CFI, vtv | LLVM 与 GCC 实现差异; 与 type-id 的耦合 |
| Kuznetsov 等: "Code-Pointer Integrity" (OSDI 2014) | paper | https://dslab.epfl.ch/pubs/cpi.pdf | CPI / SafeStack 理论基础 | SS | safe region 假设; setjmp/longjmp / 异常的逃逸路径; CPI vs SafeStack 的覆盖差异 |
| Necula 等 (Deputy / SafeC) + N2778 | paper + standard | https://people.eecs.berkeley.edu/~necula/Papers/deputy-esop07.pdf, https://open-std.org/jtc1/sc22/wg14/www/docs/n2778.pdf | 早期 C bounds-safety 设计与 ISO C 标准提案 | BS | flexible array / `[N]` 数组 / `__counted_by` 的语义来源; ISO C 提案中 `[[counted_by]]` 形式 |
| Pewny 等: "Pointer Authentication on ARMv8.3" (S&P 2019) 等 PAC 评测 | paper | https://arxiv.org/abs/2104.12188 等 | PAC 安全模型分析 | PAC | 截断 hash 的暴力空间; substitution / oracle 攻击的实测可行性; 用于校验 Clang PAC 文档中的攻击模型描述 |

## 验证: 已知锚点 invariant 与本表的覆盖关系

| 已知 invariant | 主证据来源 (本表行) | 备注 |
|---|---|---|
| AArch64 stack canary slot 必须在 locals 与 saved registers 之间, 否则 carefully sized smash 可绕过 | `gcc/config/aarch64/aarch64.cc` (canary 布局注释) + `gcc/cfgexpand.cc` + `gcc-patches` 邮件列表 + GCC Bugzilla + CVE-2023-4039 公开分析 | 注释原文已在 GCC 源码内核对; 补丁系列 [PATCH 00/19] 是历史动机来源 |
| `_FORTIFY_SOURCE` 在 `__builtin_object_size` 返回 `(size_t)-1` 时退化为非 `_chk` 调用 | GCC: Object Size Checking + `gcc/builtins.cc`/`gcc/tree-object-size.cc`; LLVM: Clang Language Extensions; libc: GLIBC Source Fortification + `bits/string_fortified.h` | 跨编译器 + libc 三方一致 |
| canary 不应跨调用驻留在 caller-saved 寄存器中, 跨调用使用的参数副本应避开会被相邻 buffer 覆盖的栈槽 | GCC: Stack and Calling internals + `gcc/cfgexpand.cc` + AArch64 backend + `gcc-patches`; LLVM: `llvm/lib/CodeGen/StackProtector.cpp` + AArch64/X86 backend | 实际是 calling convention + spill slot 选择共同决定的 invariant 簇 |
| Intel IBT 启用时, `endbr64`/`endbr32` 字节序列不能出现在普通指令的立即数中 (否则可被构造为伪 landing pad) | `gcc/config/i386/predicates.md` (`ix86_endbr_immediate_operand`) + `gcc/config/i386/i386.cc` 引用点 + `gcc/testsuite/gcc.target/i386/cet-pr124366.c`; Intel CET specification; Linux `objtool` | 字节模式 `0xfa1e0ff3`/`0xfb1e0ff3` 在源码内可定位; 字节级移位匹配是 GCC 该谓词的关键细节 |
| ShadowCallStack 必须保留专用寄存器 (AArch64 `x18` / RISC-V `gp` / `ssp`); 不能被未声明 reserved 的代码触碰 | Clang docs: ShadowCallStack + AAPCS64 + RISC-V Zicfilp/Zicfiss + GCC AArch64 backend (`-fsanitize=shadow-call-stack` 强制 `-ffixed-x18`) | 前端文档与 ABI 规范一致, backend 在编译期对违反者直接报错 |
| Clang CFI 除 `kcfi` 外都需要 `-flto` 与 `-fvisibility=hidden`, 否则隐式失效 | Clang docs: Control Flow Integrity + LTO Visibility + `llvm/lib/Transforms/Instrumentation/{KCFI,CFGuard}.cpp` + Itanium C++ ABI | 文档已显式写出, 实现层在 link 时由 metadata 强制 |

## 给 DeFuzz 的使用建议

### 抽 invariant 时优先级

1. 先在用户文档 (Clang docs / GCC online manual / gccint) 建立机制名、flag、属性命名空间.
2. 用 backend / pass 源码 (本表"source"行) 找到 target-specific 的真实约束, 以注释和判定逻辑为准.
3. 用 testsuite / lit tests (本表"test"行) 把 intent 落到"当前版本不能回退"的具体 codegen pattern.
4. 用 RFC / mailing-list / Bugzilla / CVE 分析 (本表"mailing-list" / "RFC" / "bug-disclosure"行) 拿到设计动机和历史动因.
5. 用 ABI / 论文 / libc (跨编译器表) 校验 invariant 的"为什么必须这样", 并发现编译器侧未文档化的隐式契约.

### 每条 invariant 推荐保存的字段

- `compiler` (GCC / LLVM / 共用 ABI)
- `version` (例: GCC 17.x trunk / LLVM 23 trunk / glibc 2.39)
- `mechanism` (用上面的简写)
- `target` (x86_64 / aarch64 / riscv64 / generic)
- `statement` (一句话表述)
- `source_kind` (来自本表"类型"列)
- `source_url_or_path` (来自"入口"列)
- `evidence_snippet` (源码注释 / 文档原文 / patch hunk)
- `version_sensitivity` (stable / target-specific / likely-to-drift)
- `oracle_mapping` (在 DeFuzz 里如果违反这条 invariant, 应该被哪个 oracle 抓到)

### 哪些场景必须下钻到源码 / ABI / testsuite, 用户文档不够

- 需要"栈帧布局"或"哪个寄存器在哪个位置".
- 需要"caller-saved / callee-saved 寄存器在该 target 上的精确分类".
- 需要"跨 DSO / LTO / exception / unwind / setjmp / longjmp"的成立条件.
- 需要"runtime 是否必须存在、由谁提供、失败如何 trap".
- 需要"某 target 是否例外", 例如 SCS 在 x86_64 已被 LLVM 移除.
- 需要"某 bug 修复后新增了什么隐含约束", 例如 CVE-2023-4039 之后的 AArch64 canary 布局.

### 一句话总结

- 对 GCC, 关键证据在 **backend 注释 + cfgexpand/explow + testsuite + `gcc-patches` + Bugzilla**.
- 对 LLVM/Clang, 关键证据在 **专门设计文档 + Transforms/Target pass + lit tests + Discourse RFC**.
- 跨编译器 invariant 的最终仲裁通常在 **Itanium C++ ABI / AAPCS64 PAuth / Intel CET / RISC-V CFI / glibc fortify** 这类外部规范.
