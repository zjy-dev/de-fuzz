# AArch64 / arm64e Pointer Authentication (PAC) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / LLVM/Clang / Arm A-profile ARM / AAPCS64 PAuth-ABI / Apple arm64e ABI / Linux kernel / glibc 中与 **AArch64 Pointer Authentication (PAC)** 直接相关的 invariants 抽取归类, 作为 DeFuzz PAC oracle 的形式化依据.
>
> 机制简写与 survey 一致: **PAC** = Pointer Authentication. 与 BTI / SCS 同属 `-mbranch-protection`. Apple `arm64e` ABI 是 PAC 在用户态最完整的部署形态.

## 0. 术语与坐标

- **PAC**: Armv8.3-A 引入的指针签名硬件特性. CPU 用 64-bit Authentication Code (PAC) 替代指针未使用的高位, 签时把 PAC 写入 (`PACIA{B,SP}`), 验时校验后剥离 PAC (`AUTIA{B,SP}`). 校验失败把 PAC 替换为非法地址模式; 后续解引用 / 跳转触发 `SIGSEGV` (经 FEAT_FPAC 后直接 `SIGILL`).
- **5 keys**: `IA, IB` (指令指针 A/B), `DA, DB` (数据指针 A/B), `GA` (general PAC, 用于 `PACGA` 任意值签名). `IA` 默认用于 LR ret-signing, `IB` 可选 (`pac-ret+b-key`).
- **discriminator**: 64-bit 鉴别值, 通常是 SP 或常数 + address. `paciasp` = `pacia LR, SP`. `__ptrauth(key, addr_div, disc)` 限定符在 Clang 给开发者细粒度控制.
- **`pac-ret`**: GCC/Clang 模式名, 表示函数 prologue 用 `PACIASP` 给 LR 签名, epilogue 用 `AUTIASP` 验. `pac-ret+leaf` 扩到 leaf 函数; `pac-ret+b-key` 用 IB key.
- **arm64e**: Apple Darwin ABI, 把 PAC 用到 vtable / 函数指针 / 异常 / 各种 C++ ABI 关键值. 是 PAC 应用最深的设计.
- **FEAT_FPAC**: Armv8.6+ 扩展, PAC 验证失败立即 `SIGILL` 而非延迟到解引用. 简化 oracle.
- **GNU Property `PAC` bit**: ELF `GNU_PROPERTY_AARCH64_FEATURE_1_AND` bit 1 (`PAC = 0x2`). 不强制运行时 enforcement, 只标识构建配置.

每条 invariant 字段同前.

## 1. 静态前提 (Static Preconditions)

### INV-PAC-E01 — `-mbranch-protection=pac-ret[+leaf][+b-key]`

- **statement**: GCC / Clang AArch64 选项 `-mbranch-protection=pac-ret` 在所有非 leaf 函数 prologue 发 `PACIASP`, epilogue 发 `AUTIASP`. `+leaf` 扩到 leaf 函数. `+b-key` 用 IB key 替代 IA. `=standard` = `pac-ret+bti`.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 7+ (基础), Clang 7+
- **target**: aarch64
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/AArch64-Options.html ; `gcc/config/aarch64/aarch64.cc` (`aarch64_handle_pac_ret_protection`, `aarch64_return_address_signing_enabled`)
- **evidence_snippet**: GCC manual: *"`pac-ret`: protect return addresses by signing"*.
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz CFLAGS 矩阵正反控组; oracle 抓 `PACIASP` (`0xD503233F`) / `AUTIASP` (`0xD50323BF`) 字节模式.

### INV-PAC-E02 — `__ptrauth(key, addr_div, disc)` 限定符 (Clang only, arm64e)

- **statement**: Clang `__ptrauth(<key>, <address-discriminate>, <constant-discriminator>)` 限定符给指针字段添加 PAC. 写入时自动签, 读出时自动验. GCC 暂不实现该限定符. 是 arm64e 的核心抽象.
- **compiler**: LLVM/Clang
- **version**: Clang 11+ (基础), arm64e 路径需 Apple toolchain 或社区分支
- **target**: aarch64 (主要 Apple 平台)
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/PointerAuthentication.html ; https://clang.llvm.org/docs/LanguageExtensions.html
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: arm64e 路径独立 seed.

### INV-PAC-E03 — `-fhardened` 不隐式启用 PAC

- **statement**: GCC `-fhardened` *不* 隐式开启 PAC (与 SCP / SP 不同). PAC 必须显式 `-mbranch-protection=...`.
- **compiler**: GCC
- **version**: GCC 14+
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

### INV-PAC-E04 — runtime 需 CPU + kernel 支持

- **statement**: PAC 实际生效要 (a) CPU 报告 `ID_AA64ISAR1_EL1.{API,APA,GPI,GPA}` 非零; (b) 内核启用 PAC (Linux ≥ 5.0 用户态, ≥ 5.7 内核态). 否则 `PACIASP` 等指令是 NOP, 安全保证失效.
- **runtime**: Linux kernel
- **version**: Linux ≥ 5.0
- **target**: aarch64
- **source_kind**: user-doc + runtime
- **source_url_or_path**: https://www.kernel.org/doc/html/latest/arch/arm64/pointer-authentication.html
- **version_sensitivity**: target-specific
- **oracle_mapping**: oracle 启动前 `getauxval(AT_HWCAP) & HWCAP_PACA`.

## 2. 指令编码

### INV-PAC-B01 — PAC 系列指令 4 字节固定编码

- **statement**: 关键编码:
  - `PACIASP`: `0xD503233F` (sign LR with IA + SP)
  - `AUTIASP`: `0xD50323BF` (auth LR with IA + SP)
  - `PACIBSP`: `0xD503237F`, `AUTIBSP`: `0xD50323FF`
  - `RETAA`: `0xD65F0BFF` (combined ret + autia)
  - `RETAB`: `0xD65F0FFF`
  - `BLRAA`/`BLRAAZ`/`BRAA`/`BRAAZ`: 间接调用带 auth
  - `XPACI`/`XPACD`/`XPACLRI`: strip PAC 不验证
  - 在不支持 PAC 的 CPU 上 `PACI*SP/AUTI*SP/XPACI/XPACLRI` 在 hint space 解码为 NOP, `BLRAA*` 等独立编码不是 NOP.
- **hardware**: Armv8.3-A+
- **source_kind**: ABI-spec
- **source_url_or_path**: Arm A-profile ARM ; AAPCS64 PAuth-ABI
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 静态扫描指令字节.

### INV-PAC-B02 — PAC 字节宽度由 TBI / VA 配置决定

- **statement**: PAC 占用指针的虚拟地址未使用高位, 字节数依内核 VA 配置 (39/42/48/52 位 VA), 通常 7-15 bits. PAC 与 Top Byte Ignore (TBI) 不冲突: TBI 把 bit 56-63 留给 tag, PAC 占 bit 47/48 - 55. 这决定攻击者暴力空间: 7 bit 即 128 次猜中 1 次, 15 bit 即 1/32768.
- **hardware**: Armv8.3-A+
- **source_kind**: ABI-spec + paper
- **source_url_or_path**: Arm ARM §D6 ; "Pointer Authentication on ARMv8.3" (S&P 2019)
- **version_sensitivity**: target-specific (内核 VA 配置)
- **oracle_mapping**: oracle 不依赖具体 bit 数, 但需注意 brute-force 空间.

## 3. 插入位置 (Placement)

### INV-PAC-P01 — 非 leaf 函数 prologue 必须 `PACIASP`, epilogue 对称 `AUTIASP`

- **statement**: 启用 `pac-ret` 后, GCC/Clang 在非 leaf 函数 (即调用其他函数, LR 需保存) 的 prologue 第一指令 (或 BTI 之后) 发 `PACIASP`; epilogue 在 `RET` 之前发 `AUTIASP`, 或合并为 `RETAA`. 必须 *对称* — prologue 与 epilogue 一对一. 中间 LR 不可被覆写但又不重新签.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 7+, Clang 7+
- **source_kind**: source + 注释
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc` ; `llvm/lib/Target/AArch64/AArch64PointerAuth.cpp`
- **evidence_snippet**: GCC `aarch64.cc` 注释明确 PAC 与 LR 安全窗口.
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 抓 prologue/epilogue PAC 指令对.

### INV-PAC-P02 — `+leaf` 扩到所有函数

- **statement**: `pac-ret+leaf` 让编译器对所有函数 (含 leaf) 发 PAC 序列, 不依赖"是否调用其他函数". 一定程度下保护 leaf 函数被构造覆盖 LR 后跳到 ROP gadget.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/AArch64-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: leaf 函数 + LR 覆盖 seed.

### INV-PAC-P03 — `+b-key` 与 `pac-ret` 不可链混用

- **statement**: `+b-key` 让 `PACIBSP/AUTIBSP` 替代 `PACIASP/AUTIASP`, 用 IB key. 同一进程内 *不能* 一部分库用 IA, 一部分用 IB; 否则 caller 与 callee 的 PAC 无法对应. 这意味着发行版必须全 IA 或全 IB. 当前 Linux glibc 默认 IA.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: user-doc + source
- **source_url_or_path**: AAPCS64 PAuth-ABI
- **version_sensitivity**: stable
- **oracle_mapping**: 反例 seed: 混 IA / IB 库, 期望 `SIGILL`.

### INV-PAC-P04 — 函数指针类型: arm64e 用 `__ptrauth` 强类型化

- **statement**: arm64e ABI 把所有 C++ vtable / member function pointer / Objective-C `IMP` 都加 `__ptrauth(IA, address-discriminate, type-disc)`; vtable 每条 entry 单独签名. 普通 Linux ABI 默认 *不* 给 C 函数指针签名 (即"non-PAC C function pointer"), 因此 substitution attack 仍可能绕过. 这是 arm64e 与 Linux PAC ABI 的安全模型差异核心.
- **compiler**: Clang (arm64e)
- **target**: aarch64-apple-darwin
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/PointerAuthentication.html
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: arm64e seed: vtable substitution 应失败.

### INV-PAC-P05 — `setjmp` / `longjmp` 维护 LR 签名

- **statement**: `jmp_buf` 在 PAC 启用下保存的是 *已签名的 LR*. `longjmp` 直接恢复后, `RET` / `AUTIASP` 自动验证. 因此 `jmp_buf` 内被篡改时 `longjmp` 后下一 `ret` 触发 PAC fault. glibc `setjmp.S` 在 PAC 路径下显式保存 PAC 状态.
- **runtime**: glibc
- **version**: glibc ≥ 2.30 (CET/PAC 完整支持)
- **source_kind**: source
- **source_url_or_path**: glibc `sysdeps/aarch64/setjmp.S`
- **version_sensitivity**: stable
- **oracle_mapping**: setjmp + 故意篡改 jmp_buf -> SIGSEGV/SIGILL.

## 4. 属性 / 局部控制

### INV-PAC-A01 — `target("branch-protection=...")` 函数级覆盖

- **statement**: 同 BTI, GCC/Clang 支持 `target("branch-protection=pac-ret")` 函数级 attribute, 覆盖 TU 默认.
- **compiler**: GCC 11+, Clang 13+
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 函数级测试.

### INV-PAC-A02 — `__ptrauth_*` builtin (Clang)

- **statement**: Clang 提供 `__builtin_ptrauth_sign_unauthenticated`, `__builtin_ptrauth_auth`, `__builtin_ptrauth_strip`, `__builtin_ptrauth_sign_generic_data` 等 builtins 用于显式 PAC 操作. 文档明确给出每个 builtin 的安全使用约束.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/LanguageExtensions.html
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: arm64e 自定义 seed.

## 5. ELF 元数据

### INV-PAC-M01 — `GNU_PROPERTY_AARCH64_FEATURE_1_PAC` bit

- **statement**: 启用 `pac-ret` 的 ELF 在 GNU property 写 `PAC = 0x2`. 链接器 AND 归并; ld.so 不依赖此位做 enforcement, 但工具 (compiler-rt / debugger / unwinder) 用于识别构建配置.
- **compiler + linker**: GCC, Clang, binutils, lld
- **version**: GCC 9+, Clang 9+, binutils ≥ 2.33
- **target**: aarch64
- **source_kind**: ABI-spec
- **source_url_or_path**: AAPCS64 PAuth-ABI 附录
- **version_sensitivity**: stable
- **oracle_mapping**: `readelf -n` 含 `PAC`.

### INV-PAC-M02 — `-Wl,-z,force-pac-plt` 链接期 enforcement

- **statement**: 链接器选项 `-z pac-plt` 让 ld 生成 PAC-aware PLT (PLT entry 用 `BLR` + PAC). `-z force-pac-plt` 进一步在缺位时报错.
- **linker**: binutils, lld
- **source_kind**: user-doc
- **source_url_or_path**: binutils `ld` "AArch64 ELF Options"
- **version_sensitivity**: stable
- **oracle_mapping**: CI 前置门禁.

## 6. 运行时语义

### INV-PAC-R01 — 验证失败 (无 FEAT_FPAC) 表现为 `SIGSEGV`

- **statement**: `AUTI*` 验证失败时, CPU 把 PAC 字段替换为非法地址模式 (高位 `0x20000000_00000000` 之类), 后续解引用 / 间接跳转触发常规 `SIGSEGV` / `SIGBUS`. siginfo 与普通指针错误难以直接区分, 仅能通过 fault 地址模式或 PC 上下文推断.
- **hardware + runtime**: Armv8.3-A + Linux kernel
- **version**: Linux ≥ 5.0
- **source_kind**: ABI-spec + runtime
- **source_url_or_path**: Arm ARM §D6 ; Linux `arch/arm64/kernel/traps.c`
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 看 `SIGSEGV` + fault PC 在 `AUT*` / `RET*` 附近.

### INV-PAC-R02 — FEAT_FPAC 直接 `SIGILL`

- **statement**: Armv8.6+ 的 FEAT_FPAC 让验证失败立即 `SIGILL`, `si_code = ILL_ILLOPC`. 这简化 oracle: 不需推断 fault 来源.
- **hardware**: Armv8.6+
- **source_kind**: ABI-spec
- **source_url_or_path**: Arm ARM "FEAT_FPAC"
- **version_sensitivity**: target-specific
- **oracle_mapping**: 现代 CPU 上首选信号.

### INV-PAC-R03 — `setjmp` / fork: PAC keys 继承

- **statement**: `fork` 时子进程继承父的 PAC keys (内核 `arch_dup_task_struct` 复制 `mm_context_t.keys`); `clone(CLONE_VM)` 同 mm 共享. `execve` 时内核重新随机化 keys. 因此一个进程内的 PAC 值在 fork 后跨父子可继续工作, exec 后失效.
- **runtime**: Linux kernel
- **source_kind**: source
- **source_url_or_path**: Linux `arch/arm64/kernel/process.c`
- **version_sensitivity**: stable
- **oracle_mapping**: fork+ret seed.

### INV-PAC-R04 — `prctl(PR_PAC_SET_ENABLED_KEYS)` 控制单进程 keys

- **statement**: `prctl(PR_PAC_SET_ENABLED_KEYS, ...)` 可在用户态选择启用哪些 key, 也可重新随机化. 用于沙箱 / debugger 调试. 关闭某 key 后, 该 key 的 `PACI*` 变为 NOP, 已签的指针解释为非法.
- **runtime**: Linux kernel
- **version**: Linux ≥ 5.7
- **source_kind**: runtime
- **source_url_or_path**: Linux `arch/arm64/kernel/pointer_auth.c`
- **version_sensitivity**: stable
- **oracle_mapping**: 调试 / 沙箱 seed.

## 7. 与其他机制的交互

### INV-PAC-I01 — PAC ret-signing + SHSTK / GCS 互补

- **statement**: x86 SHSTK / Aarch64 GCS 是硬件影子栈, 直接对 `ret` 比对; PAC ret-signing 是在每个返回地址上加密签. 概念上互补但通常二选一. 同时启用允许更深防御但代码膨胀.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/shadow-stack.md` ; `@/home/yall/project/de-fuzz/docs/invariants/gcs.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-PAC-I02 — PAC + BTI 共用 `-mbranch-protection=standard`

- **statement**: `=standard` = `pac-ret + bti`. 两者独立, 但常并用. 见 `@/home/yall/project/de-fuzz/docs/invariants/bti.md`.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/AArch64-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-PAC-I03 — `memcpy` 与 PAC: address discriminator 不可序列化

- **statement**: 因 PAC 与具体地址绑定 (address discriminator), `memcpy` 一个 PAC 指针字段到另一个地址, 验证会失败. 因此函数指针字段如要兼容 `memcpy`, 不应使用 address discriminator. arm64e ABI 明确要求 C `memcpy` 兼容性的指针字段不带 address div.
- **compiler**: Clang (arm64e)
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/PointerAuthentication.html
- **version_sensitivity**: stable
- **oracle_mapping**: arm64e seed: memcpy + verify.

### INV-PAC-I04 — vtable / dynamic_cast / member function pointer (arm64e)

- **statement**: arm64e 把 vtable entry 单独签名 (key=IA, addr-disc=true, const-disc=type ID). `dynamic_cast` 实现要先 strip 再签新. member function pointer 是 `{signed_ptr, this_adjust}`. 见 Clang docs §"Layout and ABI".
- **compiler**: Clang (arm64e)
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/PointerAuthentication.html ; Itanium C++ ABI
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: vtable hijack 防御 seed.

### INV-PAC-I05 — Linux 内核 PAC

- **statement**: Linux 内核 AArch64 自 5.7+ 启用 PAC ret-signing (`CONFIG_ARM64_PTR_AUTH_KERNEL=y`), 用 IB key, 与用户态 IA 区隔. 内核 PAC 故障 panic.
- **target-specific**: Linux kernel
- **version**: Linux ≥ 5.7
- **source_kind**: source
- **source_url_or_path**: Linux `arch/arm64/Kconfig` ; `arch/arm64/kernel/pointer_auth.c`
- **version_sensitivity**: stable
- **oracle_mapping**: 内核 seed.

## 8. 验证与已知回归

### INV-PAC-VER-CVE-2023-4039 — AArch64 SP + PAC 与动态分配

- **statement**: CVE-2023-4039 (PR111703): AArch64 SP 在含 alloca / VLA 时, canary 与 LR 的相对位置不正确, 攻击者可绕 canary 改写 LR. PAC ret-signing 在该场景独立工作 (因 `AUTIASP` 仍验), 但揭示了 PAC 与 SP 互依赖的 invariant. 修复在 GCC 13.2 / Clang 17.
- **compiler**: GCC, Clang
- **version**: 修复于 GCC 13.2, Clang 17
- **source_kind**: bug-disclosure + mailing-list
- **source_url_or_path**: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=111703 ; https://rtx.meta.security/mitigation/2023/09/12/CVE-2023-4039.html
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 GCC + alloca seed.

### INV-PAC-VER-PR93423 — GCC 早期 PAC + EH unwind 漏

- **statement**: GCC PR93423: AArch64 PAC + 异常 unwind 时 LR 状态恢复有边角错误. 修复在 GCC 10.
- **compiler**: GCC
- **version**: 修复于 GCC 10
- **source_kind**: mailing-list
- **source_url_or_path**: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=93423
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 GCC C++ 异常 seed.

### INV-PAC-VER-LLVM-D85288 — Clang PAC ret-signing GCC 兼容

- **statement**: Clang D85288 等系列补丁让 Clang AArch64 PAC ret-signing 与 GCC 二进制兼容, 早期版本字节模式存在分歧.
- **compiler**: LLVM/Clang
- **version**: 修复散布 Clang 11-12
- **source_kind**: mailing-list
- **source_url_or_path**: https://reviews.llvm.org/D85288
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 跨编译器 ABI 兼容 seed.

## 9. DeFuzz PAC Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-PAC-P01 | `SIGSEGV` 或 `SIGILL`, fault PC 在 `RET`/`AUTIASP` | 缓冲区覆盖 LR 后 ret |
| INV-PAC-P02 | 同上 | leaf 函数 + LR 覆盖 |
| INV-PAC-P03 | `SIGSEGV/SIGILL` | 混 IA/IB 库 |
| INV-PAC-P04 | `SIGSEGV` | arm64e vtable substitution |
| INV-PAC-P05 | 同上 | setjmp + 篡改 jmp_buf |
| INV-PAC-R02 | `SIGILL` (FEAT_FPAC) | 任意 PAC fault |
| INV-PAC-I03 | 同上 | memcpy 带 address-disc 的指针 |
| INV-PAC-VER-CVE-2023-4039 | canary 漏检但 PAC 拦截 | 老 GCC alloca + LR 覆盖 |

## 10. 开放问题

- **PAC 与 ASLR brute-force**: PAC bit 数取决于 VA 配置, brute-force 空间可量化, oracle 是否需统计?
- **`__ptrauth` 在 GCC 实现路线图**: GCC 主线尚不支持, 待补充版本敏感 invariant.
- **arm64e 与 Linux PAC 的 ABI 差异**: 部分 builtins / 限定符仅 Apple 工具链支持. DeFuzz 跨平台 oracle 需明确分支.
- **`-mbranch-protection=gcs`**: GCC 14+ / Clang 18+ 添加 GCS 支持, 与 PAC 同选项. 见 `@/home/yall/project/de-fuzz/docs/invariants/gcs.md`.
- **PACBTI hint 编码**: `BTI` 指令本身在 PAC enabled 时仍是普通 BTI, 但 `PACIASP` 等指令在 BTI 下也算 BTI c (因为它们在 hint space). 待详细文档化.

## 11. 使用建议

- 新增编译器版本时, 对 §1, §3, §6, §7 逐条回归.
- 运行 oracle 前 `getauxval(AT_HWCAP) & HWCAP_PACA` + `cat /proc/cpuinfo | grep paca` 自检.
- arm64e 路径仅在 macOS / iOS toolchain 下完整, Linux PAC 范围有限.
- `likely-to-drift` invariant (INV-PAC-E02 `__ptrauth`, INV-PAC-I04 vtable schema) 在每次 Clang major 升级人工核对.
