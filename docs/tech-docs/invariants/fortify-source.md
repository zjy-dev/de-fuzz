# `_FORTIFY_SOURCE` / Object Size Checking Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC / LLVM/Clang / glibc / ABI 中与 **`_FORTIFY_SOURCE` (FORT)** 直接相关的 invariants 统一抽取、归类, 作为 DeFuzz fortify oracle (`@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md`) 的形式化依据.
>
> 机制简写与 survey 一致: **FORT** = `_FORTIFY_SOURCE` / Object Size Checking. 涉及的编译器 builtin: `__builtin_object_size` (BOS) 与 `__builtin_dynamic_object_size` (BDOS).

## 0. 术语与坐标

- **level**: `-D_FORTIFY_SOURCE=N`, `N ∈ {0, 1, 2, 3}`. level=0 等于关闭; level=1/2 用 `__builtin_object_size`; level=3 (GCC 12+/Clang 9+) 用 `__builtin_dynamic_object_size`.
- **object size**: `__builtin_object_size(ptr, type)`, `type ∈ {0,1,2,3}`. bit0=0 取最大对象 (包住整个包含结构), bit0=1 取最小对象 (sub-object, 仅字段自身); bit1 控制"未知时返回 -1 vs 0".
- **`_chk` 变体**: libc 针对每个被 fortify 的函数提供的 `__<name>_chk` 实体, 接受一个额外的 `dstlen`/`objsize` 参数; 由头文件的 `static __inline__` wrapper 转发.
- **wrapper**: glibc `bits/string_fortified.h` / `bits/stdio2.h` 等中的 `__fortify_function` 定义, 在调用点把原函数重写成 `__<name>_chk(...)` 或 `__<name>_chk_warn(...)`.
- **BOS/BDOS**: 上述两个 builtin 的简写. BOS 只在编译期可证的场景返回有效值; BDOS 可下降为运行时计算 (可与 `__counted_by` 配合).
- **退化 (fall back)**: 当 BOS 返回 `(size_t)-1` (type 0/1) 或 `0` (type 2/3) 时, wrapper 改为调用原始 libc 函数, fortify 运行时检查被消除.
- **`__chk_fail`**: glibc 发现 `dstlen < len` 时调用的 `noreturn` 失败处理函数, 打印 "buffer overflow detected" 后 `abort()`, 进程退出码 134.

每条 invariant 采用 survey 推荐字段: `ID / statement / compiler / version / target / source_kind / source_url_or_path / evidence_snippet / version_sensitivity / oracle_mapping`.

## 1. 静态前提 (Static Preconditions)

### INV-FORT-E01 — 需要预处理宏 + 优化等级

- **statement**: `_FORTIFY_SOURCE` 在用户侧由宏 `-D_FORTIFY_SOURCE=N` 启用, 实际生效需要满足: (a) `N ≥ 1`; (b) 优化等级 `≥ -O1` (GCC 手册明确说 `-O0` 下 wrapper 不发挥作用, glibc 头文件的 `__fortify_function` 内联路径对未优化代码基本无效); (c) 编译器实现了 `__builtin_object_size` 且 libc 提供 `__*_chk` 实体 (或链接 `libssp`).
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 4.0+ (level 1/2), GCC 12+ (level 3); Clang 5+ (level 1/2), Clang 9+ (level 3)
- **target**: generic (需 glibc 或 musl/bionic/newlib 提供 `__*_chk`)
- **source_kind**: user-doc + runtime
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Object-Size-Checking.html ; https://sourceware.org/glibc/manual/latest/html_node/Source-Fortification.html
- **evidence_snippet**: glibc manual: "Use of `_FORTIFY_SOURCE` requires that the program is compiled with `gcc 4.1` or later, or another compiler that implements the `__builtin_object_size` function and that the program is compiled with optimization (`-O1` or higher)."
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz fortify oracle 编译命令强制 `-O2 -D_FORTIFY_SOURCE=2 -fno-stack-protector` (`@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md:260-266`); 低于 `-O1` 的配置属 "预期无效", 不报 bug.

### INV-FORT-E02 — `-fhardened` 隐式启用 level 3

- **statement**: GCC `-fhardened` (Linux 用户空间默认 profile) 隐式开启 `-D_FORTIFY_SOURCE=3`, 同时叠加 `-fstack-protector-strong -fstack-clash-protection -fcf-protection=full -ftrivial-auto-var-init=zero -fPIE -pie -Wl,-z,relro,-z,now`.
- **compiler**: GCC
- **version**: GCC 14+
- **target**: generic (Linux)
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: 在 CFLAGS 变种里 `-fhardened` 作为一个配置; fortify oracle 期望其与显式 `-D_FORTIFY_SOURCE=3 -O2` 行为等价.

### INV-FORT-E03 — 级别向下覆盖规则

- **statement**: 同一 TU 内多次定义 `_FORTIFY_SOURCE` 时, 以最后一次 `#define` 为准; `-U_FORTIFY_SOURCE` 强制关闭; glibc 的 `features.h` 在 `_FORTIFY_SOURCE` 未显式定义或 optimization 等级 `< 1` 时会 `#undef _FORTIFY_SOURCE` 并发出 `#warning` (取决于版本).
- **compiler + runtime**: GCC/Clang + glibc
- **version**: glibc 2.3.4+
- **target**: generic
- **source_kind**: runtime
- **source_url_or_path**: glibc `include/features.h` (`_FORTIFY_SOURCE` 处理块)
- **version_sensitivity**: stable (warning 文案会漂移)
- **oracle_mapping**: 构建时捕获 `#warning _FORTIFY_SOURCE requires compiling with optimization (-O)` 作为 "fortify 实际未启用" 的早期信号.

### INV-FORT-E04 — 第三方 libc 必须提供 `__*_chk`

- **statement**: 若 target libc 不提供 `__strcpy_chk` / `__memcpy_chk` / `__snprintf_chk` / ... 等实体, 链接器必须将 GCC 的 `libssp.a` / `libssp_nonshared.a` 链入, 否则 `-D_FORTIFY_SOURCE ≥ 1` 的对象文件出现未定义引用. glibc ≥ 2.3.4, musl ≥ 1.2.5, bionic 均内建 `__*_chk`; newlib / 老版本 musl / 裸 libc 需要 `libssp`.
- **compiler + runtime**: GCC + libssp / libc
- **version**: all
- **target**: generic (主要影响嵌入式 / 非 glibc 场景)
- **source_kind**: runtime
- **source_url_or_path**: https://github.com/gcc-mirror/gcc/tree/master/libssp
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 在 musl/newlib target 上跑 fortify oracle 前需校验 `__stack_chk_fail` / `__memcpy_chk` 符号可解析, 否则属配置级 bug 而非 fortify 绕过.

## 2. 级别语义 (Level Semantics)

### INV-FORT-L01 — level 1 仅覆盖"编译期可证"越界

- **statement**: `-D_FORTIFY_SOURCE=1` 仅阻止"理论上可由编译期常量判定越界"的调用 (例如 `char b[4]; strcpy(b, "12345");`). 该级别下 `__bos(dst, 0)` (maximum object size) 为唯一来源, 不覆盖 sub-object.
- **compiler + runtime**: GCC/Clang + glibc
- **version**: glibc 2.3.4+
- **target**: generic
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Object-Size-Checking.html ; https://sourceware.org/glibc/manual/latest/html_node/Source-Fortification.html
- **evidence_snippet**: GCC manual: "object size checking at levels of checking larger than 1".
- **version_sensitivity**: stable
- **oracle_mapping**: 若 DeFuzz 用 level=1 跑 sub-object 模板 (INV-FORT-B02), 期望不拦截; oracle 不应将此结果判定为 bug.

### INV-FORT-L02 — level 2 覆盖 sub-object

- **statement**: `-D_FORTIFY_SOURCE=2` 额外使用 `__bos(dst, 1)` (minimum / sub-object size), 可在 `struct { char a[4]; char b[4]; }` 中将 `strcpy(s.a, ...)` 的边界限制为 4 字节, 而不是整个 struct 的 8 字节. **代价**: 部分合法的 C 代码 (如 flexible array / "struct hack") 可能被错误拦截或报警.
- **compiler + runtime**: GCC/Clang + glibc
- **version**: glibc 2.3.4+, GCC 4.0+, Clang 5+
- **target**: generic
- **source_kind**: user-doc
- **source_url_or_path**: 同上
- **evidence_snippet**: glibc manual: "`_FORTIFY_SOURCE` set to 2 ... adds the following additional security checks. ... array bounds in the presence of sub-objects".
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz fortify oracle 模板 2 (`struct wrapper { char buf[64]; int x; }`) 在 level=2 下期望触发 `__chk_fail`; 在 level=1 下期望退化.

### INV-FORT-L03 — level 3 允许运行时已知对象大小

- **statement**: `-D_FORTIFY_SOURCE=3` (GCC 12+, Clang 9+) 使用 `__builtin_dynamic_object_size`, 可在对象大小只在运行时可知 (例如 `malloc(n)` 返回值, `__counted_by` 关联的柔性数组, `sizeof(*p) * n` 表达式) 时也生成有效的 fortify check. 此级别要求 libc 的 `__*_chk` 变体能接受运行时大小参数 (glibc 2.34+).
- **compiler + runtime**: GCC 12+ / Clang 9+ + glibc 2.34+
- **target**: generic
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Object-Size-Checking.html ; https://clang.llvm.org/docs/LanguageExtensions.html ; `gcc/tree-object-size.cc`
- **evidence_snippet**: GCC manual: "`__builtin_dynamic_object_size` ... may return a non-constant value".
- **version_sensitivity**: stable (新引入, 但接口已冻结)
- **oracle_mapping**: DeFuzz level=3 的 seed 应包含 `malloc(n) + memcpy(p, src, fill)` 模式; level=2 下期望退化, level=3 下期望拦截.

### INV-FORT-L04 — 各级别包含关系

- **statement**: 对任意合法 fortify 可拦截的违例集合 `S_N`, 有 `S_0 = ∅ ⊂ S_1 ⊆ S_2 ⊆ S_3`. 即提升级别不会让之前能拦截的 case 退化为不拦截 (实现保证); 反之若在高级别不拦截, 在低级别也一定不拦截.
- **compiler + runtime**: GCC + glibc, Clang + glibc
- **version**: all
- **target**: generic
- **source_kind**: user-doc + test
- **source_url_or_path**: glibc `debug/tst-chk*.c` (覆盖矩阵)
- **version_sensitivity**: stable (semver 契约)
- **oracle_mapping**: DeFuzz 可以在同一 seed 上跨级别跑 diff, 出现 "level=3 不拦截但 level=2 拦截" 立即视为回归 bug.

## 3. `__builtin_object_size` 与 `__builtin_dynamic_object_size` (BOS / BDOS)

### INV-FORT-B01 — `type` 参数的四象限语义

- **statement**: `__builtin_object_size(ptr, type)` 的 `type` 为 2-bit 掩码. bit0 选择 max (0) / min (1) object size; bit1 选择未知时返回 `(size_t)-1` (0) / `0` (1). 即: type=0 = "保守取最大, 未知返回 -1"; type=1 = "子对象, 未知返回 -1"; type=2 = "保守取最大, 未知返回 0"; type=3 = "子对象, 未知返回 0".
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 4.0+, Clang 3.1+
- **target**: generic
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Object-Size-Checking.html ; `gcc/tree-object-size.cc` (`BOS_OBJECT_SIZE_TYPE`) ; https://clang.llvm.org/docs/LanguageExtensions.html
- **evidence_snippet**: GCC manual: "If `type` is 0 or 1, the `__builtin_object_size` returns the number of bytes ... If `type` is 2 or 3, it returns 0 when it cannot determine ..."
- **version_sensitivity**: stable (外部 ABI 级别)
- **oracle_mapping**: DeFuzz 可直接用 `__builtin_object_size(p, 0)` 在种子代码里断言某指针的静态已知大小, 作为"fortify 应当拦截"的前置条件.

### INV-FORT-B02 — BOS 对 sub-object 的收紧由 type bit0 控制

- **statement**: 对 `struct S { char a[4]; char b[4]; }; struct S s;`, `__builtin_object_size(s.a, 0) == 8` (整个 struct), `__builtin_object_size(s.a, 1) == 4` (仅字段 `a`). Fortify level 2 使用 `type=1/3`, 因此在 sub-object 上比 level 1 更严格.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 4.0+, Clang 3.1+
- **target**: generic
- **source_kind**: user-doc + source + test
- **source_url_or_path**: GCC Object Size Checking 手册; glibc `debug/tst-chk1.c` 的 struct 用例
- **version_sensitivity**: stable
- **oracle_mapping**: fortify oracle 模板 2 (`struct wrapper { char buf[64]; int x; }`) 在 level=2 下的期望行为由此条决定.

### INV-FORT-B03 — BOS 在不可证场景退化为 -1 或 0

- **statement**: 下列任一情况会使 BOS 返回"未知": (a) 指针来自外部函数返回值且无 `alloc_size` 属性; (b) 指针经过复杂别名 (union / 强制类型转换 / `memcpy` 来源); (c) 变长数组 (VLA); (d) `alloca()` / `__builtin_alloca()` 返回的指针; (e) `malloc/calloc/realloc` 返回值 (在 level ≤ 2 下, 因为 BOS 不看 `alloc_size`; level 3 的 BDOS 可下降); (f) 指针是多个可能来源的 φ 节点而无法确定最小值.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: generic
- **source_kind**: source + user-doc
- **source_url_or_path**: `gcc/tree-object-size.cc` (`compute_builtin_object_size`, `addr_object_size`); Clang `llvm/lib/Analysis/MemoryBuiltins.cpp` (`ObjectSizeOffsetVisitor`)
- **evidence_snippet**: GCC 手册: "If the expression uses multiple variables or is too complex, `__builtin_object_size` will return `(size_t)-1`."
- **version_sensitivity**: likely-to-drift (别名分析与 inliner 强度随版本变动)
- **oracle_mapping**: DeFuzz fortify oracle 模板 3 (alloca/VLA) 本质上就是此条的反例, **不应**将其 "fortify 未拦截" 判定为编译器 bug, 而应报告为"设计限制" (`@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md:247-255`).

### INV-FORT-B04 — BDOS 下降为运行时表达式

- **statement**: `__builtin_dynamic_object_size(ptr, type)` 在 BOS 无法静态求解时会生成运行时计算, 例如读 `malloc` 的 size 参数 (经 `alloc_size` 属性或 IR metadata), 或从 `__counted_by` 关联字段读取长度. 生成的表达式必须是 side-effect free 且 `ptr` 的定义 dominator 可达.
- **compiler**: GCC 12+, LLVM/Clang 9+
- **target**: generic
- **source_kind**: source + user-doc
- **source_url_or_path**: `gcc/tree-object-size.cc` (dynamic codepath); `llvm/lib/Analysis/MemoryBuiltins.cpp` (`ObjectSizeOffsetEvaluator`)
- **version_sensitivity**: likely-to-drift (能成功下降的 pattern 随优化 pass 扩展)
- **oracle_mapping**: level=3 的 seed 中 `malloc(n)` 路径需依赖此条; 若 BDOS 不下降, level=3 退化为 level ≤ 2 的效果.

### INV-FORT-B05 — `alloc_size` 属性是 BOS/BDOS 可见性的入口

- **statement**: 用户自定义分配器若标注 `__attribute__((alloc_size(n)))` 或 `alloc_size(n, m)`, 其返回值可被 BOS (level 3 的 BDOS) 识别为 "size = 参数 n" 或 "size = n * m"; 否则外部函数返回值一律按未知处理.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 4.3+, Clang 4+
- **target**: generic
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 若构造"自定义 allocator + fortify"的 seed, 必须显式加 `alloc_size`, 否则 fortify 退化为无保护.

### INV-FORT-B06 — `__counted_by` / `pass_object_size` 为 BDOS 提供字段绑定

- **statement**: Clang 的 `__counted_by(field)` / `__sized_by(field)` 属性 (以及 `pass_object_size(n)` 函数参数修饰符) 把指针与同级字段/参数绑定, 使 BDOS 能在结构体成员上返回精确大小. 生效要求: paired assignment (指针与 count 同语句更新, 中间无 side-effect); ABI-visible 指针必须声明为 `__single` 或等价.
- **compiler**: Clang
- **version**: Clang 17+ (`__counted_by` 稳定), Clang 19+ (`-fbounds-safety`)
- **target**: generic
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html ; https://clang.llvm.org/docs/BoundsSafety.html ; `clang/lib/Sema/SemaBoundsSafety*.cpp`
- **version_sensitivity**: likely-to-drift (语言扩展仍在演进)
- **oracle_mapping**: level=3 的柔性数组 + `__counted_by` seed 依赖此条; GCC 侧的对应支持 (`gcc/c-family/c-attribs.cc` 的 `counted_by`) 在 GCC 15+ 引入, 语义相近但实现独立.

## 4. 被 fortify 覆盖的函数集 (Covered Functions)

### INV-FORT-C01 — 只有 libc 头文件 wrapper 的函数被覆盖

- **statement**: fortify 不对任意用户函数生效, 仅对 glibc / musl / bionic 提供 `__fortify_function` wrapper 的特定 libc 函数有效. 典型列表 (glibc): string (`memcpy/memmove/mempcpy/memset/strcpy/stpcpy/strncpy/stpncpy/strcat/strncat`), stdio (`sprintf/snprintf/vsprintf/vsnprintf/printf/fprintf/gets/fgets/fread/fread_unlocked`), unistd (`read/pread/readlink/getcwd/confstr`), wchar (`wmemcpy/wcscpy/...`), socket (`recv/recvfrom`), poll/select (`poll/FD_SET`), mbs (`mbsnrtowcs/...`). 每个 libc 版本列表略有差异.
- **runtime**: glibc (reference), musl, bionic
- **version**: glibc 2.3.4+
- **target**: generic
- **source_kind**: runtime + user-doc
- **source_url_or_path**: https://sourceware.org/glibc/manual/latest/html_node/Source-Fortification.html ; glibc `string/bits/string_fortified.h`, `libio/bits/stdio2.h`, `posix/bits/unistd.h`, `wcsmbs/bits/wchar2.h`, `socket/bits/socket2.h`, `io/bits/poll2.h`
- **evidence_snippet**: glibc manual 源码注释: "The fortified versions of the string/IO/net/... functions check for buffer overflows at runtime."
- **version_sensitivity**: stable (大框架), likely-to-drift (具体函数加入/移除)
- **oracle_mapping**: DeFuzz 种子必须使用此列表中的函数 (见 `@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md:67-69`); 手写循环 / 非 libc 函数 (如 `memcpy_s` from Annex K) 不在 fortify 覆盖内, 属预期不拦截.

### INV-FORT-C02 — 手写循环与 `*_s` (Annex K) 不被 fortify 覆盖

- **statement**: 形如 `for (i=0; i<n; i++) dst[i] = src[i];` 的纯语言级拷贝, 以及 `memcpy_s` / `strcpy_s` 等 C11 Annex K 函数, **不**由 `_FORTIFY_SOURCE` 保护. 这些路径独立依赖编译器的 `BoundsChecking` / ASan / `-fbounds-safety` 等其他机制.
- **compiler + runtime**: GCC + glibc, Clang + glibc
- **version**: all
- **target**: generic
- **source_kind**: user-doc
- **source_url_or_path**: 同上; 以及 `_chk` 在 glibc 源码中仅覆盖 libc 函数这一事实
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz fortify oracle 模板明令禁止手写循环 (`@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md:98`).

### INV-FORT-C03 — fortify 不拦截读越界 (除少数例外)

- **statement**: 绝大多数 fortify wrapper 只校验写入目标 (`dst`) 的对象大小. 例外: `memcmp` / `strncmp` (某些 glibc 版本) 对两个输入都有最小长度保护; `read` / `recv` 的保护对象是目标 buffer. 读越界 (例如 `memcpy(dst, src, 2*sizeof(src))` 中的 `src` 越读) 一般不被 fortify 检测, 需靠 ASan / MSan.
- **runtime**: glibc
- **version**: all
- **target**: generic
- **source_kind**: runtime
- **source_url_or_path**: glibc `string/bits/string_fortified.h` (`__bos(dst, ...)` 而非 `__bos(src, ...)`)
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz fortify oracle 种子必须让目标 buffer 小于源 buffer, 且以写入长度作为变量 (`fill_size`).

### INV-FORT-C04 — `printf` 族的格式字符串位置检查

- **statement**: `_FORTIFY_SOURCE ≥ 2` 时, `printf/fprintf/sprintf/snprintf/syslog/dprintf/vprintf` 系列要求格式字符串参数是字符串常量或 `%n` 相关的写入目标必须在 writable 段 (glibc: `_IO_FLAGS2_FORTIFY` 强制); 违者由 `__readonly_area` 触发 `__chk_fail`. 此外 `sprintf` / `snprintf` 的目标 buffer 受 BOS 约束.
- **runtime**: glibc
- **version**: glibc 2.3.4+
- **target**: generic
- **source_kind**: runtime + user-doc
- **source_url_or_path**: glibc `stdio-common/vfprintf-internal.c` (`_IO_FLAGS2_FORTIFY` 路径), `debug/sprintf_chk.c`
- **version_sensitivity**: stable
- **oracle_mapping**: fortify oracle 扩展模板可加入 `sprintf(buf, fmt, ...)` 的变体, level=2/3 下期望拦截.

## 5. 运行时契约 (Runtime Contract)

### INV-FORT-F01 — `__chk_fail` 语义

- **statement**: `__chk_fail` (在部分 libc 中名为 `__fortify_fail`) 必须是 `noreturn`; glibc 实现通过 `__libc_message` 输出类似 "`*** buffer overflow detected ***: terminated`" 后 `abort()`, 导致进程 exit code = 128 + SIGABRT = 134.
- **runtime**: glibc 2.3.4+
- **target**: generic (Linux/POSIX)
- **source_kind**: runtime
- **source_url_or_path**: glibc `debug/chk_fail.c`, `debug/fortify_fail.c`
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz fortify oracle 以 `exit_code == 134` 作为 "fortify 成功拦截" 的正向信号 (`@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md:44-49`).

### INV-FORT-F02 — `__*_chk` 的 runtime 判定表达式

- **statement**: 所有 `__<name>_chk(..., dstlen)` 的 runtime 校验形式统一为 `if (__glibc_unlikely(dstlen < len)) __chk_fail();`, 其中 `len` 是原函数的写入长度. 当 BOS 给出 `dstlen == (size_t)-1` 时, 由于 `-1` 作为 `size_t` 是最大值, 条件恒不成立 → 退化为原函数 (这是 INV-FORT-B03 的 runtime 对应点).
- **runtime**: glibc 2.3.4+
- **target**: generic
- **source_kind**: runtime
- **source_url_or_path**: glibc `debug/memcpy_chk.c`, `debug/strcpy_chk.c`, `string/bits/string_fortified.h` 等
- **evidence_snippet**: `bits/string_fortified.h`: `__fortify_function ... { return __builtin___memcpy_chk (__dest, __src, __len, __glibc_objsize0 (__dest)); }`
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 若观察到 "level ≥ 1 下 `dstlen` 恒为 `-1`", 即使 libc 存在 `__*_chk` 也无防护; 这通常意味着 compiler-side BOS/BDOS 一直退化到 unknown.

### INV-FORT-F03 — 编译期已知越界直接 `#error`

- **statement**: 当编译期 BOS 确定 `dstlen < len` 成立, glibc wrapper 会在头文件里用 `__warnattr("...")` / `__errordecl` 使链接或编译阶段失败 (具体行为依 glibc 版本: 2.3.4–2.34 为 warning + link-time abort; 2.34+ 更倾向 `__errordecl`). 这是 `_chk` 回写为 `__*_chk_warn` 的路径.
- **runtime**: glibc
- **version**: glibc 2.3.4+
- **target**: generic
- **source_kind**: runtime
- **source_url_or_path**: glibc `misc/sys/cdefs.h` (`__warndecl`, `__errordecl`), `string/bits/string_fortified.h`
- **version_sensitivity**: stable (API), likely-to-drift (wording)
- **oracle_mapping**: DeFuzz 的 `fill_size` 必须通过 `atoi(argv)` 进入 (runtime-known), 避免触发编译期 `#error` 导致 seed 无法编译 (`@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md:61-65`).

### INV-FORT-F04 — `__*_chk` 与 fork/exec 无状态

- **statement**: fortify runtime 无进程全局状态, 每次调用独立判定; fork 子进程无额外初始化; execve 后依赖新进程的 fortify 配置重新生效. 不同于 canary (INV-SP-F04), fortify 不依赖 `AT_RANDOM`.
- **runtime**: glibc
- **version**: all
- **target**: generic
- **source_kind**: runtime
- **source_url_or_path**: glibc `debug/` 子目录 (无 ctor/dtor)
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 可并发跑 fortify oracle 而不担心跨进程干扰.

## 6. 编译器实现 (Compiler Internals)

### INV-FORT-I01 — GCC 通过 `_chk` builtin 表 + `tree-object-size` 展开

- **statement**: GCC 在 `gcc/builtins.cc` 中为每个 fortify-able 函数注册 `expand_builtin___memcpy_chk` / `expand_builtin___strcpy_chk` / ... 等 `_chk` 变体. 在 `tree-object-size` pass 中解析 `__builtin_(dynamic_)object_size`, 若可证安全则把 `_chk` 调用回写为原函数调用 (消除 runtime 检查); 若证明必然越界则发编译期诊断.
- **compiler**: GCC
- **version**: GCC 4.0+
- **target**: generic
- **source_kind**: source
- **source_url_or_path**: https://github.com/gcc-mirror/gcc/blob/master/gcc/builtins.cc ; https://github.com/gcc-mirror/gcc/blob/master/gcc/tree-object-size.cc
- **version_sensitivity**: likely-to-drift (内部实现)
- **oracle_mapping**: DeFuzz 可 `objdump -d` 查 `__memcpy_chk` / `__strcpy_chk` 外部符号是否保留, 作为 fortify 是否真正生效的静态证据.

### INV-FORT-I02 — Clang 通过 `pass_object_size` + 内置前端降级

- **statement**: Clang 前端在 `clang/lib/CodeGen/CGBuiltin.cpp` (及 `CGExpr.cpp`) 中为 `__builtin___*_chk` 生成 IR intrinsic, 与 glibc 头文件里基于 `__pass_dynamic_object_size`/`__pass_object_size` 的 wrapper 配合. BDOS 的下降由 `llvm/lib/Analysis/MemoryBuiltins.cpp` 的 `ObjectSizeOffsetEvaluator` 完成.
- **compiler**: LLVM/Clang
- **version**: Clang 5+ (level 2), Clang 9+ (level 3)
- **target**: generic
- **source_kind**: source
- **source_url_or_path**: https://github.com/llvm/llvm-project/tree/main/clang/lib/CodeGen ; https://github.com/llvm/llvm-project/blob/main/llvm/lib/Analysis/MemoryBuiltins.cpp
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 与 GCC 同类 (objdump 验证).

### INV-FORT-I03 — 前端 `pass_object_size` 不影响调用约定

- **statement**: `pass_object_size(N)` 修饰的函数参数, 在 Clang 前端会被额外生成一个隐式 `size_t` 参数由调用方填充, 但该额外参数不改变函数 symbol 的 mangling (C linkage) 也不破坏 ABI (C++ 中进入 mangling). 这是 glibc fortify wrapper 选择 `__pass_object_size` 而非生成新符号的前提.
- **compiler**: LLVM/Clang
- **version**: Clang 3.3+
- **target**: generic
- **source_kind**: user-doc + source
- **source_url_or_path**: https://clang.llvm.org/docs/AttributeReference.html#pass-object-size ; `clang/lib/CodeGen/CGCall.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: 跨 DSO / 静态链接的 seed 可依赖此条, 不需担心 fortify 引入 symbol 冲突.

### INV-FORT-I04 — BOS/`_chk` 展开在 early inlining 之后, pre-LTO 完成

- **statement**: GCC 的 `tree-object-size` pass 和 Clang 的 `LowerConstantIntrinsics` / `InstCombine` 对 `_chk` 的回写都在 LTO pre-link 的 IR 上执行, 因此 `-flto` 不会改变 "该 call site 是否被 fortify"; 但 LTO 阶段的额外内联可能使 BOS 从 `-1` 变为具体值, 进一步消除 runtime 检查.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: generic
- **source_kind**: source
- **source_url_or_path**: `gcc/passes.def` (pass 顺序), `llvm/lib/Transforms/Scalar/LowerConstantIntrinsics.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: `-flto` 作为 DeFuzz CFLAGS 矩阵中的独立配置; 预期 fortify 行为与非 LTO 版本"至少一样严格".

## 7. 退化与假阴性 (Fallbacks / False Negatives)

### INV-FORT-N01 — `alloca` / VLA 导致 BOS 返回 `-1`

- **statement**: 对 `alloca(n)` 与 VLA `char buf[n]` (其中 `n` 不是编译期常量), `__builtin_object_size(buf, 0/1)` 均返回 `(size_t)-1` (GCC) 或等价的"未知"值 (Clang); 相应的 `_chk` 调用退化为原函数. level=3 下 BDOS **可以**恢复 alloca/VLA 的大小 (GCC 12+, Clang 9+), 但前提是分配点与使用点在同一 BB / dominator 链上, 且 `n` 的计算无副作用.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 4.0+, Clang 5+ (static); GCC 12+, Clang 9+ (dynamic)
- **target**: generic
- **source_kind**: source + user-doc
- **source_url_or_path**: `gcc/tree-object-size.cc`; Clang `LanguageExtensions.html` (`__builtin_dynamic_object_size`)
- **version_sensitivity**: likely-to-drift (BDOS 能下降的 alloca/VLA 场景逐步扩大)
- **oracle_mapping**: DeFuzz fortify oracle 模板 3 (alloca) 在 level=2 下期望 bypass, level=3 下取决于具体 compiler 版本 — 属于"预期退化"区域 (`@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md:247-255`).

### INV-FORT-N02 — 复杂指针算术使 BOS 退化

- **statement**: `union` 成员间跨字段指针、`(char *)(uintptr_t)value`、跨函数的 escape pointer、`memcpy` 返回值的链式使用等均使 BOS 返回未知. LLVM 的 BDOS 在某些 reaching-definition 可证的情况下能恢复, GCC 的 BDOS 覆盖范围通常略窄.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: generic
- **source_kind**: source
- **source_url_or_path**: `gcc/tree-object-size.cc`; `llvm/lib/Analysis/MemoryBuiltins.cpp`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 构造这类"对抗"seed 可用于评估 GCC vs LLVM 的 fortify 覆盖差异, 但不应被直接判定为 bug.

### INV-FORT-N03 — 外部函数返回值的默认不可见

- **statement**: 除非外部函数标注 `alloc_size` 或等价 IR metadata, 否则其返回值的对象大小对 BOS/BDOS 不可见. 典型反例: 手写 `my_alloc(size_t n) { return malloc(n); }` 若无 `alloc_size`, fortify 对 `my_alloc()` 的返回值无保护.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: generic
- **source_kind**: user-doc + source
- **source_url_or_path**: GCC `Common-Function-Attributes` (`alloc_size`)
- **version_sensitivity**: stable
- **oracle_mapping**: 自建 allocator 的 seed 必须显式加 `alloc_size`, 否则 fortify oracle 的 "无防护" 结果无意义.

### INV-FORT-N04 — 编译器不实现 BOS 时 fortify 完全失效

- **statement**: 若 C 编译器不实现 `__builtin_object_size` / `__builtin_dynamic_object_size` (返回常量 `-1` / `0` 或完全不识别), 则 glibc 头文件的 `__glibc_objsize0 / __bos` 宏无法得到有效边界, 所有 `_chk` 调用都会退化为原函数. 对用户而言等价于 level=0.
- **compiler**: third-party C compilers
- **version**: n/a
- **target**: generic
- **source_kind**: user-doc + runtime
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Object-Size-Checking.html ; https://sourceware.org/glibc/manual/latest/html_node/Source-Fortification.html
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 对这类编译器跑 fortify oracle 时期望 level ≥ 1 下仍发生 bypass (exit 139); 不视为回归, 但可作为"该编译器无 fortify 能力"的结论性证据.

### INV-FORT-N05 — 部分 libc 显式禁用 `_FORTIFY_SOURCE`

- **statement**: 如果编译器驱动、内建预定义或 libc 头文件在 TU 预处理阶段显式 `#undef _FORTIFY_SOURCE` 或强制设为 `0`, 用户命令行的 `-D_FORTIFY_SOURCE=N` 也不会真正生效. 这属于工具链能力 / 配置问题, 不是 fortify oracle 的目标 bug.
- **compiler**: third-party C compilers
- **version**: n/a
- **target**: generic
- **source_kind**: user-doc
- **source_url_or_path**: https://sourceware.org/glibc/manual/latest/html_node/Source-Fortification.html
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz 构建前应运行 `gcc -D_FORTIFY_SOURCE=2 -E empty.c | grep _FORTIFY_SOURCE` 或同类探针验证 `_FORTIFY_SOURCE` 在 TU 末端仍然定义, 否则 oracle 结果无效.

## 8. 与其他机制的交互 (Interactions)

### INV-FORT-X01 — 与 `-fstack-protector*` 的独立性

- **statement**: fortify 与 stack canary 是正交机制: fortify 在写入**之前或过程中**拦截 (SIGABRT 134 via `__chk_fail`), canary 在函数返回**之前**拦截 (SIGABRT 134 via `__stack_chk_fail`). 两者失败路径都是 134, 因此在 oracle 层必须用 `-fno-stack-protector` 关闭 canary 才能独立判定 fortify 成效.
- **compiler + runtime**: GCC/Clang + glibc
- **version**: all
- **target**: generic
- **source_kind**: user-doc + bug-disclosure
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md:53-56`
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz fortify oracle 编译命令固定 `-fno-stack-protector` (`@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md:260-266`).

### INV-FORT-X02 — 与 ASan / HWASan 的叠加

- **statement**: `-fsanitize=address` 或 `-fsanitize=hwaddress` 下, 越界访问先被 sanitizer 拦截 (exit code = 1 + sanitizer 消息), 早于 fortify 的 `__chk_fail`. 即: ASan + fortify 同时启用时观察到的信号是 ASan 的, 不是 fortify 的. 部分 libc 版本在 ASan 启用时将 `__*_chk` 重定向为 ASan 的 interceptor.
- **compiler + runtime**: GCC/Clang + libasan / hwasan
- **version**: all
- **target**: generic
- **source_kind**: source + test
- **source_url_or_path**: `compiler-rt/lib/asan/asan_interceptors.cpp`
- **version_sensitivity**: stable
- **oracle_mapping**: DeFuzz fortify oracle 必须禁用 ASan/HWASan, 否则退出码判定被污染.

### INV-FORT-X03 — 与 LTO 的联动

- **statement**: `-flto` 使 BOS/BDOS 看到更多 inline 上下文, 可能使 level ≤ 2 下原本返回 `-1` 的 call site 在 LTO 后变为可证越界, 从而触发编译期 `__errordecl` 或 runtime 拦截. 反之 LTO 不会让 fortify 退化.
- **compiler**: GCC, LLVM/Clang
- **version**: all
- **target**: generic
- **source_kind**: source
- **source_url_or_path**: `gcc/lto/` 相关 pass 调度; LLVM LTO pipeline
- **version_sensitivity**: stable (单调性契约), likely-to-drift (具体覆盖面)
- **oracle_mapping**: DeFuzz 在 `-flto` 配置下跑同一 seed 应当"至少一样严格".

### INV-FORT-X04 — 与 `-fbounds-safety` 的关系

- **statement**: Clang 的 `-fbounds-safety` (BS) 与 fortify 是两套独立机制: BS 在 Sema / IR 生成阶段插入 trap, 覆盖所有指针访问 (不限 libc 函数); fortify 仅覆盖 libc wrapper. BS 不替代 fortify 的跨 libc 调用保护 (例如通过 libc 内部的间接越界); 两者并存时以谁先失败为准.
- **compiler**: LLVM/Clang
- **version**: Clang 19+
- **target**: generic
- **source_kind**: user-doc + RFC
- **source_url_or_path**: https://clang.llvm.org/docs/BoundsSafety.html ; https://discourse.llvm.org/t/rfc-enforcing-bounds-safety-in-c-fbounds-safety/70854
- **version_sensitivity**: likely-to-drift (BS 仍在演进)
- **oracle_mapping**: DeFuzz 暂不把 BS 纳入 fortify oracle 矩阵; 未来可作独立 oracle.

## 9. DeFuzz Fortify Oracle 与上述 invariants 的映射总表

| Invariant | Oracle 信号 | 触发 seed 模式 |
|---|---|---|
| INV-FORT-E01 | `exit_code ∈ {134, 139}` (取决于是否拦截) | 任意 `memcpy/strcpy` 越界, 编译用 `-O2 -D_FORTIFY_SOURCE=2` |
| INV-FORT-L01 | level=1 拦截但 level=2 额外拦截 sub-object | 同一 seed 跨 level 跑 diff |
| INV-FORT-L02 | `exit_code == 134` | sub-object 模板 (`struct wrapper { char buf[64]; int x; }` + `strcpy(w.buf, src)`) |
| INV-FORT-L03 | level=3 拦截 malloc/`__counted_by`, level=2 退化 | `char *p = malloc(n); memcpy(p, src, fill);` |
| INV-FORT-B03 | level ≤ 2 下 alloca/VLA 不拦截 | 模板 3 (alloca) |
| INV-FORT-B04 | level=3 下 alloca/VLA 可能拦截 | 同上, 跨 level 对比 |
| INV-FORT-C01 | 手写循环不拦截 (预期) | `for (i=0;i<fill;i++) buf[i]='A';` |
| INV-FORT-F01 | `exit_code == 134` | 任意被 fortify 覆盖的越界 |
| INV-FORT-F02 | 观察到 `dstlen == -1` → 退化 | objdump 确认 `__*_chk` 被保留但 runtime 不触发 |
| INV-FORT-X01 | 必须 `-fno-stack-protector` | oracle 标准配置 |
| INV-FORT-N04/N05 | 第三方编译器下 level ≥ 1 仍 exit 139 | 无 BOS / `_FORTIFY_SOURCE` 被 toolchain 清空的编译器 |

## 10. 假阳性处理 (False Positive)

与 canary oracle 相同 (见 `@/home/yall/project/de-fuzz/docs/invariants/stack-canary.md` INV-SP-L04), fortify oracle 在观察到 `exit_code == 139` 时也需要借助 **sentinel 输出** (`printf("SEED_RETURNED\n"); fflush(stdout);`) 区分:

| stdout 含 `SEED_RETURNED` | exit_code | 判定 |
|---|---|---|
| Yes | 139 | 真正的 fortify bypass — 报 bug |
| No | 139 | 函数内部间接崩溃 (如参数副本被破坏) — 假阳性, 不报 |
| — | 134 | fortify 生效 — 安全 |
| — | 0 | 未越界 / 越界量不足触发 |

原理见 `@/home/yall/project/de-fuzz/docs/oracles/fortify-oracle.md:204-243`.

## 11. 开放问题 / 未覆盖 invariants (Follow-ups)

- **GCC 15 `__counted_by` 与 glibc ≥ 2.38 的端到端联动**: GCC 15 开始支持 `__attribute__((counted_by(field)))`, 与 Clang 17 的语义对齐, 但 BDOS 下降规则是否完全一致待核.
- **musl / bionic 的 `__*_chk` 覆盖清单**: 与 glibc 有差异 (musl 历史上较晚引入 `_FORTIFY_SOURCE`, 1.2.5+ 才接近 glibc 覆盖面), 需单独校对.
- **`-D_FORTIFY_SOURCE=3` 下 `__counted_by` flexible array 的 paired-assignment 语义**: Clang 已在 Sema 层检查, GCC 是否有等价 warning 待核.
- **fortify 与 `setjmp/longjmp` / C++ 异常穿越的交互**: 若 `_chk` 失败路径被 longjmp 跨越, `__chk_fail` 的 `noreturn` 契约是否仍被尊重 (glibc 实现走 `abort()`, 理论上无法被绕过, 但 longjmp 越过 `__fortify_fail` 的入口前一瞬间的行为未文档化).
- **交叉编译场景下 target libc 与 host BOS 计算的一致性**: 跨 ISA 交叉编译时 `size_t` 宽度差异可能影响 `(size_t)-1` 的判定 (32-bit target vs 64-bit host BOS).
- **`_FORTIFY_SOURCE` 对 C++ `std::string` / `std::vector` 的覆盖缺口**: 这些容器内部使用 libc 函数, 但从用户代码视角 `s.resize()` / `v.push_back()` 不走 fortify 路径, 依赖 `_GLIBCXX_ASSERTIONS` / libc++ hardening 独立机制.

## 12. 使用建议

- **新增 CFLAGS 变种**时, 优先确认 `-O1/-O2/-O3 × level∈{0,1,2,3} × {带/不带 -flto}` 的笛卡尔积结果矩阵是否仍满足 INV-FORT-L04 (单调性).
- **升级 glibc / gcc / clang** 时, 对 §5-§7 逐条回归, §9 的退化模式作为第三方编译器的正控组.
- 遇到 `exit_code == 139` 时必须先查 sentinel, 区分 **INV-FORT-N01/N02 的合法退化** vs **真正的 fortify bypass**.
- `version_sensitivity = likely-to-drift` 的条目 (INV-FORT-B03, INV-FORT-B04, INV-FORT-B06, INV-FORT-I01, INV-FORT-I02, INV-FORT-X04) 每次编译器升级都需要人工确认覆盖面变化.
- fortify 的核心哲学是 **"覆盖 libc wrapper, 不覆盖语言"**; DeFuzz 在设计 seed 模板时应严格遵守 INV-FORT-C01 / C02 的覆盖边界, 避免把 "手写循环越界" 误判为 fortify bug.
