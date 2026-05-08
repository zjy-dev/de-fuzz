# `-fstrub` (Stack Scrubbing) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC `gcc/ipa-strub.cc` / `libgcc/strub.c` 中与 **STRUB** 直接相关的 invariants 抽取归类, 作为 DeFuzz STRUB oracle 的形式化依据.
>
> 机制简写与 survey: **STRUB** = `-fstrub` 栈擦除. GCC 13+ 引入, 用于减少跨调用栈残留泄漏 (类似 ZCUR 但针对栈).

## 0. 术语与坐标

- **STRUB**: 编译器在函数返回时擦除该函数曾使用过的栈区, 使 caller / 后续 callee 无法读到敏感残留 (例如解密的 key 在临时 buffer 中).
- **modes**: GCC 提供两类:
  - `at-calls`: ABI 改变, callee 与 caller 的栈布局协作擦除. 性能更好但需要双方同意 (即跨函数调用契约).
  - `internal`: ABI 不变, callee 自管, 通过 wrapper 函数实现.
  - `disabled`: 关闭 (默认).
  - `default`: 编译期决策, 通常 = `disabled` 在用户态, `=internal` 在敏感 TU.
- **`-fstrub=` levels**: `disabled` / `default` / `relaxed` / `strict` / `at-calls` / `internal` / `at-calls-strict` / `internal-strict` / `all`.
- **wrapper / body**: `internal` 模式把每个 strub 函数 IPA pass 拆为 *wrapper* (公开符号, 不擦) 和 *body* (实际工作, 擦栈).
- **`__strub_enter` / `__strub_leave` / `__strub_update`**: libgcc runtime 入口, 维护擦除区域.

每条 invariant 字段同前.

## 1. 启用条件

### INV-STRUB-E01 — `-fstrub=<mode>` 启用

- **statement**: GCC 13+ 接受 `-fstrub={disabled,default,relaxed,strict,at-calls,internal,...}`. 通常用 `=internal` (ABI 不变) 或 `=all` (整 TU 强制).
- **compiler**: GCC
- **version**: GCC 13+
- **target**: 通用
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Optimize-Options.html ; `gcc/ipa-strub.cc` ; `libgcc/strub.c`
- **evidence_snippet**: GCC manual: *"`-fstrub=...` enables stack scrubbing for selected functions"*.
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: oracle 抓 epilogue 是否含 `__strub_leave` 调用.

### INV-STRUB-E02 — Clang 不实现

- **statement**: STRUB 是 GCC 独有, Clang 没有等价机制. 跨编译器对照时仅 GCC 路径.
- **compiler**: Clang (无)
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-STRUB-E03 — 函数级 `__attribute__((strub("mode")))`

- **statement**: 函数属性细控制每函数 strub mode. 可指定 `at-calls` / `internal` / `disabled` / `relaxed` / `strict`. 用于 crypto 关键函数.
- **compiler**: GCC
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Common-Function-Attributes.html
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 函数级 seed.

## 2. 编译器侧 (IPA pass)

### INV-STRUB-C01 — IPA pass 拆 wrapper + body

- **statement**: `internal` 模式下 GCC IPA pass `pass_ipa_strub` 把每个 strub 函数变成 `f` (wrapper, 公开) + `f.strub` (body). wrapper 调 `__strub_enter`, 然后调 body, body 完后 `__strub_leave` 擦栈, 返回. body 是私有的, 无 ABI 暴露.
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: `gcc/ipa-strub.cc`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描 ELF 是否含 `f.strub` 私有符号.

### INV-STRUB-C02 — `at-calls` 模式改 ABI

- **statement**: `at-calls` 模式 caller 与 callee 都需协作: caller 保留 strub 区, callee 在返回前擦除该区. 这改变 ABI: 普通函数不可调 strub 函数, 反之亦然 (除非中间有 wrapper). 因此只在闭合代码集 (整个项目) 内可用.
- **compiler**: GCC
- **source_kind**: user-doc + source
- **source_url_or_path**: `gcc/ipa-strub.cc`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 跨 TU strub 兼容性 seed.

### INV-STRUB-C03 — 模式间 callee-callable 矩阵

- **statement**: 不是所有模式组合都可互调. 大致规则:
  - `disabled` 函数可调任何 strub 函数 (经 wrapper).
  - `internal` 函数可调 disabled / internal / at-calls 函数.
  - `at-calls` 函数仅可调 at-calls / disabled.
  - `strict` 模式拒绝任何"不擦"调用路径 (包括 disabled), 编译期报错.
  详见 `gcc/ipa-strub.cc` 注释.
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: `gcc/ipa-strub.cc`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 模式组合 seed.

### INV-STRUB-C04 — Dynamic alloca / VLA 协助点

- **statement**: STRUB 必须知道擦除区域上下界. 含 alloca / VLA 的函数需要后端协助记录动态大小. GCC 通过 `STRUB_DYNAMIC_ALLOCA` 后端 hook 处理, 部分后端实现, 否则运行时退化为擦"已知静态部分".
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: `gcc/ipa-strub.cc` (`update_alloca`)
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: alloca + STRUB seed.

## 3. Runtime (libgcc)

### INV-STRUB-R01 — `__strub_enter(watermark*)`, `__strub_leave(watermark*)`

- **statement**: libgcc 提供两个 runtime 入口:
  - `__strub_enter(wm)`: 在 wrapper 进入 body 前调用, 把当前 SP 写入 `wm`.
  - `__strub_leave(wm)`: body 返回时调用, 把 [SP_old, SP_now] 区间清零 (通常用 `memset(0)`).
- **runtime**: libgcc
- **source_kind**: source
- **source_url_or_path**: `libgcc/strub.c`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描调用; runtime 可观察清零栈值.

### INV-STRUB-R02 — `__strub_update(wm)` 处理深递归

- **statement**: 长函数中可能多次擦除 (减少瞬时栈占用峰值). `__strub_update` 把 wm 上调到当前 SP, 让后续 `__strub_leave` 只擦增量.
- **runtime**: libgcc
- **source_kind**: source
- **source_url_or_path**: `libgcc/strub.c`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 文档.

### INV-STRUB-R03 — 异常 unwind 经过 strub 边界需重新初始化

- **statement**: 异常跳过 strub wrapper 时, body 可能未走到 `__strub_leave`. libgcc unwinder 在跨 strub 边界时必须重新初始化或调用 `__strub_leave` 擦除. 这是 STRUB 与异常的契约关键.
- **runtime**: libgcc
- **source_kind**: source
- **source_url_or_path**: `libgcc/unwind-dw2.c` (strub-aware 路径)
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 频繁 throw seed, 验证 strub 仍执行.

## 4. ELF 元数据

### INV-STRUB-M01 — 无 ELF property; 通过私有符号识别

- **statement**: STRUB 不引入 ELF property. 但 internal 模式下 ELF 含 `<func>.strub` 私有符号, oracle 可作识别.
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 静态扫描 `nm` 含 `.strub` 后缀.

## 5. 与其他机制交互

### INV-STRUB-I01 — STRUB 与 ZCUR 互补

- **statement**: STRUB 擦栈, ZCUR 清寄存器. 同启提供深度防御; 二者不冲突.
- **compiler**: GCC
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/zero-call-used-regs.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-STRUB-I02 — STRUB 与 SP / SCS / PAC 正交

- **statement**: STRUB 不影响 canary / SCS / PAC, 仅修改栈帧返回时的 *清零* 行为.
- **compiler**: GCC
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-STRUB-I03 — STRUB 与 SafeStack 概念不同

- **statement**: SafeStack 把"unsafe"局部移到独立栈; STRUB 不分栈, 只在返回时擦. 二者目的接近但实现不同, 可同启 (但 GCC 无 SafeStack).
- **compiler**: GCC vs Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-STRUB-I04 — STRUB 与多线程

- **statement**: 每线程独立栈, STRUB 仅清 *当前线程* 栈区. 不需特殊跨线程同步.
- **compiler**: GCC
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 多线程 seed.

## 6. 验证与已知回归

### INV-STRUB-VER-PR93508 — STRUB 早期 ABI 边角

- **statement**: GCC 13 引入 STRUB 时若干边角 (alloca + at-calls 互动, 异常 unwind) 在补丁系列内逐步修复. 跨 GCC 13.x 子版本行为不一致, 需精确 commit hash.
- **compiler**: GCC
- **version**: GCC 13+ (持续)
- **source_kind**: mailing-list
- **source_url_or_path**: gcc-patches 邮件归档 "stack scrubbing"
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 老 GCC 13 seed.

## 7. DeFuzz Oracle 映射总表

| Invariant | Oracle 信号 | seed |
|---|---|---|
| INV-STRUB-C01 | ELF 含 `.strub` 私有符号 | 任意 internal 函数 |
| INV-STRUB-R01 | 函数返回后栈区清零 | 关键函数返回后读栈 |
| INV-STRUB-R03 | 异常路径下栈仍清零 | throw 中跳过 strub leave |
| INV-STRUB-C03 | 编译失败 | 模式不兼容组合 |

## 8. 开放问题

- **Clang 是否未来支持**: 目前无路线图; 如未来上游需补 invariant.
- **`relaxed` vs `strict`** 对错误处理的差异, 待补.
- **dynamic alloca + at-calls**: 部分后端未实现 dynamic alloca hook, 行为退化, 列表化.
- **strub 性能开销量化**: 实测数据待补.
- **Linux 内核中是否使用 STRUB**: 截至 6.x 内核未默认启用, 待跟踪.

## 9. 使用建议

- 仅在 GCC 13+ 项目使用, Clang 不支持.
- crypto / 处理敏感数据的函数加 `__attribute__((strub("internal")))`.
- 与 ZCUR 配合提供完整跨调用残留擦除.
- `likely-to-drift` invariant 多, 主分支 GCC 升级时 audit.
