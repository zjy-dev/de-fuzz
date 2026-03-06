# 开源 C 编译器调研报告

> 目标：为 DeFuzz 黑盒模糊测试选择合适的 C 编译器作为测试目标。
>
> 调研日期：2026-03-06

## 选型标准

黑盒 fuzz 的理想目标编译器应满足：

1. **可黑盒调用** — 接受 `.c` 文件，输出二进制或汇编
2. **不够成熟** — 代码库较小或开发阶段较早，更容易发现 bug
3. **有一定社区关注** — GitHub Stars > 50，发现的 bug 有报告价值
4. **开源** — 可复现、可分析

---

## 一、高优先级目标（推荐）

### 1. TCC (Tiny C Compiler)

| 项目 | 信息 |
|------|------|
| GitHub | [TinyCC/tinycc](https://github.com/TinyCC/tinycc) (镜像，主仓在 repo.or.cz) |
| Stars | ~2,773 |
| 语言 | C |
| C 标准 | C99（不含 complex types），部分 C11/GNU 扩展 |
| 目标架构 | x86, x86-64, ARM, ARM64, RISC-V 64 |
| 状态 | 活跃（mob 分支） |
| 调用方式 | `tcc -o out input.c` |
| 特点 | ~100KB 可执行文件，编译速度是 GCC 的 9 倍，支持 C 脚本模式 (`#!/usr/local/bin/tcc -run`) |
| Fuzz 价值 | **非常高** — 生产可用，多架构，小代码库，C99 支持，学术界已有 fuzz 先例 |

### 2. Kefir

| 项目 | 信息 |
|------|------|
| GitHub | [protopopov1122/kefir](https://github.com/protopopov1122/kefir)（镜像，主仓在 sourcehut） |
| Stars | ~64 |
| 语言 | C（完全从零实现） |
| C 标准 | C17 完整，C23 开发中 |
| 目标架构 | x86-64 (System-V AMD64 ABI) |
| 状态 | 活跃（单人开发） |
| 调用方式 | 标准编译器 CLI |
| 特点 | SSA 优化管线，DWARF-5 调试信息，PIC 代码生成；已通过 80+ 真实项目验证（binutils, curl, nginx, OpenSSL, PostgreSQL 等） |
| Fuzz 价值 | **非常高** — 独立从零实现 C17/C23，单人开发，较新，边界情况 bug 多 |

### 3. slimcc

| 项目 | 信息 |
|------|------|
| GitHub | [fuhsnn/slimcc](https://github.com/fuhsnn/slimcc) |
| Stars | ~238 |
| 语言 | C99 |
| C 标准 | C23 + C2Y/GNU 扩展，支持 `defer` 和 MSVC 特性 |
| 目标架构 | x86-64 Linux/BSD |
| 状态 | 活跃（chibicc 的重度改进分支） |
| 调用方式 | GCC 兼容 CLI |
| 特点 | 可编译 GCC, Perl, Python, PostgreSQL 等大型项目 |
| Fuzz 价值 | **非常高** — C23/C2Y 前沿特性，活跃开发，基于 chibicc 但已大幅修改 |

### 4. Cuik (CuikC)

| 项目 | 信息 |
|------|------|
| GitHub | [RealNeGate/Cuik](https://github.com/RealNeGate/Cuik) |
| Stars | ~1,239 |
| 语言 | C |
| C 标准 | C11 |
| 目标架构 | x64（aarch64 计划中） |
| 状态 | 早期/活跃开发 |
| 调用方式 | 标准编译器 CLI |
| 特点 | 自定义后端 (TB - Tilde Backend)，目标是替代 GCC/MSVC/LLVM 的调试编译 |
| Fuzz 价值 | **非常高** — 明确处于早期阶段，自定义后端，bug 概率大 |

### 5. MIR (c2m - C to MIR 编译器)

| 项目 | 信息 |
|------|------|
| GitHub | [vnmakarov/mir](https://github.com/vnmakarov/mir) |
| Stars | ~2,542 |
| 语言 | C (~20k LOC) |
| C 标准 | C11（不含 VLA, complex, atomic, thread-local） |
| 目标架构 | x86-64, aarch64, ppc64le, s390x, riscv64 |
| 状态 | 活跃（作者为 GCC 开发者 Vladimir Makarov） |
| 调用方式 | 可作为独立编译器或 JIT 库使用 |
| 特点 | JIT 编译，性能达到 GCC -O2 的 91%，SSA 优化 |
| Fuzz 价值 | **非常高** — JIT 编译增加攻击面，多架构，C11，代码库小 |

### 6. ccc (Claude's C Compiler)

| 项目 | 信息 |
|------|------|
| GitHub | [anthropics/claudes-c-compiler](https://github.com/anthropics/claudes-c-compiler) |
| Stars | ~2,484 |
| 语言 | Rust (~180k LOC) |
| C 标准 | 大部分 C（可编译 Linux 内核、PostgreSQL、SQLite、TCC 等） |
| 目标架构 | x86-64, i686, AArch64, RISC-V 64 |
| 状态 | 实验性（AI 生成，未经正确性验证） |
| 调用方式 | 直接生成 ELF，无需外部工具链 |
| 特点 | 完全由 AI 生成，自包含汇编器/链接器 |
| Fuzz 价值 | **非常高** — 明确标注"未经正确性验证"，4 种架构，完整管线（解析+优化+代码生成+汇编+链接） |

---

## 二、高价值目标

### 7. chibicc

| 项目 | 信息 |
|------|------|
| GitHub | [rui314/chibicc](https://github.com/rui314/chibicc) |
| Stars | ~10,971 |
| 语言 | C |
| C 标准 | C11（大部分必选+可选特性，部分 GCC 扩展） |
| 目标架构 | x86-64 Linux |
| 状态 | 已归档（教学参考，不再活跃开发） |
| 调用方式 | `./chibicc -o out input.c` |
| 特点 | 可编译 Git, SQLite, libpng，可自编译 |
| Fuzz 价值 | **高** — 广泛使用的教学编译器，C11 覆盖面大 |

### 8. cproc

| 项目 | 信息 |
|------|------|
| GitHub | [michaelforney/cproc](https://github.com/michaelforney/cproc)（镜像，主仓在 sourcehut） |
| Stars | ~813 |
| 语言 | C99 |
| C 标准 | C11 大部分特性，部分 C23 + GNU 扩展 |
| 目标架构 | x86-64, aarch64, riscv64（通过 QBE 后端） |
| 状态 | 活跃 |
| 调用方式 | GCC 兼容 CLI（需外部 QBE + 汇编器 + 链接器） |
| 特点 | 不支持 VLA、内联汇编，无内建预处理器 |
| Fuzz 价值 | **高** — C11 合规目标，QBE 后端也可分别 fuzz |

### 9. lacc

| 项目 | 信息 |
|------|------|
| GitHub | [larmel/lacc](https://github.com/larmel/lacc) |
| Stars | ~980 |
| 语言 | C (C89, ~19k LOC) |
| C 标准 | 完整 C89，部分 C99/C11 |
| 目标架构 | x86-64 Linux (ELF) |
| 状态 | 玩具项目/维护中 |
| 调用方式 | `lacc -o out input.c`，GCC 兼容标志 (`-S`, `-c`, `-E`, `-std=c89/c99/c11`, `-O0..3`) |
| 特点 | 自托管，无外部依赖，已使用 Csmith 测试 |
| Fuzz 价值 | **高** — GCC 兼容 CLI，已有 Csmith 验证，小代码库 |

### 10. SmallerC

| 项目 | 信息 |
|------|------|
| GitHub | [alexfru/SmallerC](https://github.com/alexfru/SmallerC) |
| Stars | ~1,549 |
| 语言 | C（单趟编译） |
| C 标准 | C89/C99 公共子集 |
| 目标架构 | x86 16-bit, x86 32-bit (386+), MIPS |
| 状态 | 活跃 |
| 调用方式 | GCC 兼容标志，含预处理器、链接器、编译器驱动 |
| 特点 | 单趟编译，自编译，生成 NASM/YASM/FASM 汇编 |
| Fuzz 价值 | **高** — 多目标（16/32 位、MIPS），DOS 支持，自编译 |

### 11. Cake

| 项目 | 信息 |
|------|------|
| GitHub | [thradams/cake](https://github.com/thradams/cake) |
| Stars | ~659 |
| 语言 | C |
| C 标准 | C23 前端，C2Y 提案，输出 C89 兼容代码 |
| 目标架构 | 转译器（C23 → C89），非直接生成原生代码 |
| 状态 | 活跃 |
| 调用方式 | 转译 C23 至 C89，同时可作为静态分析器 |
| 特点 | 前端解析 C23，转译+静态分析增加攻击面 |
| Fuzz 价值 | **高** — C23 前端解析，复杂转换逻辑 |

### 12. Aro (arocc)

| 项目 | 信息 |
|------|------|
| GitHub | [Vexu/arocc](https://github.com/Vexu/arocc) |
| Stars | ~1,599 |
| 语言 | Zig |
| C 标准 | C89 ~ C23 完整，GNU 扩展，Clang 扩展 |
| 目标架构 | 通过 Zig 后端 |
| 状态 | 活跃（将作为 Zig 的 C 前端） |
| 调用方式 | 部分可作为独立工具使用 |
| 特点 | 全面的 C 标准支持（C89-C23），Zig 实现带来不同的 bug 类型 |
| Fuzz 价值 | **高** |

### 13. cparser (+ libfirm)

| 项目 | 信息 |
|------|------|
| GitHub | [libfirm/cparser](https://github.com/libfirm/cparser) |
| Stars | ~358 |
| 语言 | C99 |
| C 标准 | C99 |
| 目标架构 | x86, x86-64, ARM, SPARC（通过 libfirm） |
| 状态 | 维护中（学术来源：卡尔斯鲁厄理工学院） |
| 调用方式 | GCC 兼容驱动 |
| 特点 | 图 IR 优化，学术背景 |
| Fuzz 价值 | **高** |

---

## 三、中高优先级目标

### 14. 8cc

| 项目 | 信息 |
|------|------|
| GitHub | [rui314/8cc](https://github.com/rui314/8cc) |
| Stars | ~6,362 |
| 语言 | C |
| C 标准 | C11 |
| 目标架构 | x86-64 Linux |
| 状态 | 不再活跃（chibicc 的前身） |
| Fuzz 价值 | **高** — 小代码库，C11 特性，独立实现 |

### 15. shecc

| 项目 | 信息 |
|------|------|
| GitHub | [sysprog21/shecc](https://github.com/sysprog21/shecc) |
| Stars | ~1,352 |
| 语言 | ANSI C |
| C 标准 | C 子集 |
| 目标架构 | ARMv7-A, RV32IM (RISC-V 32-bit) |
| 状态 | 活跃/教学 |
| 调用方式 | 编译 .c 为 ARM/RISC-V ELF，无需外部汇编器/链接器 |
| 特点 | 自包含二进制生成，双架构，自托管，两趟编译 |
| Fuzz 价值 | **高** |

### 16. AMaCC

| 项目 | 信息 |
|------|------|
| GitHub | [jserv/amacc](https://github.com/jserv/amacc) |
| Stars | ~1,052 |
| 语言 | C |
| C 标准 | C 子集（基于 c4） |
| 目标架构 | 32-bit ARM Linux (ELF) + JIT |
| 状态 | 活跃/教学 |
| 特点 | JIT 编译增加攻击面，ARM ELF 生成 |
| Fuzz 价值 | **高** |

### 17. PCC (Portable C Compiler)

| 项目 | 信息 |
|------|------|
| GitHub | 镜像（主仓在 CVS） |
| Stars | 少量 |
| 语言 | C |
| C 标准 | C99 |
| 目标架构 | ~16 种（x86, ARM, MIPS, PowerPC, SPARC 等） |
| 状态 | 停滞（最后发布 2014，2012 年从 OpenBSD 移除） |
| 特点 | BSD 许可，曾在 OpenBSD/NetBSD 中使用 |
| Fuzz 价值 | **中高** — 多目标 C99，开发停滞意味着更多潜在 bug |

### 18. widcc

| 项目 | 信息 |
|------|------|
| GitHub | [fuhsnn/widcc](https://github.com/fuhsnn/widcc) |
| Stars | ~47 |
| 语言 | C |
| C 标准 | C11（从 slimcc 简化） |
| 目标架构 | x86-64 Linux |
| 状态 | 活跃（slimcc 的简化移植版） |
| 特点 | 可编译 Curl, GCC, Git, PHP, Perl, Python, PostgreSQL |
| Fuzz 价值 | **高** — 简化但仍能编译大型项目 |

---

## 四、嵌入式/特殊领域编译器

### 19. SDCC (Small Device C Compiler)

| 项目 | 信息 |
|------|------|
| 网站 | [sdcc.sourceforge.net](https://sdcc.sourceforge.net/) |
| Stars | 少量（SourceForge 托管） |
| C 标准 | C89, C99, C11, C23 |
| 目标架构 | Intel 8051, Z80, Z180, eZ80, STM8, HC08, PDK, MOS 6502 等 |
| 状态 | 活跃 |
| Fuzz 价值 | **高** — 多种 8 位后端，C 标准在特殊架构上的实现 |

### 20. cc65

| 项目 | 信息 |
|------|------|
| GitHub | [cc65/cc65](https://github.com/cc65/cc65) |
| Stars | ~2,610 |
| C 标准 | 接近 ANSI C (C89) |
| 目标架构 | 6502, 65C02, 65C816（C64, Apple II, Atari, NES 等） |
| 状态 | 活跃 |
| Fuzz 价值 | **高** — 6502 代码生成，复古平台，复杂工具链 |

### 21. Open Watcom V2

| 项目 | 信息 |
|------|------|
| GitHub | [open-watcom/open-watcom-v2](https://github.com/open-watcom/open-watcom-v2) |
| Stars | ~1,168 |
| C 标准 | C89/C99 部分 |
| 目标架构 | x86 16-bit, x86 32-bit（DOS, Windows, OS/2, Linux） |
| 状态 | 维护中 |
| Fuzz 价值 | **高** — 大型遗留代码库，16/32 位代码生成，复杂优化 |

### 22. ACK (Amsterdam Compiler Kit)

| 项目 | 信息 |
|------|------|
| GitHub | [davidgiven/ack](https://github.com/davidgiven/ack) |
| Stars | ~648 |
| C 标准 | ANSI C |
| 目标架构 | CP/M, Linux (i386, m68020, MIPS32, PowerPC), Minix, MS-DOS, RPi GPU 等 |
| 状态 | 维护中（BSD 许可） |
| 特点 | 1980 年代起源（Tanenbaum），支持 C + Pascal + Modula2 |
| Fuzz 价值 | **高** — 多种异构目标 |

### 23. vbcc

| 项目 | 信息 |
|------|------|
| 网站 | [compilers.de/vbcc.html](http://www.compilers.de/vbcc.html) |
| Stars | N/A（无 GitHub） |
| C 标准 | C89，部分 C99 |
| 目标架构 | 68k, ColdFire, PowerPC, 6502, VideoCore, x86, Alpha, Z-machine 等 |
| 状态 | 活跃（手册最后更新 2025-02） |
| Fuzz 价值 | **高** — 大量异构后端，比主流编译器更少被 fuzz |

---

## 五、研究/实验性编译器

### 24. Onramp

| 项目 | 信息 |
|------|------|
| GitHub | [ludocode/onramp](https://github.com/ludocode/onramp) |
| Stars | ~203 |
| C 标准 | 最终阶段目标 C17 |
| 特点 | 从十六进制到 C17 的分阶段引导，可编译 Doom |
| Fuzz 价值 | **中高** — 多阶段编译器，可逐阶段 fuzz |

### 25. CompCert

| 项目 | 信息 |
|------|------|
| GitHub | [AbsInt/CompCert](https://github.com/AbsInt/CompCert) |
| Stars | ~2,122 |
| 语言 | OCaml / Coq |
| C 标准 | C99 大子集 |
| 目标架构 | PowerPC, ARM, x86, RISC-V, AArch64 |
| 状态 | 活跃 |
| 特点 | 形式化验证——编译正确性已被证明 |
| Fuzz 价值 | **中** — 验证核心不太可能有 bug，但解析器/预处理器/链接器仍可 fuzz |

### 26. VAST

| 项目 | 信息 |
|------|------|
| GitHub | [trailofbits/vast](https://github.com/trailofbits/vast) |
| Stars | ~435 |
| 语言 | C++（基于 MLIR） |
| 特点 | Tower of MLIR IRs，程序分析管线 |
| Fuzz 价值 | **中高** — 复杂 IR 转换 |

---

## 六、教学/微型编译器

### 27. c4 (C in Four Functions)

| 项目 | 信息 |
|------|------|
| GitHub | [rswier/c4](https://github.com/rswier/c4) |
| Stars | ~10,575 |
| 语言 | C (~500 LOC) |
| 特点 | 解释器，~500 行，自解释 |
| Fuzz 价值 | **低-中** |

### 28. acwj (A Compiler Writing Journey)

| 项目 | 信息 |
|------|------|
| GitHub | [DoctorWkt/acwj](https://github.com/DoctorWkt/acwj) |
| Stars | ~12,818 |
| 特点 | 教学旅程，最终阶段可自编译 |
| Fuzz 价值 | **中** — C 子集有限 |

### 29. lcc

| 项目 | 信息 |
|------|------|
| GitHub | [drh/lcc](https://github.com/drh/lcc) |
| Stars | ~2,516 |
| C 标准 | ANSI C (C89) |
| 目标架构 | Alpha, SPARC, MIPS R3000, x86 |
| 特点 | 经典教科书配套 |
| Fuzz 价值 | **中** — 历史悠久但仅 C89 |

### 30. neatcc / ncc

| 项目 | 信息 |
|------|------|
| GitHub | [aligrudi/neatcc](https://github.com/aligrudi/neatcc) (~176 Stars), [ghaerr/ncc](https://github.com/ghaerr/ncc) (~31 Stars) |
| 目标架构 | ARM, x86, x86-64 / AArch64, x86-64 |
| 特点 | ncc 含自己的链接器和 ELF 加载器 |
| Fuzz 价值 | **中高** |

---

## 七、辅助工具

### Csmith

| 项目 | 信息 |
|------|------|
| GitHub | [csmith-project/csmith](https://github.com/csmith-project/csmith) |
| Stars | ~1,163 |
| 用途 | 随机 C 程序生成器（C99 兼容），专为差分测试 C 编译器设计 |
| 备注 | 非编译器，但是编译器 fuzz 的核心工具，lacc 已使用其进行验证 |

### QBE (编译器后端)

| 项目 | 信息 |
|------|------|
| GitHub | [8l/qbe](https://github.com/8l/qbe)（镜像，主仓在 c9x.me） |
| Stars | ~393 |
| 特点 | 编译器后端，被 cproc 使用，可通过其 IL 输入格式单独 fuzz |

---

## 八、推荐排序

按黑盒 fuzz 价值综合排序（考虑：不成熟度、攻击面、标准覆盖、易用性、发现 bug 概率）：

| 优先级 | 编译器 | Stars | 核心理由 |
|--------|--------|-------|----------|
| 1 | **TCC** | 2.7k | 生产可用 C99，多架构，小代码库，学术界已有 fuzz 先例 |
| 2 | **Kefir** | 64 | 从零实现 C17/C23，单人开发，较新 |
| 3 | **slimcc** | 238 | C23/C2Y 前沿特性，活跃开发 |
| 4 | **Cuik** | 1.2k | 明确早期阶段，自定义后端 |
| 5 | **MIR (c2m)** | 2.5k | C11 JIT 编译器，多架构，SSA 优化 |
| 6 | **ccc** | 2.5k | AI 生成，明确未验证，4 种架构 |
| 7 | **chibicc** | 11k | C11，可编译真实项目，但已归档 |
| 8 | **cproc** | 813 | C11 via QBE，活跃，干净的代码库 |
| 9 | **lacc** | 980 | C89 自托管，GCC 兼容 CLI，已 Csmith 测试 |
| 10 | **SmallerC** | 1.5k | 多目标（16/32 位、MIPS），自编译 |
| 11 | **Cake** | 659 | C23 转译器，复杂转换 |
| 12 | **Aro** | 1.6k | C23 in Zig，全面标准支持 |
| 13 | **cparser** | 358 | C99 + libfirm 优化器，学术来源 |
| 14 | **SDCC** | (低) | 异构 8 位后端，多标准 |
| 15 | **shecc** | 1.4k | 自托管，ARM+RISC-V，直接 ELF 生成 |

---

## 九、与 DeFuzz 集成考量

当前 DeFuzz 采用**白盒**方式（使用 gcov 插桩测量 GCC 内部覆盖率）。如果要做黑盒 fuzz，需考虑：

1. **Oracle 策略**：黑盒下无法获取编译器内部覆盖率，需要设计新的 oracle：
   - **差分测试**（Differential Testing）：同一 `.c` 文件送给多个编译器，比较输出行为
   - **崩溃检测**：检查编译器 crash (SIGSEGV, SIGABRT) 或 ICE (Internal Compiler Error)
   - **输出一致性**：编译后运行，比较不同编译器产生的二进制行为是否一致
   - **标准合规性**：合法 C 程序不应导致编译器崩溃

2. **种子生成**：
   - Csmith 可直接用于生成符合标准的随机 C 程序
   - 当前 LLM 驱动的种子生成框架可复用，只需调整 prompt 和 oracle

3. **目标编译器接口**：
   - 大多数目标编译器支持 GCC 兼容 CLI (`compiler -o out input.c`)
   - 需要处理不同编译器的错误输出格式
