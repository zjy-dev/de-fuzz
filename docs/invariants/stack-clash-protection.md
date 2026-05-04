# `-fstack-clash-protection` Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / LLVM/Clang / glibc / Linux kernel 中与 **stack clash protection (SCP)** 直接相关的 invariants 统一抽取、归类, 作为 DeFuzz SCP oracle 的形式化依据.
>
> 机制简写与 survey 一致: **SCP** = `-fstack-clash-protection`. 与 `_FORTIFY_SOURCE` (FORT) / stack canary (SP) / `-fstack-check` (SCK) 协同, 但保护面不同 (见 §7).

## 0. 术语与坐标

- **stack clash**: 一类攻击范式 (Qualys 2017): 通过让程序分配一块超过单个守护页 (guard page) 的栈对象, 使下一次访问跳过 guard page 直接落到 *相邻 mmap / heap 段*, 从而打通栈与堆的隔离, 实现任意写.
- **guard page**: 内核在栈段下方 (向下生长栈) 留下的不可访问页, 越界访问应触发 `SIGSEGV`. 单一 guard page 大小通常等于一个 page (4KB / 16KB / 64KB), 但攻击者可一次分配 ≥ 8KB 跳过它.
- **probe**: 编译器在大栈分配前/后向栈上写入一个无意义字节, 强迫 CPU 实际触碰每页, 从而踩到 guard page.
- **probe interval**: 两次连续 probe 之间的字节距离, 必须 ≤ guard page 大小, 通常等于 4096. 由 `--param stack-clash-protection-probe-interval`/ `STACK_CLASH_PROTECTION_PROBE_INTERVAL` 控制.
- **probe granularity**: 单次 probe 的位置偏移, 必须确保任意大小的栈分配都被覆盖. 见 GCC `compute_stack_clash_protection_loop_data`.
- **outgoing args / dynamic stack**: 函数调用前为参数预留的栈区, 与 `alloca` / VLA 的动态分配同样是攻击目标.
- **`STACK_CLASH_MIN_BYTES_OUTGOING_ARGS`**: 后端常量, 决定 outgoing args 区是否需 probe 的下界.

每条 invariant 字段: `ID / statement / compiler / version / target / source_kind / source_url_or_path / evidence_snippet / version_sensitivity / oracle_mapping`.

## 1. 启用条件 (Enablement)

### INV-SCP-E01 — `-fstack-clash-protection` 启用每页 probe

- **statement**: GCC / Clang 选项 `-fstack-clash-protection` 启用栈分配的逐页探测, 使任意 alloca / VLA / 大栈帧 / outgoing args 在分配后必须立即触碰每个跨过的页面, 保证不能跳过 guard page.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 11+
- **target**: x86_64, i386, aarch64, powerpc, s390x (非全部架构启用)
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html ; `gcc/explow.cc` (`probe_stack_range`, `anti_adjust_stack_and_probe_stack_clash`)
- **evidence_snippet**: GCC manual: *"Generate code to prevent stack clash style attacks. ... allocates only one page at a time and immediately probes it"*.
- **version_sensitivity**: stable (后端实现差异 likely-to-drift)
- **oracle_mapping**: DeFuzz SCP oracle 用 `-fstack-clash-protection` 与 `-fno-stack-clash-protection` 作正反控组; 同一 seed (含大 alloca) 期望前者每页有 probe 指令, 后者无.

### INV-SCP-E02 — `-fhardened` 隐式启用 SCP

- **statement**: GCC `-fhardened` 隐式包含 `-fstack-clash-protection`. 因此 Linux 用户态默认 profile 下 SCP 默认开启.
- **compiler**: GCC
- **version**: GCC 14+
- **target**: 同 INV-SCP-E01
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: 与 `-fhardened` 整体行为一致.

### INV-SCP-E03 — 仅在大栈帧 / 动态分配触发实际 probing 代码

- **statement**: 编译器在静态栈帧 ≤ probe interval (通常 4096 字节) 时不发 probe (因为单次分配不会跨页); 仅当帧 > 该阈值或存在 alloca/VLA 时才生成 probe 序列. 因此小函数即便 `-fstack-clash-protection` 也无可观察的 probe.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 11+
- **source_kind**: source
- **source_url_or_path**: `gcc/explow.cc` (`STACK_CLASH_PROTECTION_PROBE_INTERVAL`) ; LLVM `llvm/lib/Target/X86/X86FrameLowering.cpp` (`emitStackProbeCallWithCFA`)
- **version_sensitivity**: stable (阈值 likely-to-drift)
- **oracle_mapping**: 大缓冲区 / 大数组 / `alloca(big)` seed; oracle 检查 prologue 是否含 `or %r..., 0(%rsp)` 或等价 probe 指令.

## 2. 指令模式 (Instruction Patterns)

### INV-SCP-B01 — x86_64 上的 probe 序列

- **statement**: GCC / Clang 在 x86_64 上的 probe 序列典型形式为 `or QWORD PTR [rsp], 0` (8 字节读改写, 对栈无影响, 但确保物理触碰), 或 `mov QWORD PTR [rsp+offs], 0`. 对大分配, 编译器展开为循环 `1: sub $4096, %rsp; or $0, (%rsp); cmp ..., %rsp; jne 1b`.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 11+
- **target**: x86_64
- **source_kind**: source
- **source_url_or_path**: `gcc/config/i386/i386.cc` (`ix86_adjust_stack_and_probe_stack_clash`) ; LLVM `X86FrameLowering.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 用 `objdump -d` 抓 `or` / `mov` 至 `[rsp]` 的 probe 指令计数, 与 alloca 大小匹配.

### INV-SCP-B02 — AArch64 上的 probe 序列

- **statement**: AArch64 上 `aarch64_allocate_and_probe_stack_space` 生成 `sub sp, sp, #4096` 后立即 `str xzr, [sp]` 序列, 对大分配展开为循环. 对静态可知大小的栈帧, 编译器在 prologue 内单条 `sub sp, sp, #N` 后逐页 probe.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 11+
- **target**: aarch64
- **source_kind**: source
- **source_url_or_path**: `gcc/config/aarch64/aarch64.cc` (`aarch64_allocate_and_probe_stack_space`) ; LLVM `llvm/lib/Target/AArch64/AArch64FrameLowering.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 抓 `str xzr, [sp]` 出现频次, 应与栈大小 / probe interval 比例一致.

### INV-SCP-B03 — RISC-V 上的 probe 序列 (实验)

- **statement**: RISC-V GCC 13+ / Clang 16+ 实现 `-fstack-clash-protection`, 使用 `addi sp, sp, -4096; sd zero, 0(sp)` 序列. 较新, 边角细节 likely-to-drift.
- **compiler**: GCC 13+, LLVM 16+
- **version**: 实验
- **target**: riscv64
- **source_kind**: source
- **source_url_or_path**: `gcc/config/riscv/riscv.cc` (`riscv_allocate_and_probe_stack_space`)
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 同 AArch64, 但抓 `sd zero, 0(sp)`.

### INV-SCP-B04 — i386 / s390x / powerpc 各自后端实现

- **statement**: 各后端有自己的 probe 序列实现; i386 使用 `or DWORD PTR [esp], 0`, s390x 用 `mvc 0(8,%r15), 0(%r15)`, powerpc 用 `stwu` / `stdu` 跨页. 这些后端的 SCP 触发点完全位于 `gcc/config/<arch>/<arch>.cc` 的 `*_adjust_stack_and_probe*` 函数.
- **compiler**: GCC
- **target**: i386, s390x, powerpc
- **source_kind**: source
- **source_url_or_path**: 对应后端 `*.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: 跨架构的 SCP oracle 需架构特定字节模式.

### INV-SCP-B05 — probe 对 stack 内容透明 (or 0 / xzr 写)

- **statement**: probe 指令不能改变栈语义, 所以选用 `or [rsp], 0` / `str xzr, [sp]` 等"读改写为同值"或"写零"模式. 这要求触碰的字节是 *尚未被业务代码使用* 的页面 (因此写 0 不破坏数据).
- **compiler**: GCC, LLVM/Clang
- **source_kind**: source + 设计文档
- **source_url_or_path**: `gcc/explow.cc` 注释
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 验证 probe 字节模式, 反例: 如果 probe 指令使用了非零非守恒模式, 视为 bug.

## 3. 栈帧布局约束 (Frame Layout)

### INV-SCP-F01 — probe 间距 ≤ guard page

- **statement**: 任意两次连续 probe 的字节距离必须 ≤ probe interval (默认 4096), 否则在分配跨过未触碰页面时无法保证踩到 guard. GCC `--param stack-clash-protection-probe-interval=12` (单位是 log2 字节) 控制.
- **compiler**: GCC
- **version**: GCC 8+
- **source_kind**: source + user-doc
- **source_url_or_path**: GCC manual `--param` 节; `gcc/params.opt`
- **evidence_snippet**: GCC `params.opt`: *"`stack-clash-protection-probe-interval`: log2 of stack clash probe interval"*.
- **version_sensitivity**: stable (不同架构默认值不同, 但通常 12)
- **oracle_mapping**: 大 alloca seed (例 32KB) 应观察至少 8 个 probe.

### INV-SCP-F02 — alloca / VLA 后必须立即 probe

- **statement**: `alloca(n)` / VLA 在分配后必须立即 probe 跨过的所有页面, 不能延迟 (例如等 alloca 后下次 store 时才碰). 否则攻击者控制 `n` 可一次性跳过 guard page.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 8+, Clang 11+
- **source_kind**: source + test
- **source_url_or_path**: `gcc/explow.cc` (`anti_adjust_stack_and_probe_stack_clash`) ; `gcc/testsuite/gcc.dg/stack-clash-*.c`
- **version_sensitivity**: stable
- **oracle_mapping**: 用户控制 `alloca(n)` 大小的 seed, oracle 在 prologue/分配点后期待 probe 循环.

### INV-SCP-F03 — outgoing args 区域大于 `STACK_CLASH_MIN_BYTES_OUTGOING_ARGS` 时也需 probe

- **statement**: 函数调用前 push 大量参数或预留 large outgoing args 区, 等同于一次大栈分配; 当该区 ≥ `STACK_CLASH_MIN_BYTES_OUTGOING_ARGS` (后端常量, 默认 1024 或 4096) 时必须 probe.
- **compiler**: GCC
- **version**: GCC 8+
- **source_kind**: source
- **source_url_or_path**: `gcc/explow.cc` (`compute_stack_clash_protection_loop_data`) ; `gcc/config/i386/i386.cc`
- **version_sensitivity**: stable (常量 likely-to-drift)
- **oracle_mapping**: seed 模板: `f(struct big_arg_t arg)` 通过值传递大结构.

### INV-SCP-F04 — prologue 末尾必须 probe 第一页

- **statement**: 即使函数总栈帧 < probe interval, 若函数也包含 alloca / VLA / 大 outgoing args, prologue 末尾必须 probe 至少一页. 这是 *函数入口与第一次大动态分配之间不允许跨页 unprobed 间隙* 的具体表现.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `gcc/explow.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: 含 alloca 但帧很小的 seed, oracle 仍期望 prologue 末尾有 probe.

### INV-SCP-F05 — `_chkstk` (Windows) / Wine 路径不替代 SCP

- **statement**: Windows MSVC `_chkstk` 是另一种栈探测 (针对 Windows 仅 1-page commit 模型), 与 Linux SCP 不直接对等. 编译器为 Windows target 自动用 `_chkstk` 而非 GCC 风格 probe; DeFuzz Linux 路径应当避免误把 `_chkstk` 当作 SCP 实现.
- **compiler**: MSVC, clang-cl, MinGW
- **target**: x86_64-win, i386-win, mingw
- **source_kind**: user-doc
- **source_url_or_path**: Microsoft `_chkstk` docs
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

## 4. 属性 / 局部控制 (Attributes)

### INV-SCP-A01 — 没有函数级开关属性

- **statement**: 与 `no_stack_protector` 不同, GCC / Clang **不**提供 `no_stack_clash_protection` 函数级属性. 启停只在编译单元级别. 因此对极少数对栈布局极敏感的函数 (汇编 ABI 接口), 必须以 `-fno-stack-clash-protection` 编译整 TU.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html (无对应属性)
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

## 5. ELF 元数据

### INV-SCP-M01 — 不依赖 ELF property

- **statement**: SCP 是纯编译期 codegen, 与 ELF GNU property / dynamic tag 无关. 链接器混编 SCP / 非 SCP 对象不会冲突, 但安全保证降级到 "未启用 SCP 的最弱单位".
- **compiler + linker**: 不适用
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 不需 `readelf` 校验; 直接看 `.text` 中 probe 指令存在性.

## 6. 运行时语义

### INV-SCP-R01 — 栈耗尽触发 `SIGSEGV` (普通段错误, 不区分)

- **statement**: SCP 启用下, 攻击者制造的"超大 alloca"在分配过程中必踩 guard page, 触发 `SIGSEGV`. siginfo 表现为普通段错误 (`si_code = SEGV_MAPERR` 或 `SEGV_ACCERR`), 与解引用空指针无显著区别. 这是 SCP 的根本 oracle 信号: *未崩溃 = 失败 (绕过 guard page); 崩溃 = 成功 (拦截)*.
- **runtime**: Linux kernel
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz SCP oracle 的核心: 用 "alloca(N)" 配合 N 增长穷举, 期望从某个阈值起必崩溃.

### INV-SCP-R02 — guard page 必须由内核分配

- **statement**: SCP 假设栈段下方有至少一页 guard page. Linux 通过 `arch_align_stack` + `STACK_GUARD_GAP` 维护; 默认 `STACK_GUARD_GAP = 256 * PAGE_SIZE` (1MB on x86_64), 远超单 guard page. 但 SCP 的 probe interval 默认仍是 1 page, 因此 SCP 在 1-page guard 即可工作.
- **runtime**: Linux kernel
- **version**: Linux ≥ 4.12
- **source_kind**: source
- **source_url_or_path**: Linux `mm/mmap.c` (`STACK_GUARD_GAP`)
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 在启动前读 `/proc/<pid>/maps` 确认栈段下方 gap.

### INV-SCP-R03 — 多线程栈与主栈一致

- **statement**: pthread 子线程栈由 glibc `pthread_create` 通过 `mmap` 分配, 默认配 `PROT_NONE` guard page. SCP 同样适用, 因为 probe 行为与栈来源无关.
- **runtime**: glibc nptl
- **source_kind**: source
- **source_url_or_path**: glibc `nptl/allocatestack.c`
- **version_sensitivity**: stable
- **oracle_mapping**: 多线程 alloca seed.

## 7. 与其他机制的交互 (Interactions)

### INV-SCP-I01 — SCP 与 `-fstack-check` 部分重叠但语义不同

- **statement**: `-fstack-check` (SCK) 是 Ada 风格的栈耗尽检测, 在每次分配后访问栈顶以触发 fault, 主要用于 Ada runtime; 不保证 *所有跨页* 都被触碰, 因此对 stack clash 攻击不完全有效. SCP 引入后取代了 SCK 的安全用途. 二者可同时启用, GCC 文档明确说 "stack-clash-protection supersedes -fstack-check for safety".
- **compiler**: GCC, LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html ; `@/home/yall/project/de-fuzz/docs/invariants/stack-check.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 反例 seed: 仅开 `-fstack-check` 但不开 SCP, 期望 stack clash 攻击成功.

### INV-SCP-I02 — SCP 与 stack canary 互不替代

- **statement**: canary 拦截 *返回地址前的字节级溢出*, SCP 拦截 *栈分配跳过 guard page*. 两者覆盖完全不同窗口, 须并用. 见 `@/home/yall/project/de-fuzz/docs/invariants/stack-canary.md`.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵 CFLAGS 穷举.

### INV-SCP-I03 — SCP 与 `_FORTIFY_SOURCE` 正交

- **statement**: FORT 在 libc 函数级别拦截已知 length 的越界; SCP 在编译期插桩防分配跨页. 二者互不影响.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/fortify-source.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵 CFLAGS.

### INV-SCP-I04 — `setjmp` / `longjmp` 不影响 SCP

- **statement**: `setjmp/longjmp` 跨函数跳转时栈指针被恢复到旧帧, 不涉及新分配, 因此不会引入未 probe 的页面. SCP 对其透明.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

### INV-SCP-I05 — SCP 不保护 mmap / heap; 仅栈

- **statement**: SCP 仅针对*栈* clash. 用户对 `mmap` / `malloc` 的越界仍依赖 ASLR / guard pages / sanitizer. DeFuzz oracle 对 SCP 的覆盖范围限定在栈分配场景.
- **compiler**: GCC, LLVM/Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: oracle scope 限制.

## 8. 验证与已知回归 (Known Regressions)

### INV-SCP-VER-CVE-2017-1000364 — Qualys "Stack Clash" 披露

- **statement**: CVE-2017-1000364 是 SCP 引入的直接动机: Qualys 演示通过大 alloca 跳过 1-page guard 完成栈与堆混杂. 内核侧响应是 `STACK_GUARD_GAP` 扩大到 1MB; 编译器侧响应是引入 `-fstack-clash-protection`. 历史 invariant: *guard page 单 1 页, 不能依赖单页*.
- **bug-disclosure**
- **source_url_or_path**: https://www.qualys.com/2017/06/19/stack-clash/stack-clash.txt
- **version_sensitivity**: stable
- **oracle_mapping**: 历史回归 seed, 大 alloca 跳过 guard.

### INV-SCP-VER-PR91458 — GCC AArch64 outgoing args 漏 probe

- **statement**: GCC PR91458: AArch64 上 SCP 在 outgoing args 区超 `STACK_CLASH_MIN_BYTES_OUTGOING_ARGS` 但 < probe interval 时漏 probe 的回归. 修复在 GCC 10.
- **compiler**: GCC
- **version**: 修复于 GCC 10
- **source_kind**: mailing-list + source
- **source_url_or_path**: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=91458
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 GCC 版本回归 seed.

### INV-SCP-VER-LLVM-D75598 — Clang AArch64 SCP 实现补齐

- **statement**: Clang 11 通过 D75598 等系列补丁补齐 AArch64 SCP, 早期版本 (Clang 9-10) 仅在 x86_64 实现.
- **compiler**: LLVM/Clang
- **version**: Clang 11+
- **source_kind**: mailing-list + source
- **source_url_or_path**: https://reviews.llvm.org/D75598
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 Clang 在 AArch64 跑 SCP seed 应回归未实现.

## 9. DeFuzz SCP Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-SCP-R01 (开 SCP) | `SIGSEGV` 在 alloca/VLA 期间 | `alloca(user_size)`, user_size 由 fuzz 数据控制 |
| INV-SCP-R01 (关 SCP) | 进程仍正常运行或在某个写时崩溃 | 对照 |
| INV-SCP-F02 | prologue/分配点后 `or [rsp],0` 频次正比于栈大小 | 大 VLA |
| INV-SCP-F03 | 大参数 byval 调用前 probe 序列 | 传值大 struct |
| INV-SCP-VER-PR91458 | 老 GCC AArch64 漏 probe | seed 触发 outgoing args 介于阈值 |

## 10. 开放问题

- **probe-interval tuning**: `--param stack-clash-protection-probe-interval` 在边角值 (e.g. 13 = 8KB) 下的安全性是否足够; 内核 guard page 大小 ≥ 4KB 时安全, < 4KB 时无意义. 待补充 invariant.
- **glibc nptl 动态栈大小**: 子线程栈由用户传入大小, 部分历史版本未 mmap guard page, 需结合 glibc 版本验证.
- **JIT 生成的栈分配**: V8 / LuaJIT 自管栈帧, 不走 GCC/Clang 路径, 无 SCP 保护; oracle 不覆盖.
- **绿色线程 / coroutines**: 协程栈通常较小且自管, SCP 在协程切换前后行为待验证.
- **SCP 在异常 unwind 中的开销**: 每次 unwind 跨多帧后需重新 probe 吗? 目前 GCC/Clang 都是 *仅在分配时* probe, unwind 不重 probe (因恢复的栈位置已 probed). 待加入 invariant 文字化.

## 11. 使用建议

- 在 CFLAGS 矩阵中 `-fstack-clash-protection` 与 `-fno-stack-clash-protection` 须穷举; 配合大 alloca 模板 seed.
- 多架构回归: x86_64 / aarch64 / riscv64 各有自身 probe 序列, oracle 字节模式不可共用.
- 对 `likely-to-drift` invariant (INV-SCP-B03 RISC-V 实现, INV-SCP-F01 probe interval) 在每次主分支编译器升级时核对.
- DeFuzz oracle 启动前应 `cat /proc/sys/vm/...` 或 `cat /proc/<pid>/maps` 验证栈 guard 存在.
