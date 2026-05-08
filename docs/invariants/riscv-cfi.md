# RISC-V CFI (Zicfilp + Zicfiss) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / LLVM/Clang / RISC-V 国际 / Linux kernel 中与 **RISC-V Zicfilp + Zicfiss** 直接相关的 invariants 抽取归类, 作为 DeFuzz 的形式化依据.
>
> 机制简写与 survey: **Zicfilp** = Landing Pad (前向 CFI, 类似 Intel IBT / AArch64 BTI); **Zicfiss** = Shadow Stack (反向 CFI, 类似 Intel SHSTK / AArch64 GCS).

## 0. 术语与坐标

- **Zicfilp**: RISC-V "Control flow Integrity Landing Pad" 扩展. 添加 `lpad` 指令作为间接跳转目标; CPU 维护 *expected landing pad label* 状态, 不匹配则触发 *Software Check exception*.
- **`lpad` 指令**: 4 字节 hint 指令, 含 20-bit label. 间接跳转 (`jalr`) 后必须在第一条指令是匹配 label 的 `lpad`, 否则异常.
- **`jalr` 配合**: `jalr` 在 Zicfilp 启用下隐式设置 *expected landing pad label* 寄存器 (`x7` 或专用 elp).
- **Zicfiss**: RISC-V "Control flow Integrity Shadow Stack" 扩展. 添加 `ssp` 寄存器, `sspush` / `sspopchk` / `ssprr` 指令.
- **`ssp` 寄存器**: 类似 Intel SHSTK 的 SSP, 内核管理.
- **`sspush rs1`**: 把 rs1 push 到影子栈.
- **`sspopchk rs1`**: pop 影子栈到临时, 与 rs1 比对, 不一致 -> exception.
- **`ssprr rd`**: 读 SSP 到 rd (用于 setjmp/longjmp).
- **GNU property**: 计划中 `GNU_PROPERTY_RISCV_FEATURE_1_AND` 含 LP / SS bits (待标准化).
- **软件 SCS (`gp/x3`)**: 在不支持 Zicfiss 的 CPU 上, 用 `gp` 作为 ssp, 软件维护. 见 `@/home/yall/project/de-fuzz/docs/invariants/shadow-call-stack.md`.

每条 invariant 字段同前.

## 1. 启用条件

### INV-RVCFI-E01 — `-march=...+zicfilp+zicfiss`

- **statement**: GCC 14+ / Clang 18+ 接受 `-march` 子选项 `zicfilp` / `zicfiss`. 启用 ISA 解码 + 编译器发射 lpad / sspush / sspopchk 指令. 必须配 `-mcfi=...` 或类似启用 codegen (具体编译器选项形态尚在演进).
- **compiler**: GCC 14+, LLVM/Clang 18+
- **target**: riscv32, riscv64
- **source_kind**: user-doc + source
- **source_url_or_path**: https://github.com/riscv/riscv-cfi ; `gcc/config/riscv/riscv.cc` ; `llvm/lib/Target/RISCV/`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: oracle 抓 lpad / sspush / sspopchk 字节模式.

### INV-RVCFI-E02 — Linux kernel `CONFIG_RISCV_USER_CFI`

- **statement**: Linux 内核 6.x (具体 merge 尚在 patch 阶段) 通过 `CONFIG_RISCV_USER_CFI=y` 启用用户态 CFI; 用户进程通过 `prctl(PR_SET_INDIR_BR_LP_STATUS)` / `prctl(PR_SET_SHADOW_STACK_STATUS)` 启用.
- **runtime**: Linux kernel
- **version**: Linux 主线 patch 阶段 (2024-2025)
- **target**: riscv64
- **source_kind**: source + mailing-list
- **source_url_or_path**: lkml.kernel.org "RISC-V CFI" 主题
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 内核 + 真实 / QEMU CFI-aware seed.

### INV-RVCFI-E03 — runtime enforcement 需 CPU + kernel + ld.so

- **statement**: 与 x86 / aarch64 同构: CPU `misa.zicfilp/zicfiss` + 内核启用 + ld.so prctl 同时成立才生效.
- **runtime**: Linux kernel + glibc
- **source_kind**: 设计
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: oracle 启动前自检.

## 2. 指令编码

### INV-RVCFI-B01 — `lpad label20` 编码

- **statement**: `lpad` 指令 4 字节, opcode 在 `LUI` 编码空间内重用 (rd = x0 + 特定位模式), label 占 20 bit. 编码具体待最终标准, 但已在 RVI v1.0 frozen.
- **hardware**: RISC-V Zicfilp
- **source_kind**: ABI-spec
- **source_url_or_path**: https://github.com/riscv/riscv-cfi/blob/main/specifications/cfi.adoc
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描.

### INV-RVCFI-B02 — `sspush rs1`, `sspopchk rs1`, `ssprr rd`

- **statement**: 编码: `sspush` 用 `OP-V` opcode + 特定位模式; `sspopchk` 类似. 详见 ratified spec.
- **hardware**: RISC-V Zicfiss
- **source_kind**: ABI-spec
- **source_url_or_path**: 同上
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描.

## 3. 插入位置 (Placement)

### INV-RVCFI-P01 — 全局函数入口 `lpad`

- **statement**: 与 BTI / IBT 同构: 编译器在所有可被间接调用的函数入口插 `lpad` 指令 (label 由编译器选, 通常 0 表示"接受任意 label"或类型签名 hash).
- **compiler**: GCC, Clang
- **source_kind**: source
- **source_url_or_path**: `gcc/config/riscv/riscv.cc` ; `llvm/lib/Target/RISCV/RISCVAsmPrinter.cpp`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描.

### INV-RVCFI-P02 — switch jump table 目标 `lpad`

- **statement**: switch jump table 目标 BB 同样插 `lpad`.
- **compiler**: GCC, Clang
- **source_kind**: source
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: switch seed.

### INV-RVCFI-P03 — 非 leaf 函数 prologue: `sspush ra`, epilogue: `sspopchk ra`

- **statement**: 启用 Zicfiss 后, 非 leaf 函数:
  - prologue 在保存 `ra` 到普通栈后发 `sspush ra` (push 到影子栈)
  - epilogue 在还原 `ra` 后发 `sspopchk ra` (pop 影子栈与 ra 比对, 不匹配 fault)
- **compiler**: GCC, Clang
- **source_kind**: source
- **source_url_or_path**: `gcc/config/riscv/riscv.cc` ; `llvm/lib/Target/RISCV/RISCVFrameLowering.cpp`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描.

### INV-RVCFI-P04 — `setjmp` / `longjmp` 维护 ssp

- **statement**: glibc setjmp 用 `ssprr` 读当前 SSP 存到 jmp_buf; longjmp 计算 ssp 调整 + `sspopchk` 验证.
- **runtime**: glibc
- **source_kind**: source
- **source_url_or_path**: glibc `sysdeps/riscv/setjmp.S` (CFI-aware 路径)
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: setjmp/longjmp seed.

## 4. ELF 元数据

### INV-RVCFI-M01 — `GNU_PROPERTY_RISCV_FEATURE_1_AND` (待标准化)

- **statement**: ELF GNU property 计划添加 RISC-V LP/SS bits, 与 x86 / aarch64 同构. 链接器 AND 归并; ld.so 决定 prctl 启用.
- **compiler + linker**: GCC, Clang, binutils, lld
- **source_kind**: ABI-spec
- **source_url_or_path**: RISC-V psABI doc
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: `readelf -n` 检.

## 5. 运行时

### INV-RVCFI-R01 — `lpad` 不匹配触发 software check exception

- **statement**: 间接 jalr 后第一指令不是匹配 `lpad` -> Software Check Exception (`scause = 0x12`). 内核翻为用户态信号 (待 si_code 标准化).
- **hardware + runtime**: RISC-V Zicfilp + Linux
- **source_kind**: ABI-spec
- **source_url_or_path**: RISC-V CFI spec
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 间接调用错目标 seed.

### INV-RVCFI-R02 — `sspopchk` 不匹配触发同类异常

- **statement**: `sspopchk ra` 比对失败 -> Software Check Exception. 内核翻信号.
- **hardware + runtime**: RISC-V Zicfiss
- **source_kind**: ABI-spec
- **source_url_or_path**: 同上
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 缓冲区溢出 ra seed.

### INV-RVCFI-R03 — 影子栈段写保护

- **statement**: 与 SHSTK / GCS 同构: 内核分配特殊 VMA, 普通 store 写不进, 仅 sspush / sspopchk / ssprr 可读写.
- **runtime**: Linux kernel
- **source_kind**: source
- **source_url_or_path**: Linux 6.x patch series
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 反例.

## 6. 与其他机制交互

### INV-RVCFI-I01 — Zicfiss vs 软件 SCS (gp/x3)

- **statement**: 老 CPU 不支持 Zicfiss 时用软件 SCS (`-fsanitize=shadow-call-stack` + `gp/x3`); 新 CPU 用 Zicfiss. 互斥优先 Zicfiss.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/shadow-call-stack.md`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 矩阵.

### INV-RVCFI-I02 — Zicfilp + Zicfiss 与 KCFI

- **statement**: Linux 内核 RISC-V 可同时启用 Zicfilp (硬件 LP) + KCFI (软件 type-id), 类似 x86 FineIBT. 提供深度防御.
- **compiler**: Clang
- **source_kind**: 设计
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 矩阵.

### INV-RVCFI-I03 — 与 sanitizers 正交

- **statement**: 与 ASan 等 sanitizer 不冲突.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

## 7. 验证与已知回归

### INV-RVCFI-VER-RFC — spec frozen 前所有 invariant 标 likely-to-drift

- **statement**: RISC-V CFI spec v1.0 frozen 但实施细节 / Linux ABI / glibc 仍在演进, 大部分 invariant 待主线 merge 后稳定.
- **compiler + runtime**
- **source_kind**: ABI-spec + RFC
- **source_url_or_path**: https://github.com/riscv/riscv-cfi
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 严格版本控制.

## 8. DeFuzz Oracle 映射

| Invariant | Oracle 信号 | seed |
|---|---|---|
| INV-RVCFI-P01 | software check exception | 间接调用错 lpad |
| INV-RVCFI-P03 | 同上 | 缓冲区溢出 ra |
| INV-RVCFI-P04 | 同上 | setjmp + 篡改 jmp_buf |
| INV-RVCFI-R03 | SIGSEGV | 普通 store 写影子栈段 |

## 9. 开放问题

- **真实硬件**: 截至 2026 主流 RISC-V CPU 尚未广泛实现 Zicfilp/Zicfiss. QEMU + tcg 模拟为主.
- **Linux ABI**: prctl 接口名 / si_code 待 mainline 稳定.
- **glibc setjmp/longjmp**: jmp_buf 字段布局未定型.
- **C 函数指针类型化**: 类似 arm64e `__ptrauth`, 是否在 RISC-V 引入限定符? 待跟踪.
- **Linux 内核 KERNEL CFI**: kernel-mode Zicfiss / Zicfilp 待跟踪.

## 10. 使用建议

- 当前 (2026) 仅 QEMU + 模拟器路径可跑 oracle.
- 跟踪 RISC-V psABI doc 与 Linux LKML 主题.
- 与 SHSTK / GCS oracle 共用语义抽象, 跨 ISA seed 复用.
- 大部分 invariant `likely-to-drift`, 主分支升级时人工 audit.
