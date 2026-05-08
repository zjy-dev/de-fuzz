# `-fbounds-safety` (Bounds Safety) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 LLVM/Clang / Apple "Bounds Safety" RFC / Checked C / N2778 中与 **`-fbounds-safety`** 直接相关的 invariants 抽取归类.
>
> 机制简写与 survey: **BS** = `-fbounds-safety`. Apple 主推, LLVM/Clang 实验性, 是 C 语言级别的 bounds-checked 编程模型 (类似 Checked C 但更工程化).

## 0. 术语与坐标

- **指针种类 (pointer kind)**: BS 把 C 指针分多类, 描述 *访问能力*:
  - `__single`: 普通 1-元素指针, 只能 `*p` 与 `&p[0]`. 默认 ABI 可见.
  - `__bidi_indexable`: 带上下界, 可 `p[i]` 任意正负 i. 默认局部可见 (非 ABI).
  - `__indexable`: 单方向带上界, 可 `p[i]` 正向 i.
  - `__terminated_by(T)`: 以 sentinel value T 结尾 (如 `\0`-terminated string).
  - `__counted_by(N)`: 关联 struct 字段 N 表示元素数.
  - `__sized_by(N)`: 关联字段 N 表示字节大小.
- **paired assignment**: 关联 count/size 字段必须与指针 *同一表达式* 赋值, 中间不可有副作用.
- **runtime trap**: 越界访问触发 `__bounds_safety_trap` 或 `ud2`, 类似 ubsan trap.
- **diagnostic mode**: 编译期可分 `warn` / `error` / `runtime-trap`.

每条 invariant 字段同前.

## 1. 启用条件

### INV-BS-E01 — `-fbounds-safety` 启用

- **statement**: Clang `-fbounds-safety` (实验性) 启用 bounds-checked 编程模型. 必须配合 `__has_feature(bounds_safety)` 与对应头文件中的限定符宏 (`__counted_by` 等).
- **compiler**: LLVM/Clang
- **version**: Clang 18+ (RFC merge), Apple toolchain 完整支持
- **target**: 通用
- **source_kind**: user-doc + RFC
- **source_url_or_path**: https://clang.llvm.org/docs/BoundsSafety.html ; https://discourse.llvm.org/t/rfc-enforcing-bounds-safety-in-c-fbounds-safety/70854
- **version_sensitivity**: likely-to-drift (实验)
- **oracle_mapping**: oracle 用 `-fbounds-safety` vs 不启用对照, 越界访问期望 trap.

### INV-BS-E02 — GCC 不实现

- **statement**: GCC 暂无 bounds-safety. 可能未来跟进 ISO C 标准 N2778 类似机制.
- **compiler**: GCC (无)
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-BS-E03 — `__has_feature(bounds_safety)` 探测

- **statement**: 源码用 `__has_feature(bounds_safety)` 检测; 在不支持的编译器上限定符宏退化为空, 不破坏语义.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/LanguageExtensions.html
- **version_sensitivity**: stable
- **oracle_mapping**: 兼容性 seed.

## 2. 类型系统

### INV-BS-T01 — ABI-visible 指针默认 `__single`

- **statement**: 函数参数 / struct 字段 / 全局变量上的指针默认 `__single` (单元素), 不携带 bounds. 这保持 ABI 不变. 用户显式标 `__counted_by(N)` / `__sized_by(N)` 才把 bounds 内联到接口.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/BoundsSafety.html
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: ABI 兼容 seed.

### INV-BS-T02 — 局部变量默认 `__bidi_indexable`

- **statement**: 函数局部指针默认 `__bidi_indexable` (带上下界). 不属于 ABI, 可在不破坏调用者下检查越界. 这是 BS 在大量已有代码上 *零改动* 检测越界的关键.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 局部数组越界 seed 期望 trap.

### INV-BS-T03 — `__counted_by(N)` paired assignment

- **statement**: struct 字段 `T *ptr __counted_by(N)` 关联 `N` 字段表元素数. 写指针与写 N 必须 *paired* (同表达式 / 同 statement), 中间不可有副作用. Sema 阶段强制.
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `clang/lib/Sema/SemaBoundsSafety.cpp`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: paired assignment 错位 seed -> 编译失败.

### INV-BS-T04 — `__sized_by(N)` 上界依赖运行时

- **statement**: `__sized_by(N)` 与 `__counted_by` 类似但 N 是字节大小. 上界推导需运行时计算 `ptr + size`, 且必须保证不溢出. trap 在 `ptr + size > UINTPTR_MAX` 时触发.
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: 同上
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 大 size 溢出 seed.

### INV-BS-T05 — `__terminated_by(T)` sentinel

- **statement**: `T *ptr __terminated_by(T(0))` 表示 `\0`-terminated 字符串等价. 编译器把 `strlen` 风格的扫描内化, 越界检测由 sentinel 提供.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/BoundsSafety.html
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 字符串 seed.

## 3. 编译期约束

### INV-BS-C01 — 不允许"宽指针逃逸到 ABI"

- **statement**: `__bidi_indexable` 等带 bounds 的指针不可作为 ABI 函数参数 / 全局; 编译器报错. 这保持 ABI 兼容性.
- **compiler**: LLVM/Clang
- **source_kind**: source + user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/BoundsSafety.html
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 反例 seed.

### INV-BS-C02 — Cast 规则

- **statement**: 多种指针类型间显式 cast 必须经 `__unsafe_forge_*` 或 `__terminated_by_to_indexable` 等显式 builtin. 隐式 cast 仅在安全方向 (例如 `__bidi_indexable -> __single`).
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `clang/lib/Sema/SemaCast.cpp` (BS 路径)
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 反例 cast seed.

### INV-BS-C03 — 部分 trap 在 IR/CFG 优化中可被消除

- **statement**: 编译器对可证明安全的访问消除 trap (类似 ubsan recoverable). 仅 *可能不安全* 的访问保留 trap.
- **compiler**: LLVM/Clang
- **source_kind**: source
- **source_url_or_path**: `llvm/lib/Transforms/Utils/BoundsChecking.cpp`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 文档.

## 4. 属性

### INV-BS-A01 — 限定符宏在头文件展开

- **statement**: `__counted_by`, `__sized_by`, `__terminated_by` 是宏, 来自 `<bounds_safety.h>` 或 Apple SDK. 在不支持的编译器上展开为空, 旧代码可保持兼容.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/BoundsSafety.html
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 文档.

### INV-BS-A02 — `__unsafe_forge_*` builtin

- **statement**: `__unsafe_forge_bidi_indexable`, `__unsafe_forge_single` 等让用户显式构造非默认指针种类, 标记"我手动检查过安全". 默认 trap 不插入.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 显式不安全 seed.

## 5. 运行时

### INV-BS-R01 — 默认 trap 路径 -> SIGILL

- **statement**: 越界 / counted_by 不一致 / sized_by 溢出 等运行时检测命中时, 默认 `__builtin_trap()` -> `SIGILL` (`si_code = ILL_ILLOPC`). 可定制为 ubsan handler 模式.
- **runtime**: 不依赖外部
- **source_kind**: 设计
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: oracle 看 SIGILL.

### INV-BS-R02 — `-fbounds-safety-trap=...` 控制 trap 模式

- **statement**: 类比 ubsan, 选 `trap` / `handler` / `recover`. 默认 `trap`.
- **compiler**: LLVM/Clang
- **source_kind**: user-doc
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 矩阵.

## 6. 与其他机制交互

### INV-BS-I01 — BS 与 UBSan / ASan 部分重叠

- **statement**: BS 在编译期 + runtime 静态检查; ASan / UBSan 在 runtime 检查. BS 启用下 OOB 访问被静态拦截或 runtime trap, 减少 ASan 触发面. 通常 BS 在 release, ASan 在 debug.
- **compiler**: Clang
- **source_kind**: 设计
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 矩阵.

### INV-BS-I02 — BS 与 `_FORTIFY_SOURCE` 互补

- **statement**: FORT 在 libc 函数级查; BS 是 *任意 C 表达式* 检查. 同启不冲突, 提供深度防御.
- **compiler**: Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/fortify-source.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-BS-I03 — BS 与 LTO

- **statement**: BS 检测多发生在前端 / 早期 IR; LTO 后期优化可消除冗余 trap. 同启不冲突.
- **compiler**: Clang
- **source_kind**: 设计
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 矩阵.

### INV-BS-I04 — BS vs Checked C

- **statement**: Checked C 是早期类似设计, 但用户体验复杂; BS 简化为限定符 + paired assignment, 更工程化.
- **compiler**: Clang
- **source_kind**: paper + RFC
- **source_url_or_path**: Necula et al. Deputy / SafeC ; https://open-std.org/jtc1/sc22/wg14/www/docs/n2778.pdf
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

## 7. 验证与已知回归

### INV-BS-VER-RFC-EVOLVE — RFC 阶段 ABI 演化

- **statement**: BS 截至 2024 末仍 RFC + 实验阶段, 限定符宏命名 / paired assignment 规则在多版补丁内调整. DeFuzz 跑 oracle 必须记录精确 Clang commit / Apple SDK 版本.
- **compiler**: LLVM/Clang
- **version**: 持续演化
- **source_kind**: RFC
- **source_url_or_path**: LLVM Discourse "BoundsSafety"
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 严格版本控制.

## 8. DeFuzz Oracle 映射总表

| Invariant | Oracle 信号 | seed |
|---|---|---|
| INV-BS-T02 | 局部数组越界 trap | 局部 buffer + 越界 |
| INV-BS-T03 | paired assignment 错位编译失败 | counted_by 设字段错 |
| INV-BS-T04 | sized_by 溢出 trap | 大 size 值 |
| INV-BS-A02 | `__unsafe_forge_*` 不报 | 显式不安全 |
| INV-BS-R01 | SIGILL | 越界 |

## 9. 开放问题

- **GCC 路线图**: GCC 是否实现, 需跟踪 wg14 N2778 提案.
- **C++ 语义**: BS 主要针对 C; 在 C++ 中 std::span 等已有抽象, BS 是否扩展, 待补.
- **musl / glibc 头文件**: bionic / Apple SDK 已大规模标记 `__counted_by`; glibc / musl 进度待跟踪.
- **kernel BS**: Linux 内核是否引入 BS 扫描? 6.x 主线尚无.
- **效益量化**: 与 C++ + std::span 比较, BS 的实际安全收益.

## 10. 使用建议

- 仅 Clang 18+ 可用, 仍实验性.
- 优先在 *新* 项目使用; 老项目用兼容宏渐进迁移.
- crypto / parser 等高风险代码加 `__counted_by`.
- DeFuzz oracle 把 BS 作为 *额外* 检查层, 不替代 ASan / UBSan.
- `likely-to-drift` invariant 极多, 主分支 Clang 升级 audit.
