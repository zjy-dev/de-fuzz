# `-fhardened` 元 flag Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 GCC 14+ 中 **`-fhardened`** 元 flag 的展开关系与传递性约束统一抽取归类, 作为 DeFuzz 整体硬化 oracle 的索引.
>
> 机制简写与 survey: **HARD** = `-fhardened`. 是 *打包* 选项, 自身无独立 codegen, 等价于一组其他硬化 flag 的同时启用.

## 0. 术语与坐标

- **元 flag**: 单一 flag 等价于多个具体 flag 的并集. `-fhardened` 是首个进入 GCC 主线的此类 flag (GCC 14).
- **profile 形式**: GNU/Linux 默认 profile 在 GCC 14+ 已明确; 其他 OS / target 组合可能不一致.
- **传递性**: 凡 `-fhardened` 隐式启用的子 flag, 其文档中所有 invariant 都自动适用本元 flag.

每条 invariant 字段同前.

## 1. 启用条件 (展开)

### INV-HARD-E01 — `-fhardened` 在 GNU/Linux 的展开

- **statement**: GCC 14+ `-fhardened` 在 GNU/Linux 用户空间隐式开启:
  - `-D_FORTIFY_SOURCE=3`
  - `-ftrivial-auto-var-init=zero`
  - `-fPIE -pie`
  - `-Wl,-z,relro,-z,now`
  - `-fstack-protector-strong`
  - `-fstack-clash-protection`
  - `-fcf-protection=full` (x86 平台支持时)
  原话见 GCC manual.
- **compiler**: GCC
- **version**: GCC 14+
- **target**: x86_64 / aarch64 (Linux), 仅部分子 flag 在非 Linux 自动展开
- **source_kind**: user-doc + source
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html ; `gcc/opts.cc` (`enable_fhardened_flags`)
- **evidence_snippet**: GCC 14 manual: *"`-fhardened` enables `-D_FORTIFY_SOURCE=3 -ftrivial-auto-var-init=zero -fPIE -pie -Wl,-z,relro,-z,now -fstack-protector-strong -fstack-clash-protection -fcf-protection=full`"*.
- **version_sensitivity**: stable (具体子集 likely-to-drift)
- **oracle_mapping**: DeFuzz 把 `-fhardened` 与显式列出的子 flag 集合做等价性 oracle.

### INV-HARD-E02 — Clang 不实现等价 `-fhardened`

- **statement**: Clang 主线截至 18 无对应 `-fhardened` 元 flag. 用户须显式拼装. 这是跨编译器对照时的关键差异.
- **compiler**: Clang (无)
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 跨编译器分支.

### INV-HARD-E03 — 子 flag 用户显式覆盖优先

- **statement**: `-fhardened` 与显式子 flag 同行命令行时, *后出现* 的 flag 覆盖. 例如 `-fhardened -fno-stack-protector` 会关闭 SP. 这是 GCC 标准 flag 处理顺序.
- **compiler**: GCC
- **source_kind**: source
- **source_url_or_path**: `gcc/opts.cc`
- **version_sensitivity**: stable
- **oracle_mapping**: 命令行排序 seed.

### INV-HARD-E04 — `-fhardened` 不开 BTI / PAC / SCS / SHSTK / KCFI / SafeStack 等

- **statement**: 截至 GCC 14, `-fhardened` 不隐式启用 AArch64 BTI / PAC, 也不启用 ShadowCallStack / KCFI / SafeStack / sanitizers / `-fzero-call-used-regs` / STRUB / HCFR / Bounds Safety / 内核 KCFI. 用户必须额外拼装.
- **compiler**: GCC
- **source_kind**: user-doc
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: 反例 seed: 仅 `-fhardened`, 反向边硬化保留弱.

## 2. 传递性 invariant (引用)

`-fhardened` 隐式启用的每个子机制的所有 invariant 自动是 `-fhardened` 的 invariant. 详细见各专文:

- **FORTIFY_SOURCE=3**: `@/home/yall/project/de-fuzz/docs/invariants/fortify-source.md`
- **`-ftrivial-auto-var-init=zero`**: `@/home/yall/project/de-fuzz/docs/invariants/auto-var-init.md`
- **`-fstack-protector-strong`**: `@/home/yall/project/de-fuzz/docs/invariants/stack-canary.md`
- **`-fstack-clash-protection`**: `@/home/yall/project/de-fuzz/docs/invariants/stack-clash-protection.md`
- **`-fcf-protection=full` (x86 IBT + SHSTK)**: `@/home/yall/project/de-fuzz/docs/invariants/endbr-ibt.md` 与 `@/home/yall/project/de-fuzz/docs/invariants/shadow-stack.md`
- **`-fPIE -pie`**: 见下面 §3 PIE 部分 (本机制不再单独写 invariants 文档, 因 ABI 级)
- **`-Wl,-z,relro,-z,now`**: 见下面 §3

## 3. 链接器层 invariants (-fPIE/-pie 与 RELRO)

### INV-HARD-L01 — `-fPIE -pie` 强制 ASLR

- **statement**: `-fPIE -pie` 让可执行体作为 PIE (Position-Independent Executable), 加载基址随机化 (ASLR). 这是 ROP / ret-to-libc 攻击的 *第一防线*. 内核 `randomize_va_space=2` (Linux 默认) 才生效.
- **compiler + linker**: GCC, Clang, ld
- **version**: 长期稳定
- **target**: 通用
- **source_kind**: user-doc + ABI
- **source_url_or_path**: https://gcc.gnu.org/onlinedocs/gcc/Code-Gen-Options.html
- **version_sensitivity**: stable
- **oracle_mapping**: `readelf -h` 看 ELF type DYN; `cat /proc/<pid>/maps` 看主程序基址变化.

### INV-HARD-L02 — `-Wl,-z,relro` (partial RELRO)

- **statement**: `-z relro` 让 GOT/PLT 等段在 ld.so 完成绑定后变只读. 防止攻击者覆写 GOT 进行劫持.
- **linker**: binutils, lld
- **source_kind**: user-doc
- **source_url_or_path**: binutils `ld` "Options"
- **version_sensitivity**: stable
- **oracle_mapping**: `readelf -d` 含 `BIND_NOW`, `readelf -l` 含 `GNU_RELRO`.

### INV-HARD-L03 — `-Wl,-z,now` (full RELRO + bind-now)

- **statement**: `-z now` 让 ld.so 在加载时立即解析所有动态符号, 而非延迟绑定. 与 `-z relro` 配合即 *full RELRO*: GOT 在程序开始执行前已只读. 性能微损但安全提升明显.
- **linker**: binutils, lld
- **source_kind**: user-doc
- **source_url_or_path**: binutils `ld` "Options"
- **version_sensitivity**: stable
- **oracle_mapping**: `readelf -d` 含 `FLAGS_1: NOW`.

## 4. ELF 元数据 (聚合)

### INV-HARD-M01 — `-fhardened` 留下的 ELF 标记

- **statement**: 启用 `-fhardened` 后产物特征:
  - ELF type `DYN` (PIE)
  - `GNU_RELRO` 段存在
  - dyn `FLAGS_1: NOW`
  - `.note.gnu.property` 含 `IBT`, `SHSTK` (x86) 或 `BTI`, `PAC`, `GCS` (aarch64, 视具体子 flag 设置)
  - canary 符号 (`__stack_chk_guard`) 引用
  这些可作为 *构建是否真的启 hardened* 的外观校验.
- **compiler + linker**
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: CI 前置门禁: 构建后 `readelf -ldn` 自动检验.

## 5. 运行时

### INV-HARD-R01 — 子 flag 各自的运行时信号

- **statement**: `-fhardened` 自身无 runtime; 各子 flag 的运行时信号见专文:
  - SP: `__stack_chk_fail` -> `SIGABRT` (134)
  - SCP: 大栈分配触发 `SIGSEGV`
  - FORT: `__chk_fail` -> `SIGABRT` (134)
  - IBT/SHSTK: `SIGSEGV` + `SEGV_CPERR`
- **runtime**: 各子机制
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: oracle 综合判定.

## 6. 与其他机制交互

### INV-HARD-I01 — `-fhardened` + sanitizers

- **statement**: sanitizers (`-fsanitize=...`) 可与 `-fhardened` 同启, 不冲突. 但 ASan/MSan/TSan 自身有性能开销; release 不并用.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/sanitizers.md`
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-HARD-I02 — `-fhardened` + LTO

- **statement**: LTO 兼容. 子 flag 在 LTO 下行为见各专文.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 矩阵.

### INV-HARD-I03 — `-fhardened` + AArch64 hardening

- **statement**: AArch64 上 `-fhardened` 不开 BTI/PAC/SCS/GCS. 用户应叠加 `-mbranch-protection=standard+gcs` (GCC 14+) 与 `-fsanitize=shadow-call-stack` (老 CPU) 完成 AArch64 完整硬化.
- **compiler**: GCC, Clang
- **source_kind**: 设计
- **version_sensitivity**: target-specific
- **oracle_mapping**: AArch64 矩阵.

### INV-HARD-I04 — `-fhardened` + ZCUR / STRUB / HCFR

- **statement**: 这些"次世代"硬化机制不在 `-fhardened` 集合内 (因开销 / 实验性). DeFuzz 若关心这些, 必须显式追加.
- **compiler**: GCC
- **source_kind**: 设计
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

## 7. 验证与已知回归

### INV-HARD-VER-DPKG — 发行版自定义 `-fhardened` 集合

- **statement**: Debian / Ubuntu / Fedora 在 dpkg-buildflags / RPM macros 中自定义 hardening flags 集合, 可能与 GCC `-fhardened` 不完全一致 (例: Ubuntu 24.04 已包含 BTI / PAC). DeFuzz 跑发行版包时必须 record 实际 build flags.
- **runtime**: 发行版构建系统
- **source_kind**: user-doc
- **source_url_or_path**: `man dpkg-buildflags` ; `redhat-rpm-config`
- **version_sensitivity**: target-specific
- **oracle_mapping**: 跑前 `dpkg-buildflags --get CFLAGS` audit.

### INV-HARD-VER-GCC14-INTRO — 引入版本

- **statement**: `-fhardened` 是 GCC 14 新增. 老 GCC 不识别.
- **compiler**: GCC
- **version**: GCC 14+
- **source_kind**: source
- **version_sensitivity**: stable
- **oracle_mapping**: 老 GCC 反例.

### INV-HARD-VER-DRIFT — 子 flag 集合可能演化

- **statement**: 未来 GCC 主版本可能增减 `-fhardened` 子集 (例如加入 BTI/PAC). DeFuzz 跨版本对照时必须按精确 GCC 版本展开.
- **compiler**: GCC
- **source_kind**: user-doc
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 跨版本 audit.

## 8. DeFuzz Oracle 映射总表

| Invariant | Oracle 信号 | seed |
|---|---|---|
| INV-HARD-E01 | `-fhardened` 与显式子 flag 集等价 | 编译同一 seed 两种命令行对比产物 |
| INV-HARD-M01 | `readelf -ldn` 含全部 hardening 特征 | 任意 hardened 构建 |
| INV-HARD-L01 | `cat /proc/<pid>/maps` 主程序基址随运行变化 | 多次启动 |
| INV-HARD-L03 | `readelf -d` 含 `FLAGS_1: NOW` | 任意 hardened 构建 |
| INV-HARD-VER-DPKG | 发行版 audit | 真实发行版包 |

## 9. 开放问题

- **Clang 等价 flag**: 主线 Clang 是否引入 `-fhardened`? 需跟踪 Discourse / Phabricator.
- **`-fhardened` 跨平台扩展**: 当前主要 GNU/Linux x86_64; aarch64 / Windows / Darwin 集合差异.
- **未来 AArch64 加入 BTI/PAC/GCS**: 是 GCC 14 后续改进的高优先级.
- **次世代机制 (ZCUR / STRUB / HCFR / KCFI / Bounds Safety) 何时进 `-fhardened`**: 取决于成熟度.
- **发行版 / GCC `-fhardened` 集合分歧**: 长期维护性问题.

## 10. 使用建议

- 用户态 release 用 `-fhardened -fstack-clash-protection -mbranch-protection=standard` (AArch64 加 BTI/PAC).
- AArch64 / Linux 内核场景再叠加 KCFI / GCS / SCS.
- 发行版打包用各自的 hardening macro, 不直接用 `-fhardened`.
- DeFuzz CI 必须 record 完整展开后的 CFLAGS 与 ELF 特征, 不能只 record `-fhardened`.
- 跨 GCC 主版本升级时 audit 子集是否变化.

## 11. 索引: 全部 invariants 一览

| 简写 | 文档 |
|---|---|
| SP | `@/home/yall/project/de-fuzz/docs/invariants/stack-canary.md` |
| FORT | `@/home/yall/project/de-fuzz/docs/invariants/fortify-source.md` |
| CET-IBT | `@/home/yall/project/de-fuzz/docs/invariants/endbr-ibt.md` |
| SHSTK | `@/home/yall/project/de-fuzz/docs/invariants/shadow-stack.md` |
| SCP | `@/home/yall/project/de-fuzz/docs/invariants/stack-clash-protection.md` |
| SCK | `@/home/yall/project/de-fuzz/docs/invariants/stack-check.md` |
| BTI | `@/home/yall/project/de-fuzz/docs/invariants/bti.md` |
| PAC | `@/home/yall/project/de-fuzz/docs/invariants/pointer-authentication.md` |
| SCS | `@/home/yall/project/de-fuzz/docs/invariants/shadow-call-stack.md` |
| GCS | `@/home/yall/project/de-fuzz/docs/invariants/gcs.md` |
| CFI | `@/home/yall/project/de-fuzz/docs/invariants/cfi.md` |
| KCFI | `@/home/yall/project/de-fuzz/docs/invariants/kcfi.md` |
| SS | `@/home/yall/project/de-fuzz/docs/invariants/safestack.md` |
| Sanitizers | `@/home/yall/project/de-fuzz/docs/invariants/sanitizers.md` |
| SanCov | `@/home/yall/project/de-fuzz/docs/invariants/sancov.md` |
| AVI | `@/home/yall/project/de-fuzz/docs/invariants/auto-var-init.md` |
| ZCUR | `@/home/yall/project/de-fuzz/docs/invariants/zero-call-used-regs.md` |
| STRUB | `@/home/yall/project/de-fuzz/docs/invariants/strub.md` |
| HCFR | `@/home/yall/project/de-fuzz/docs/invariants/hcfr.md` |
| BS | `@/home/yall/project/de-fuzz/docs/invariants/bounds-safety.md` |
| STRP | `@/home/yall/project/de-fuzz/docs/invariants/structure-protection.md` |
| RV-CFI | `@/home/yall/project/de-fuzz/docs/invariants/riscv-cfi.md` |
| HARD | 本文 |
