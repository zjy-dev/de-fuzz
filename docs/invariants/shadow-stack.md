# Intel CET Shadow Stack (SHSTK) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / LLVM/Clang / Intel SDM / ELF psABI / glibc / ld.so / Linux kernel 中与 **Intel CET Shadow Stack (SHSTK)** 直接相关的 invariants 统一抽取、归类, 作为 DeFuzz CET/SHSTK oracle 的形式化依据. IBT 部分见 `@/home/yall/project/de-fuzz/docs/invariants/endbr-ibt.md`.
>
> 机制简写与 survey 一致: **CET** = Intel Control-flow Enforcement Technology (IBT + Shadow Stack). 本文只覆盖 **SHSTK 分支**, 即返回地址影子栈相关.

## 0. 术语与坐标

- **SHSTK (Shadow Stack)**: Intel CET 的反向控制流完整性子特性. 启用后, 普通 `call` 指令在常规栈写入返回地址的同时, 把同一返回地址 *推入* 影子栈; `ret` 指令读 `[rsp]` 与影子栈栈顶比对, 不一致则 `#CP(NEAR-RET)` 异常.
- **SSP (Shadow Stack Pointer)**: 用户态由 `IA32_PL3_SSP` MSR 维护, 硬件上对用户不可直接读写, 通过 `RDSSPQ`/`RDSSPD` 读、`INCSSPQ`/`INCSSPD` 增 SSP, `SAVEPREVSSP` / `RSTORSSP` 维护跨线程保存恢复, `WRSS{Q,D}` (内核可启用) 或 `WRUSS{Q,D}` (内核态写用户 SSP) 用于初始化.
- **shadow stack page**: 影子栈所占内存页, 内核分配, 标记 `VM_SHADOW_STACK` (linux), 普通 `mov` 指令写入会触发 `#PF`, 仅 `call/ret/wrss*/incsspq` 等专用指令可改.
- **shadow stack token**: 影子栈段顶端写入的 64-bit token (含 SSP 自身值 + bit0 标志), 由 `RSTORSSP` 校验; 用于 `swapcontext` / 多线程切换语义.
- **SS (Supervisor Stack), `IA32_S_CET`, `IA32_PL{0,1,2,3}_SSP`**: 内核用; 用户态主要用 `PL3_SSP`. 本文以用户态为主, 内核 SHSTK 单列章节.
- **GNU property**: ELF note `NT_GNU_PROPERTY_TYPE_0` 中 `GNU_PROPERTY_X86_FEATURE_1_AND` 的 bit 1 (`SHSTK = 0x2`).
- **`#CP(NEAR-RET)`**: SHSTK 检测失败时的硬件异常子码 (1); 用户态在 Linux 翻译为 `SIGSEGV` + `si_code = SEGV_CPERR`.

每条 invariant 采用 survey 推荐字段: `ID / statement / compiler / version / target / source_kind / source_url_or_path / evidence_snippet / version_sensitivity / oracle_mapping`.

## 1. 启用条件 (Enablement)

### INV-SHSTK-E01 — `-fcf-protection={return,full}` 启用 SHSTK 代码生成

- **statement**: GCC / Clang 的 `-fcf-protection=return` 仅启用 SHSTK (不发射 `endbr`); `-fcf-protection=full` = `branch` + `return` 同时发射 `endbr` 与 SHSTK 元数据. 注: 启用 SHSTK 不需要编译器在每个函数 prologue/epilogue 显式插入 `incsspq` / `rdsspq`, 因为 SHSTK 由 *普通 `call` / `ret` 指令的硬件副作用* 实现; 编译器侧主要工作是在 `setjmp` / `longjmp` / `__builtin_setjmp` / 异常 unwind 等需手工 SSP 操作的路径插入 `incsspq` / `rstorssp` 等指令, 并在 ELF property 标记 `SHSTK`.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html ; https://gcc.gnu.org/onlinedocs/gcc/x86-Options.html ; https://clang.llvm.org/docs/ClangCommandLineReference.html
- **evidence_snippet**: GCC manual: *"`return` enables shadow stack built-in functions from `x86gprintrin.h`"*.
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz SHSTK oracle 把 `-fcf-protection=return` 与 `-fcf-protection=none` 作为正/反控组; 关键是 ELF property bit 与 unwinder 相关代码差异, 而非函数体每条指令.

### INV-SHSTK-E02 — `-mshstk` 仅开 ISA 特性, 不自动启用 SHSTK

- **statement**: `-mshstk` 仅告诉汇编器/编译器 *目标 CPU 能理解 `wrss`/`incsspq`/`rstorssp` 等 SHSTK 指令*, 不发射任何 SHSTK lowering, 也不影响 ELF property. 真正启用由 `-fcf-protection=return` (或 `=full`) 控制. `-mshstk` 缺失但 `-fcf-protection=return` 存在时, 编译器仍可生成 SHSTK 元数据 (因 SHSTK 本质依靠普通 `call/ret`); 仅当生成 `incsspq` 等显式指令时才需 `-mshstk`.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/x86-Options.html ; `gcc/config/i386/i386.opt`
- **version_sensitivity**: stable
- **oracle_mapping**: 反例 seed: `-mshstk` 但无 `-fcf-protection=return`, 应观察 ELF property 不含 `SHSTK` 位.

### INV-SHSTK-E03 — `-fhardened` (Linux GCC) 隐式开启 SHSTK

- **statement**: GCC `-fhardened` 隐式包含 `-fcf-protection=full`, 因此在支持 CET 的平台上自动启用 SHSTK 与 IBT.
- **compiler**: GCC
- **version**: GCC 14+
- **target**: i386, x86_64 (Linux)
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: `-fhardened` 与 `-fcf-protection=full` 应产生等价 SHSTK 元数据.

### INV-SHSTK-E04 — runtime enforcement 需要 CPU + kernel + ld.so 三者协同

- **statement**: SHSTK 在用户态实际生效要满足: (a) CPU 报告 `CPUID.(07H,0):ECX[bit 7]` 含 `CET_SS`; (b) 内核 (`CONFIG_X86_USER_SHADOW_STACK=y`, Linux ≥ 6.6) 通过 `arch_prctl(ARCH_SHSTK_ENABLE, ARCH_SHSTK_SHSTK)` 为进程分配并切换影子栈, 置位 `IA32_U_CET.SH_STK_EN`; (c) glibc ld.so 解析所有 loaded object 的 GNU property 后调用 `arch_prctl`. 任一缺失则即便编译期开了 SHSTK, `ret` 不做硬件比对, oracle 无信号.
- **runtime**: Linux kernel + glibc ld.so
- **version**: Linux ≥ 6.6, glibc ≥ 2.39 (用户态 SHSTK 默认可用)
- **target**: x86_64
- **source_kind**: user-doc + runtime
- **source_url_or_path**: https://www.kernel.org/doc/html/latest/x86/shstk.html ; glibc `sysdeps/x86/dl-cet.c` ; glibc `sysdeps/unix/sysv/linux/x86_64/__longjmp.S`
- **evidence_snippet**: kernel doc *"Linux supports user-mode shadow stack on x86_64. ... Enabled via `arch_prctl(ARCH_SHSTK_ENABLE, ARCH_SHSTK_SHSTK)`."*
- **version_sensitivity**: target-specific (内核版本 / 发行版策略)
- **oracle_mapping**: DeFuzz 启动 oracle 前读 `/proc/self/status` 的 `x86_Thread_features:` 行, 必须出现 `shstk`, 否则跳过 SHSTK 正确性断言, 避免假阴.

## 2. 指令编码与寄存器 (Encoding)

### INV-SHSTK-B01 — `call` / `ret` 是隐式 SHSTK 指令

- **statement**: SHSTK 启用后, `call` 指令在 `(rsp -= 8; *rsp = ret_addr)` 之外**额外**执行 `*ssp = ret_addr; ssp -= 8` (近调用) 或对应 32-bit 路径; `ret` 在普通取栈返回地址的同时执行 `ssp += 8; cmp [ssp - 8], ret_addr; if neq -> #CP(NEAR-RET)`. 编译器**不需**显式插桩, 但需保证 `ret` 前栈顶返回地址未被 *合法但非 SHSTK 协议* 的写法替换.
- **hardware**: Intel CET CPU
- **version**: all CET-capable CPUs (Tiger Lake +, Sapphire Rapids +, Zen4 +)
- **target**: i386, x86_64
- **source_kind**: ABI-spec
- **source_url_or_path**: Intel SDM Vol.1 §18.2 ; Intel CET specification
- **version_sensitivity**: stable
- **oracle_mapping**: SHSTK oracle 的根本信号: 对栈上返回地址做合法越权写, 期望 `ret` 时 `SIGSEGV` + `si_code == SEGV_CPERR`.

### INV-SHSTK-B02 — `incsspq` / `incsspd` 用于 unwinder 调整 SSP

- **statement**: `incsspq imm8` (x86_64) / `incsspd imm8` (i386) 把 SSP 上调 8*imm8 字节, 等同于"丢弃 imm8 个影子栈帧". 用于 C++ 异常 unwind / `longjmp` 跨多帧场景: unwinder 算出常规栈上跨过的帧数 N, 发 `incsspq N`. 该指令必须在 SHSTK 启用下执行, 否则在 `-fcf-protection=none` 进程中是 `#UD`.
- **compiler + runtime**: glibc / libgcc / libunwind
- **version**: glibc ≥ 2.28 (含 SHSTK longjmp), libgcc ≥ 11
- **target**: i386, x86_64
- **source_kind**: ABI-spec + source
- **source_url_or_path**: Intel SDM Vol.2 `INCSSP` ; libgcc `unwind-dw2.c` (`_Unwind_Frames_Extra`) ; glibc `__longjmp.S`
- **version_sensitivity**: stable
- **oracle_mapping**: 异常路径 seed 必须经 `incsspq` 调整, 缺失即 `ret` 报 `SEGV_CPERR`. oracle 用 `try/throw/catch` 跨多帧 seed.

### INV-SHSTK-B03 — `rdsspq` / `saveprevssp` / `rstorssp` 维护 SSP 持久化

- **statement**: `rdsspq reg` 读取当前 SSP 到普通寄存器 (是 SSP 唯一的合法读路径); `saveprevssp` 把当前 SSP 与 token 写到旧影子栈; `rstorssp [mem]` 切换到 mem 指向的影子栈段, 并在该段顶部留 token. 这三者构成 `swapcontext` / 用户级线程切换 / `setcontext` 的 SHSTK 协议.
- **compiler + runtime**: glibc, libgcc
- **version**: glibc ≥ 2.39
- **target**: i386, x86_64
- **source_kind**: ABI-spec + source
- **source_url_or_path**: Intel SDM Vol.2 `RDSSPQ`, `SAVEPREVSSP`, `RSTORSSP` ; glibc `sysdeps/unix/sysv/linux/x86_64/swapcontext.S`
- **version_sensitivity**: stable
- **oracle_mapping**: `swapcontext` / `makecontext` / `setcontext` 路径 seed.

### INV-SHSTK-B04 — `wrssq` 写影子栈, 默认在用户态非法

- **statement**: `wrssq reg, [mem]` 直接把 reg 写到 [mem] (mem 必须在 shadow stack 段中). 默认 *仅在 supervisor 启用* `IA32_S_CET.WR_SHSTK_EN = 1` 时合法; 用户态默认不可用. Linux 内核为支持 user-mode SHSTK 在用户进程的 `IA32_U_CET.WR_SHSTK_EN` 默认置 *0*, 因此用户程序无法直接 `wrssq` 修改自己的影子栈 — 这是 SHSTK 安全模型的基础.
- **hardware**: Intel CET CPU + Linux kernel
- **version**: Linux ≥ 6.6
- **target**: i386, x86_64
- **source_kind**: ABI-spec + runtime
- **source_url_or_path**: Intel SDM Vol.2 `WRSS` ; Linux `arch/x86/kernel/shstk.c`
- **version_sensitivity**: stable
- **oracle_mapping**: 反例 seed: 用户态 `wrssq` 必须 `#UD` (`SIGILL`); 若不报错则 SHSTK 已被某种方式开放写权 (调试 / 漏洞).

## 3. 编译器侧的代码改动 (Codegen)

### INV-SHSTK-C01 — `setjmp` / `longjmp` 必须 unwind 影子栈

- **statement**: `longjmp` 跨 N 个 C 调用帧时, 必须把影子栈也回滚 N 帧. glibc `__longjmp` 在 SHSTK 启用下, 先 `rdsspq` 取当前 SSP, 计算与 `jmp_buf` 中保存的 SSP 之差, 用循环 `incsspq 255 / incsspq imm8` 把 SSP 上调到目标位置, 再执行普通 `ret`. 失败 (例如 `setjmp` 之后 `jmp_buf` 被破坏) 表现为 `incsspq` 无法对齐到合法 token 时 `ret` 触发 `SEGV_CPERR`.
- **runtime**: glibc
- **version**: glibc ≥ 2.28 (CET path), 默认启用 ≥ 2.39
- **target**: i386, x86_64
- **source_kind**: source
- **source_url_or_path**: glibc `sysdeps/unix/sysv/linux/x86_64/__longjmp.S` ; glibc `sysdeps/unix/sysv/linux/x86_64/__longjmp_chk.S`
- **version_sensitivity**: stable
- **oracle_mapping**: `setjmp/longjmp` seed 在 SHSTK 启用下应正常返回; 故意篡改 `jmp_buf->ssp` 字段则期望 `SEGV_CPERR`.

### INV-SHSTK-C02 — `__builtin_setjmp` / `__builtin_longjmp` 同步 SSP

- **statement**: GCC 内置 `__builtin_setjmp` / `__builtin_longjmp` (用于 `tracking exceptions`, `pthread_cleanup_*`) 在 SHSTK 启用下展开必须保存 / 恢复 SSP. GCC `i386.cc` 的 `ix86_expand_builtin_setjmp_receiver` / `ix86_expand_builtin_longjmp` 在 `TARGET_SHSTK` 为真时插入 `rdsspq` / `incsspq` 指令.
- **compiler**: GCC
- **version**: GCC 8+
- **target**: i386, x86_64
- **source_kind**: source
- **source_url_or_path**: `gcc/config/i386/i386.cc` (`ix86_expand_builtin_longjmp`)
- **version_sensitivity**: stable
- **oracle_mapping**: GCC 嵌套异常 seed; LLVM 的 `intrinsic` 路径同理.

### INV-SHSTK-C03 — `__builtin_eh_return` 调整 SSP

- **statement**: `__builtin_eh_return` 用于 ABI 兼容的异常 unwind, 把控制流"魔法"地跳到 unwinder 计算的 PC. 在 SHSTK 下, libgcc `_Unwind_RaiseException` 的尾巴会发 `incsspq` 把影子栈与即将到达的栈帧对齐.
- **compiler + runtime**: GCC + libgcc
- **version**: libgcc ≥ 11
- **target**: i386, x86_64
- **source_kind**: source
- **source_url_or_path**: libgcc `unwind-dw2.c` ; `gcc/config/i386/i386.cc` (`ix86_expand_eh_return`)
- **version_sensitivity**: stable
- **oracle_mapping**: C++ 异常 seed.

### INV-SHSTK-C04 — `makecontext` 写入伪返回地址需 `wrss` (内核辅助)

- **statement**: `makecontext` 创建用户级协程时, 必须在新影子栈段顶部写入"协程入口的返回地址". 由于用户态默认 `WR_SHSTK_EN = 0`, glibc 通过 `map_shadow_stack(2)` 系统调用 (Linux ≥ 6.6) 让内核帮忙 `wrussq`, 然后 `rstorssp` 切换到该段. 编译器 / runtime 与内核之间的契约即"用户态自己不写影子栈, 由 syscall 协助".
- **runtime**: glibc + Linux kernel
- **version**: Linux ≥ 6.6, glibc ≥ 2.39
- **target**: x86_64
- **source_kind**: source + user-doc
- **source_url_or_path**: glibc `sysdeps/unix/sysv/linux/x86_64/makecontext.c` ; Linux `arch/x86/kernel/shstk.c` (`SYS_map_shadow_stack`)
- **version_sensitivity**: target-specific
- **oracle_mapping**: `makecontext + swapcontext + setcontext` 协程 seed.

## 4. 属性与局部控制 (Attributes)

### INV-SHSTK-A01 — `nocf_check` 不影响 SHSTK

- **statement**: `__attribute__((nocf_check))` 仅关闭 *该函数的 IBT* (`endbr` + `NOTRACK`), **不**关闭 SHSTK. SHSTK 由 `call/ret` 隐式驱动, 函数级属性无法关闭. 因此带 `nocf_check` 的函数仍受 SHSTK 保护.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明; 用于澄清 IBT/SHSTK 解耦.

### INV-SHSTK-A02 — `-fcf-protection=return` 与 `-fcf-protection=branch` 独立

- **statement**: 两个选项分别控制 SHSTK 与 IBT, ELF property bit 也独立. 应用程序可只开 SHSTK 而关 IBT, 反之亦然.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵化 CFLAGS 时两 bit 独立穷举.

## 5. ELF 元数据 (Metadata)

### INV-SHSTK-M01 — `GNU_PROPERTY_X86_FEATURE_1_SHSTK` 位必须存在

- **statement**: 启用 SHSTK 的 ELF 对象在 `.note.gnu.property` 段中置 `GNU_PROPERTY_X86_FEATURE_1_AND` 的 bit 1 (`SHSTK = 0x2`). ld.so 对所有 loaded object 的 SHSTK 位 AND 归并; 全部为 1 才启用进程级 SHSTK.
- **compiler + linker**: GCC / Clang + binutils / lld
- **version**: GCC 8+, Clang 7+, binutils ≥ 2.32
- **target**: i386, x86_64
- **source_kind**: ABI-spec + source
- **source_url_or_path**: https://gitlab.com/x86-psABIs/x86-64-ABI ; `gcc/config/i386/cet.h`
- **version_sensitivity**: stable
- **oracle_mapping**: `readelf -n` 含 `SHSTK` 字样作为外观校验.

### INV-SHSTK-M02 — `-Wl,-z,shstk` / `-z,force-shstk` 让链接器拒绝缺位

- **statement**: 链接器选项 `-z shstk` 启用 SHSTK 元数据, `-z force-shstk` 强制要求所有输入对象都带 `SHSTK` 位, 否则链接失败. 这是 "所有 TU 必须 `-fcf-protection=return`" 的链接期 enforcement.
- **linker**: binutils, lld
- **version**: binutils ≥ 2.32, lld ≥ 10
- **target**: i386, x86_64
- **source_kind**: user-doc
- **source_url_or_path**: binutils `ld` manual "X86 ELF Options"
- **version_sensitivity**: stable
- **oracle_mapping**: 构建系统加 `-z force-shstk` 作为 CI 前置门禁.

### INV-SHSTK-M03 — ld.so 按 AND 归并

- **statement**: ld.so 在加载每个 DSO 时把其 `FEATURE_1_SHSTK` 位与进程当前状态做 AND. `dlopen` 一个无 SHSTK 的 DSO 会让进程 SHSTK 关闭 (除 `glibc.cpu.x86_shstk` tunable 强制策略外).
- **runtime**: glibc ld.so
- **version**: glibc ≥ 2.28
- **target**: x86_64
- **source_kind**: runtime
- **source_url_or_path**: glibc `sysdeps/x86/dl-cet.c`
- **version_sensitivity**: stable
- **oracle_mapping**: `dlopen` 老旧 DSO 后, 期望 `arch_prctl(ARCH_SHSTK_STATUS)` 报告 SHSTK 已关.

## 6. 运行时语义 (Runtime Semantics)

### INV-SHSTK-R01 — `ret` 返回地址不一致触发 `#CP(NEAR-RET)`

- **statement**: SHSTK 启用下, `ret` 取出 `[rsp]` 与 `[ssp]` 比较 (硬件读); 不一致 -> `#CP` 异常子码 `NEAR-RET (0x1)`. Linux 翻译为 `SIGSEGV` + `si_code == SEGV_CPERR`. **这是 SHSTK 的根本 oracle 信号**.
- **hardware + runtime**: Intel CPU + Linux kernel
- **version**: all CET-capable
- **target**: i386, x86_64
- **source_kind**: ABI-spec + runtime
- **source_url_or_path**: Intel SDM Vol.3 §17 (异常向量 `#CP = vector 21`) ; Linux `arch/x86/kernel/traps.c` (`exc_control_protection`)
- **version_sensitivity**: stable
- **oracle_mapping**: 缓冲区溢出覆盖返回地址 + 普通栈失败 / canary 漏掉时, SHSTK 仍能拦截; oracle 期望 `SEGV_CPERR`.

### INV-SHSTK-R02 — `si_code == SEGV_CPERR` 与 IBT 共享

- **statement**: SHSTK 与 IBT 都用同一 `SEGV_CPERR` (10), siginfo 不直接区分. 区分需读取 CPU `IA32_U_CET.TRACKER` 或 fault 地址附近指令 (`call*` / `jmp*` / `ret`) 推断子原因. 实务上 oracle 把两者并入 "CET 类违例".
- **runtime**: Linux kernel + glibc
- **version**: Linux ≥ 5.18
- **source_kind**: runtime
- **source_url_or_path**: Linux `arch/x86/kernel/traps.c`
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 通过 fault PC 的反汇编辨别 IBT 还是 SHSTK 命中.

### INV-SHSTK-R03 — 影子栈页 W^X 强约束

- **statement**: 影子栈页由内核分配为特殊 VMA (linux `VM_SHADOW_STACK`), 普通 `mov` / `mmap` 写访问产生 `#PF`. 用户进程无法 `mmap(PROT_WRITE)` 一个 shadow stack 段, 必须用 `map_shadow_stack(2)` 系统调用. 这是 SHSTK 安全模型的根基: 攻击者即便有任意写, 也不能改影子栈内容.
- **runtime**: Linux kernel
- **version**: Linux ≥ 6.6
- **target**: x86_64
- **source_kind**: runtime
- **source_url_or_path**: Linux `arch/x86/include/asm/pgtable_types.h` (`_PAGE_DIRTY` / shadow stack 编码) ; `arch/x86/kernel/shstk.c`
- **version_sensitivity**: target-specific
- **oracle_mapping**: 反例 seed: 试图 `*ssp_addr = 0x...` 应触发 `SIGSEGV`; 若成功则内核 SHSTK 实现有 bug.

### INV-SHSTK-R04 — 多线程: 每线程独立影子栈

- **statement**: 每个线程在创建时由内核分配独立影子栈, 切换上下文时 `IA32_PL3_SSP` 随线程切换. `pthread_create` 后子线程的初始 SSP 来自内核, 父子线程影子栈互不可见. 因此对 SHSTK 而言, 多线程 fuzzing seed 与单线程行为本质相同, 但 `pthread_exit` / `cancel` 路径需 unwinder 协调.
- **runtime**: Linux kernel + glibc nptl
- **version**: Linux ≥ 6.6, glibc ≥ 2.39
- **target**: x86_64
- **source_kind**: runtime
- **source_url_or_path**: glibc `sysdeps/unix/sysv/linux/x86_64/clone.S` ; Linux `arch/x86/kernel/shstk.c` (`shstk_alloc_thread_stack`)
- **version_sensitivity**: stable
- **oracle_mapping**: 多线程 seed 测试 `pthread_create` + `pthread_cancel` + 异常.

### INV-SHSTK-R05 — fork: 子进程继承 SSP, 影子栈 COW

- **statement**: `fork()` 时子进程继承父进程的 SHSTK 状态; 影子栈段以 COW 复制, 因此 fork 后父子两边都能正常 `ret`. `vfork` 因共享栈也共享影子栈, 通常受限于子进程立即 `exec`.
- **runtime**: Linux kernel
- **version**: Linux ≥ 6.6
- **source_kind**: runtime
- **source_url_or_path**: Linux `arch/x86/kernel/shstk.c` (`shstk_alloc_fork`)
- **version_sensitivity**: stable
- **oracle_mapping**: `fork+ret` seed 验证基础语义; 反例: 父修改返回地址后 fork, 子 `ret` 期望 `SEGV_CPERR`.

## 7. 与其他机制的交互 (Interactions)

### INV-SHSTK-I01 — SHSTK 与 stack canary 互补但互不替代

- **statement**: 栈 canary 在 *函数 epilogue* 检查"局部缓冲区与保存寄存器之间"的字节; SHSTK 在 *`ret` 指令本身* 检查返回地址. 两者覆盖不同窗口: canary 可拦"覆盖了 saved regs 但 `ret` 前", SHSTK 可拦"绕过 canary 但 `ret` 时". 启用其一不应假设另一无效.
- **compiler**: GCC, LLVM/Clang
- **target**: i386, x86_64
- **source_kind**: 设计文档
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/stack-canary.md` ; Intel CET specification
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 用 "覆盖 saved regs 但不破坏 canary" 类 seed 验证 SHSTK 单独命中.

### INV-SHSTK-I02 — SHSTK 与 SafeStack 概念重叠

- **statement**: Clang `SafeStack` 把"返回地址 + 安全 spill"放到独立 stack, 避免缓冲区溢出污染. 在 SHSTK 启用的环境下, SafeStack 多余但不冲突. Clang 通常不强制互斥.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/SafeStack.html ; `@/home/yall/project/de-fuzz/docs/invariants/safestack.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

### INV-SHSTK-I03 — SHSTK 与 BTI/SCS (AArch64) 跨 ISA 等价

- **statement**: AArch64 上反向控制流由 PAC ret-signing + GCS (硬件) / SCS (软件) 提供, 与 x86 SHSTK 思路对称. 跨 ISA 测试时, oracle 信号都是"`ret` 与影子值不一致 -> 同步异常". 不同 ISA 下进程退出码不同, 但 oracle 抽象层相同.
- **compiler**: 跨架构
- **source_kind**: 设计文档
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/pointer-authentication.md` ; `@/home/yall/project/de-fuzz/docs/invariants/gcs.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 跨 ISA seed 共享语义, 编译矩阵分别开 SHSTK / PAC ret-signing.

### INV-SHSTK-I04 — `signal handler` 在 SHSTK 启用下使用备用影子栈

- **statement**: 信号处理函数被内核以 `call signal_handler` 间接进入, `IA32_PL3_SSP` 在进入信号处理前会被内核保存; 处理完 `sigreturn` 时恢复. 用户若用 `sigaltstack` 切换到备用栈, 内核必须为 alt-stack 提供对应的 alt-shadow-stack (通过 `arch_prctl(ARCH_SHSTK_ALT_SHSTK_*)` 接口), 否则 `signal handler` 内的 `ret` 触发 `SEGV_CPERR`.
- **runtime**: Linux kernel
- **version**: Linux ≥ 6.6
- **target**: x86_64
- **source_kind**: runtime
- **source_url_or_path**: Linux `arch/x86/kernel/shstk.c` (`shstk_alloc_thread_stack`, `shstk_setup`)
- **version_sensitivity**: target-specific
- **oracle_mapping**: 信号处理 + alt-stack 复合 seed.

### INV-SHSTK-I05 — `exec` 重置影子栈

- **statement**: `execve` 系列系统调用会丢弃旧影子栈, 由内核重新分配; SSP 重置. 因此 SHSTK 不跨 `exec` 持续, 攻击者不能利用旧地址.
- **runtime**: Linux kernel
- **source_kind**: runtime
- **source_url_or_path**: Linux `fs/exec.c` ; `arch/x86/kernel/shstk.c`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

## 8. 验证与已知回归 (Known Regressions)

### INV-SHSTK-VER-GLIBC-LONGJMP — `__longjmp` 在 SHSTK 下的 SSP 校准

- **statement**: glibc `__longjmp.S` 在 CET 路径下使用 `incsspq imm8` 循环 unwinding 影子栈; 早期实现对 `jmp_buf` 中保存的 SSP 字段尚未稳定 (glibc 2.27 之前). 自 glibc 2.28 起 `jmp_buf` 增加 SSP 字段并稳定.
- **runtime**: glibc
- **version**: glibc ≥ 2.28
- **source_kind**: source
- **source_url_or_path**: glibc `sysdeps/unix/sysv/linux/x86_64/__longjmp.S`
- **version_sensitivity**: stable since 2.28
- **oracle_mapping**: 老 glibc 回归组.

### INV-SHSTK-VER-LINUX-SAS — Alt-stack 信号路径修复

- **statement**: Linux 6.6 早期版本对 `sigaltstack` + SHSTK 的备用影子栈分配存在多个补丁; 在某些边角情形 (信号嵌套, 信号处理中再次设置 alt-stack) 早期内核会泄漏影子栈或触发 `BUG`. 已在后续 stable 版本修复.
- **runtime**: Linux kernel
- **version**: 修复散布于 6.6 - 6.10
- **source_kind**: source + mailing-list
- **source_url_or_path**: Linux `arch/x86/kernel/shstk.c` ; lkml.kernel.org SHSTK 主题
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 内核版本敏感, oracle 跑前应 `uname -r` 记录.

### INV-SHSTK-VER-LLVM-WINDOWS — Windows 上 SHSTK 由 OS 处理

- **statement**: Windows (Server 2019+) 自 OS 层面启用 CET-SS, 与 Linux 不同, MSVC / clang-cl 编译器侧无需 `-fcf-protection=return`; ELF 元数据机制不适用. DeFuzz 仅 Linux 路径与本文 invariant 表对齐.
- **compiler**: clang-cl, MSVC
- **target**: x86_64 (Windows)
- **source_kind**: user-doc
- **source_url_or_path**: Microsoft Docs "Hardware-enforced Stack Protection"
- **version_sensitivity**: target-specific
- **oracle_mapping**: 不适用 (Linux only).

## 9. DeFuzz SHSTK Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-SHSTK-R01 | `SIGSEGV` + `si_code == SEGV_CPERR`, fault PC 为 `ret` | 缓冲区溢出覆盖返回地址 |
| INV-SHSTK-C01 | 同上 | `setjmp` 后篡改 `jmp_buf` SSP 字段 |
| INV-SHSTK-C03 | 同上 | C++ 异常 unwind, 故意破坏返回地址 |
| INV-SHSTK-B04 | `SIGILL` (`#UD`) | 用户态执行 `wrssq` 指令 |
| INV-SHSTK-R03 | `SIGSEGV` (`#PF`) | 普通 `mov` 写影子栈页 |
| INV-SHSTK-R05 | 父子分别 `ret` 应正常 | `fork()` + 普通返回 |
| INV-SHSTK-I01 | SHSTK 命中, canary 未命中 | 覆盖 saved regs 但保留 canary |
| INV-SHSTK-M01 | `readelf -n` 含 `SHSTK` | 任意 `-fcf-protection=return` 构建 |
| INV-SHSTK-M02 | `-z force-shstk` 链接失败 | 一 TU 关 CET, 一 TU 开 |

## 10. 开放问题 / 未覆盖 invariants (Follow-ups)

- **glibc `setjmp` ABI 兼容**: 老 `jmp_buf` 不含 SSP 字段时, 与新 glibc 混用 (静态链接的 1.x 老程序在新内核下) 行为待补充 invariant.
- **协程库 (libco / Boost.Context / golang runtime)**: 用户态不通过 glibc `swapcontext` 切换栈帧时, 必须自管 SSP. 各自实现的 SHSTK 协议合规性 oracle 需逐库验证.
- **AMD CET-SS 行为差异**: Zen4+ 报告支持 CET, 但部分 AMD-specific 异常向量与 Intel 描述存在措辞细微差异. 待具体硬件回归.
- **SHSTK 与 LD_PRELOAD 拦截器**: 部分老 LD_PRELOAD 注入工具未带 SHSTK property, 进程加载即降级 SHSTK; oracle 须显式禁用 LD_PRELOAD 或用 `force-shstk`.
- **JIT / V8 / LuaJIT**: 动态生成代码的 SHSTK 配合方式 (`map_shadow_stack(2)` + `wrssq` via syscall) 待补.
- **kernel-mode SHSTK**: `IA32_S_CET` / supervisor SSP 在 Linux KERNEL CET 启用时的行为, 本文未覆盖.

## 11. 使用建议

- 新增 x86 编译器 / 内核版本 / glibc 版本时, 对 §1, §3, §4, §6 逐条回归.
- 运行 oracle 前必须先 `arch_prctl(ARCH_SHSTK_STATUS, &status)`, 校验 `status & ARCH_SHSTK_SHSTK` 非零, 否则跳过 INV-SHSTK-R* 类断言.
- 静态扫描类 (INV-SHSTK-M01, INV-SHSTK-M02) 可作为 CI 前置门禁.
- `likely-to-drift` 的 invariant (INV-SHSTK-VER-LINUX-SAS, INV-SHSTK-I04) 在每次内核 stable 升级时人工核对.
