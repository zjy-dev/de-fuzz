# AArch64 Branch Target Identification (BTI) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / LLVM/Clang / Arm A-profile ARM / AAPCS64 PAuth-ABI / Linux kernel / glibc 中与 **AArch64 Branch Target Identification (BTI)** 直接相关的 invariants 抽取归类, 作为 DeFuzz BTI oracle 的形式化依据.
>
> 机制简写与 survey 一致: **BTI** = AArch64 Branch Target Identification. 与 PAC / SCS 同属 `-mbranch-protection`, 但语义独立, 见 `@/home/yall/project/de-fuzz/docs/invariants/pointer-authentication.md` / `@/home/yall/project/de-fuzz/docs/invariants/shadow-call-stack.md`.

## 0. 术语与坐标

- **BTI**: Armv8.5-A 引入的前向 CFI 硬件特性. 启用后 *所有* 间接分支 (`BR <reg>`, `BLR <reg>`, `BR <reg>*` 等) 的目标指令必须是 `BTI` 指令; 否则触发 *Branch Target Exception* (`ESR_ELx.EC = 0x0D`), Linux 用户态翻译为 `SIGILL`, `si_code = ILL_BTCFI`.
- **`BTI` 指令**: 单字节 hint 指令 `0xD503241F` (BTI), `0xD503243F` (BTI c), `0xD503245F` (BTI j), `0xD503247F` (BTI jc). 4 字节, 在不支持 BTI 的 CPU 上是 NOP.
- **类型**: `c` (call target), `j` (jump target), `jc` (both), 无后缀 (无效, 仅占位). `c` 允许 `BLR` 进入; `j` 允许 `BR` 进入; `jc` 兼容. 函数入口用 `BTI c`, switch jump table 目标用 `BTI j`.
- **PSTATE.BTYPE**: CPU 内部 2-bit 状态字段, 间接分支后置为该分支类型 (`BR=10, BLR=01, BR-X16/X17=11`); 下一指令解码时与 `BTI` 类型比对, 不匹配则 `BTC`.
- **`-mbranch-protection=bti`**: 仅启用 BTI; `=standard` = `pac-ret + bti`; `=none` 关闭.
- **GNU Property `BTI` bit**: ELF note `NT_GNU_PROPERTY_TYPE_0` 中 `GNU_PROPERTY_AARCH64_FEATURE_1_AND` 的 bit 0 (`BTI = 0x1`).

每条 invariant 字段: `ID / statement / compiler / version / target / source_kind / source_url_or_path / evidence_snippet / version_sensitivity / oracle_mapping`.

## 1. 静态前提 (Static Preconditions)

### INV-BTI-E01 — `-mbranch-protection=bti` 启用 BTI 发射

- **statement**: GCC / Clang AArch64 选项 `-mbranch-protection=bti` 启用 BTI: 在所有可被间接调用的函数入口插 `BTI c`, switch jump table 目标插 `BTI j`. `=standard` 等于 `bti+pac-ret`. `=none` 关闭.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 9+, Clang 9+
- **target**: aarch64
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/AArch64-Options.html ; https://clang.llvm.org/docs/ClangCommandLineReference.html ; `gcc/config/aarch64/aarch64.cc` (`aarch64_handle_*_branch_protection`)
- **evidence_snippet**: GCC manual: *"`bti`: insert a `BTI` instruction at the beginning of every function that can be called indirectly"*.
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz BTI oracle 用 `=bti` 与 `=none` 作正反控组; 同一 seed 期望前者函数入口出现 `BTI c` (字节 `0x5F 0x24 0x03 0xD5`).

### INV-BTI-E02 — `-fhardened` 在 AArch64 默认是否含 BTI

- **statement**: GCC `-fhardened` 在 AArch64 上 *尚未* 隐式启用 `-mbranch-protection=standard`; 但发行版 (Ubuntu 24.04+, Fedora 38+) 通常通过 `dpkg-buildflags` / `redhat-rpm-config` 默认追加. DeFuzz 不能假设 `-fhardened` 自动开 BTI.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 14+
- **target**: aarch64 (Linux)
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: target-specific (发行版策略)
- **oracle_mapping**: DeFuzz CFLAGS 矩阵显式包含 `-mbranch-protection=bti`, 不依赖 `-fhardened`.

### INV-BTI-E03 — runtime enforcement 需 CPU + kernel + ld.so

- **statement**: BTI 在用户态生效要求 (a) CPU 报告 `ID_AA64PFR1_EL1.BT == 0b0001`; (b) 内核启用 BTI (Linux ≥ 5.8, 默认开); (c) ld.so 解析 GNU property 并通过 `mprotect(PROT_BTI)` 给可执行段加 BTI 标志. 任一缺失则 `BTI` 指令仅为 NOP, 间接分支不 enforce.
- **runtime**: Linux kernel + glibc ld.so
- **version**: Linux ≥ 5.8, glibc ≥ 2.32
- **target**: aarch64
- **source_kind**: user-doc + runtime
- **source_url_or_path**: https://www.kernel.org/doc/html/latest/arch/arm64/elf_hwcaps.html ; glibc `sysdeps/aarch64/dl-bti.c`
- **version_sensitivity**: target-specific
- **oracle_mapping**: DeFuzz 启动 oracle 前读 `getauxval(AT_HWCAP2) & HWCAP2_BTI`, 否则跳过 INV-BTI-R* 类断言.

## 2. 指令编码 (Encoding)

### INV-BTI-B01 — `BTI {c,j,jc}` 4 字节固定编码

- **statement**: 指令编码 (LE):
  - `BTI` (无后缀, hint #32): `0xD503241F`
  - `BTI c` (hint #34): `0xD503243F`
  - `BTI j` (hint #36): `0xD503245F`
  - `BTI jc` (hint #38): `0xD503247F`
  在不支持 BTI 的 CPU 上属 `HINT` 家族, 无副作用解码为 NOP.
- **hardware**: Armv8.5-A+
- **target**: aarch64
- **source_kind**: ABI-spec
- **source_url_or_path**: Arm A-profile ARM `BTI` 条目 (`https://developer.arm.com/documentation/ddi0487/`)
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 静态扫描 `.text` 字节模式, 计数 BTI 指令.

### INV-BTI-B02 — `BTI` 必须是间接分支后第一条指令

- **statement**: 间接分支跳转后, CPU 设置 `PSTATE.BTYPE`, 下一条 fetched 指令必须是与该 BTYPE 兼容的 `BTI` 形式. 不允许"先一条 nop 再 BTI".
- **hardware**: Armv8.5-A+
- **source_kind**: ABI-spec
- **source_url_or_path**: Arm ARM §D6
- **version_sensitivity**: stable
- **oracle_mapping**: 反例: 间接分支目标第一条不是 BTI -> `SIGILL`.

## 3. 插入位置 (Placement)

### INV-BTI-P01 — 全局函数入口必须 `BTI c`

- **statement**: 编译器在 `-mbranch-protection=bti` 下, 对以下函数发 `BTI c`:
  1. 所有外部可见函数 (`.globl`, weak, hidden 但非 static); 
  2. 所有被取地址的 static 函数 (保守判定可能被间接调用);
  3. 所有 PLT 入口 (链接器责任).
  仅被直接 `BL` 调用的 static 函数可省略.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 9+, Clang 9+
- **target**: aarch64
- **source_kind**: source + test
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc` (`aarch64_print_function_pointer_value`, `aarch64_output_bti`) ; `llvm/lib/Target/AArch64/AArch64BranchTargets.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 扫描 `.text` 中 global / address-taken 函数入口, 期望首字节序列含 `BTI c` (`0x5F 0x24 0x03 0xD5`).

### INV-BTI-P02 — switch jump table 目标用 `BTI j`

- **statement**: switch 展开为间接 `BR <reg>` 跳到 jump table 目标时, 目标 BB 入口必须是 `BTI j`. 当代码同时可被普通函数调用 (`BLR`) 与 switch 跳转 (`BR`) 进入时, 用 `BTI jc`.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: source + test
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc` ; `llvm/lib/Target/AArch64/AArch64BranchTargets.cpp` ; LLVM lit test `aarch64-bti-jump-table.ll`
- **version_sensitivity**: stable
- **oracle_mapping**: switch seed 验证 `BTI j` 字节模式.

### INV-BTI-P03 — `setjmp` / `__builtin_setjmp` 返回点需 `BTI c`

- **statement**: `longjmp` 通过 `BR <reg>` 间接跳回 `setjmp` 调用点, 编译器必须在 `setjmp` 调用之后紧接 `BTI c` (或 `jc`). 无此 BTI -> 跳回时 `SIGILL`.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: source + test
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc` ; `llvm/lib/Target/AArch64/AArch64BranchTargets.cpp` ; AArch64 lit test `setjmp-bti.ll`
- **version_sensitivity**: stable
- **oracle_mapping**: setjmp/longjmp seed.

### INV-BTI-P04 — 异常 landing pad 需 `BTI`

- **statement**: C++ 异常 / `__builtin_eh_return` landing pad 由 unwinder 间接 `BR` 进入, 必须以 `BTI` 开头. GCC 在 `aarch64.cc` 的 EH lowering 自动插入.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc` ; libgcc `unwind-dw2.c`
- **version_sensitivity**: stable
- **oracle_mapping**: try/catch seed.

### INV-BTI-P05 — PLT 入口由链接器发 `BTI c`

- **statement**: 动态链接 PLT 入口由 ld.bfd / lld 在生成 `.plt` 时插 `BTI c`. 否则跨 DSO 间接调用第一指令是 `ADRP` 等普通指令, 触发 `SIGILL`. 链接器自动检测 GNU property `BTI` 位决定是否使用 BTI-aware PLT.
- **linker**: binutils, lld
- **version**: binutils ≥ 2.33, lld ≥ 11
- **target**: aarch64
- **source_kind**: source
- **source_url_or_path**: binutils `bfd/elfnn-aarch64.c` (`elfNN_aarch64_finish_dynamic_symbol`) ; lld `ELF/Arch/AArch64.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 动态库调用 seed; oracle 反汇编 `.plt` 验证.

### INV-BTI-P06 — `nocf_check` 关闭函数级 BTI

- **statement**: GCC / Clang 函数属性 `__attribute__((nocf_check))` 关闭该函数的 `BTI c` 发射, 同时函数指针类型变更, 通过该指针调用时编译器不要求目标有 BTI. 类型系统区分 `void (*fp)(void)` 与带属性的版本.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: stable
- **oracle_mapping**: 函数属性 + 函数指针 seed.

## 4. 属性 / 局部控制

### INV-BTI-A01 — `target("branch-protection=...")` 函数级覆盖

- **statement**: GCC / Clang 支持 `__attribute__((target("branch-protection=...")))` 覆盖单个函数的 BTI 设定. 用于例如汇编 ABI 接口需保留无 BTI 入口.
- **compiler**: GCC 11+, Clang 13+
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html ; `gcc/config/aarch64/aarch64.cc`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 函数级 BTI 测试 seed.

### INV-BTI-A02 — `naked` 函数不发 BTI

- **statement**: `__attribute__((naked))` 函数不生成 prologue, 因此不发 BTI; 责任在用户在汇编中显式插.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: stable
- **oracle_mapping**: naked + 间接调用 seed 验证手写 BTI 必要性.

## 5. ELF 元数据

### INV-BTI-M01 — `GNU_PROPERTY_AARCH64_FEATURE_1_BTI` 位必须存在

- **statement**: 启用 BTI 的 ELF 对象在 `.note.gnu.property` 写 `GNU_PROPERTY_AARCH64_FEATURE_1_AND` 的 bit 0 (`BTI = 0x1`). 链接器对所有输入对象做 AND 归并; ld.so 用此位决定是否给可执行段加 `PROT_BTI`.
- **compiler + linker**: GCC, Clang, binutils, lld
- **version**: GCC 9+, Clang 9+, binutils ≥ 2.33, lld ≥ 11
- **target**: aarch64
- **source_kind**: ABI-spec + source
- **source_url_or_path**: https://github.com/ARM-software/abi-aa (PAuth-ABI 附录) ; `gcc/config/aarch64/aarch64.cc` (`aarch64_file_end_note`)
- **version_sensitivity**: stable
- **oracle_mapping**: `readelf -n` 含 `BTI`.

### INV-BTI-M02 — `-Wl,-z,force-bti` 链接期 enforcement

- **statement**: 链接器选项 `-z force-bti` 让链接器在发现任何输入对象缺 `BTI` 位时报错而非降级. 这是 "所有 TU 必须 `-mbranch-protection=bti`" 的链接期执行.
- **linker**: binutils, lld
- **version**: binutils ≥ 2.33, lld ≥ 11
- **source_kind**: user-doc
- **source_url_or_path**: binutils `ld` manual "AArch64 ELF Options"
- **version_sensitivity**: stable
- **oracle_mapping**: CI 前置门禁.

### INV-BTI-M03 — ld.so 按 AND 归并 BTI 位

- **statement**: glibc ld.so 加载每个 DSO 时, 把其 `BTI` 位与进程当前状态 AND. 任一 DSO 不带 BTI 即整个进程降级 (该 DSO 范围段不加 `PROT_BTI`). 但 *已加 PROT_BTI 的段不会被取消*, 所以混编时只是新加 DSO 不受 BTI 保护.
- **runtime**: glibc ld.so
- **version**: glibc ≥ 2.32
- **source_kind**: runtime
- **source_url_or_path**: glibc `sysdeps/aarch64/dl-bti.c`
- **version_sensitivity**: stable
- **oracle_mapping**: `dlopen` 老 DSO 验证.

## 6. 运行时语义

### INV-BTI-R01 — 间接分支目标无 `BTI` 触发 `SIGILL` + `ILL_BTCFI`

- **statement**: BTI 启用下, `BR <reg>` / `BLR <reg>` / `BR X16/X17` 分支后第一条指令若不是匹配的 BTI, 触发 BTC 异常, Linux 用户态翻为 `SIGILL`, `siginfo_t.si_code = ILL_BTCFI` (值 8). 这是 BTI 的根本 oracle 信号.
- **hardware + runtime**: Armv8.5-A + Linux kernel
- **version**: Linux ≥ 5.8
- **target**: aarch64
- **source_kind**: ABI-spec + runtime
- **source_url_or_path**: Arm ARM ; Linux `arch/arm64/kernel/traps.c` (`do_bti`)
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 用 `sigaction(SA_SIGINFO)` + `si_code == ILL_BTCFI` 区分普通 `SIGILL`.

### INV-BTI-R02 — 仅可执行段被设置 `PROT_BTI` 才 enforce

- **statement**: BTI enforcement 是页粒度: ld.so 在加载时通过 `mprotect(addr, size, PROT_READ | PROT_EXEC | PROT_BTI)` 标记, 否则 CPU 不做 BTI 检查. JIT 代码若未设置该标志, 即使发了 BTI 指令也不 enforce.
- **runtime**: Linux kernel + glibc ld.so
- **source_kind**: runtime
- **source_url_or_path**: Linux `arch/arm64/kernel/mmu.c` (`__enable_bti`) ; glibc `sysdeps/aarch64/dl-bti.c`
- **version_sensitivity**: stable
- **oracle_mapping**: JIT seed: 用户 mmap exec 段不加 PROT_BTI, BTI 不触发.

### INV-BTI-R03 — 信号 / 异常路径不破坏 BTYPE

- **statement**: CPU 在进入异常 / syscall 时保存 `PSTATE.BTYPE`; 返回时恢复. 因此信号路径不能用作 BTI 绕过.
- **hardware**: Armv8.5-A+
- **source_kind**: ABI-spec
- **source_url_or_path**: Arm ARM §D6
- **version_sensitivity**: stable
- **oracle_mapping**: 信号 seed 不需特殊处理.

## 7. 与其他机制的交互

### INV-BTI-I01 — BTI 与 PAC (`pac-ret`) 独立但常组合

- **statement**: BTI 保护 *前向边间接分支*; PAC ret-signing 保护 *返回地址*. 两者独立 ELF property 位, 通常通过 `-mbranch-protection=standard` 一起开. 见 `@/home/yall/project/de-fuzz/docs/invariants/pointer-authentication.md`.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: CFLAGS 矩阵分别穷举.

### INV-BTI-I02 — BTI 与 SCS 相容

- **statement**: SCS 仅影响 LR 保存路径, 不改变 `BR/BLR` 分支结构, 因此与 BTI 完全相容. 启用 SCS 不影响 BTI 字节序列.
- **compiler**: Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/shadow-call-stack.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-BTI-I03 — BTI 与 GCS (硬件 SCS) 互补

- **statement**: GCS 是 Armv9.4 引入的硬件影子栈, 独立于 BTI; 类似 x86 SHSTK 与 IBT 的关系.
- **compiler**: GCC 14+, Clang 18+
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/gcs.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-BTI-I04 — `objtool` 等价: Linux 内核 `vmlinux.o` BTI 校验

- **statement**: Linux 内核 AArch64 在 `CONFIG_ARM64_BTI_KERNEL=y` 时由 `kallsyms` / 链接脚本检验所有 `.text` 入口的 BTI 完整性. 这是用户态 `objtool` (x86 only) 的 AArch64 等价.
- **target-specific**: Linux kernel
- **version**: Linux ≥ 5.10
- **source_kind**: source
- **source_url_or_path**: Linux `arch/arm64/Kconfig` (`ARM64_BTI_KERNEL`) ; `arch/arm64/kernel/cpufeature.c`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 内核 seed 时构建本身是 oracle.

### INV-BTI-I05 — ICF 不得合并 `nocf_check` 与普通函数

- **statement**: 链接期 ICF (lld `--icf=all` / GCC IPA-ICF) 必须把 `nocf_check` 属性纳入函数判等签名; 否则可能合并出 BTI/无 BTI 混合, 破坏类型系统假设.
- **compiler + linker**
- **source_kind**: source + test
- **source_url_or_path**: lld `ICF.cpp` ; gcc `ipa-icf.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: ICF 回归 seed.

## 8. 验证与已知回归

### INV-BTI-VER-PLT-LATE — 早期 binutils PLT 缺 BTI

- **statement**: binutils ≤ 2.32 的 PLT 生成路径无 BTI 适配, 在 BTI 进程中调用动态符号会 `SIGILL`. 修复在 2.33 引入 BTI-aware PLT.
- **linker**: binutils
- **version**: 修复于 binutils 2.33
- **source_kind**: source
- **source_url_or_path**: binutils `bfd/elfnn-aarch64.c`
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 binutils + 共享库 seed.

### INV-BTI-VER-PR93009 — GCC 9 早期 jump table 漏 BTI

- **statement**: GCC 9 早期 AArch64 switch jump table 目标 BB 漏发 `BTI j`, 修复在 9.3 / 10.
- **compiler**: GCC
- **version**: 修复于 GCC 9.3
- **source_kind**: mailing-list
- **source_url_or_path**: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=93009
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 GCC switch seed.

### INV-BTI-VER-LLVM-D62609 — LLVM 9 早期 setjmp BTI 漏

- **statement**: LLVM D62609 之前, AArch64 setjmp 调用点未加 BTI. 修复在 LLVM 9.
- **compiler**: LLVM/Clang
- **version**: 修复于 LLVM 9
- **source_kind**: mailing-list
- **source_url_or_path**: https://reviews.llvm.org/D62609
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 Clang setjmp seed.

## 9. DeFuzz BTI Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-BTI-P01 | `SIGILL` + `si_code == ILL_BTCFI`, fault PC 在函数首指令 | 间接调用一个"应有 BTI 但未插"的函数 |
| INV-BTI-P02 | 同上 | switch + 跳到 jump table 目标 |
| INV-BTI-P03 | 同上 | setjmp + longjmp |
| INV-BTI-P04 | 同上 | C++ try/catch |
| INV-BTI-P05 | 同上 | dlopen + 跨 DSO 函数调用 |
| INV-BTI-A01 | `BTI c` 字节存在与否随属性变 | function attribute 测试 |
| INV-BTI-M01 | `readelf -n` 含 `BTI` | 任意 `=bti` 构建 |
| INV-BTI-M02 | `-z force-bti` 混编链接失败 | 一 TU 关 BTI, 一 TU 开 |
| INV-BTI-R02 | JIT 段无 PROT_BTI 时 BTI 不触发 | 用户 mmap exec |

## 10. 开放问题

- **AArch64 GCS 启用后 BTI 与 GCS 的字节序列重排**: GCC 14+ / Clang 18+ 添加 GCS 后, 函数 prologue 顺序变更, BTI 仍是首指令吗? 待补 invariant.
- **`pac-ret+leaf+b-key+bti` 组合的代码生成顺序**: 不同 `-mbranch-protection` 子选项排列下 PAC 指令与 BTI 的相对位置. 需对照 GCC/Clang 输出.
- **MTE (Memory Tagging) 与 BTI 的元数据冲突**: GNU property bits 都在同一 AND mask, 待补 BTI + MTE 的混合矩阵.
- **JIT BTI 配合**: V8 / LuaJIT 在 AArch64 上必须自管 `PROT_BTI` 与 BTI 指令插入, 实现细节按引擎差异. oracle 暂不覆盖.
- **`__nocfi` 在内核**: Linux 内核 AArch64 的 `__nocfi` 属性与 BTI 关系细节, 待补.

## 11. 使用建议

- 新增 AArch64 编译器版本 / 内核版本时, 对 §1, §3, §6 逐条回归.
- 运行 oracle 前必须 `getauxval(AT_HWCAP2) & HWCAP2_BTI` 自检, 否则 INV-BTI-R* 类 oracle 假阴.
- 静态扫描 (INV-BTI-P01-P05 字节模式, INV-BTI-M01) 作为 CI 前置门禁.
- `likely-to-drift` invariant (INV-BTI-A01 attribute spelling, INV-BTI-I04 内核策略) 在每次主分支升级人工核对.
