# `_FORTIFY_SOURCE` / Object Size Checking Invariants — Silent-Bypass 视角

> 本文只关心**会让 `_FORTIFY_SOURCE` 静默失效或被静默削弱**的不变量, 即"`-D_FORTIFY_SOURCE≥1` 已生效, 优化等级达标, libc 提供 `__*_chk` 实体, glibc 头文件的 `__fortify_function` wrapper 已替换原始符号, 但攻击者构造的越界写仍能完成而 `__chk_fail` / `__fortify_fail` 不被调用". 形如"`-O0` 下 wrapper 是否退化"、"宏未定义是否真不发 `_chk`"、"musl/newlib 是否提供 `__*_chk`"、"`#warning` 是否被发出"等问题, 后果或是机制更强、或是配置/链接/编译失败, 不在本文档范围内.
>
> 机制简写: **FORT** = `_FORTIFY_SOURCE` / Object Size Checking. 涉及编译器 builtin: `__builtin_object_size` (BOS) 与 `__builtin_dynamic_object_size` (BDOS).
>
> 来源覆盖: GCC 主线, LLVM/Clang 主线, glibc, Linux kernel hardening, GCC Bugzilla / LLVM PR / Red Hat Bugzilla / sourceware, CVE-2012-0864, hxp CTF 2017 实战 PoC, MaskRay FORTIFY 综述.

## 0. 术语与坐标

- **level**: `-D_FORTIFY_SOURCE=N`, `N ∈ {1, 2, 3}`. level 1/2 用 BOS; level 3 (GCC 12+ / Clang 9+ + glibc 2.34+) 用 BDOS.
- **wrapper**: glibc `bits/string_fortified.h` / `bits/stdio2.h` / `bits/wchar2.h` / `bits/socket2.h` / `bits/poll2.h` / `bits/unistd.h` 等头文件中的 `__fortify_function` 内联定义, 在调用点把原 libc 函数重写为 `__<name>_chk(...)`.
- **`_chk` 实体**: libc 提供的 `__<name>_chk(..., dstlen)` 真函数, 内部判定 `if (__glibc_unlikely(dstlen < len)) __chk_fail();`.
- **`__chk_fail` / `__fortify_fail`**: glibc 中 `noreturn` 的失败处理函数, 输出 "buffer overflow detected" 后 `abort()`.
- **退化 (fall back)**: BOS 返回 `(size_t)-1` (type 0/1) 或 `0` (type 2/3) 时, 比较 `dstlen < len` 在 `-1` 路径下因 `(size_t)-1` 是 SIZE_MAX 而恒不成立 ⇒ wrapper 在 IR 层折叠为原函数调用. 退化属于 fortify 的设计行为, **不是** silent bypass.
- **silent bypass**: 攻击者构造的越界写发生时, BOS/BDOS 给出的 `dstlen` 仍 *声称* 写入合法, 或 wrapper 整条路径 *在编译产物中不存在*, 或 runtime check 路径在攻击者可控前提下静默放行; `__chk_fail` 不被调用, 进程不被 `abort()`, 写入完成. 这是本文所有不变量的统一威胁模型.

每条不变量按 [`README.md` §2](./README.md#2-survey-字段约定) 的字段记录: `ID / statement / compiler / version / target / source_kind / source_url_or_path / evidence_snippet / version_sensitivity / observation`.

## 1. BOS / BDOS 的对象大小估计不得超过实际对象

估计类不变量的共同威胁: 编译器对某指针 `p` 给出的 `__builtin_object_size(p, …)` / `__builtin_dynamic_object_size(p, …)` 返回值大于 `p` 实际可访问字节数, wrapper 把虚高的 `dstlen` 喂给 `__*_chk`, runtime 比较 `dstlen < len` 不成立 ⇒ chk 路径不调 `__chk_fail`, 越界写完成.

### INV-FORT-O01 — BOS 不得对结构体最后成员数组返回 `(size_t)-1`

- **statement**: 当数组是结构体最后一个成员 (无论是否真的是 flexible array), `__builtin_object_size(p->arr, 1)` 必须返回该字段在静态布局上的字节数; 不得退化为 `(size_t)-1`. 一旦返回 `-1`, 任何 `__memcpy_chk(p->arr, src, len, -1)` / `__strcpy_chk(p->arr, src, -1)` 比较 `(size_t)-1 < len` 永远为假, fortify 整条 wrapper 退化, 该字段任意越界写不被拦截.
- **compiler**: GCC, LLVM/Clang
- **version**: GCC 长期存在 (PR 101836 至今状态 NEW), Clang 等价路径需独立验证
- **target**: generic
- **source_kind**: bug-disclosure + source
- **source_url_or_path**: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=101836 ; https://maskray.me/blog/2022-11-06-fortify-source (FORTIFY 综述, 明确指出 "prone to return -1 and defeat `_FORTIFY_SOURCE`")
- **evidence_snippet**: PR 101836: *"`__builtin_object_size(P->M, 1)` where M is an array and the last member of a struct fails"* — 实际 backing storage 大小已知, BOS 仍返回 unknown.
- **version_sensitivity**: likely-to-drift (与 `-fstrict-flex-arrays` 行为耦合, 行为随主线漂移)
- **observation**: `__memcpy_chk` 等 `_chk` 符号仍被链接保留, 但 `dstlen` 实参始终为 `(size_t)-1` (即 `0xFFFFFFFFFFFFFFFF`); 越界写完成后 `__chk_fail` 未被调用 ⇒ 不变量被违反 (silent bypass).

### INV-FORT-O02 — BDOS 在 `__counted_by` 路径上必须返回 = `count * sizeof(elem)`

- **statement**: 当结构体声明 `struct S { int n; T arr[] __attribute__((counted_by(n))); }`, 对 `p->arr` 的 BDOS 必须返回 `p->n * sizeof(T)`. 已知两类实现错误: (a) 嵌套指针访问 (`outer->inner->arr`) 时 Clang < 19.1.2 把 `MemberExpr` 当指针处理, BDOS 返回 0, 配合 `if (size > 0) check` 短路使 wrapper 退化; (b) 整 struct 形式 `__bdos(p, 0)` 在 Clang < 19.1.3 误算为 `sizeof(struct) + count*sizeof(T)` 而非 `offsetof(struct, arr) + count*sizeof(T)`, 偏差精确 4 字节, 攻击者可在偏差范围内静默越界写而不触发 `__chk_fail`. 对应 kernel 直接拒绝 `__counted_by` < 修复版本的 Clang.
- **compiler**: LLVM/Clang
- **version**: Clang ≤ 19.1.2 (PR #110497), Clang ≤ 19.1.3 (PR #112636); 修复后 kernel 进一步要求 ≥ 20.1.0
- **target**: generic
- **source_kind**: bug-disclosure + source
- **source_url_or_path**: https://github.com/llvm/llvm-project/pull/110497 ; https://github.com/llvm/llvm-project/pull/112636 ; https://www.mail-archive.com/linux-hardening@vger.kernel.org/msg06358.html (kernel disable for clang < 19.1.3) ; https://www.mail-archive.com/linux-hardening@vger.kernel.org/msg10369.html (require clang 20.1.0)
- **evidence_snippet**: PR #110497: *"Fix 'counted_by' for nested struct pointers"* — 嵌套场景下 BDOS 返回 0; kernel patch: *"disable `__counted_by` for clang < 19.1.3"*.
- **version_sensitivity**: target-specific (修复后稳定)
- **observation**: 含 `counted_by` 的结构体在 `_FORTIFY_SOURCE=3` 下编译, 对嵌套指针成员或整 struct BDOS 路径写入超过 `count * sizeof(elem)` 字节, `__chk_fail` 未被调用 ⇒ 不变量被违反.

### INV-FORT-O03 — BDOS 对被本地重赋值过的尺寸变量必须重读最新值

- **statement**: BDOS 在生成运行时计算时, 若引用了某个尺寸变量 (例如来自 `argc` 或前驱 BB 写入的 local), 必须读其在 BDOS 计算点的最新 SSA 值, 不得使用前驱 BB 中已被覆盖的旧值. GCC PR 113514 报告: 对 `f.bar[argc][40]` 类成员调用 `__bdos`, 当 `argc` 在调用前被本地重新赋值, GCC 14 错误地用一个比实际尺寸大 8 字节的值, 使得对 `f.bar` 的越界写在 fortify 比较中合法.
- **compiler**: GCC
- **version**: GCC 14.0 (PR 113514, P3, UNCONFIRMED 时被报告)
- **target**: generic
- **source_kind**: bug-disclosure
- **source_url_or_path**: https://gcc.gnu.org/bugzilla/show_bug.cgi?id=113514
- **evidence_snippet**: PR 113514 标题: *"Wrong `__builtin_dynamic_object_size` when using a set local variable"*, 报告中 BDOS 返回 48 而实际可写区域为 40.
- **version_sensitivity**: likely-to-drift
- **observation**: 反汇编 `__memcpy_chk` 调用点的 `dstlen` 实参表达式 (RDX 或栈位置) 在调用前已被覆盖为旧的 `argc` 值; 攻击者按 BDOS 虚高值越界写而 `__chk_fail` 不触发 ⇒ 不变量被违反.

## 2. wrapper 内联 / 优化不得丢失 BOS 上下文

### INV-FORT-W01 — `__fortify_function` 内联后必须保留 BOS / `pass_object_size` 上下文

- **statement**: glibc 头文件的 `__fortify_function` wrapper 是 `static __always_inline`, 调用 `__builtin_object_size(dst, …)` 后再转发到 `__<name>_chk(…, dstlen)`. 若编译器 inliner 在内联 wrapper 时丢失了 caller 上下文中的 BOS 信息 (例如把 `__bos(buf, 1)` 折叠为 `-1`, 或在 `__pass_object_size` 标注的隐式参数路径上传错值), 编译期分支判断和 runtime `__write_overflow*` warning 都被消除, `__*_chk` 退化为原函数, 越界写不再触发 `__fortify_panic` / `__chk_fail`. Clang < 13 在 Linux kernel 上下文中触发该回归, 触发后 kernel 整段 fortify-string.h 必须改写为宏化 + `__pass_object_size` 强制传递.
- **compiler**: LLVM/Clang
- **version**: Clang ≤ 12 (kernel commit `a28a6e860c6c` "fortify: Work around Clang inlining bugs", merged in 5.18 cycle)
- **target**: generic
- **source_kind**: bug-disclosure + source
- **source_url_or_path**: https://lore.kernel.org/lkml/20210818060533.3569517-64-keescook@chromium.org/ ; https://github.com/llvm/llvm-project/issues/77813
- **evidence_snippet**: kernel commit message: 内联后 `__write_overflow*` 编译期分支被消除, 运行期 `*_chk` 路径退化为普通函数; workaround 把 fortify-string.h 全部宏化, 用 `__pass_object_size` 把 BOS 显式作隐式参数传入.
- **version_sensitivity**: target-specific (Clang 修复后稳定)
- **observation**: 受影响 Clang 编译的二进制中, 含 `__fortify_function` 包装的 libc 调用点对应的 `.text` 仅出现原函数的 `call`, 未出现 `__memcpy_chk` / `__strcpy_chk` 等 `_chk` 符号引用, 同时源代码确实越界 ⇒ wrapper 已退化为原函数, 不变量被违反.

## 3. Runtime check 必须 fail-closed

### INV-FORT-R01 — `__readonly_area` 在 `/proc/self/maps` 不可用时不得返回 "安全"

- **statement**: glibc `_FORTIFY_SOURCE ≥ 2` 下, `vfprintf` 检测 `%n` 类格式串向写段写入时, 通过 `__readonly_area(addr, len)` 判定目标地址是否在只读段; 实现读 `/proc/self/maps`. 若 `fopen("/proc/self/maps")` 返回 `ENOENT` / `EACCES` (chroot / sandbox / 容器内无 `/proc` / seccomp 过滤 `open` 返回 errno), 当前实现走 "假定安全" 分支返回 1 ⇒ `vfprintf` 内部不再调用 `__chk_fail`, 攻击者构造的 `%n` 向任意可写或可写段地址执行写入完成. hxp CTF 2017 "hardened_flag_store" 是该路径的实战 PoC: 用 seccomp `SECCOMP_RET_ERRNO` 把 `open("/proc/self/maps")` 强制返回 `ENOENT`, 然后让 `printf` 的 `%n` 在 `_FORTIFY_SOURCE=2` 下静默通过.
- **runtime**: glibc
- **version**: 长期存在于 glibc Linux 后端
- **target**: Linux
- **source_kind**: source + bug-disclosure
- **source_url_or_path**: https://codebrowser.dev/glibc/glibc/sysdeps/unix/sysv/linux/readonly-area.c.html ; https://0xacb.com/2017/11/19/hxp-flag-store/ ; https://fatalbit.net/2017/11/19/hardened-flag-store.html ; https://bruce30262.github.io/hxp-CTF-2017-hardened-flag-store/
- **evidence_snippet**: `readonly-area.c` 在 `fopen` 失败 + `errno ∈ {ENOENT, EACCES}` 时 `return 1`; CTF writeup 直接用 seccomp 触发该路径绕过 `_chk_fail`.
- **version_sensitivity**: stable
- **observation**: 进程在 `/proc` 不可访问的环境下运行被 fortify 的 `printf` 系列, 含 `%n` 写入到可写段, 未触发 `__chk_fail`, 进程未 `abort` ⇒ 不变量被违反.

### INV-FORT-R02 — `__chk_fail` / `__fortify_fail` 必须 `noreturn`

- **statement**: glibc / libssp 提供的 `__chk_fail` (旧路径) 与 `__fortify_fail` (统一路径) 必须以 `noreturn` 结束, 实现内部一定要 `abort()` (或等价不可继续路径); 编译器据此不在 `_chk` call 之后插入正常返回路径. 若被替换的实现 (freestanding stub / 自定义 hook) 真的返回, 函数 epilogue 继续执行污染过的状态 ⇒ silent bypass. 与 `__stack_chk_fail` 同类语义, 是 fortify 与 stack canary 共同依赖的最低 runtime 契约.
- **runtime**: glibc, libssp, 第三方 stub
- **version**: all
- **target**: generic
- **source_kind**: runtime
- **source_url_or_path**: glibc `debug/chk_fail.c` ; glibc `debug/fortify_fail.c`
- **version_sensitivity**: stable
- **observation**: 触发任意一条 fortify check 后函数继续返回正常路径, 或 caller 上下文得到正常返回值 ⇒ 不变量被违反.

## 4. wrapper 必须真正覆盖被攻击的写入 sink

### INV-FORT-C01 — `vfprintf` 入口必须设置统一的 fortify flag

- **statement**: `printf` 族 (含 `printf` / `fprintf` / `sprintf` / `snprintf` / `vprintf` / `vsprintf` / `vsnprintf` / `dprintf` / `syslog` / `asprintf`) 的所有 `__*_chk` 入口必须在调用 `vfprintf` 内部之前设置统一的 fortify flag (旧实现是 `_IO_FLAGS2_FORTIFY` per-FILE bit, 新实现是 `PRINTF_FORTIFY` 局部参数), 使 `vfprintf` 在格式串解析阶段对 `%n` / 位置参数 / 类型不匹配等做检查. 若任一入口漏设, 该入口下所有 `%n` 写入均不被 fortify 拦截; CVE-2012-0864 即此结构性问题的一个表征 (位置参数路径下整数溢出绕过 fortify), 后续 glibc 主线把 per-FILE bit 重构为 `PRINTF_FORTIFY` 显式参数, 正是为统一覆盖而做.
- **runtime**: glibc
- **version**: 历史 glibc (≤ 2.16 触发 CVE-2012-0864), 重构见 glibc 2.29 周期 patchwork
- **target**: generic
- **source_kind**: bug-disclosure + source
- **source_url_or_path**: https://bugzilla.redhat.com/show_bug.cgi?id=794766 ; https://nvd.nist.gov/vuln/detail/CVE-2012-0864 ; https://patchwork.ozlabs.org/project/glibc/patch/20181115214449.19262-7-gabriel@inconstante.eti.br/
- **evidence_snippet**: glibc patch: 把 `_IO_FLAGS2_FORTIFY` 替换为 `PRINTF_FORTIFY`, 明确承认旧旗标在多个 wrapper 间设置不一致导致 bypass 隐患.
- **version_sensitivity**: stable since fix
- **observation**: 在被 fortify 的 `printf` 族入口构造 `%n` 写入或类型不匹配的位置参数, 进程未触发 `__chk_fail` ⇒ 该入口未参与 fortify 覆盖, 不变量被违反.

### INV-FORT-C02 — `<err.h>` / `<error.h>` 系列必须有 `_chk` wrapper

- **statement**: BSD/GNU 的 `err()` / `errx()` / `warn()` / `warnx()` / `verr()` / `vwarn()` / `error()` / `error_at_line()` 内部走 `vfprintf`, 因此同样可能被 `%n` 攻击. 这些函数若没有 `__fortify_function` wrapper, 即便编译时 `_FORTIFY_SOURCE=2` 已生效, 用户对它们的调用走的是裸 `vfprintf` 路径 ⇒ `%n` 写入完成而无 `__chk_fail`. Red Hat BZ 836931 / sourceware 24987 公开报告该长期空洞.
- **runtime**: glibc
- **version**: glibc 全版本 (至该 BZ 报告时未修)
- **target**: generic
- **source_kind**: bug-disclosure
- **source_url_or_path**: https://bugzilla.redhat.com/show_bug.cgi?id=836931 ; https://sourceware.org/bugzilla/show_bug.cgi?id=24987
- **version_sensitivity**: likely-to-drift (取决于 glibc 主线是否补 wrapper)
- **observation**: 在被 `_FORTIFY_SOURCE=2` 编译的程序中调用 `warn`/`err` 系列含 `%n` 的格式串向可写段写入, 进程未触发 `__chk_fail` 而正常退出/继续 ⇒ 不变量被违反.

## 5. 已知 silent-bypass 案例

| Case | 违反的不变量 | Toolchain / 系统 | 说明 |
| --- | --- | --- | --- |
| GCC PR 101836 | INV-FORT-O01 | GCC 长期存在 | struct 最后成员数组的 BOS 退化为 `(size_t)-1`, 任何对该字段的越界写 `__memcpy_chk` 比较恒为假, fortify 失效. |
| LLVM PR #110497 | INV-FORT-O02 | Clang ≤ 19.1.2 | `counted_by` 嵌套指针访问下 BDOS 返回 0, `_chk` 路径在错误的 0 边界上比较, 越界写不触发 `__fortify_panic`. |
| LLVM PR #112636 | INV-FORT-O02 | Clang ≤ 19.1.3 | `counted_by` whole-struct BDOS 计算公式偏差 4 字节, 攻击者可在偏差窗口内越界写而不触发 fortify. |
| GCC PR 113514 | INV-FORT-O03 | GCC 14.0 | 局部变量重赋值后 BDOS 返回旧值导致虚高 8 字节, `__memcpy_chk` 静默放行. |
| kernel "Work around Clang inlining bugs" (a28a6e860c6c) | INV-FORT-W01 | Clang ≤ 12 | `__fortify_function` 内联后 BOS 上下文丢失, `_chk` wrapper 退化为原函数, 越界写不触发 `__fortify_panic`. |
| hxp CTF 2017 hardened_flag_store | INV-FORT-R01 | glibc 长期存在 | seccomp 强制 `open("/proc/self/maps")` 返回 `ENOENT`, `__readonly_area` 走 fail-open 路径, `%n` 在 `_FORTIFY_SOURCE=2` 下静默写入. |
| CVE-2012-0864 | INV-FORT-C01 | glibc ≤ 2.16 | `vfprintf` 位置参数处理整数溢出, `_IO_FLAGS2_FORTIFY` 路径下 `%n` 类型检查被绕过. |
| Red Hat BZ 836931 / sourceware 24987 | INV-FORT-C02 | glibc 全版本 (未修) | `err`/`warn` 系列从未被 fortify 包裹, 走裸 `vfprintf` 让 `%n` 静默通过. |

## 6. 可程序化筛选结果

> 筛选维度与静/动态归属准则定义在 [`README.md` §3 可程序化筛选方法论](./README.md#3-可程序化筛选方法论). 本章只列结果. 所有不变量均直接对应某条 silent-bypass 路径, 因此本机制下不再单列"非 silent-bypass 排除项".

### 通过筛选

- **INV-FORT-O01** — BOS 不得对 struct 最后成员返回 `-1`
  - 类别: 静态
  - 通过理由: 源码层可对结构体定义构造测试程序, 编译产物中检查 `__memcpy_chk` 调用点 `dstlen` 实参是否被折叠成立即数 `-1` (即 `0xFFFF…`); 已知 PR 101836 提供反例样本, 可作为 oracle 正反控.

- **INV-FORT-O02** — `counted_by` BDOS 必须 = `count * sizeof(elem)`
  - 类别: 静态
  - 通过理由: 给定带 `counted_by` 的源, 编译后反汇编 `__memcpy_chk` 调用点的 `dstlen` 表达式; 在 LLVM PR #110497 / #112636 提供的最小用例上比对预期值与实际值即可定性. 限定在 Clang.

- **INV-FORT-O03** — BDOS 对重赋值变量必须读最新值
  - 类别: 静态
  - 通过理由: 编译产物中反汇编 `dstlen` 计算路径, 比对其 SSA 来源是否取自前驱 BB 的旧值 vs 调用点最新值; PR 113514 提供具体最小用例.

- **INV-FORT-W01** — wrapper 内联后必须保留 BOS 上下文
  - 类别: 静态
  - 通过理由: 二进制中 `__memcpy_chk` / `__strcpy_chk` / `__snprintf_chk` 等 `_chk` 符号是否出现在期望位置 (`objdump -d` + 符号引用扫描). Clang < 13 + glibc 头文件的最小用例可复现 wrapper 全部退化为原函数. 与 `_FORTIFY_SOURCE=2/3 -O2` 编译开关交叉.

- **INV-FORT-R01** — `__readonly_area` 必须 fail-closed
  - 类别: 动态
  - 通过理由: 必须运行二进制 + 在 chroot / seccomp / 无 `/proc` 环境下触发 `%n`, 观察 `__chk_fail` 是否调用. 静态分析无法判定 runtime `/proc` 可用性. hxp 2017 PoC 可作样本.

- **INV-FORT-R02** — `__chk_fail` / `__fortify_fail` 必须 `noreturn`
  - 类别: 动态
  - 通过理由: 替换或观察实际 `__chk_fail` 实现是否在调用后真正终止进程. 仅静态分析符号属性不充分, freestanding 场景需运行验证.

- **INV-FORT-C01** — `vfprintf` 入口必须设置统一 fortify flag
  - 类别: 动态
  - 通过理由: 对每个 `printf` 族入口构造 `%n` 或类型不匹配的位置参数测试, 观察 `__chk_fail` 是否触发; 与 glibc 版本矩阵交叉. CVE-2012-0864 等历史样本提供正反控.

- **INV-FORT-C02** — `err`/`warn` 系列必须 `_chk` 包裹
  - 类别: 静态
  - 通过理由: 反汇编含 `_FORTIFY_SOURCE=2` 编译的二进制, 验证 `err`/`warn` 调用点是否走 `__*_chk` 符号; 静态可枚举.

### 未通过筛选

本文档已按 silent-bypass 视角剔除非相关项. 历史档案中曾列出的 `_FORTIFY_SOURCE` 启用所需的 `-O1` 前提、`-fhardened` 隐含 level 3、级别向下覆盖规则、`__*_chk` 链接是否存在、level 1/2/3 语义差异、BOS `type` 参数四象限语义、BOS 在 alloca/VLA/外部函数/复杂指针上 *按设计* 退化为 `-1`、`__counted_by`/`pass_object_size` 的 ABI 入口约定、被 fortify 覆盖的 libc 函数清单、手写循环不被 fortify 覆盖、读越界不被 fortify 覆盖、编译期 `__errordecl` 警告、第三方编译器无 BOS、`-flto` 与 fortify 单调性、与 ASan / `-fbounds-safety` 的叠加策略, 后果或为机制更强、或为编译/链接/启动失败、或为只读规范陈述、或为按设计的合法退化, 不构成 silent bypass, 因此不再列入本文档的筛选范围.

## 7. 开放问题

- **GCC 15 `__counted_by` 与 BDOS 下降覆盖面**: GCC 15 主线开始支持 `counted_by` 属性, 与 Clang 等价 silent-bypass 路径 (嵌套指针 / off-by-4 / paren) 是否被独立引入需逐 case 验证, 现阶段无对应 GCC PR 公开.
- **`__readonly_area` 是否会被改为 fail-closed**: glibc 主线讨论中曾出现把 `/proc/self/maps` 不可用视为不安全的提案, 但默认行为至今未变; 该不变量的修复时机直接决定 INV-FORT-R01 的有效边界.
- **`err.h` / `error.h` 系列何时纳入 fortify 覆盖**: sourceware 24987 长期 open, 一旦补 wrapper, INV-FORT-C02 的检测样本需要相应更新.
- **Clang `__fortify_function` 内联回归监测**: Clang 主线对 `__pass_object_size` + `__always_inline` 的处理仍偶有改动 (cf. issue #77813), kernel hardening 邮件列表是观察该不变量退化的最敏感前哨.
- **`__bdos` 在 LTO / ThinLTO 下的结果稳定性**: BDOS 下降发生在 IR 早期, 但 inliner 在 LTO 阶段会进一步重写表达式; 是否存在 LTO 下 BDOS 由"正确"退化回"错误的虚高值"的场景, 当前无公开证据但属潜在 silent-bypass 路径.
- **musl 与 bionic 的 fortify silent-bypass 矩阵**: 本文所有引用的 runtime 不变量基于 glibc 实现; musl 1.2.5+ 与 bionic 的 `__*_chk` 实现差异, 尤其 `__readonly_area` / `vfprintf` fortify flag / `err` 系列覆盖, 需要独立调研.
