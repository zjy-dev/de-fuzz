# `-fstack-check` (老牌 stack check) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / GNAT (Ada) / Linux kernel 中与 **`-fstack-check`** 直接相关的 invariants 抽取归类. 本机制主要面向 Ada / GNAT, 在 C/C++ 中部分有效但已被 `-fstack-clash-protection` (SCP) 取代.
>
> 机制简写与 survey 一致: **SCK** = `-fstack-check`. 与 SCP 关系见 `@/home/yall/project/de-fuzz/docs/invariants/stack-clash-protection.md`.

## 0. 术语与坐标

- **stack check**: 在每个函数 prologue 探测栈是否还有空间, 不够则触发 `Storage_Error` (Ada) 或 `SIGSEGV` (C/C++).
- **specific check**: GCC `-fstack-check=specific` 形式, 编译器明确发射 probing 序列.
- **generic check**: `-fstack-check=generic`, 或裸 `-fstack-check`, 由后端选择默认实现.
- **`STACK_CHECK_BUILTIN`**: 后端宏, 决定该后端是否原生支持 stack check 功能.
- **`STACK_CHECK_PROTECT`** / **`STACK_CHECK_MAX_FRAME_SIZE`**: 后端常量, 决定保留多少字节给异常处理 / 单帧上限.
- **probe loop**: 实际探测代码, 通常逐页向下 read.

每条 invariant 字段: `ID / statement / compiler / version / target / source_kind / source_url_or_path / evidence_snippet / version_sensitivity / oracle_mapping`.

## 1. 启用条件 (Enablement)

### INV-SCK-E01 — `-fstack-check` 三档

- **statement**: GCC `-fstack-check[=KIND]`, KIND ∈ `{generic, specific, no}`. 默认 `specific` (优先后端原生路径); `generic` 强制 GCC 通用路径; `no` 关闭. Ada 编译 (`gnatmake`) 默认隐式启用.
- **compiler**: GCC
- **version**: GCC 4.0+ (基础), 现代 KIND 语法 GCC 5+
- **target**: 多架构, 但仅部分后端实现 specific 路径
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html ; `gcc/explow.cc` (`probe_stack_range`)
- **evidence_snippet**: GCC manual: *"Generate code to verify that you do not go beyond the boundary of the stack. ... Specific is target dependent."*.
- **version_sensitivity**: stable
- **oracle_mapping**: 主要用于 Ada / GNAT runtime; C/C++ 路径下 oracle 优先 SCP, SCK 作为辅助对照.

### INV-SCK-E02 — Clang 不实现 `-fstack-check`

- **statement**: Clang 接受 `-fstack-check` 但实际*不实现* C/C++ 路径 (`clang -fstack-check` 编译产物与不带该 flag 一致). Clang 上对应安全功能由 SafeStack / SCP / SHSTK 覆盖.
- **compiler**: LLVM/Clang
- **version**: all
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/ClangCommandLineReference.html (该选项静默接受)
- **version_sensitivity**: stable
- **oracle_mapping**: Clang 路径不应依赖 SCK; oracle 跨编译器对比时此点必须显式排除.

### INV-SCK-E03 — Ada / GNAT 默认启用

- **statement**: GNAT (Ada 前端) 默认隐式 `-fstack-check`, 因 Ada 语义要求栈耗尽抛 `Storage_Error`. 关闭需 `-fno-stack-check` 或 `pragma Suppress(Storage_Check)`.
- **compiler**: GNAT (GCC Ada)
- **version**: 全部 GNAT 版本
- **target**: 同 GCC 后端覆盖
- **source_kind**: user-doc
- **source_url_or_path**: GNAT user guide "Stack Related Facilities"
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz Ada 路径 (若有) 对 SCK 的 oracle 与 C 不同.

## 2. 指令模式 (Instruction Patterns)

### INV-SCK-B01 — specific 路径: 后端原生 probe 序列

- **statement**: 后端实现 `STACK_CHECK_BUILTIN = 1` 时由 backend 提供 probe 序列, 通常等同于 SCP 但 probe 间距更大 (整个帧大小级别), 不保证逐页. 例: x86_64 GCC 在 `-fstack-check=specific` 下生成 `cmp %rsp, [stack_limit]; jb fail`-风格代码 (Ada runtime 提供 stack_limit 全局).
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: `gcc/config/i386/i386.cc` (`ix86_emit_probe_stack_range`)
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 对 specific 路径不能假定 "每页一 probe".

### INV-SCK-B02 — generic 路径: 通用 probe 循环

- **statement**: GCC 通用路径 (`gcc/explow.cc` `probe_stack_range`) 遍历栈帧大小, 每 `STACK_CHECK_PROBE_INTERVAL` 字节发一次 probe (load 或 store). 与 SCP 不同处: probe interval 默认 4096 但 *未必随 guard page 严格匹配*; 老版本曾用 8192.
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: `gcc/explow.cc`
- **version_sensitivity**: likely-to-drift (interval 历史改过)
- **oracle_mapping**: C/C++ 用 generic 路径作为 SCP 的弱替代.

### INV-SCK-B03 — probe 是 read 而非 write

- **statement**: SCK 经典 probe 是 *load* (`mov (%rsp), %rax` 类), 不像 SCP 的 `or` / `str xzr` 写. 因此 SCK probe 不修改栈内容, 但仍能触发缺页. 仅在 guard page 不可读时才触发 fault — 这是 SCK 与 SCP 设计差异核心.
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: `gcc/explow.cc` (`emit_stack_probe`)
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 对 SCK 的字节模式不同于 SCP.

## 3. 栈帧布局约束

### INV-SCK-F01 — `STACK_CHECK_PROTECT` 字节数为异常处理保留

- **statement**: SCK 保留 `STACK_CHECK_PROTECT` 字节 (默认 8KB) 在栈底, 用于栈耗尽时调用 `__gnat_unhandled_terminate` / 信号处理函数. 该保留区不被 probe, 但任何普通栈分配不得侵入.
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: `gcc/defaults.h` (`STACK_CHECK_PROTECT`)
- **version_sensitivity**: stable
- **oracle_mapping**: 不直接面向 fuzzing, 但解释为何 SCK 在 *接近耗尽* 时仍可恢复.

### INV-SCK-F02 — 单帧 `STACK_CHECK_MAX_FRAME_SIZE` 上限

- **statement**: SCK 假设单函数帧不超 `STACK_CHECK_MAX_FRAME_SIZE` (默认 100KB); 超过则编译器警告 "stack frame too large". 老 Ada 程序常因此报警.
- **compiler**: GCC
- **source_kind**: user-doc + source
- **source_url_or_path**: `gcc/defaults.h`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

### INV-SCK-F03 — alloca / VLA 不被 SCK 覆盖

- **statement**: SCK 主要在 prologue 一次性 probe 静态帧, 对运行时 alloca / VLA 的页跨越 *不一定*覆盖 (取决于后端). 这是 SCK 在 stack clash 攻击下不足的根本原因, 也是 SCP 引入的动机.
- **compiler**: GCC
- **source_kind**: source + 设计
- **source_url_or_path**: `gcc/explow.cc` 注释 ; CVE-2017-1000364 历史
- **version_sensitivity**: stable
- **oracle_mapping**: 反例 seed: 仅开 SCK, alloca(big) 应能跨过 guard page.

## 4. 属性 / 局部控制

### INV-SCK-A01 — `no_stack_limit` 属性

- **statement**: GCC 函数属性 `no_stack_limit` 关闭该函数的 stack limit 检查 (与 `-fstack-limit-*` 配合, 不直接是 SCK 关停, 但在 SCK / split-stack 场景影响 prologue probe). Clang 不识别该属性.
- **compiler**: GCC
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

## 5. ELF 元数据

### INV-SCK-M01 — 不依赖 ELF property

- **statement**: SCK 是纯 codegen, 与 ELF property 无关. 同 SCP. (除 GNAT runtime 通过 `__gnat_handle_vms_condition` 等符号挂钩外.)
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 不需 readelf 校验.

## 6. 运行时语义

### INV-SCK-R01 — 触发栈耗尽时, C/C++ 路径表现为 `SIGSEGV`

- **statement**: SCK 的 probe 命中 guard page 时触发 `SIGSEGV`, 与 SCP 一致, siginfo 同样普通段错误. C/C++ 路径无显著区分点.
- **runtime**: Linux kernel
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 信号与 SCP 相同.

### INV-SCK-R02 — Ada 路径: GNAT runtime 把 fault 翻译为 `Storage_Error`

- **statement**: GNAT runtime (`s-stchop.adb`) 通过信号处理把 SCK 触发的 `SIGSEGV` 翻成 Ada `Storage_Error` 异常, 让程序 catch. 这是 SCK 在 Ada 中"可恢复"的原因.
- **runtime**: GNAT
- **source_kind**: source
- **source_url_or_path**: GCC `gcc/ada/s-stchop.adb`
- **version_sensitivity**: stable
- **oracle_mapping**: Ada 路径独立, oracle 不同.

## 7. 与其他机制的交互

### INV-SCK-I01 — SCK 与 SCP 可同时启用; SCP 优先

- **statement**: 同时使用 `-fstack-check` 与 `-fstack-clash-protection` 时, SCP 的逐页 probe 覆盖 SCK 的预 probe; GCC 不会冲突, 但 SCP 已包含 SCK 的所有有效保护.
- **compiler**: GCC
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵 CFLAGS.

### INV-SCK-I02 — SCK 与 split-stack (gccgo) 互斥

- **statement**: `-fsplit-stack` (Go runtime 用) 与 SCK 不兼容, 因为 split-stack 自管栈分配. GCC 在 split-stack TU 上忽略 SCK.
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: `gcc/config/i386/i386.cc` (`ix86_expand_split_stack_prologue`)
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明; gccgo 路径不在 DeFuzz 主要 oracle 范围.

## 8. 验证与已知回归

### INV-SCK-VER-CVE-2017-1000364 — SCK 不足以防 stack clash

- **statement**: Qualys 演示 SCK 无法防御 stack clash (alloca 可跳过 guard page). 这是 GCC 引入 SCP 的直接动因. 历史 invariant: *SCK 不可作为 stack clash 缓解*.
- **bug-disclosure**
- **source_url_or_path**: https://www.qualys.com/2017/06/19/stack-clash/stack-clash.txt
- **version_sensitivity**: stable
- **oracle_mapping**: 反例 seed.

### INV-SCK-VER-PR89725 — GCC SCK 在 AArch64 prologue 漏检

- **statement**: 历史回归 PR89725: AArch64 SCK probe 序列在含 LR signing (PAC) 的 prologue 中漏检. 修复在 GCC 9.
- **compiler**: GCC
- **version**: 修复于 GCC 9
- **source_kind**: mailing-list
- **source_url_or_path**: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=89725
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 GCC AArch64 + PAC + SCK 组合 seed.

## 9. DeFuzz SCK Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-SCK-R01 | `SIGSEGV` 在大栈帧 prologue | 大数组局部 |
| INV-SCK-F03 (反例) | alloca(big) 不被拦截, 程序成功越界 | alloca + 写远端 |
| INV-SCK-VER-CVE-2017-1000364 | 反例: 仅 SCK 时 stack clash 成功 | Qualys POC |

## 10. 开放问题

- **interval 默认值历史**: GCC 在 4.x / 5.x / 7.x 各自调整过 SCK probe interval, 文献分散. 需补 `git log --grep stack-check` 历史 invariant.
- **GNAT runtime 对 alt-stack / 多线程的处理**: SCK 在 pthread 子线程是否生效, 需在 GNAT runtime 中查 `s-stchop.adb` 实现.
- **Clang 是否未来实现 SCK**: 目前无路线图, 但若未来支持需补 invariant.

## 11. 使用建议

- C/C++ 项目优先用 SCP (强语义), SCK 作为兼容选项.
- Ada 项目中 SCK 必开, 但应额外 `+SCP` 强化.
- `likely-to-drift` invariant (probe interval) 在每次 GCC major 升级人工核对.
- DeFuzz oracle 对 SCK 的覆盖优先级低于 SCP; 在矩阵 CFLAGS 中作为 *变体* 而非 *主控*.
