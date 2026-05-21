# `endbr32` / `endbr64` (Intel CET IBT) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / LLVM/Clang / Intel SDM / ELF psABI / glibc / ld.so / Linux kernel 中与 **Intel CET Indirect Branch Tracking (IBT)** 的 `endbr32` / `endbr64` landing pad 直接相关的 invariants 统一抽取、归类, 作为 DeFuzz CET/IBT oracle 的形式化依据.
>
> 机制简写与 survey 一致: **CET** = Intel Control-flow Enforcement Technology (IBT + Shadow Stack). 本文只覆盖 **IBT 分支**, 即 `endbr` 指令相关; SHSTK (shadow stack) 另文.

## 0. 术语与坐标

- **IBT (Indirect Branch Tracking)**: Intel CET 的前向控制流完整性子特性. 启用后, 任何间接跳转 (`jmp`/`call` via register or memory) 的下一条指令 *必须* 是 `endbr32` / `endbr64`, 否则 CPU 触发 `#CP` (Control Protection) 异常.
- **endbr64** (x86_64 long mode): 4 字节编码 `F3 0F 1E FA`, 即 32-bit 小端常数 `0xFA1E0FF3`.
- **endbr32** (32-bit / compat mode): 4 字节编码 `F3 0F 1E FB`, 即 32-bit 小端常数 `0xFB1E0FF3`.
- **landing pad**: 合法的间接跳转目标指令, 即 `endbr32` / `endbr64` 本身.
- **CET tracker state**: CPU 内部状态机 `IDLE` 与 `WAIT_FOR_ENDBRANCH`. 任何间接分支把状态从 `IDLE` 切换到 `WAIT_FOR_ENDBRANCH`; 若下一条指令是 `endbr`, 状态回到 `IDLE`; 否则 `#CP(ENDBRANCH)` fault.
- **NOTRACK 前缀**: 单字节 `3E` (段前缀 DS 的 CET 复用) 加在 `jmp*/call*` 指令前, 告诉 CPU "该间接分支已经过类型化检查, 无需进入 `WAIT_FOR_ENDBRANCH`". 仅用于编译器已证安全的场合 (主要是 jump table / switch).
- **NOP 兼容性**: `F3 0F 1E FA` / `FB` 在不支持 CET 的 CPU 上解码为 `NOP`-form (`rep nop r/m32`), 因此 `endbr` 字节序列对旧 CPU 无语义副作用, 可同一二进制兼容跑新旧机器.
- **GNU property**: ELF note `NT_GNU_PROPERTY_TYPE_0` 中的 `GNU_PROPERTY_X86_FEATURE_1_AND` 位图, 包含 `IBT (0x1)` 与 `SHSTK (0x2)` 两位. ld.so 对进程做 *所有 loaded object 的 AND 归并*: 任一对象缺失位即整个映像降级该特性.
- **nocf_check**: 函数级属性, 关闭该函数的 `endbr` 插入, 同时要求编译器对向该函数的间接调用使用 `NOTRACK` 前缀.

每条 invariant 采用 survey 推荐字段: `ID / statement / compiler / version / target / source_kind / source_url_or_path / evidence_snippet / version_sensitivity / oracle_mapping`.

## 1. 静态前提 (Static Preconditions)

### INV-IBT-E01 — `-fcf-protection` 三档语义

- **statement**: GCC / Clang 的 `-fcf-protection={none,branch,return,full,check}` 控制 CET 启用粒度: `none` 关闭; `branch` 仅启用 IBT (发射 `endbr`); `return` 仅启用 SHSTK; `full` = `branch` + `return`; `check` 在目标不支持 CET 时强制编译失败. 仅 `branch` / `full` / `check` 路径会发射 `endbr` landing pad.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html ; https://gcc.gnu.org/onlinedocs/gcc/x86-Options.html ; https://clang.llvm.org/docs/ClangCommandLineReference.html
- **evidence_snippet**: GCC manual: *"`branch` enables instrumentation of indirect branches with the NOTRACK prefix or the `endbr` instruction"*.
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz CET oracle 把 `-fcf-protection=branch` 与 `-fcf-protection=none` 作为正/反控组; 对同一 seed 对比是否出现 `endbr64` 前缀字节.

### INV-IBT-E02 — `-fhardened` 隐式开启 `-fcf-protection=full`

- **statement**: GCC `-fhardened` (GNU/Linux 用户空间) 隐式开启 `-fcf-protection=full`, 因此在支持 CET 的平台上自动发射 `endbr`.
- **compiler**: GCC
- **version**: GCC 14+
- **target**: i386, x86_64 (Linux)
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: CFLAGS 矩阵中 `-fhardened` 应与显式 `-fcf-protection=full` 产生等价 `endbr` 布局, oracle 以此做交叉验证.

### INV-IBT-E03 — `-mcet` / `-mibt` / `-mshstk` 只开硬件 ISA 特性位

- **statement**: `-mibt` / `-mshstk` 仅告诉汇编器/编译器 "目标 CPU 能理解 `endbr` / `wrss` 等 CET 指令", *不自动发射 endbr*. 真正的 lowering 由 `-fcf-protection` 控制. 老的 `-mcet` 等同于 `-mibt -mshstk`. 这两类 flag 功能正交, 混用不矛盾但也不互补.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/x86-Options.html ; `gcc/config/i386/i386.opt`
- **version_sensitivity**: stable
- **oracle_mapping**: 构造 `-mibt` 但无 `-fcf-protection` 的反例, oracle 应发现"编译器未插 endbr" — 非 bug 而是配置问题.

### INV-IBT-E04 — 需要 ld.so + 内核同时启用 CET 才会 enforce

- **statement**: 用户空间 IBT 的 *运行时 enforcement* 依赖三件事同时成立: (a) CPU 支持 CET (`CPUID.(07H,0):ECX[bit 7] = CET_IBT`); (b) 内核通过 `arch_prctl(ARCH_CET_ENABLE, CET_SHSTK|CET_IBT)` (或等价 prctl) 为进程打开对应的 `CR4.CET` / `IA32_U_CET` 位; (c) ld.so 根据 ELF GNU property 归并结果调用该 prctl. 缺任一项, 即便编译进了 `endbr`, `#CP` 也不会触发, oracle 看不到违例信号.
- **runtime**: Linux kernel + glibc ld.so
- **version**: Linux ≥ 6.6 (IBT user-mode merged), glibc ≥ 2.28 (CET 解析)
- **target**: x86_64 (i386 仅平台支持但发行版几乎不启)
- **source_kind**: user-doc + runtime
- **source_url_or_path**: https://www.kernel.org/doc/html/latest/x86/shstk.html ; glibc `sysdeps/x86/dl-cet.c`
- **version_sensitivity**: target-specific (kernel/glibc 版本与发行版策略敏感)
- **oracle_mapping**: DeFuzz 在启动 oracle 前必须读 `/proc/self/status` 的 `x86_Thread_features: shstk, ibt` 或执行 `readelf -n` 自检, 以确认 "不插 endbr 的绕过" 不是被 runtime 吃掉.

## 2. 指令字节模式 (Instruction Encoding)

### INV-IBT-B01 — `endbr64` / `endbr32` 的 4 字节固定编码

- **statement**: `endbr64` 永远是 `F3 0F 1E FA`; `endbr32` 永远是 `F3 0F 1E FB`. 两者 4 字节对齐无要求 (可落在任意地址), 但执行时 CPU 必须整 4 字节取指成功. 作为 32-bit 小端整数读取即 `0xFA1E0FF3` (endbr64) / `0xFB1E0FF3` (endbr32).
- **compiler**: GCC, LLVM/Clang, binutils
- **version**: all
- **target**: x86_64 (endbr64), i386/x32 (endbr32)
- **source_kind**: ABI-spec
- **source_url_or_path**: Intel CET specification (`https://software.intel.com/sites/default/files/managed/4d/2a/control-flow-enforcement-technology-preview.pdf`) ; Intel SDM Vol.2 `ENDBR64/ENDBR32` 条目
- **evidence_snippet**: Intel SDM: *"ENDBR64 — Terminate an Indirect Branch in 64-bit Mode. Opcode: F3 0F 1E FA"*.
- **version_sensitivity**: stable (ISA 级)
- **oracle_mapping**: DeFuzz 静态扫描工具以 `objdump -d` 或字节 pattern (`F3 0F 1E FA`) 统计 landing pad 数量, 与期望的间接可达函数数量比对.

### INV-IBT-B02 — `endbr` 在 legacy CPU 上是 NOP

- **statement**: `endbr32` / `endbr64` 的 4 字节编码是 `NOP r/m` 家族的保留形式, 在不支持 CET 的 CPU 上解码为无副作用 NOP, 因此带 `endbr` 的二进制可在同一发行版跑新旧机器. 这条是 Intel 特地选择该 opcode 的历史原因, 也是编译器默认可无条件发射的基础.
- **hardware**: Intel CET CPU
- **version**: all
- **target**: x86_64, i386
- **source_kind**: ABI-spec
- **source_url_or_path**: Intel CET specification §2.2; Intel SDM Vol.2 `NOP` 条目脚注
- **version_sensitivity**: stable
- **oracle_mapping**: 编译产物可在非 CET 机器上运行作为功能回归对照 (无 `#CP`, 但语义正确).

### INV-IBT-B03 — `endbr` 字节序列不得作为常数立即数出现在指令流中

- **statement**: **这是 IBT 最隐蔽的 invariant**. 由于 CPU 在 `WAIT_FOR_ENDBRANCH` 状态下仅对指令流做字节级匹配, 若任何普通指令的立即数或 disp 字段恰好含有 `F3 0F 1E FA` / `F3 0F 1E FB` 字节序列, 攻击者可把间接分支瞄向该字节偏移, 得到一个伪 landing pad. 为此 GCC `ix86_endbr_immediate_operand` 谓词禁止形如 `0xFA1E0FF3` / `0xFB1E0FF3` 以及其**任意字节移位** (例如 `0x....FA1E0FF3....` 的八种字节对齐移位) 出现在 `mov imm`, `push imm`, `cmp imm`, PIC `lea` 等常数中; 命中后该常数被强制走 constant pool (`.rodata`).
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+ (引入), 跨若干版本补齐 (见 PR104140, PR124366 等)
- **target**: i386, x86_64
- **source_kind**: source + test
- **source_url_or_path**: https://github.com/gcc-mirror/gcc/blob/master/gcc/config/i386/predicates.md (`ix86_endbr_immediate_operand`) ; `gcc/config/i386/i386.cc` (`legitimate_constant_p`, `legitimate_pic_constant_p` 引用点) ; `gcc/testsuite/gcc.target/i386/cet-pr124366.c`
- **evidence_snippet**: GCC `predicates.md`: *"Match (CONST_INT) that would be rejected as ENDBR immediate when `-fcf-protection=branch` is on"* — 字节级移位检查匹配 `0xFA1E0FF3` / `0xFB1E0FF3` 的 8 种可能对齐.
- **version_sensitivity**: likely-to-drift (新指令 / 新常数路径若漏判即回归, 详见 GCC Bugzilla 以 "endbr" + "immediate" 关键字)
- **oracle_mapping**: DeFuzz CET oracle 应含 seed 模板 "函数里有常量 `0xFA1E0FF3` / `0xFB1E0FF3`", 断言编译器产物在 `-fcf-protection=branch` 下该常数不以 `mov $imm, %reg` 形式出现在 `.text` 内, 而应落 `.rodata`.

### INV-IBT-B04 — `NOTRACK` 前缀编码

- **statement**: 间接分支前的 `NOTRACK` 前缀是单字节 `3E` (传统 DS 段前缀的 CET 复用). 仅当 CPU 处于 IBT enabled 且 `IA32_U_CET.NO_TRACK_EN = 1` (用户模式) 或 `IA32_S_CET.NO_TRACK_EN = 1` (内核模式) 时该前缀才解除后续 `jmp*/call*` 的 landing pad 要求. `NO_TRACK_EN` 未置位时该前缀被 CPU 当作非法, 触发 `#CP`.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: i386, x86_64
- **source_kind**: ABI-spec
- **source_url_or_path**: Intel SDM Vol.1 §18 "Control-flow Enforcement Technology" ; `gcc/config/i386/i386.cc` (`ix86_output_call_insn`, `notrack` 输出路径)
- **version_sensitivity**: stable
- **oracle_mapping**: 对带 `NOTRACK` 的 switch 跳转, oracle 可核对 `.text` 是否含 `3E FF 24` / `3E FF D0` 这类字节模式.

## 3. `endbr` 插入位置 (Placement)

### INV-IBT-P01 — 可被间接调用的函数入口必须是 `endbr`

- **statement**: 在 `-fcf-protection=branch` / `full` 下, 编译器在以下位置发射 `endbr64` (x86_64) / `endbr32` (i386):
  1. 所有外部可见函数 (`.globl`, `weak`, `.hidden` 的非 static) 的第一条指令;
  2. 所有被取地址的 static 函数入口 (编译器需保守认为其可能被间接调用);
  3. 所有异常落地页 / landing pad (与 unwind 协作).
  不含 *仅被直接调用的 static / internal* 函数: 编译器若能证明函数只在当前 TU 内被直接 `call rel32` 调用, 可省略入口 `endbr`.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: source + user-doc
- **source_url_or_path**: `gcc/config/i386/i386.cc` (`ix86_notrack_prefixed_insn_p`, `ix86_function_needs_cet_endbr_p`) ; `llvm/lib/Target/X86/X86IndirectBranchTracking.cpp`
- **evidence_snippet**: LLVM `X86IndirectBranchTracking.cpp` 顶部注释描述在每个 function entry / address-taken BB 插入 `ENDBR64`.
- **version_sensitivity**: stable at doc level, likely-to-drift at impl level (address-taken 判定差异)
- **oracle_mapping**: DeFuzz 扫描产物 `.text` 中 global / address-taken 函数入口, 必须首指令为 `endbr64`. 漏插即 oracle 报 bug.

### INV-IBT-P02 — `setjmp` / `__builtin_setjmp` 返回点必须是 `endbr`

- **statement**: 由于 `longjmp` 实质上以间接跳转 (`ret` / `jmp *reg`) 回到 `setjmp` 的调用点, 编译器必须在 `setjmp` 调用点的**返回指令之后**紧接 `endbr`, 否则 `longjmp` 后 CPU 仍处 `WAIT_FOR_ENDBRANCH` 状态, 下一条指令触发 `#CP`.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: source + test
- **source_url_or_path**: `gcc/config/i386/i386.cc` (`ix86_setjmp_endbr` 相关路径) ; `gcc/testsuite/gcc.target/i386/cet-sjlj-*.c` ; `llvm/lib/Target/X86/X86IndirectBranchTracking.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: seed 模板应含 `setjmp/longjmp` 路径, oracle 核对 `setjmp` 调用后紧邻 `endbr`.

### INV-IBT-P03 — 间接可达的 BB (jump table 目标) 使用 `NOTRACK`, 非 `endbr`

- **statement**: switch 展开的 jump table 目标 BB 不发 `endbr`, 而是让 "跳到 table 的间接分支" 带 `NOTRACK` 前缀. 原因: 每个 BB 前插 `endbr` 会膨胀代码体积, 而 jump table 已由编译器类型化 (同一表内所有目标签名一致), 属于 "编译器已证类型安全" 场景. 反例: 带 `-fno-jump-tables` 或 `-fno-cf-protection-notrack` 时, switch 退化为 "间接分支 + 每个目标 `endbr`" 模式.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: source
- **source_url_or_path**: `gcc/config/i386/i386.cc` (`ix86_notrack_prefixed_insn_p`) ; `llvm/lib/Target/X86/X86IndirectBranchTracking.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 对 switch seed, oracle 扫描 `.text`, 期望 `3E FF 24` (`jmp *(...)` 带 notrack) 存在而 BB 内无 `endbr`.

### INV-IBT-P04 — 直接调用目标不加 `endbr`

- **statement**: 单纯被直接 `call rel32` 调用的 static 函数不需要入口 `endbr`, 因为直接分支不改变 CET tracker 状态. 编译器对 "address-taken" 判定保守: 函数一旦被取地址 (例如赋给函数指针、放进 vtable、作为回调参数), 就视为可能被间接调用, 必须插 `endbr`.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: source + test
- **source_url_or_path**: `gcc/config/i386/i386.cc` ; `gcc/testsuite/gcc.target/i386/cet-intdir-*.c`
- **version_sensitivity**: stable
- **oracle_mapping**: 用 "纯直接调用" 的 static + "被取地址" 的 static 对比模板验证启发式.

### INV-IBT-P05 — 异常处理 landing pad 需 `endbr`

- **statement**: C++ 异常 / `cleanup` / `__builtin_eh_return` 的 landing pad 由 unwinder 间接跳入, 必须以 `endbr` 开头. GCC `dwarf2cfi` + `i386.cc` 会在 eh landing pad label 前发 `endbr`.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: source
- **source_url_or_path**: `gcc/config/i386/i386.cc` ; `gcc/except.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: C++ 异常 seed 必须通过 CET runtime 检查, oracle 可用 `try/catch` 模板交叉验证.

## 4. 属性 / 局部控制 (Attributes)

### INV-IBT-A01 — `nocf_check` 关闭函数级 IBT

- **statement**: `__attribute__((nocf_check))` (GCC) / `[[clang::nocf_check]]` 等价, 对该函数:
  (1) 不发射入口 `endbr`;
  (2) 通过该函数指针类型调用时, 编译器自动在间接分支前加 `NOTRACK`.
  类型系统区分 `void (*fp)(void)` 与 `void (* __attribute__((nocf_check)) fp)(void)`; 前者调用后者 (或反过来) 视为类型不兼容.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html ; https://clang.llvm.org/docs/AttributeReference.html#nocf-check
- **evidence_snippet**: GCC: *"`nocf_check` attribute on a function indicates that no control-flow check should be performed on the function's entry point"*.
- **version_sensitivity**: stable
- **oracle_mapping**: seed 模板 "两函数签名相同, 一个带 `nocf_check`", 分别通过兼容/不兼容的函数指针调用, oracle 核对 `NOTRACK` 前缀的出现条件.

### INV-IBT-A02 — ICF 不得合并 `nocf_check` 与普通函数

- **statement**: 链接期 identical code folding (ICF, `--icf=all` / `-fipa-icf`) 禁止将带 `nocf_check` 的函数与不带的函数合并成同一符号, 因为合并后任一调用路径的语义都会被破坏. GCC/LLVM 的 ICF 判等函数必须将 `nocf_check` (以及 `endbr` 是否存在) 列入判等签名.
- **compiler**: GCC, LLVM/Clang + lld/gold/mold
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: source + test
- **source_url_or_path**: `gcc/ipa-icf.cc` ; `gcc/testsuite/gcc.target/i386/cet-notrack-icf-*.c` ; LLD `ICF.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: ICF 回归 seed: 两个字节级相同但属性不同的函数, 在 `-flto` + ICF 下不得被合并, 否则 oracle 报链接器 / IPA-ICF bug.

### INV-IBT-A03 — `-fcf-protection` 必须跨所有 TU / DSO 一致

- **statement**: 若一个 DSO 以 `-fcf-protection=branch` 编译而另一个以 `-fcf-protection=none`, 链接器会在最终 ELF 的 GNU property `FEATURE_1_IBT` 位上做 AND (缺即清零). ld.so 据此决定是否启用 IBT. 结果: 混编 DSO 会使整个进程 IBT 降级.
- **compiler + linker**: GCC / Clang + ld.bfd / gold / lld / mold
- **version**: binutils ≥ 2.32, lld ≥ 10
- **target**: i386, x86_64
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/x86-Options.html ; `gcc/config/i386/cet.h` ; binutils `bfd/elfxx-x86.c` (`_bfd_x86_elf_link_setup_gnu_properties`)
- **evidence_snippet**: GCC manual: *"The `-fcf-protection` option must be used with all translation units of the program"*.
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz CFLAGS 矩阵在混合配置下 (`main` 不开 CET, 被 `dlopen` 的 DSO 开 CET) 观察 `readelf -n` 的 feature 位, 验证 AND 语义.

## 5. ELF 元数据 (Metadata)

### INV-IBT-M01 — `GNU_PROPERTY_X86_FEATURE_1_IBT` 位必须存在

- **statement**: 启用 IBT 的 ELF 对象必须在 `.note.gnu.property` 段中置 `GNU_PROPERTY_X86_FEATURE_1_AND` 的 bit 0 (`IBT = 0x1`). ld.so 进程起动时对所有 loaded object 的该位 AND 归并; 结果为 1 才启用进程级 IBT. 缺一对象即整个进程降级, 已有的 `endbr` 不 enforce.
- **compiler + linker**: GCC / Clang + binutils / lld
- **version**: GCC 8+, Clang 7+, binutils ≥ 2.32
- **target**: i386, x86_64
- **source_kind**: ABI-spec + source
- **source_url_or_path**: https://gitlab.com/x86-psABIs/x86-64-ABI (附录 "GNU Property Types") ; `gcc/config/i386/cet.h` (`.note.gnu.property` 发射)
- **version_sensitivity**: stable
- **oracle_mapping**: `readelf -n <ELF>` 抓 `NT_GNU_PROPERTY_TYPE_0` 含 `IBT` 字样, 作为 "编译器真的开了 IBT" 的外观校验.

### INV-IBT-M02 — 缺 IBT 元数据的 static library 污染最终可执行体

- **statement**: 若一个 `.a` / `.o` 不含 `GNU_PROPERTY_X86_FEATURE_1_IBT`, binutils 默认将整个 output 的 IBT bit 清零 (AND 语义). 通过 `-Wl,-z,force-ibt` 可强制保留 IBT 位并在链接期对缺失对象报错; `-Wl,-z,ibt=all` 类似. 这是"所有 TU 必须 `-fcf-protection`"在链接层的实际执行点.
- **linker**: binutils (`ld.bfd`, `gold`), `lld`
- **version**: binutils ≥ 2.32
- **target**: i386, x86_64
- **source_kind**: source + user-doc
- **source_url_or_path**: binutils `ld` manual "X86 ELF Options" ; `bfd/elfxx-x86.c`
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 在 oracle 前的构建自检里加 `-Wl,-z,force-ibt`, 使混编配置直接链接失败, 避免 "编译期以为开了 IBT 实际没开" 的伪阳.

### INV-IBT-M03 — 动态链接器按 AND 归并 IBT 位

- **statement**: glibc ld.so 在加载每个 DSO 时把其 `FEATURE_1_IBT` 位与进程当前状态做 AND; 任一 DSO 不带 IBT 即整个进程 IBT 降级 (除非指定 `GLIBC_TUNABLES=glibc.cpu.x86_ibt=on` 等显式强制策略). 这解释 "为什么 `dlopen` 一个老旧 DSO 会关掉整个进程的 IBT".
- **runtime**: glibc ld.so
- **version**: glibc ≥ 2.28
- **target**: x86_64 (主要), i386
- **source_kind**: runtime
- **source_url_or_path**: glibc `sysdeps/x86/dl-cet.c` (`_dl_cet_check`, `dl_isa_level`)
- **version_sensitivity**: stable (tunables 名随 glibc 微调)
- **oracle_mapping**: 当 seed 涉及 `dlopen` 时, oracle 先夹带一个无 IBT 的哨兵 DSO, 验证 IBT 是否被动态关闭.

## 6. 运行时语义 (Runtime Semantics)

### INV-IBT-R01 — 间接分支后第一条指令必须是 `endbr`, 否则 `#CP`

- **statement**: IBT 启用下, `call *reg/mem`, `call *[rip+...]`, `jmp *reg/mem`, `ret` 后的 IP (因为 `ret` 被 SHSTK 覆盖, 但 IBT 专注于非 `ret` 的间接分支) 指向的第一条指令必须是 `endbr32` / `endbr64`. CPU 在该分支后进入 `WAIT_FOR_ENDBRANCH` 状态, 若目标首指令不是 `endbr` 则立即 `#CP(ENDBRANCH)` 异常, Linux 用户空间翻译为 `SIGSEGV` (`si_code = SEGV_CPERR`), 进程退出码 139.
- **hardware + runtime**: Intel CPU + Linux kernel
- **version**: all CET-capable CPUs
- **target**: i386, x86_64
- **source_kind**: ABI-spec + runtime
- **source_url_or_path**: Intel SDM Vol.1 §18 ; Linux kernel `arch/x86/kernel/traps.c` (`DEFINE_IDTENTRY_ERRORCODE(exc_control_protection)`)
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz CET oracle 的正向信号: *通过间接分支跳入 "不带 endbr 的目标" 时产生 `SIGSEGV` + `si_code == SEGV_CPERR` (系统定义值 10)*. 凡编译器漏插 landing pad 即命中.

### INV-IBT-R02 — `#CP` 在用户空间表现为 `SIGSEGV` + `SEGV_CPERR`

- **statement**: Linux 把 x86 `#CP` 异常分派为 `SIGSEGV`, 同时 `siginfo_t.si_code = SEGV_CPERR` (值 10, 定义于 `<asm/siginfo.h>` / `uapi`). 这是区分 "经典空指针 `SIGSEGV`" 与 "IBT/SHSTK `SIGSEGV`" 的唯一可靠信号.
- **runtime**: Linux kernel + glibc
- **version**: Linux ≥ 5.18 (SEGV_CPERR 引入), glibc ≥ 2.35
- **target**: i386, x86_64
- **source_kind**: runtime
- **source_url_or_path**: Linux `arch/x86/kernel/traps.c` ; glibc `sysdeps/unix/sysv/linux/bits/siginfo-consts-arch.h`
- **version_sensitivity**: stable since 5.18
- **oracle_mapping**: DeFuzz CET oracle 必须用 `sigaction(SA_SIGINFO)` 捕获 `SIGSEGV` 并检查 `si_code == SEGV_CPERR`, 否则无法与普通段错误区分.

### INV-IBT-R03 — tracker 状态在中断 / syscall / 信号处理后保留

- **statement**: CPU 在进入中断、syscall、信号处理时会保存 `IA32_U_CET.TRACKER`; 返回用户态时恢复. 因此 "在一个 indirect `call` 后先进入信号处理再回来" 不会丢状态 — 下一条用户态指令仍须是 `endbr`. 对 DeFuzz 意义: 信号路径不是绕过 IBT 的缝隙.
- **hardware + runtime**: Intel CPU + Linux kernel
- **version**: all
- **target**: i386, x86_64
- **source_kind**: ABI-spec
- **source_url_or_path**: Intel SDM Vol.1 §18.3 ; Linux `arch/x86/kernel/signal.c`
- **version_sensitivity**: stable
- **oracle_mapping**: 对涉及信号的 seed 无需额外特殊处理; oracle 以统一规则判定 `SEGV_CPERR`.

### INV-IBT-R04 — legacy bitmap 可在内存区域级解除 IBT

- **statement**: CPU 支持 "CET legacy bitmap": 一张 per-process 位图, 每 4KB 页一位, 置 1 表示该页内的间接分支目标不强制要求 `endbr` (用于加载老式 JIT / legacy 代码). 启用由 `arch_prctl(ARCH_CET_LEGACY_BITMAP_BASE, ...)` + `IA32_U_CET.LEG_IW_EN = 1` 控制. 一般发行版默认关闭; 若启用, DeFuzz 对该页范围的 IBT 失败无意义.
- **hardware + runtime**: Intel CPU + Linux kernel
- **version**: all CET-capable CPUs
- **target**: i386, x86_64
- **source_kind**: ABI-spec
- **source_url_or_path**: Intel SDM Vol.1 §18.3.3 ; Linux `arch/x86/kernel/shstk.c`
- **version_sensitivity**: target-specific (发行版策略)
- **oracle_mapping**: DeFuzz 自检阶段读 `arch_prctl(ARCH_CET_STATUS)`, 若 legacy bitmap 已挂载, 跳过 IBT oracle 的正确性断言.

## 7. 与其他机制的交互 (Interactions)

### INV-IBT-I01 — SHSTK 独立于 IBT 的启停

- **statement**: `-fcf-protection=branch` 只开 IBT, 不发射 SHSTK 代码; `-fcf-protection=return` 只开 SHSTK, 不插 `endbr`. 两者 ELF property 位独立 (`IBT=0x1`, `SHSTK=0x2`), ld.so 独立归并. `-fcf-protection=full` 同时开. 不应假设一个启用蕴含另一个.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 7+
- **target**: i386, x86_64
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: CET oracle 与 SHSTK oracle 解耦; DeFuzz 对两者应用独立的 seed 矩阵.

### INV-IBT-I02 — IFUNC resolver 必须发 `endbr`

- **statement**: GNU IFUNC (`__attribute__((ifunc(...)))`) 的 resolver 在 ld.so 启动时以函数指针形式被间接调用, 因此 resolver 入口必须有 `endbr`. glibc 的 `_dl_runtime_resolve*` 路径同理. 编译器对 `ifunc` 属性的函数自动发 `endbr`; 手写汇编 resolver (如 glibc IFUNC strcmp) 需作者显式放 `endbr64`.
- **compiler + runtime**: GCC/Clang + glibc
- **version**: glibc ≥ 2.28
- **target**: i386, x86_64
- **source_kind**: runtime + source
- **source_url_or_path**: glibc `sysdeps/x86_64/dl-machine.h` ; glibc `sysdeps/x86_64/multiarch/*.S` (含 `ENTRY` 宏内置 `endbr64`)
- **version_sensitivity**: stable
- **oracle_mapping**: seed 不直接依赖 IFUNC; 作为 "手写汇编要小心" 的文档说明.

### INV-IBT-I03 — trampoline (nested function, closure) 需 `endbr`

- **statement**: GCC 嵌套函数的 trampoline (栈上或 executable heap 上生成的小段代码) 必须以 `endbr` 开头, 否则对它的间接调用在 IBT 下触发 `#CP`. GCC `i386.cc` 的 `x86_output_trampoline_template` 在 `TARGET_IBT` 为真时显式发射 `endbr64` / `endbr32` 作为第一条指令.
- **compiler**: GCC
- **version**: GCC 8+
- **target**: i386, x86_64
- **source_kind**: source
- **source_url_or_path**: `gcc/config/i386/i386.cc` (`x86_output_trampoline_template`)
- **version_sensitivity**: stable
- **oracle_mapping**: GCC 嵌套函数 + 函数指针作为 seed 的专门模板.

### INV-IBT-I04 — Linux 内核用 `objtool` 构建期校验 endbr 完整性

- **statement**: 内核自 `CONFIG_X86_KERNEL_IBT=y` 起, `objtool --ibt` 扫描 `.text`, 对每个间接跳转目标要求前一条是 `endbr64`, 对每个未标注 `__noendbr` 的全局/取地址函数要求入口 `endbr64`, 反向对每个"不应被间接调用"的函数禁止 `endbr`. 失败即构建报错. 这是编译器输出之外的 *第二道 enforcement*.
- **target-specific**: Linux kernel
- **version**: Linux ≥ 5.18
- **target**: x86_64
- **source_kind**: source
- **source_url_or_path**: https://github.com/torvalds/linux/tree/master/tools/objtool ; `tools/objtool/check.c` (`validate_ibt`)
- **version_sensitivity**: likely-to-drift (objtool 规则持续加严)
- **oracle_mapping**: 构建 Linux 内核 seed 时, `objtool` warning/error 本身是 oracle 信号; 不需额外 runtime 触发.

### INV-IBT-I05 — FineIBT (arity-aware IBT) 与普通 IBT 的共存

- **statement**: LLVM/Clang Discourse 上的 FineIBT RFC 提出在每个 `endbr64` 后追加一段 "arity / 类型 hash" 校验, 使 IBT 从 "可达即合法" 加强为 "签名匹配才合法". 启用 FineIBT 后, landing pad 仍以 `endbr64` 开头 (底层 IBT 依旧生效), 但 `endbr64` 后的字节模式会出现固定的比较 / trap 序列. 不启用 FineIBT 时, `endbr64` 后可以是任意普通指令.
- **compiler**: LLVM/Clang (实验性), Linux 内核侧 merged
- **version**: LLVM 18+ (RFC 阶段), Linux ≥ 6.2 (`CONFIG_FINEIBT`)
- **target**: x86_64
- **source_kind**: RFC + source
- **source_url_or_path**: https://discourse.llvm.org/c/clang/6 (FineIBT 主题) ; Linux `arch/x86/kernel/alternative.c` (`apply_fineibt`)
- **version_sensitivity**: likely-to-drift (RFC / 策略未最终化)
- **oracle_mapping**: 当 seed 在 FineIBT 启用的内核下运行, oracle 需要放宽 "`endbr` 后立即是业务代码" 的假设.

## 8. 验证与已知回归 (Known Regressions)

### INV-IBT-VER-PR104140 — 常量立即数漏检的历史回归

- **statement**: GCC PR104140 系: `movabsq $const, %reg` 和 PIC 相关的 `lea` 路径中, 若 `const` 含 `endbr` 字节序列, 早期 `ix86_endbr_immediate_operand` 未覆盖此路径, 会把 `endbr` 字节直接泄入 `.text`. 补丁补齐了 `legitimate_pic_constant_p` 的拒绝路径.
- **compiler**: GCC (historical)
- **version**: 修复于 GCC 12.1 / 回补至 11.x
- **target**: x86_64
- **source_kind**: mailing-list + source
- **source_url_or_path**: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=104140
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老编译器版本回归组用 `movabsq $0xFA1E0FF3..., %rax` 模板可复现.

### INV-IBT-VER-PR124366 — 字节对齐移位漏检

- **statement**: GCC PR124366 是 `ix86_endbr_immediate_operand` 对 "常数中 `0xFA1E0FF3` 出现在非 0/4/8/... 字节偏移" 这类非自然对齐位置的漏检修复. 对应测试用例 `gcc.target/i386/cet-pr124366.c`.
- **compiler**: GCC
- **version**: 修复于 GCC 14.1
- **target**: i386, x86_64
- **source_kind**: test + mailing-list
- **source_url_or_path**: `gcc/testsuite/gcc.target/i386/cet-pr124366.c` ; https://gcc.gnu.org/bugzilla/show_bug.cgi?id=124366
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 作为 GCC ≤13 的受控反例.

### INV-IBT-VER-LLVM-NESTED-BB — `X86IndirectBranchTracking` 对 address-taken BB 的判定

- **statement**: LLVM `X86IndirectBranchTracking.cpp` 曾在"同函数内 computed goto 目标 BB"漏插 `endbr`, 后续补丁统一用 `MachineBasicBlock::isIRBlockAddressTaken` 做判定. 该属性在 JIT / 特殊 IR 入口下容易漏标.
- **compiler**: LLVM/Clang
- **version**: 修复于 LLVM 12+ (具体 PR 见 LLVM issue tracker)
- **target**: i386, x86_64
- **source_kind**: source + issue
- **source_url_or_path**: `llvm/lib/Target/X86/X86IndirectBranchTracking.cpp` (`isEndBRnRequired`)
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: computed goto / label-as-value seed 作为针对性测试.

## 9. DeFuzz CET / IBT Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-IBT-P01 | `SIGSEGV` + `si_code == SEGV_CPERR` | 通过函数指针调用一个"应有 endbr 但未插"的函数 |
| INV-IBT-P02 | 同上 | `setjmp` + `longjmp`, 返回点若无 endbr |
| INV-IBT-P03 | 无 crash 即合规 | switch + jump table, 要求 `NOTRACK` 前缀 |
| INV-IBT-P05 | 同上 | C++ `try/catch` + 异常抛出, landing pad 首指令 |
| INV-IBT-B03 | 静态扫描 `.text` 中不得出现裸 `0xFA1E0FF3` / `0xFB1E0FF3` 立即数 | 函数含该常数 |
| INV-IBT-A01 | `NOTRACK` 前缀出现在指向 `nocf_check` 函数的间接分支前 | 函数指针 + 属性 |
| INV-IBT-A02 | 链接后两函数保留独立符号 | 字节相同但属性不同的两函数 + ICF |
| INV-IBT-M01 | `readelf -n` 含 `IBT` feature | 任意 `-fcf-protection=branch` 构建 |
| INV-IBT-M02 | `-Wl,-z,force-ibt` 下混编链接失败 | 一个 TU 开 CET, 另一个关 |
| INV-IBT-R02 | `si_code == SEGV_CPERR` 区分普通段错误 | 所有 IBT 违例 seed |

## 10. 开放问题 / 未覆盖 invariants (Follow-ups)

- **LLVM `X86IndirectBranchTracking` 与 GCC `ix86_function_needs_cet_endbr_p` 的 address-taken 启发式差异**: 两者对 "static 函数取地址后变 inline" 等场景的判定并不一一对应, 需要单独 lit test 对照.
- **FineIBT 启用时的 landing pad 字节模式**: 内核侧已 merge, 用户态 RFC 仍在讨论; 一旦 Clang trunk 启用, INV-IBT-P01 的 oracle 需扩展到 "endbr + 类型 hash" 双字节模式.
- **legacy bitmap 与 JIT**: V8 / LuaJIT / Node.js JIT 代码在 CET 下如何与 `ARCH_CET_LEGACY_BITMAP_BASE` 配合; DeFuzz 暂不覆盖.
- **AMD IBT-equivalent**: AMD Zen4+ 实现了 CET, 行为与 Intel 一致; 但 AMD SDM 措辞与 Intel SDM 有细微差异, 未来若 DeFuzz 支持 AMD-specific 回归需单独核对.
- **32-bit 用户空间 (endbr32)**: 当前 Linux 发行版几乎不对 i386 用户空间启用 CET; `endbr32` 的 runtime enforcement 实际极少见, 但编译器仍发射. 属于 "代码层 invariant 成立, runtime enforcement 不触发" 的特殊组合.
- **`NOTRACK` 前缀被攻击者滥用**: 理论上, 攻击者若能控制"带 NOTRACK 的间接分支指令"字节, 可绕过 IBT. GCC / LLVM 对此要求 `NOTRACK` 仅用于已证安全的 jump table; DeFuzz 可构造 "写入 `3E FF` 前缀到可执行内存" 的 seed 测试 W^X 与 CET 的交叉保护.
- **`endbr` 与 BTI (AArch64) 的等价性**: 两者在思路上完全对称 (landing pad + tracker state); 后续可做跨 ISA 共享的 invariant 抽象. 见 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 中 BTI 行对照.

## 11. 使用建议

- 新增 x86 编译器版本 / 发行版内核时, 对 §1-§6 逐条回归; §8 的 PR 列表作为 GCC / LLVM 版本退行的正控组.
- 运行 oracle 前必须先调用 `arch_prctl(ARCH_CET_STATUS)` (或读 `/proc/self/status`) 自检, 确认进程 IBT 实际被 kernel enforce, 否则所有 INV-IBT-R* 类 oracle 永不触发 (假阴).
- 静态扫描类 invariant (INV-IBT-B03, INV-IBT-M01, INV-IBT-M02) 可作为 CI 前置门禁, 无需启动 runtime.
- `likely-to-drift` 的 invariant (INV-IBT-B03, INV-IBT-P01 impl 侧, INV-IBT-I04, INV-IBT-I05) 在每次编译器 / 内核 major 升级时必须人工确认.
