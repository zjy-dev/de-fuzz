# `-fzero-call-used-regs` Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / LLVM/Clang / Linux kernel 中与 **`-fzero-call-used-regs`** 直接相关的 invariants 抽取归类, 作为 DeFuzz ZCUR oracle 的形式化依据.
>
> 机制简写与 survey: **ZCUR** = `-fzero-call-used-regs`. 在函数返回前清零曾被该函数使用过的"调用相关"寄存器, 减少 ROP gadget 与寄存器残留泄漏.

## 0. 术语与坐标

- **call-used register**: caller-saved 寄存器, 函数内可自由破坏 (例 x86_64 `rax`, `rcx`, `rdx`, `r8-r11`, `xmm0-xmm15`, `k0-k7`).
- **call-preserved register**: callee-saved (例 `rbx`, `rbp`, `r12-r15`).
- **mode**: ZCUR 的清零策略:
  - `skip` (默认, 不清零)
  - `used`: 仅清零本函数实际使用过的 call-used regs
  - `used-arg`: + 参数寄存器
  - `used-gpr`: 仅 GPR
  - `used-gpr-arg`
  - `all`: 所有 call-used regs (含 SIMD)
  - `all-gpr`, `all-arg`, `all-gpr-arg`
  - 共 9 档.
- **ROP gadget reduction**: 攻击者利用残留寄存器值构造 gadget; 清零让 gadget 失效.

每条 invariant 字段同前.

## 1. 启用条件

### INV-ZCUR-E01 — `-fzero-call-used-regs=<mode>` 启用

- **statement**: GCC 11+, Clang 15+ 接受该选项. 共 9 档. 默认 `skip` 不清零. `used` 是性能折衷常用值; `all` 最强但开销 ~5-10%.
- **compiler**: GCC 11+, LLVM/Clang 15+
- **target**: x86, x86_64, aarch64 (主流), 部分扩展
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html ; https://clang.llvm.org/docs/ClangCommandLineReference.html ; `gcc/function.cc` (`zero_call_used_regs`)
- **evidence_snippet**: GCC manual: *"`-fzero-call-used-regs=<mode>`: zero call-used registers at function return"*.
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 抓 epilogue 是否含 `xor reg, reg` (x86_64) / `mov xN, #0` (aarch64) 序列.

### INV-ZCUR-E02 — `-fhardened` 不隐式启用 ZCUR

- **statement**: 截至 GCC 14, `-fhardened` 不隐式启用 ZCUR (因开销不可忽略); DeFuzz 须显式设置.
- **compiler**: GCC 14+
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-ZCUR-E03 — Linux 内核默认 `=used-gpr`

- **statement**: Linux 内核 5.15+ 在 x86_64 默认 `-fzero-call-used-regs=used-gpr` (`CONFIG_ZERO_CALL_USED_REGS=y`).
- **runtime**: Linux kernel
- **version**: Linux ≥ 5.15
- **source_kind**: source
- **source_url_or_path**: Linux `Makefile` ; `arch/x86/Kconfig`
- **version_sensitivity**: stable
- **oracle_mapping**: 内核 seed.

## 2. 字节模式

### INV-ZCUR-B01 — x86_64 epilogue 清零序列

- **statement**: x86_64 的 `xor %eax, %eax` (32-bit 写隐式清零高位 64-bit) 是最 compact 清零方式. 编译器对 GPR 用 `xor`, 对 XMM 用 `vzeroall` / `pxor xmm0, xmm0`. 顺序通常: 先 GPR 后 SIMD.
- **compiler**: GCC, Clang
- **target**: x86_64
- **source_kind**: source + lit test
- **source_url_or_path**: `gcc/config/i386/i386.cc` ; `llvm/lib/Target/X86/X86FrameLowering.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描.

### INV-ZCUR-B02 — AArch64 epilogue 清零序列

- **statement**: aarch64 用 `mov xN, #0` 序列, SIMD 用 `movi vN.16b, #0`. 编译器在 epilogue 末尾 (在 RET / AUTIASP 之前) 插入.
- **compiler**: GCC, Clang
- **target**: aarch64
- **source_kind**: source
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc` ; `llvm/lib/Target/AArch64/AArch64FrameLowering.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描.

### INV-ZCUR-B03 — 仅清零 *实际使用过的* 寄存器 (used 模式)

- **statement**: `used` 系列模式下, 编译器只对该函数 *写过* 的 call-used regs 发清零指令. 这是 used vs all 的唯一区别. used 在小函数下几乎零开销.
- **compiler**: GCC, Clang
- **source_kind**: source
- **source_url_or_path**: `gcc/function.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: 小函数静态扫描差异.

## 3. 编译期约束

### INV-ZCUR-C01 — 不影响 ABI

- **statement**: ZCUR 仅清零, 不改变 ABI: caller 不依赖 callee 保留 call-used regs, 因此清零不破坏调用契约.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-ZCUR-C02 — 不影响 callee-saved 寄存器

- **statement**: ZCUR 不清零 callee-saved (`rbx`, `r12-r15`, `x19-x29` 等), 因为它们在 epilogue 前已被还原为 caller 的值. 清零 callee-saved 会破坏 caller.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描验证 callee-saved 不被清零.

### INV-ZCUR-C03 — `target("zero-call-used-regs=...")` 函数级覆盖

- **statement**: GCC `__attribute__((target("zero-call-used-regs=all")))` 函数级覆盖 TU 默认. Clang 同支持. 用于关键函数额外硬化.
- **compiler**: GCC, Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: stable
- **oracle_mapping**: 函数级 seed.

### INV-ZCUR-C04 — `__attribute__((zero_call_used_regs("...")))` 函数级 (LLVM/Clang)

- **statement**: 等价的 LLVM/Clang 属性 `zero_call_used_regs(<mode>)`, 直接覆盖 fn-level mode.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html
- **version_sensitivity**: stable
- **oracle_mapping**: 同上.

## 4. ELF 元数据

### INV-ZCUR-M01 — 无 ELF property

- **statement**: ZCUR 是纯 codegen, 无 ELF / property bit. 链接器不感知, 混编时安全保证降级到最弱.
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描每函数 epilogue.

## 5. 运行时

### INV-ZCUR-R01 — 不引入 runtime

- **statement**: 不需 runtime / libc 协助, 全编译期完成.
- **runtime**: 不适用
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-ZCUR-R02 — Sigreturn / setcontext 等系统调用不感知

- **statement**: `sigreturn` / `setcontext` 等通过 syscall 重置寄存器到给定值, ZCUR 不改变这一点. 信号处理路径不需特殊处理.
- **runtime**: kernel
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

## 6. 与其他机制交互

### INV-ZCUR-I01 — 与 STRUB 互补但目的不同

- **statement**: ZCUR 仅清 *寄存器*; STRUB 擦 *栈*. 共同减少跨调用残留泄漏. 同启提供深度防御.
- **compiler**: GCC
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/strub.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-ZCUR-I02 — 与 SP / SHSTK / PAC 正交

- **statement**: ZCUR 不影响栈 / 返回地址, 与 canary / 影子栈 / PAC 完全独立.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-ZCUR-I03 — 与函数 ABI 边角: tail-call

- **statement**: 在尾调用 (`jmp` 替代 `call`) 路径下, ZCUR 必须在 jump 之前完成清零. 编译器对 tail-call 做特殊处理. 早期实现存在边角漏清, 见 §8.
- **compiler**: GCC, Clang
- **source_kind**: source + test
- **source_url_or_path**: `gcc/function.cc`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: tail-call seed.

## 7. 验证与已知回归

### INV-ZCUR-VER-PR104666 — GCC 11 早期 SIMD 漏清

- **statement**: GCC PR104666 修复 ZCUR 在 `=all` 下漏清 AVX-512 mask register. 修复在 GCC 12.
- **compiler**: GCC
- **version**: 修复于 GCC 12
- **source_kind**: mailing-list
- **source_url_or_path**: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=104666
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 GCC AVX seed.

### INV-ZCUR-VER-LLVM-D110869 — Clang 15 引入

- **statement**: Clang 15 引入 ZCUR (D110869), 早期版本不支持. 跨编译器对照需注意 GCC 比 Clang 早 2 年.
- **compiler**: LLVM/Clang
- **version**: Clang 15+
- **source_kind**: mailing-list
- **source_url_or_path**: https://reviews.llvm.org/D110869
- **version_sensitivity**: stable
- **oracle_mapping**: 老 Clang 反例.

## 8. DeFuzz Oracle 映射总表

| Invariant | Oracle 信号 | seed |
|---|---|---|
| INV-ZCUR-B01 | epilogue 含 `xor` 序列 | 任意函数 + `=used` 构建 |
| INV-ZCUR-B03 | `=used` 与 `=all` 字节差 | 简单函数 |
| INV-ZCUR-C03 | 函数级 attribute 影响 epilogue | 单函数 attribute |
| INV-ZCUR-VER-PR104666 | AVX-512 mask 不清 | 老 GCC |

## 9. 开放问题

- **AVX-512 / SVE / SME 等扩展寄存器**: 各模式对 ZMM / Z 寄存器 / SVE 寄存器的覆盖, 待详细文档化.
- **Inline asm 与 ZCUR**: inline asm 写 call-used reg 是否被 ZCUR 视为"使用过", 待 audit.
- **JIT**: JIT 生成函数无 ZCUR, 部分回到攻击面.
- **AArch64 SVE2 + ZCUR**: AArch64 SVE 寄存器在 ZCUR 中的处理, 待补.
- **效益量化**: ZCUR 实际减少的 gadget 数量, 经验数据待补.

## 10. 使用建议

- 内核默认 `=used-gpr` 是性能 / 安全平衡点.
- 用户态高安全场景用 `=all`.
- 函数级 `target("zero-call-used-regs=all")` 给 crypto / parser 等关键函数.
- 与 STRUB 配合提供寄存器 + 栈双重擦除.
- 老 GCC / Clang 版本需检查 SIMD / AVX 漏清回归.
