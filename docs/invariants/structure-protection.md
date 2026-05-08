# Clang Structure Protection (STRP) Invariants

> 本文依据 `@/home/yall/project/de-fuzz/docs/invariants/gcc-llvm-defense-invariant-source-survey.md` 列出的一手信息源, 将 LLVM/Clang Structure Protection RFC / 设计文档中与 **STRP** 直接相关的 invariants 抽取归类.
>
> 机制简写与 survey: **STRP** = Clang Structure Protection. 实验性 UAF / type-safety 缓解, 通过 *字段重排序* + *deactivation symbols* 在堆对象生命周期结束后让攻击者难以通过类型混淆访问字段.

## 0. 术语与坐标

- **field reordering**: 链接期 / IR 期把 struct 字段重排, 隐藏布局; 攻击者获得对象指针后无法静态预测字段偏移.
- **deactivation symbol**: 对象 free 后, 写入特殊"deactivated" 标记字节, 后续访问触发 trap.
- **type-safety mitigation**: 阻止"通过 freed 对象的指针访问其字段, 错把它当作 *新分配的不同类型对象*"的攻击 (UAF + type confusion).
- **LTO 依赖**: 字段重排在跨 TU 时必须一致, 因此需 LTO 全程序闭包.
- **`-fstructure-protection=...`** (实验): 计划中的 Clang 选项.

每条 invariant 字段同前.

## 1. 启用条件

### INV-STRP-E01 — 实验性, 暂未上游 mainline

- **statement**: STRP 截至 2024 末仍是 LLVM Discourse RFC 阶段, 主线 Clang 不直接提供 `-fstructure-protection`. 需要 Apple toolchain / Google Pixel 内部分支或 RFC patch series.
- **compiler**: LLVM/Clang (实验)
- **version**: 计划 Clang 19+
- **source_kind**: RFC + user-doc
- **source_url_or_path**: https://clang.llvm.org/docs/StructureProtection.html ; https://discourse.llvm.org/c/clang/6 (Structure Protection 主题)
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: DeFuzz 暂不部署; 文档跟踪.

### INV-STRP-E02 — GCC 不实现

- **statement**: GCC 无对应机制.
- **compiler**: GCC (无)
- **source_kind**: user-doc
- **version_sensitivity**: stable
- **oracle_mapping**: 文档.

### INV-STRP-E03 — 与 LTO visibility 共享设计假设

- **statement**: STRP 假设 *跨 linkage unit 的类型不可继承* (类似 CFI), 需要 `lto_visibility_public` 显式标注公开类型. 不公开类型可 reorder.
- **compiler**: Clang
- **source_kind**: 设计
- **source_url_or_path**: https://clang.llvm.org/docs/LTOVisibility.html
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 同 CFI.

## 2. 编译期变换

### INV-STRP-C01 — 字段重排不破坏语言访问语义

- **statement**: 编译器在 IR / 链接期把 `struct S { int a; char b; long c; }` 的字段顺序打乱, 但保持源码访问 (`s.a` / `s.b` / `s.c`) 行为正确. 通过编译器维护 *逻辑 -> 物理* 偏移映射.
- **compiler**: Clang
- **source_kind**: 设计
- **source_url_or_path**: STRP RFC
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 静态扫描 LLVM IR 内 GEP 偏移随机化.

### INV-STRP-C02 — 仅对非公开类型重排

- **statement**: ABI-公开类型 (如 `std::pair`, glibc 头中暴露的 struct) 不可重排, 否则破坏接口. STRP 仅对 *本程序私有* 类型进行.
- **compiler**: Clang
- **source_kind**: 设计
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 文档.

### INV-STRP-C03 — Deactivation symbols 在 free 路径

- **statement**: 编译器在 `free(ptr)` / `delete obj` 等释放点, 插入"对象的所有字段写为 deactivation pattern" 的指令序列, 让后续访问明显异常. 一种"主动 poison".
- **compiler**: Clang
- **source_kind**: 设计
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 反例: free 后访问 -> 期望 SIGSEGV / SIGILL.

## 3. 运行时

### INV-STRP-R01 — 越界 / freed 访问 trap

- **statement**: 运行时若访问 deactivation 字节 (例如 0xDEADBEEF 或类似 magic), 行为不定 (segfault / 业务逻辑错). STRP 不强制 trap, 但常配合其他 sanitizer 验证.
- **runtime**: 不直接
- **source_kind**: 设计
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 文档.

## 4. 与其他机制交互

### INV-STRP-I01 — STRP + ASan

- **statement**: ASan 已在 freed 对象上 poison shadow, 与 STRP deactivation 概念重叠. 同启不冲突, ASan 提供更明确的报告.
- **compiler**: Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/sanitizers.md`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 矩阵.

### INV-STRP-I02 — STRP + CFI

- **statement**: CFI 检 vptr 类型一致, STRP 改字段偏移. 同启提供深度防御.
- **compiler**: Clang
- **source_kind**: 设计
- **source_url_or_path**: `@/home/yall/project/de-fuzz/docs/invariants/cfi.md`
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 矩阵.

## 5. 验证

### INV-STRP-VER-RFC — 全部 invariant 标 likely-to-drift

- **statement**: STRP 仍是 RFC 阶段, 所有 invariant 等待主线 merge 后稳定.
- **compiler**: LLVM/Clang (实验)
- **source_kind**: RFC
- **source_url_or_path**: STRP RFC
- **version_sensitivity**: likely-to-drift
- **oracle_mapping**: 暂不部署.

## 6. DeFuzz Oracle 映射

| Invariant | Oracle 信号 | seed | 状态 |
|---|---|---|---|
| INV-STRP-C01 | LLVM IR GEP 偏移随机化 | 静态扫描 | 待主线 merge |
| INV-STRP-C03 | free 后字段呈 deactivation pattern | UAF seed | 待主线 merge |

## 7. 开放问题

- **何时上游 main**: 取决于 RFC discussions 与 Apple 推进度.
- **GCC 是否跟进**: 暂无信息.
- **跨 DSO / LTO 边界 enforcement**: 与 CFI 类似, 跨 linkage unit 类型如何处理.
- **性能 / 兼容性**: 字段重排可能影响 cache 友好度; deactivation 增加 free 路径开销.
- **debugger 显示**: 字段名与逻辑/物理偏移映射对 gdb / lldb 透明吗?

## 8. 使用建议

- 当前 (2026) 仍 *实验性*, DeFuzz oracle 不主用; 关注 RFC 进度.
- 跟踪 LLVM Discourse "Structure Protection" 主题.
- 若 Apple toolchain 提供, 可作 macOS 路径独立 oracle.
