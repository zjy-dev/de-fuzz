# `-ftrivial-auto-var-init` Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / LLVM/Clang / Linux kernel 中与 **`-ftrivial-auto-var-init`** 直接相关的 invariants 抽取归类, 作为 DeFuzz AVI oracle 的形式化依据.
>
> 机制简写与 survey: **AVI** = `-ftrivial-auto-var-init={zero,pattern,uninitialized}`. 让自动变量在声明时被初始化, 减少未初始化读取漏洞表面.

## 0. 术语与坐标

- **trivial auto var**: 未显式初始化的局部 (含数组 / struct / VLA), 类型 trivially-default-initializable (即 C struct, C++ POD).
- **`zero`**: 全 0 初始化. 安全 + 有时性能更好 (可被合并). 是 Linux 内核 + 主流发行版选择.
- **`pattern`**: 用一个固定 pattern 字节填充, 每架构不同 (x86 通常 `0xAA`, AArch64 类似). 用于调试 / 触发"初始化未起作用"的回归.
- **`uninitialized`**: 默认行为, 不初始化 (与不带 flag 等价).
- **discardable**: 编译器若证明该变量在写之前不被读, 可移除 init store. 这是 `zero` 与 `pattern` 大部分时间无开销的根源.

每条 invariant 字段同前.

## 1. 启用条件

### INV-AVI-E01 — `-ftrivial-auto-var-init={zero,pattern,uninitialized}`

- **statement**: GCC 12+, Clang 8+ 接受三档. `=zero` 全 0; `=pattern` 用平台特定 pattern; `=uninitialized` 等同未启用. 关键变体 `-fzero-init-padding-bits` (Clang) 控制 struct padding 是否也清零.
- **compiler**: GCC 12+, LLVM/Clang 8+
- **target**: 通用
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html ; https://clang.llvm.org/docs/ClangCommandLineReference.html ; `gcc/gimplify.cc` (`gimplify_init_constructor`)
- **evidence_snippet**: GCC manual: *"`-ftrivial-auto-var-init=...` controls how trivial automatic variables are initialized"*.
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 用 `=zero` 与 `=uninitialized` 对照; 期望 `=zero` 下未初始化读取得到 0.

### INV-AVI-E02 — `-fhardened` 隐式 `=zero`

- **statement**: GCC `-fhardened` 隐式 `-ftrivial-auto-var-init=zero`.
- **compiler**: GCC 14+
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-AVI-E03 — Linux kernel 默认 `=zero`

- **statement**: Linux 内核自 5.15+ 默认 `-ftrivial-auto-var-init=zero` (`CONFIG_INIT_STACK_ALL_ZERO=y`). 调试构建用 `=pattern`.
- **runtime**: Linux kernel
- **version**: Linux ≥ 5.15
- **source_kind**: source
- **source_url_or_path**: Linux `Makefile` ; `lib/Kconfig.debug`
- **version_sensitivity**: stable
- **oracle_mapping**: 内核 fuzz 默认假设 zero-init.

## 2. 字节模式

### INV-AVI-B01 — `=zero` 编译器在 prologue 把每个 trivial auto var 初始化为 0

- **statement**: 编译器在函数 prologue 对每个未显式初始化的 auto var 发 `mov $0, mem` / `memset(mem, 0, size)`. 大数组用循环 / SIMD store. 小变量可与寄存器分配合并.
- **compiler**: GCC, Clang
- **source_kind**: source
- **source_url_or_path**: `gcc/gimplify.cc` ; `clang/lib/CodeGen/CGDecl.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描 prologue 是否含初始化指令.

### INV-AVI-B02 — `=pattern` 用平台 pattern 字节

- **statement**: x86 用 `0xAA`, aarch64 / 通用用 `0xAAAAAAAA AAAAAAAA` 模式. 指针类型用 `0xAAAAAAAAAAAAAAAA` (在常见 VA 配置下是 invalid VA, 解引用 segfault). 浮点 NaN.
- **compiler**: GCC, Clang
- **source_kind**: source
- **source_url_or_path**: `clang/lib/CodeGen/CGDecl.cpp` (`getLargestFillablePattern`)
- **version_sensitivity**: target-specific
- **oracle_mapping**: 静态扫描.

## 3. 编译器优化

### INV-AVI-C01 — Discardable: 编译器可消除 init store

- **statement**: 若变量在所有路径都被显式赋值后才读, 编译器可移除 init store. 这是 AVI 性能开销低的根本. GCC / Clang 都做这优化, 不可关闭.
- **compiler**: GCC, Clang
- **source_kind**: source
- **source_url_or_path**: `gcc/tree-ssa-dse.cc` (DSE) ; `llvm/lib/Transforms/Scalar/DeadStoreElimination.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-AVI-C02 — `__attribute__((uninitialized))` 关闭单变量 init

- **statement**: Clang 提供 `__attribute__((uninitialized))` (Clang 14+) 在变量级别关闭 AVI, 用于性能极敏感场景. GCC 等价是 `__attribute__((no_init))`(尚未上游).
- **compiler**: Clang (主)
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html
- **version_sensitivity**: stable
- **oracle_mapping**: 变量级 seed.

## 4. ELF 元数据

### INV-AVI-M01 — 无 ELF property

- **statement**: AVI 是纯 codegen, 无 ELF 标识. 链接器不感知.
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描 prologue 验证.

## 5. 运行时

### INV-AVI-R01 — `=zero` 把"未初始化读取"语义稳定为 0

- **statement**: 在 `=zero` 下, 任何未初始化读得 0; 这本身不报错 (UBSan 等 sanitizer 仍可识别 *未显式初始化*). 安全保证: 攻击者不能利用栈泄漏.
- **runtime**: 不适用 (codegen)
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 反例: 在 `=uninitialized` 下未初始化读返回栈残留, `=zero` 下返回 0.

### INV-AVI-R02 — `=pattern` 触发更多 fault

- **statement**: 在 `=pattern` 下, 把未初始化指针解引用立即 segfault (因 0xAA... 是非法地址). 用于 *早期发现* 未初始化使用. 内核 / debug build 优先 pattern.
- **runtime**: kernel
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 反例: pattern 模式下未初始化使用会 SIGSEGV.

## 6. 与其他机制交互

### INV-AVI-I01 — AVI 与 MSan 重叠但目的不同

- **statement**: MSan 检测 *任何* 未初始化读取并报错; AVI 直接 *消除* 未初始化语义 (zero/pattern 替换). MSan 在 AVI=zero 启用下失去价值 (因变量已初始化, 无未定义).
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/sanitizers.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵: MSan 配 `=uninitialized`, AVI 配 `=zero` 二选一.

### INV-AVI-I02 — AVI 与 stack canary / SCS 正交

- **statement**: AVI 不影响保留寄存器 / canary / SCS 路径.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-AVI-I03 — AVI 与 `-fzero-init-padding-bits` (Clang)

- **statement**: 默认 `=zero` 不一定填充 struct 内 padding; Clang `-fzero-init-padding-bits=...` 控制是否填. 在 `=pattern` 下 padding 也用 pattern 填.
- **compiler**: Clang
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: padding 信息泄漏 seed.

## 7. 验证与已知回归

### INV-AVI-VER-CLANG-INTRO — Clang 8 引入

- **statement**: Clang 8 引入 AVI, 早期版本仅 `=pattern`, 后加 `=zero` 在 9+. GCC 滞后到 12.
- **compiler**: Clang
- **version**: Clang 8+
- **source_kind**: source
- **source_url_or_path**: LLVM commit log
- **version_sensitivity**: stable
- **oracle_mapping**: 老编译器反例.

### INV-AVI-VER-PR105112 — GCC AVI 边角

- **statement**: GCC PR105112 系: AVI 在含 union 的 struct 上初始化逻辑边角, 修复在 GCC 13.
- **compiler**: GCC
- **version**: 修复于 GCC 13
- **source_kind**: mailing-list
- **source_url_or_path**: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=105112
- **version_sensitivity**: stable since fix
- **oracle_mapping**: 老 GCC seed.

## 8. DeFuzz Oracle 映射总表

| Invariant | Oracle 信号 | seed |
|---|---|---|
| INV-AVI-R01 | `=zero` 下未初始化读 = 0 | 局部 var 不赋值, 直接读 |
| INV-AVI-R02 | `=pattern` 下未初始化指针解引用 SIGSEGV | 同上 + 解引用 |
| INV-AVI-C02 | `__attribute__((uninitialized))` 让 var 回到原始 | 变量属性 |
| INV-AVI-I01 | MSan + `=zero` 不报告 | 反例 |

## 9. 开放问题

- **C++ 类型边角**: AVI 对 trivially-constructible 但非 POD 的 C++ 类如何处理? Clang docs 有特殊规则待补.
- **GCC `__attribute__((no_init))`**: GCC 主线尚未提供变量级关停, 待跟踪.
- **AVI + LTO**: LTO 启用下 init store 是否更易被消除? 量化数据待补.
- **`-fzero-init-padding-bits` GCC 等价**: GCC 是否支持 padding 控制?
- **debugger / valgrind**: AVI=zero 下 valgrind 不报告未初始化 (因已初始化), 这是 AVI 的副作用.

## 10. 使用建议

- 默认 `-ftrivial-auto-var-init=zero` 安全开销低.
- 调试构建用 `=pattern` 提高早期发现概率.
- 性能极敏感函数加 `__attribute__((uninitialized))`.
- DeFuzz oracle 主路径用 `=zero`; 单独跑 `=uninitialized` 配 MSan 互补.
