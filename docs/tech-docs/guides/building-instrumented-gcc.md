---
title: Building Instrumented GCC
description: 如何为 fuzz 目标构建带覆盖率插桩与 CFG dump 的 GCC，及多 ISA 的脚本入口
priority: HIGH
last_updated: 2026-05-08
status: IMPLEMENTED
related_docs:
  - ../gcc-instrumentation/README.md
  - ../architecture/gcc-pipeline.md
  - ../architecture/decisions/002-multi-cfg-orchestration.md
---

# Building Instrumented GCC

DeFuzz 的"被测目标"是 **被插桩的 GCC 自身**。本指南是入口页：列出针对各 ISA 的构建脚本 + 关键步骤；详细补丁解释与构建脚本细节在 `@/home/yall/project/de-fuzz/docs/tech-docs/gcc-instrumentation/`。

## 1. 选择 ISA

| ISA | 路径 | 备注 |
| --- | --- | --- |
| x86_64 (native) | `docs/tech-docs/gcc-instrumentation/x64/build-gcc-instrumented.sh` | 最简单的场景；GCC 12.2.0 已测；不需 QEMU |
| AArch64 (cross) | `docs/tech-docs/gcc-instrumentation/aarch64/build-gcc-instrumented.sh` | 8 阶段交叉编译；插桩仅作用于最后一阶段的 host 编译器 |
| LoongArch64 | `docs/tech-docs/gcc-instrumentation/loongarch64/` | 同 AArch64 模式 |
| RISC-V64 | `docs/tech-docs/gcc-instrumentation/riscv64/` | 同 AArch64 模式 |

每个目录都有 `BUILD-GUIDE.md` 给出该 ISA 的具体步骤；本指南只覆盖共性。

## 2. 三件不变的事

补丁做的事在 `docs/tech-docs/gcc-instrumentation/Makefile.in.patch` 全文有解释，要点：

1. **白名单插桩**：只为 `cfgexpand.o` 注入 `-fprofile-arcs -ftest-coverage`，避免全量插桩的体积/性能爆炸。
2. **CFG dump**：同一份 cfgexpand.o 同时拿到 `-fdump-tree-cfg-lineno`，构建期产出 `cfgexpand.cc.015t.cfg` 文件。
3. **链接 flag**：`ALL_LINKERFLAGS += $(COVERAGE_LDFLAGS)` 让带 `-fprofile-arcs` 的 object 能正确链接到 `__gcov_*` 符号。

详见 `@/home/yall/project/de-fuzz/docs/tech-docs/architecture/gcc-pipeline.md` §2。

## 3. 多 CFG 时的扩展

对于跨多个源文件的防御（fortify、CFI、shadow-stack 等），需要把多个文件加进 CFG dump 白名单。当前补丁的 `CFG_DUMP_WHITELIST` 只列了 `cfgexpand.o`；如要扩展：

```makefile
CFG_DUMP_WHITELIST = cfgexpand.o function.o builtins.o gimple-fold.o
COVERAGE_WHITELIST = cfgexpand.o function.o builtins.o gimple-fold.o
```

并相应更新 YAML：

```yaml
compiler:
  fuzz:
    cfg_file_paths:
      - /path/to/build/gcc/cfgexpand.cc.015t.cfg
      - /path/to/build/gcc/function.cc.015t.cfg
      - /path/to/build/gcc/builtins.cc.015t.cfg
      - /path/to/build/gcc/gimple-fold.cc.015t.cfg
```

设计约束与历史评估：`@/home/yall/project/de-fuzz/docs/tech-docs/architecture/decisions/002-multi-cfg-orchestration.md`。

## 4. 验证产物

构建结束应当看到：

```
<build>/gcc/xgcc                                # fuzzer 调用的入口
<build>/gcc/cfgexpand.gcno                      # 编译期结构数据 (不变)
<build>/gcc/cfgexpand.cc.015t.cfg               # CFG dump (analyzer 输入)
<build>/gcc/cfgexpand.cc.gcov-notes             # 调试用 (可忽略)
```

跑一次 seed 之后会产生 `cfgexpand.gcda`（运行期命中数据），fuzzer 每轮 `Prepare()` 时会清理它。

## 5. 与 fuzz 配置的对应

```yaml
compiler:
  path:               <build>/gcc/xgcc
  gcovr_exec_path:    <build>                          # gcovr 在此执行，扫 .gcda
  source_parent_path: <gcc-source>                     # gcovr -r 的相对锚
  gcovr_command:      'gcovr --exclude ".*\.(h|hpp|hxx)$" --gcov-executable "gcov-14 --demangled-names" -r ..'
  fuzz:
    cfg_file_path:    <build>/gcc/cfgexpand.cc.015t.cfg
```

字段语义全表见 `@/home/yall/project/de-fuzz/docs/tech-docs/reference/config-schema.md`。

## 6. 故障域速查

| 现象 | 排查 |
| --- | --- |
| 构建脚本立即拒绝运行 | 源码未打补丁；脚本会 `grep -q "FUZZ-COVERAGE-INSTRUMENTATION"` |
| `.cfg` 文件没生成 | 看构建日志里 `cfgexpand.cc` 编译命令是否含 `-fdump-tree-cfg-lineno` |
| 链接报 `undefined reference to __gcov_*` | 链接 flag 漏了；检查 patch §3 是否生效 |
| `xgcc` 跑 seed 后 `.gcda` 不更新 | 构建用了 bootstrap，第二阶段把插桩的 stage1 覆盖了；用 `--disable-bootstrap` 重建 |

更深入的细节请直接读 `@/home/yall/project/de-fuzz/docs/tech-docs/gcc-instrumentation/README.md` 与对应 ISA 子目录的 `BUILD-GUIDE.md`。
