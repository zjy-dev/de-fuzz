# 低成熟度 C 编译器防御机制安全审计报告

**审计日期**: 2026-03-07  
**审计范围**: 18 个开源 C 编译器 × 其各自支持的 ISA  
**审计焦点**: Stack Canary 实现、`_FORTIFY_SOURCE` / `__builtin_object_size` 实现、栈帧布局安全  

---

## 一、核心发现摘要

> [!CAUTION]
> **全部 18 个编译器均未实现 Stack Canary 保护。** 这意味着使用任何一个低成熟度编译器编译的程序，都无法检测栈缓冲区溢出攻击。

### 高优先级发现

| # | 编译器 | 发现 | 严重程度 | 影响 ISA |
|---|--------|------|----------|----------|
| **F-1** | **ccc** | `__builtin_object_size` 始终返回"未知" | **高** | x86_64, i686, AArch64, RISC-V64 |
| **F-2** | **ccc** | 显式禁用 `_FORTIFY_SOURCE` 预处理宏 | 高 | 全部 |
| **F-3** | **Aro** | 解析 `no_stack_protector` 属性但不生成任何保护代码 | **高** | 全部 (via Zig/LLVM backend) |
| **F-4** | **TCC** | 显式禁用 `_FORTIFY_SOURCE`，无 canary | 中 | x86, x86_64, ARM, ARM64, RISC-V64 |
| **F-5** | **cparser** | 忽略 `-fstack-protector` 并强制 `_FORTIFY_SOURCE=0` | 中 | x86, x86_64, ARM, SPARC |
| **F-6** | **slimcc/chibicc/widcc** | 接受 `-fstack-protector` 系列 flag 但静默忽略 | 中 | x86_64 |

---

## 二、逐编译器审计详情

### 2.1 TCC (Tiny C Compiler)

| 属性 | 值 |
|------|-----|
| **语言** | C |
| **ISA** | x86, x86-64, ARM, ARM64, RISC-V64 |
| **状态** | 活跃 |

#### 2.1.1 Stack Canary

**结论: ❌ 未实现**

对 5 个 ISA 的 codegen (`x86_64-gen.c`, `arm64-gen.c`, `arm-gen.c`, `i386-gen.c`, `riscv64-gen.c`) 的 `gfunc_prolog()` / `gfunc_epilog()` 进行了深度审计：

- **x86_64**: `gfunc_prolog()` (line ~690-793) 仅设置 `rbp`、保存寄存器、分配栈空间 (`sub rsp, N`) 和处理 Windows `__chkstk`。无 canary 放置或检查。
- **ARM64**: `gfunc_prolog()` (line 1180-1289) 使用 `stp x29,x30,[sp,#-224]!` 保存帧，对 vararg 保存寄存器。`gfunc_epilog()` (line 1471-1511) 仅恢复 `sp=x29` 后 `ldp x29,x30 → ret`。无 canary。
- **其他 3 个 ISA**: 同样模式——标准 prologue/epilogue，无任何 stack protector 代码。

#### 2.1.2 FORTIFY_SOURCE

**结论: ❌ 显式禁用**

在 `tccdefs.h` 中：
```c
#undef _FORTIFY_SOURCE
```
TCC 在编译任何用户代码前都会取消定义 `_FORTIFY_SOURCE`，确保 glibc 的 FORTIFY 包装函数不会被激活。

#### 2.1.3 VLA/alloca 处理

TCC 支持 VLA 和 `alloca`，但由于没有 canary，VLA 溢出可直接覆写保存的返回地址。这与 CVE-2023-4039 (GCC AArch64) 的漏洞模式完全一致。

#### 2.1.4 黑盒 Fuzz 价值

✅ **极高**。TCC 可快速构建，支持 5 个 ISA，且完全无防御机制。应作为 DeFuzz 黑盒分支的首要目标。

---

### 2.2 ccc (C Compiler written in Rust)

| 属性 | 值 |
|------|-----|
| **语言** | Rust |
| **ISA** | x86_64, i686, AArch64, RISC-V64 |
| **状态** | 活跃开发 |

#### 2.2.1 Stack Canary

**结论: ❌ 未实现**

全局搜索 `stack.protect|stack.canary|canary|stack_chk|stack.guard` 在整个 `src/` 中无一命中（Rust 源码）。在 `src/backend/` 的 prologue/epilogue 生成代码中（`prologue.rs` for i686, 以及各 ISA 的 codegen）确认无任何 canary 相关指令发射。

#### 2.2.2 FORTIFY_SOURCE — 关键发现 ⚠️

**结论: ❌ `__builtin_object_size` 实现为空壳**

**[F-1] `__builtin_object_size` 始终返回 "未知"**

文件: `src/ir/lowering/expr_builtins.rs` (lines 303-325)

```rust
BuiltinIntrinsic::ObjectSize => {
    // Evaluate args for side effects
    for arg in args { self.lower_expr(arg); }
    let obj_type = if args.len() >= 2 {
        match self.eval_const_expr(&args[1]) {
            Some(IrConst::I64(v)) => v,
            Some(IrConst::I32(v)) => v as i64,
            _ => 0,
        }
    } else { 0 };
    // Types 0 and 1: maximum estimate -> -1 (unknown)
    // Types 2 and 3: minimum estimate -> 0 (unknown)
    let result = if obj_type == 2 || obj_type == 3 { 0i64 } else { -1i64 };
    Some(Operand::Const(IrConst::I64(result)))
}
```

**分析**: ccc 不做任何 points-to 分析，`__builtin_object_size()` 对所有类型（0和1）一律返回 `-1`（即 `(size_t)-1`），对类型2和3返回 `0`。这意味着：

- glibc 的 `_FORTIFY_SOURCE` 宏依赖 `__builtin_object_size` 在编译期获取缓冲区大小
- 当返回 `-1` 时，FORTIFY 的运行时检查会被优化掉（`-1` 表示大小未知，不需要检查）
- **所有 `strcpy_chk` / `memcpy_chk` 等保护函数都会退化为普通版本**
- 用户无法通过任何编译选项获得 FORTIFY 保护

**[F-2] 显式禁用 `_FORTIFY_SOURCE` 宏**

文件: `src/driver/pipeline.rs` (line 851)

```rust
preprocessor.undefine_macro("_FORTIFY_SOURCE");
```

注释中解释原因：glibc 的 FORTIFY 头文件使用 `__builtin_va_arg_pack()` 这一 GCC 专有 construct，ccc 无法支持。但该注释没有提到这导致的安全影响。

**[F-1 + F-2] 的叠加影响**: 即使用户显式传入 `-D_FORTIFY_SOURCE=2`，该宏也会被取消定义；即使某些代码手动调用 `__builtin_object_size`，返回值也总是无效的。**FORTIFY 在 ccc 下完全失效**。

#### 2.2.3 Fortify _chk builtins 的处理

文件: `src/ir/lowering/expr_builtins.rs` (lines 474-541)

`lower_fortify_chk()` 函数将 `__builtin___memcpy_chk` 等 builtin 直接转发到 glibc 的 `__memcpy_chk` 运行时函数——但由于 `__builtin_object_size` 返回 `-1`，传给 `__memcpy_chk` 的 `dstlen` 参数也是 `-1`，因此 glibc 的运行时检查 (`if dstlen < len`) 永远为 false，不会触发 `__chk_fail()`。

#### 2.2.4 黑盒 Fuzz 价值

✅ **高**。ccc 正在活跃开发，支持 4 个 ISA，适合 DeFuzz 黑盒 fuzz。

---

### 2.3 Aro (Zig C Compiler Frontend)

| 属性 | 值 |
|------|-----|
| **语言** | Zig |
| **ISA** | 依赖 Zig/LLVM 后端 |
| **状态** | 活跃开发 |

#### 2.3.1 Stack Canary

**结论: ❌ 未实现（属性被静默忽略）**

**[F-3] `no_stack_protector` 属性被解析但无效**

文件: `src/aro/Attribute.zig` (line 524)

```zig
pub const no_stack_protector = struct {};
```

Aro 正确解析 `__attribute__((no_stack_protector))`，但由于编译器本身从未生成任何 stack protector 代码，这个属性实际无效——它承诺"此函数禁用 stack protector"，但关闭一个不存在的保护机制没有意义。

然而，**安全隐患**在于：如果 Aro 的后端接力到 LLVM/Zig 并且 LLVM 后端默认启用 stack protector，那么 Aro 前端没有将 `no_stack_protector` 属性正确传递可能导致安全关键函数（如 crypto 代码）的 canary 被意外保留或遗漏。

> [!NOTE]
> `src/main.zig` 中的 `.canary = @truncate(0xc647026dc6875134)` 是 Zig 标准库 DebugAllocator 的堆 canary，与栈保护无关。

#### 2.3.2 FORTIFY_SOURCE

**结论: 未观察到相关实现**。Aro 主要是前端，FORTIFY 的实现依赖后端能力。

#### 2.3.3 黑盒 Fuzz 价值

⚠️ **中等**。Aro 作为前端需要 Zig/LLVM 后端配合，构建流程较复杂。

---

### 2.4 cparser (with libfirm backend)

| 属性 | 值 |
|------|-----|
| **语言** | C |
| **ISA** | x86, x86_64, ARM, SPARC (via libfirm) |
| **状态** | 低频维护 |

#### 2.4.1 Stack Canary

**结论: ❌ 显式标注为不支持**

文件: `src/driver/options.c` (lines 505-506), `src/driver/help.c` (lines 242-243)

```c
// options.c
f_yesno_arg("-fstack-protector", s) ||
f_yesno_arg("-fstack-protector-all", s)) {
// 被接受但忽略
```

```c
// help.c
help_f_yesno("-fstack-protector",     "Ignored (gcc compatibility)");
help_f_yesno("-fstack-protector-all", "Ignored (gcc compatibility)");
```

cparser **诚实地标注** `-fstack-protector` 为 "Ignored"，但用户仍可能误以为启用了保护。

#### 2.4.2 FORTIFY_SOURCE

**结论: ❌ 强制禁用**

文件: `src/driver/predefs.c` (line 316), `src/driver/c_driver.c` (lines 179-180)

```c
// predefs.c
add_define("_FORTIFY_SOURCE", "0", false);

// c_driver.c
driver_add_flag(o, "-U_FORTIFY_SOURCE");
driver_add_flag(o, "-D_FORTIFY_SOURCE=0");
```

双重保障确保 FORTIFY 被禁用。

#### 2.4.3 黑盒 Fuzz 价值

✅ **中等**。cparser 通过 libfirm 后端支持 4 个 ISA，但 libfirm 本身的构建可能存在挑战。

---

### 2.5 chibicc / slimcc / widcc (chibicc 家族)

| 属性 | chibicc | slimcc | widcc |
|------|---------|--------|-------|
| **语言** | C | C | C |
| **ISA** | x86-64 | x86-64 | x86-64 |
| **状态** | 归档 | 活跃 | 活跃 |

#### 2.5.1 Stack Canary

**结论: ❌ 未实现（全部三个）**

三个编译器的 `main.c` 中均有类似代码：

```c
!strcmp(argv[i], "-fno-stack-protector") ||  // 接受但忽略
```

它们接受 GCC 兼容性 flags 但不做任何处理。

#### 2.5.2 FORTIFY_SOURCE

**结论: ❌ 未实现**

三个编译器均无 `__builtin_object_size` 实现，也不处理 `_FORTIFY_SOURCE`。

#### 2.5.3 VLA / alloca — 安全关注点

chibicc 和 slimcc 都实现了 VLA 和 `builtin_alloca`，包括动态栈分配和 VLA 反分配。在无 canary 情况下：

- VLA 缓冲区溢出直接覆写 `%rbp` 和返回地址
- `alloca` 同样无保护
- slimcc 维护了 `vla_base_ofs` 用于 VLA 反分配，但不对溢出做任何检测

#### 2.5.4 黑盒 Fuzz 价值

✅ **高** (slimcc, widcc)。它们活跃开发、易于构建、仅 x86-64 单一目标。  
⚠️ **中** (chibicc)。已归档但代码简洁，适合教学级 fuzz。

---

### 2.6 MIR (c2m)

| 属性 | 值 |
|------|-----|
| **语言** | C |
| **ISA** | x86_64, AArch64, PPC64, S390x, RISC-V64 |
| **状态** | 活跃 |
| **特性** | JIT 编译器 |

#### 2.6.1 Stack Canary / FORTIFY

**结论: ❌ 均未实现**

审计了 `mir-gen-aarch64.c` 的栈帧布局（lines 28-61 注释）和 prologue 生成（lines 1015-1110）：

```
Stack layout (AArch64):
  | gr/vr save area  |  vararg 可选
  | saved regs       |  callee-saved
  | slots for pseudos|
  | LR | old FP      |  无 canary slot
  | alloca areas     |
```

frame_size 计算仅包含 vararg save area + callee saved regs + pseudo slots + lr/fp，**无 canary slot 分配**。

5 个 ISA 的 codegen（`mir-gen-x86_64.c`, `mir-gen-aarch64.c`, `mir-gen-ppc64.c`, `mir-gen-s390x.c`, `mir-gen-riscv64.c`）均无 stack protector 代码。

#### 2.6.2 黑盒 Fuzz 价值

✅ **极高**。MIR 是 JIT 编译器，支持 5 个 ISA，且有 c2m（C 到 MIR），非常适合 DeFuzz。

---

### 2.7 其余编译器（简要审计）

以下编译器均未实现 Stack Canary 和 FORTIFY_SOURCE：

| 编译器 | 语言 | ISA | 特殊发现 | Fuzz 价值 |
|--------|------|-----|----------|-----------|
| **Kefir** | C | x86-64 | 无防御关键词 | ✅ 高（C11/C17 完整实现） |
| **Cuik** | C | x64 | 无防御关键词；使用自研 TB 后端 | ⚠️ 中 |
| **cproc** | C | x86-64, aarch64, riscv64 (via QBE) | 无防御关键词 | ✅ 高（多 ISA via QBE） |
| **lacc** | C | x86-64 | 无防御关键词 | ✅ 中 |
| **SmallerC** | C | x86-16, x86-32, MIPS | 无防御关键词 | ⚠️ 中（稀有 ISA） |
| **Cake** | C | 转译器 (C→C) | 无防御关键词；不生成机器码 | ⚠️ 低（前端转译器） |
| **8cc** | C | x86-64 | 无防御关键词；已归档 | ⚠️ 低 |
| **shecc** | C | ARMv7-A, RV32IM | 无防御关键词 | ✅ 中（嵌入式 ISA） |
| **AMaCC** | C | ARM 32-bit + JIT | 无防御关键词 | ⚠️ 中 |
| **neatcc / ncc** | C | ARM, x86, x86-64 / AArch64, x86-64 | 无防御关键词 | ⚠️ 中 |

---

## 三、汇总分析

### 3.1 防御机制实现情况矩阵

| 编译器 | Stack Canary | FORTIFY_SOURCE | `__builtin_object_size` | 接受 `-fstack-protector` |
|--------|:---:|:---:|:---:|:---:|
| TCC | ❌ | ❌ 显式禁用 | ❌ | ❌ 不接受 |
| ccc | ❌ | ❌ 显式禁用 | ⚠️ 空壳实现 | ❌ 不接受 |
| Aro | ❌ | ❌ | ❌ | ❌（解析属性但忽略） |
| cparser | ❌ | ❌ 显式禁用 | ❌ | ⚠️ 接受但标注 Ignored |
| chibicc | ❌ | ❌ | ❌ | ⚠️ 接受但忽略 |
| slimcc | ❌ | ❌ | ❌ | ⚠️ 接受但忽略 |
| widcc | ❌ | ❌ | ❌ | ⚠️ 接受但忽略 |
| MIR | ❌ | ❌ | ❌ | ❌ 不接受 |
| Kefir | ❌ | ❌ | ❌ | ❌ 不接受 |
| Cuik | ❌ | ❌ | ❌ | ❌ 不接受 |
| cproc | ❌ | ❌ | ❌ | ❌ 不接受 |
| lacc | ❌ | ❌ | ❌ | ❌ 不接受 |
| SmallerC | ❌ | ❌ | ❌ | ❌ 不接受 |
| Cake | ❌ | ❌ | ❌ | ❌ 不接受 |
| 8cc | ❌ | ❌ | ❌ | ❌ 不接受 |
| shecc | ❌ | ❌ | ❌ | ❌ 不接受 |
| AMaCC | ❌ | ❌ | ❌ | ❌ 不接受 |
| neatcc/ncc | ❌ | ❌ | ❌ | ❌ 不接受 |

### 3.2 攻击面分析

由于所有编译器都不实现 canary，任何栈缓冲区溢出都可直接覆写返回地址实现 RIP/PC 劫持。特别危险的场景：

1. **VLA 溢出**: TCC (5 ISA), chibicc/slimcc/widcc (x86-64), ccc (4 ISA) 都支持 VLA 但无保护
2. **alloca 溢出**: 同上，且 `alloca` 返回的指针直接位于当前栈帧
3. **局部数组溢出**: 所有编译器的基本场景，最简单的攻击面
4. **格式化字符串**: 在无 FORTIFY 时，`sprintf` / `strcpy` 等无边界检查

### 3.3 DeFuzz 黑盒 Fuzz 优先级建议

| 优先级 | 编译器 | 理由 |
|--------|--------|------|
| **P0** | TCC | 5 ISA、活跃、易构建、核心测试目标 |
| **P0** | MIR (c2m) | 5 ISA、JIT、活跃 |
| **P0** | ccc | 4 ISA、活跃开发、FORTIFY 空壳实现是高价值攻击面 |
| **P1** | cproc | 3 ISA (via QBE)、活跃 |
| **P1** | slimcc | 活跃 chibicc fork、VLA 支持 |
| **P1** | Kefir | 完整 C11/C17、x86-64 |
| **P2** | widcc, cparser, SmallerC, Cuik | 中等价值 |
| **P3** | Aro, shecc, AMaCC, lacc, 8cc, Cake, chibicc, neatcc/ncc | 低/已归档/构建复杂 |

---

## 四、结论与建议

### 4.1 对 DeFuzz 项目的意义

1. **黑盒 Fuzz 基线**: 由于所有编译器都不实现防御机制，DeFuzz 的初始种子只需验证"编译器正确编译了溢出代码"（即溢出确实发生），而不需要测试"防御机制是否正确"。这降低了 Oracle 的复杂度。

2. **ccc 的 FORTIFY 空壳**: 这是一个值得上报给 ccc 开发者的发现。虽然他们知道 FORTIFY 不完整，但 `__builtin_object_size` 的空壳实现可能误导依赖该函数的用户代码。

3. **Aro 属性忽略**: 建议上报。解析但不实施安全属性是一种 silent failure。

### 4.2 可构造的 PoC 模式

以下 PoC 模式可用于验证所有编译器缺少防御的事实：

```c
// PoC: Stack buffer overflow without canary detection
#include <string.h>
void vulnerable(const char *input) {
    char buf[8];
    strcpy(buf, input);  // 无 FORTIFY, 无 canary
    // 返回时 ret addr 已被覆写
}
int main() {
    vulnerable("AAAAAAAABBBBBBBBCCCCCCCC");  // 溢出
    return 0;
}
```

```c
// PoC: VLA overflow
#include <string.h>
void vulnerable(int n) {
    char vla[n];     // 动态大小
    char buf[8] = {0};
    memset(vla, 'A', n + 64);  // 溢出到 buf 和 ret addr
}
```

### 4.3 下一步

- [ ] 对 P0 编译器执行构建验证
- [ ] 使用 DeFuzz 初始种子对 P0 编译器进行黑盒 fuzz
- [ ] 向 ccc 和 Aro 开发者报告发现
- [ ] 对 TCC 的 ARM64 VLA 处理做深度 exploit 验证（类 CVE-2023-4039 场景）
