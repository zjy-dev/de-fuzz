# AArch64 Guarded Control Stack (GCS) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / LLVM/Clang / Arm A-profile ARM (Armv9.4) / AAPCS64 / Linux kernel / glibc 中与 **AArch64 Guarded Control Stack (GCS)** 直接相关的 invariants 抽取归类, 作为 DeFuzz GCS oracle 的形式化依据.
>
> 机制简写与 survey: **GCS** = AArch64 v9.4 硬件影子栈 (FEAT_GCS). 是软件 SCS 的硬件版, 也是 x86 SHSTK 的 AArch64 对偶.

## 0. 术语与坐标

- **GCS**: Armv9.4 添加的硬件影子栈. `BL` 指令在写 LR 同时把返回地址 push 到 GCS; `RET` 指令把常规栈或 LR 提供的返回地址与 GCS 顶端比对, 不一致则 **Guarded Control Stack Exception** (GCSE).
- **GCSPR_EL0**: 用户态 GCS 指针寄存器, CPU 内部维护, `MRS` 读, `MSR` 写需特权.
- **GCS pointer ops**: `GCSPUSH`, `GCSPOP`, `GCSSS1`/`SS2` (signal save/restore), `GCSB DSYNC` 数据同步屏障.
- **GCS page**: 内核分配的特殊 VMA, 页表 attr `Guarded Control Stack` 表示, 普通 `STR` 写入触发 fault.
- **GNU property `GCS` bit**: ELF `GNU_PROPERTY_AARCH64_FEATURE_1_AND` bit 2 (`GCS = 0x4`).
- **`-mbranch-protection=gcs` / `=standard+gcs`**: 编译器选项.
- **`prctl(PR_SHADOW_STACK_*)`**: Linux ≥ 6.13 接口启用 GCS, 类似 x86 `arch_prctl(ARCH_SHSTK_*)`.

每条 invariant 字段同前.

## 1. 静态前提 (Static Preconditions)

### INV-GCS-E01 — `-mbranch-protection=gcs` 编译期 opt-in

- **statement**: GCC 14+, Clang 18+ 在 AArch64 上接受 `-mbranch-protection=gcs` 启用 GCS 元数据与若干必要的 codegen (例如 `setjmp` / 异常 unwind 路径调整). GCS 的核心由 `BL/RET` 隐式驱动, 编译器主要工作是 ELF property 与 unwinder 协调.
- **compiler**: GCC 14+, LLVM/Clang 18+
- **target**: aarch64
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/AArch64-Options.html ; `gcc/config/aarch64/aarch64.cc` (`aarch64_handle_*_branch_protection`) ; `llvm/lib/Target/AArch64/AArch64.td`
- **version_sensitivity**: likely-to-drift (实现仍在演进)
- **oracle_mapping**: DeFuzz CFLAGS 矩阵 `=gcs` vs `=none`; oracle 主要看 ELF property 与 runtime 行为.

### INV-GCS-E02 — `-fhardened` 暂不隐式开启

- **statement**: 截至 GCC 14, `-fhardened` 不隐式启用 GCS. 部分发行版 (Fedora 40+) 在 dpkg/RPM build flags 默认追加. DeFuzz 不假设.
- **compiler**: GCC 14+
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: target-specific
- **oracle_mapping**: 矩阵显式控制.

### INV-GCS-E03 — runtime enforcement 需 CPU + kernel + ld.so

- **statement**: GCS 实际生效要 (a) CPU 报告 `ID_AA64PFR1_EL1.GCS == 1`; (b) 内核启用 GCS (Linux ≥ 6.13, `CONFIG_ARM64_GCS`); (c) 通过 `prctl(PR_SHADOW_STACK_ENABLE)` 给进程开 GCS; (d) ld.so 通过 GNU property AND 归并决定调用 prctl. 任一缺失则 `RET` 不做 GCS 比对.
- **runtime**: Linux kernel + glibc
- **version**: Linux ≥ 6.13, glibc ≥ 2.41 (建议)
- **target**: aarch64
- **source_kind**: user-doc + source
- **source_url_or_path**: Linux `Documentation/arch/arm64/gcs.rst` ; `arch/arm64/kernel/gcs.c`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 启动 oracle 前 `prctl(PR_GET_SHADOW_STACK_STATUS)` 自检.

## 2. 指令编码

### INV-GCS-B01 — `BL` 隐式 GCSPUSH

- **statement**: GCS 启用下, `BL` (branch with link) 在写 LR 之外, 隐式把返回地址 push 到 `GCSPR_EL0`. `BLR` / `BLRAA` 同理. 不需编译器显式发指令.
- **hardware**: Armv9.4-A
- **target**: aarch64
- **source_kind**: ABI-spec
- **source_url_or_path**: Arm A-profile ARM "FEAT_GCS"
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描不需; oracle 看 runtime 行为.

### INV-GCS-B02 — `RET` 隐式 GCSPOP + 比对

- **statement**: GCS 启用下, `RET` (`ret xN`) 取 [xN] (常规 LR) 与 GCSPR_EL0 顶端比对; 不一致 -> GCSE 异常, Linux 翻为 `SIGSEGV` + `si_code = SEGV_CPLATFORM` 或类似 (具体 sigcode 内核版本敏感, 参考 Linux GCS 文档).
- **hardware**: Armv9.4-A
- **source_kind**: ABI-spec
- **source_url_or_path**: Arm ARM "FEAT_GCS"
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 缓冲区溢出 LR 后 ret -> GCSE.

### INV-GCS-B03 — `GCSPUSH/POP` 显式指令

- **statement**: 用户态 `GCSPUSH Xn` 把 Xn push 到 GCS, `GCSPOP Xn` 弹出. 用于 `setjmp/longjmp` / makecontext / unwinder. 普通用户代码极少手动调用.
- **hardware**: Armv9.4-A
- **source_kind**: ABI-spec
- **source_url_or_path**: Arm ARM
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描 setjmp 路径.

### INV-GCS-B4 — `GCSSS1` / `GCSSS2` 信号路径

- **statement**: 信号处理需把当前 GCS token 保存到 alt-shadow-stack, 由 `GCSSS1` (save) 和 `GCSSS2` (restore) 配合 token. 内核 `do_signal` 路径自动调用.
- **runtime**: Linux kernel
- **source_kind**: source
- **source_url_or_path**: Linux `arch/arm64/kernel/signal.c`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 信号处理 seed.

## 3. 栈帧约束

### INV-GCS-F01 — leaf 函数不 push GCS (不 BL)

- **statement**: 无被调函数的 leaf 函数没有 `BL`, 因此不入 GCS, 与 SCS 同. 缓冲区溢出对 leaf 函数 LR 仍然无效 (因 RET 直接用 LR 而非内存还原).
- **hardware**: Armv9.4-A
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: leaf seed.

### INV-GCS-F02 — `setjmp` / `longjmp` 必须保存 / 恢复 GCSPR

- **statement**: glibc `setjmp.S` 在 GCS 启用下 `mrs xN, gcspr_el0` 保存 GCS 指针; `longjmp` 用 `GCSPOP` 循环或一次性 GCS pointer 调整 (具体由 glibc 选择). 失败则 longjmp 后 ret 触发 GCSE.
- **runtime**: glibc
- **source_kind**: source
- **source_url_or_path**: glibc `sysdeps/aarch64/setjmp.S` (GCS-aware 路径)
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: setjmp/longjmp seed.

### INV-GCS-F03 — 异常 unwind 通过 `GCSPOP` 跨多帧

- **statement**: C++ 异常 unwind / `__builtin_eh_return` 跨 N 帧时, libgcc/libunwind 需循环 GCSPOP 把 GCS 调整到匹配位置. 缺失则 unwind 后第一个 RET 触发 GCSE.
- **runtime**: libgcc, libunwind
- **source_kind**: source
- **source_url_or_path**: libgcc `unwind-dw2.c` (GCS-aware 补丁)
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: try/catch 跨多帧 seed.

### INV-GCS-F04 — `makecontext` 需内核协助 (`map_gcs_stack`)

- **statement**: 用户级协程要为新协程分配 GCS 段并写入入口返回地址; 由于用户态默认 GCS 写禁用, glibc 必须通过专门 syscall (`map_shadow_stack` 类似的 GCS 版本) 让内核协助.
- **runtime**: glibc + Linux kernel
- **version**: Linux ≥ 6.13
- **source_kind**: source
- **source_url_or_path**: Linux `arch/arm64/kernel/gcs.c`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: makecontext 协程 seed.

## 4. 属性 / 局部控制

### INV-GCS-A01 — 无函数级关停

- **statement**: GCS 由硬件 BL/RET 驱动, 函数级属性无法关停. 与 SHSTK 同, 这是 hardware-rooted 设计的特性.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

### INV-GCS-A02 — `naked` 函数仍受 GCS 约束

- **statement**: naked 函数仍由硬件 BL/RET 驱动 GCS, 用户责任保持 GCS 协议正确.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

## 5. ELF 元数据

### INV-GCS-M01 — `GNU_PROPERTY_AARCH64_FEATURE_1_GCS` bit

- **statement**: 启用 GCS 的 ELF 必须在 GNU property 写 bit 2 (`GCS = 0x4`). 链接器 AND 归并; ld.so 据此决定 prctl 启用. 任一对象缺位即整个进程 GCS 关闭.
- **compiler + linker**: GCC 14+, Clang 18+, binutils ≥ 2.42, lld ≥ 18
- **target**: aarch64
- **source_kind**: ABI-spec
- **source_url_or_path**: AAPCS64 PAuth-ABI 附录
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: `readelf -n` 含 `GCS`.

### INV-GCS-M02 — `-Wl,-z,gcs=always` 链接期强制

- **statement**: 链接器选项 (binutils ≥ 2.42) `-z gcs=always|never|implicit` 控制是否在 *所有* 输入对象都未声明 GCS 时仍发 GCS property bit. `=always` 时混编可能破坏运行时正确性 — 因此通常 `=implicit` (默认: 全部对象有 GCS 才发 bit).
- **linker**: binutils
- **source_kind**: user-doc
- **source_url_or_path**: binutils `ld` "AArch64 ELF Options"
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: CI 前置门禁.

## 6. 运行时语义

### INV-GCS-R01 — RET 不一致 -> GCSE -> `SIGSEGV`

- **statement**: GCS 启用下 RET 指令检测到栈/影子栈返回地址不一致 -> GCSE -> Linux 翻 `SIGSEGV`. siginfo 由 Linux 内核版本决定具体 si_code, 当前 6.13 起使用 `SEGV_CPLATFORM` 或专用 GCS code (具体待文档化).
- **hardware + runtime**: Armv9.4 + Linux kernel
- **version**: Linux ≥ 6.13
- **source_kind**: source + 文档
- **source_url_or_path**: Linux `arch/arm64/kernel/traps.c`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 缓冲区溢出 LR seed.

### INV-GCS-R02 — GCS 段写保护 (W^X 强约束)

- **statement**: GCS 段是特殊 VMA, 普通 `STR` 写触发 fault. 仅 `BL/RET/GCSPUSH/POP/SS1/SS2` 等 GCS 专用指令可写. 这是攻击者即便有任意写也无法直接修改 GCS 的根基.
- **runtime**: Linux kernel
- **source_kind**: source
- **source_url_or_path**: Linux `arch/arm64/kernel/gcs.c` ; `arch/arm64/include/asm/pgtable.h` (GCS PTE attr)
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 反例: 普通 store 写 GCS 段 -> SIGSEGV.

### INV-GCS-R03 — 多线程 / fork

- **statement**: 每线程独立 GCS 段, 内核管理. fork 时子进程通过 COW 继承; clone 共享 mm 时共享 GCS. exec 重置.
- **runtime**: Linux kernel
- **source_kind**: source
- **source_url_or_path**: Linux `arch/arm64/kernel/gcs.c`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: fork+ret seed.

## 7. 与其他机制的交互

### INV-GCS-I01 — GCS 与软件 SCS 互斥优先 GCS

- **statement**: 启用 GCS 后, 软件 SCS (`-fsanitize=shadow-call-stack`) 多余且会浪费 x18. 一般工具链组合: 老 CPU 用软 SCS, Armv9.4+ 用 GCS. 见 `@/home/yall/project/de-fuzz/docs/invariants/shadow-call-stack.md`.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **version_sensitivity**: target-specific
- **oracle_mapping**: 矩阵.

### INV-GCS-I02 — GCS + PAC ret-signing 互补

- **statement**: 与 SCS 同概念: PAC 加密, GCS 隔离. 同启提供深度防御.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/pointer-authentication.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-GCS-I03 — GCS + BTI

- **statement**: GCS 保护反向边, BTI 保护前向边, 完全正交.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/bti.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-GCS-I04 — GCS 与 x86 SHSTK 跨 ISA 等价

- **statement**: 思路对称, 都是硬件影子栈. oracle 信号抽象: `RET` 不一致 -> 同步异常. 跨 ISA 测试用同一 seed 验证.
- **compiler**: 跨架构
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/shadow-stack.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 跨 ISA 矩阵.

## 8. 验证与已知回归

### INV-GCS-VER-LINUX-EARLY — Linux GCS 用户态早期补丁集

- **statement**: Linux 6.13 用户态 GCS 是 first merge; 早期 patch 集 (LKML 2024) 经多版迭代. DeFuzz 跑 GCS oracle 必须记录精确内核 commit hash.
- **runtime**: Linux kernel
- **version**: Linux 6.13+
- **source_kind**: mailing-list
- **source_url_or_path**: lkml.kernel.org "GCS user-mode" 主题
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 内核版本敏感.

### INV-GCS-VER-GLIBC — glibc setjmp/longjmp 待补完

- **statement**: glibc 主线 GCS 支持仍在补丁阶段 (2024 末-2025 初). 老 glibc 在新内核 GCS 启用进程中可能 longjmp 失败. 待 ABI 稳定后补 invariant.
- **runtime**: glibc
- **version**: 计划 glibc 2.41+
- **source_kind**: mailing-list
- **source_url_or_path**: sourceware.org/glibc 邮件归档 "GCS"
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: glibc 版本回归 seed.

## 9. DeFuzz GCS Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-GCS-R01 | `SIGSEGV` (GCSE), fault PC 在 RET | 缓冲区覆盖 LR + ret |
| INV-GCS-F02 | 同上 | setjmp + 篡改 jmp_buf |
| INV-GCS-F03 | 同上 | C++ 异常 + 手动破坏栈 |
| INV-GCS-R02 | `SIGSEGV` | 普通 store 写 GCS 段 |
| INV-GCS-M01 | `readelf -n` 含 `GCS` | `=gcs` 构建 |
| INV-GCS-M02 | 链接器拒绝 / 配置失败 | 混编 |

## 10. 开放问题

- **si_code 命名**: Linux 6.13 起 GCS 异常对应的 `si_code` 常量名稳定性, 待 mainline 文档.
- **glibc setjmp/longjmp ABI**: jmp_buf 是否含 GCS 字段及其字节布局, 未最终化.
- **JIT / V8 与 GCS**: 用户态 GCS 启用下, 自管 JIT 必须用 syscall 注册影子栈, 类似 SHSTK; 待补.
- **kernel-mode GCS**: 内核自身使用 GCS 的状态 (`CONFIG_ARM64_GCS_KERNEL`), 待补.
- **GCS 与 CHERI / Morello**: 在 CHERI 平台 GCS 的相互作用, 跨实验机制, 暂搁置.

## 11. 使用建议

- 现阶段 GCS oracle 主要为 *未来内核+CPU* 准备; 目前真实硬件极少, 用 QEMU + Armv9.4 emulation.
- 跑 oracle 前 `prctl(PR_GET_SHADOW_STACK_STATUS)` 自检, 否则跳过 INV-GCS-R* 类断言.
- 大量 invariant 标记 `likely-to-drift`, 主分支升级时人工 audit.
- ELF property 静态扫描可作 CI 前置.
