# Clang SafeStack Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 LLVM/Clang / compiler-rt / Code-Pointer Integrity (CPI) 论文 中与 **SafeStack** 直接相关的 invariants 抽取归类, 作为 DeFuzz SafeStack oracle 的形式化依据.
>
> 机制简写与 survey: **SS** = Clang SafeStack. CPI 论文 (OSDI 2014) 是其理论基础.

## 0. 术语与坐标

- **SafeStack**: 把每个函数的栈对象分两类:
  - **safe stack**: 仅放返回地址、callee-saved spill、寿命安全 (lifetime-safe) 的局部变量; 仍由 `RSP` 寻址 (常规栈位置).
  - **unsafe stack**: 放可能被取地址的局部 (容易溢出的 char buffer / struct), 由 `__safestack_unsafe_stack_ptr` 指向独立 mmap 段.
  缓冲区溢出仅污染 unsafe stack, 安全栈上的返回地址保持完整.
- **`__safestack_unsafe_stack_ptr`**: 每线程 TLS 变量, runtime 维护. 函数 prologue/epilogue 通过它 push/pop unsafe locals.
- **lifetime-safe**: 编译器保守推断不取地址、size 编译期已知、不超出函数体的局部, 才放 safe stack. 其他放 unsafe.
- **runtime**: `compiler-rt/lib/safestack/safestack.cc`, 提供 `__safestack_init`, 处理 pthread_create / 线程退出.

每条 invariant 字段同前.

## 1. 启用条件 (Enablement)

### INV-SS-E01 — `-fsanitize=safe-stack` 启用

- **statement**: Clang 选项 `-fsanitize=safe-stack` 启用 SafeStack. 链接时自动加 `compiler-rt` SafeStack runtime (`libclang_rt.safestack-*.a`).
- **compiler**: LLVM/Clang
- **version**: Clang 3.8+
- **target**: x86_64, aarch64 (主要), 部分 i386
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/SafeStack.html ; `clang/lib/Driver/SanitizerArgs.cpp`
- **evidence_snippet**: Clang docs: *"`SafeStack` is an instrumentation pass that protects programs against attacks ... by separating the program stack into a safe stack ... and an unsafe stack."*.
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 抓 `mov %fs:__safestack_unsafe_stack_ptr@TPOFF, %rN` 字节模式.

### INV-SS-E02 — GCC 不实现 SafeStack

- **statement**: GCC 没有等价 SafeStack 选项. SS 是 LLVM 独有的实现.
- **compiler**: GCC (无)
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/SafeStack.html
- **version_sensitivity**: stable
- **oracle_mapping**: GCC 路径不应启 SS.

### INV-SS-E03 — `-fno-omit-frame-pointer` 推荐

- **statement**: 使用 SafeStack 推荐配 `-fno-omit-frame-pointer`, 因为 unsafe stack 帧指针对调试 / 栈展开关键. 不强制, 但 release 中省 fp 可能让 unwind / stack trace 失真.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/SafeStack.html
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

## 2. 字节模式

### INV-SS-B01 — Prologue: 取 unsafe stack ptr

- **statement**: x86_64 函数 prologue 在分配 unsafe locals 时:
  ```
  mov %fs:__safestack_unsafe_stack_ptr@TPOFF, %rN   ; 取当前 unsafe sp
  sub $SIZE, %rN                                     ; 分配
  mov %rN, %fs:__safestack_unsafe_stack_ptr@TPOFF   ; 存回
  ```
  本地 unsafe local 通过 `(rN)` 寻址.
- **compiler**: LLVM/Clang
- **target**: x86_64
- **source_kind**: source + lit test
- **source_url_or_path**: `llvm/lib/CodeGen/SafeStack.cpp` ; `llvm/test/Transforms/SafeStack/`
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描.

### INV-SS-B02 — AArch64 prologue

- **statement**: AArch64 函数 prologue 用 `mrs` 读 TPIDR_EL0 取 TLS, 加上 `__safestack_unsafe_stack_ptr` 偏移; 与 x86 同构但寻址不同.
- **compiler**: LLVM/Clang
- **target**: aarch64
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/Target/AArch64/AArch64ISelLowering.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描.

## 3. 栈分配规则

### INV-SS-F01 — 取地址 / 大数组 / VLA -> unsafe stack

- **statement**: 编译器把以下放 unsafe stack:
  1. 任何被取地址的 local (`&local`)
  2. 数组 (含 char buffer)
  3. VLA / alloca
  4. 大型 struct (含数组的字段)
  5. 不能完全 SSA 提升的 spill 变量
  其余放 safe stack (返回地址、单一 scalar 已 SSA 化的 spill).
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/CodeGen/SafeStack.cpp` (`isAllocaSafeStack`)
- **version_sensitivity**: likely-to-drift (启发式细节)
- **oracle_mapping**: 含取地址 local seed.

### INV-SS-F02 — 返回地址必须在 safe stack

- **statement**: 函数 prologue 把返回地址压在 safe stack (即原始 RSP 指向的栈), 不在 unsafe stack. 这是 SafeStack 的根本属性, 保证 unsafe stack 溢出不影响返回地址.
- **compiler**: LLVM/Clang
- **source_kind**: source + 设计
- **source_url_or_path**: https://clang.llvm.org/docs/SafeStack.html
- **version_sensitivity**: stable
- **oracle_mapping**: 缓冲区溢出仍走 unsafe stack, 不影响 RIP 控制 — 这是 SS 的差分 oracle.

## 4. 属性

### INV-SS-A01 — `no_sanitize("safe-stack")` 函数级关停

- **statement**: 函数属性 `__attribute__((no_sanitize("safe-stack")))` 或 `__attribute__((safebuffers))` 关闭该函数 SafeStack. 该函数的所有 local 都回普通栈.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html
- **version_sensitivity**: stable
- **oracle_mapping**: 函数级 seed.

## 5. ELF 元数据

### INV-SS-M01 — 不需 ELF property

- **statement**: SafeStack 是 codegen 内部, 无 ELF property. 链接器不强制. 但 *runtime* 需在每线程初始化 `__safestack_unsafe_stack_ptr`.
- **compiler**: LLVM/Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 不需 readelf.

## 6. 运行时语义

### INV-SS-R01 — Runtime 在每线程分配 unsafe stack 段

- **statement**: `compiler-rt` SafeStack runtime 在 `__safestack_init` (`__attribute__((constructor))`) 给主线程分配 unsafe stack (默认 ~16MB mmap), 设 TLS ptr; 在 pthread_create 路径 hook 给子线程分配; 在线程退出释放.
- **runtime**: compiler-rt
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/safestack/safestack.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: 多线程 seed 验证.

### INV-SS-R02 — Unsafe stack 两端 guard page

- **statement**: Unsafe stack 段两端 mprotect PROT_NONE, 越界触发 SIGSEGV. 与 pthread 主栈 guard 类似.
- **runtime**: compiler-rt
- **source_kind**: source
- **source_url_or_path**: `compiler-rt/lib/safestack/safestack.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: 极深递归 / 极大 alloca seed.

### INV-SS-R03 — `setjmp` / `longjmp` 不感知 SafeStack

- **statement**: SafeStack 不影响 setjmp/longjmp; jmp_buf 仍保存 RSP, 跳回时 unsafe stack ptr 由 *该函数下次执行* 时通过 TLS 读取, 自动恢复. 但 longjmp 跨多帧时 unsafe stack 上的对象可能未释放 -> 慢慢泄漏 (类比 SCS).
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/CodeGen/SafeStack.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: longjmp 多次后 unsafe stack 大小变化.

### INV-SS-R04 — `__builtin_frame_address` 返回 safe stack 帧

- **statement**: `__builtin_frame_address(0)` 在 SafeStack 启用下返回 safe stack 上的 frame ptr, *不* 是 unsafe stack 上的. 用 `__builtin_frame_address` 做 stack walking 的代码在 SafeStack 下可能错过 unsafe locals — 这是已知泄漏点.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc + 设计
- **source_url_or_path**: https://clang.llvm.org/docs/SafeStack.html
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明.

### INV-SS-R05 — `swapcontext` / `makecontext` 不与 SafeStack 配合

- **statement**: `swapcontext` 切换 RSP, 但 unsafe stack ptr 是 TLS 变量 — 切换时不自动同步. 协程切换可能让 unsafe stack 状态错乱. SS docs 标记此为 *"known leak"*.
- **runtime**: glibc + compiler-rt
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/SafeStack.html
- **version_sensitivity**: stable
- **oracle_mapping**: 协程 seed.

## 7. 与其他机制交互

### INV-SS-I01 — SafeStack 与 SHSTK / GCS 概念重叠

- **statement**: SHSTK / GCS 是硬件方式保护返回地址; SafeStack 是软件方式. SHSTK + 软 SS 重叠保护, 但代价小. 现代平台优先 SHSTK, SS 作为不支持 SHSTK 的退化方案.
- **compiler**: LLVM/Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/shadow-stack.md` ; `@/home/yall/project/de-fuzz/docs/invariants/gcs.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-SS-I02 — SafeStack 与 stack canary 互补

- **statement**: canary 拦字节级溢出; SS 把溢出隔离到独立段. 二者机制完全不同, 同启不冲突. 缓冲区溢出在 SS 启用下根本不接触返回地址, canary 仍可作为额外检查 (但意义减小).
- **compiler**: LLVM/Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/stack-canary.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-SS-I03 — SafeStack 与 ASan 互斥

- **statement**: ASan 也修改栈分配 (poison redzone), 与 SafeStack 互斥. 同时启用编译器报错或行为不定. Clang 默认拒绝.
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `clang/lib/Driver/SanitizerArgs.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 反例 seed.

### INV-SS-I04 — SafeStack 单独不防"任意写"

- **statement**: SS 仅缩小攻击面到不含返回地址的栈段; 攻击者若有任意写仍能改 unsafe stack 上的函数指针. CPI 论文明确 SS 是 CPI 的 *弱化版*.
- **compiler**: LLVM/Clang
- **source_kind**: paper
- **source_url_or_path**: Kuznetsov et al. "Code-Pointer Integrity" (OSDI 2014)
- **version_sensitivity**: stable
- **oracle_mapping**: 文档说明; oracle 不超出该界.

## 8. 验证与已知回归

### INV-SS-VER-LIBC-COMPAT — SafeStack runtime 与 musl 不兼容

- **statement**: `compiler-rt` SafeStack runtime 主要针对 glibc, 在 musl 上存在 pthread hook 不全的回归. 部分发行版 (Alpine) 不可直接用 SS.
- **compiler + runtime**: LLVM/Clang + compiler-rt
- **source_kind**: source
- **source_url_or_path**: LLVM bug tracker
- **version_sensitivity**: target-specific
- **oracle_mapping**: musl 路径 oracle 不可用.

### INV-SS-VER-D6916 — 早期 ifunc 与 SS 冲突

- **statement**: D6916 之类早期补丁修复了 SS 与 ifunc resolver 之间的初始化顺序问题. 老 Clang (3.8 之前) 可能在 init constructor 顺序错误时 crash.
- **compiler**: LLVM/Clang
- **version**: 修复于 Clang 3.8
- **source_kind**: mailing-list
- **source_url_or_path**: https://reviews.llvm.org/D6916
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 Clang seed.

## 9. DeFuzz SafeStack Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-SS-F02 | 缓冲区溢出仍走 unsafe stack, 不影响 RIP 控制 | 缓冲区溢出 + 测量 RIP |
| INV-SS-R02 | `SIGSEGV` (unsafe stack guard) | 极深递归 / alloca |
| INV-SS-R05 | unsafe stack 状态混乱 | 协程切换 |
| INV-SS-I03 | 编译失败 | SS + ASan 同启 |

## 10. 开放问题

- **musl 与 bionic 路径**: SS runtime 在非 glibc libc 上的完整性, 待补 invariant.
- **Rust Clang SS**: Rust 编译器后端 LLVM 共享 SS, 但 Rust 抽象与 SS 启发式互动未文档化.
- **SS + LTO**: LTO 启用下 SS 启发式是否更精确? 待跟踪.
- **debugger 显示 unsafe stack**: gdb / lldb 是否能正确显示两段栈, 待 audit.
- **SS 与 PAC ret-signing**: 二者作用域有重叠 (返回地址), 同启的相容性细节待补.

## 11. 使用建议

- 仅在不支持 SHSTK / GCS / KCFI 的旧平台或为了诊断目的启用 SS.
- 必配 `-fno-omit-frame-pointer` 维护可调试性.
- 不与 ASan 同启.
- musl / bionic 上谨慎使用; oracle 跑前确认 runtime 已注册.
- `likely-to-drift` invariant (INV-SS-F01 启发式) 在每次 Clang major 升级 audit.
