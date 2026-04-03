# AArch64 Cross-Compiler Build Guide

本文档描述如何构建带有覆盖率插桩和 CFG dump 的 AArch64 GCC 15.2.0 交叉编译器，并补充当前仓库已经验证过的多文件 CFG 与增量重生成流程。

## 概述

这是一个**交叉编译器**：

- 运行在 x86_64 主机上
- 生成 AArch64 (ARM64) 目标二进制
- 覆盖率插桩针对**编译器本身**（HOST），而非生成的目标程序

当前仓库中的主构建路径是：

- `target_compilers/gcc-v15.2.0-aarch64-cross-compile/build-gcc-instrumented.sh`

这个脚本会自动下载 distfiles、展开源码、打补丁、完成 8 阶段交叉工具链构建，并在最终 GCC 阶段打开选择性覆盖率插桩和 CFG dump。

## 构建流程

AArch64 交叉编译器构建比原生编译器复杂，需要 8 个阶段：

| 阶段 | 说明 | 输出 |
|------|------|------|
| 1 | Host Tools (GMP, MPFR, MPC, ISL) | 编译器依赖库 |
| 2 | Binutils | 汇编器、链接器等 |
| 3 | Linux Headers | 内核头文件 |
| 4 | GCC Stage 1 | Bootstrap 编译器 |
| 5 | glibc Headers + CSUs | C 库头文件和启动代码 |
| 6 | GCC Stage 2 + libgcc | 运行时库 |
| 7 | Full glibc | 完整 C 库 |
| 8 | Final GCC | 最终编译器（含覆盖率和 CFG dump） |

## 前置要求

### 系统依赖

```bash
# Ubuntu/Debian
sudo apt-get install build-essential flex bison texinfo \
    libgmp-dev libmpfr-dev libmpc-dev zlib1g-dev python3 \
    wget curl xz-utils
```

可选但常用：

```bash
# 用于运行 AArch64 目标程序
sudo apt-get install qemu-user qemu-user-static
```

### 网络与磁盘

- 首次全量构建会自动下载 GCC、Binutils、glibc、Linux headers 等 distfiles
- 需要较稳定的网络
- 建议预留几十 GB 磁盘空间

### Makefile.in 插桩补丁

当前仓库的构建脚本会自动检查 `gcc/gcc/Makefile.in` 中是否已经包含 `FUZZ-COVERAGE-INSTRUMENTATION` 标记。

补丁说明文档：

- `docs/gcc-instrumentation/Makefile.in.patch`

如果你手工维护源码树，核心要求仍然是：

1. 对白名单对象加覆盖率插桩
2. 对同一批对象加 `-fdump-tree-cfg-lineno`
3. 链接阶段补上 `-lgcov --coverage`

## 当前仓库目录结构

```text
target_compilers/gcc-v15.2.0-aarch64-cross-compile/
├── build-gcc-instrumented.sh
├── gcc/
├── gmp/
├── mpfr/
├── mpc/
├── isl/
├── binutils/
├── glibc/
├── linux/
├── distfiles/
├── build-aarch64-none-linux-gnu/
│   ├── host-tools/
│   ├── binutils-build/
│   ├── gcc1-build/
│   ├── gcc2-build/
│   ├── glibc-build/
│   └── gcc-final-build/
│       └── gcc/
├── install-aarch64-none-linux-gnu/
└── build-logs/
```

## 全量构建步骤

直接使用仓库内现成脚本：

```bash
cd target_compilers/gcc-v15.2.0-aarch64-cross-compile

# 可选：确认补丁已经存在
grep -q "FUZZ-COVERAGE-INSTRUMENTATION" gcc/gcc/Makefile.in && echo "Patch OK"

# 全量构建
JOBS=16 ./build-gcc-instrumented.sh
```

说明：

- `JOBS` 控制并行编译度
- 不设置时脚本默认使用 `nproc`
- distfiles 已存在时会自动复用缓存
- 失败后通常可以直接重跑脚本继续

构建时间通常约 1-2 小时，取决于 CPU、磁盘和网络。

## 输出文件

构建完成后，关键文件位置：

| 文件 | 位置 |
|------|------|
| 交叉编译器 | `install-aarch64-none-linux-gnu/bin/aarch64-none-linux-gnu-gcc` |
| `xgcc` | `build-aarch64-none-linux-gnu/gcc-final-build/gcc/xgcc` |
| CFG dump | `build-aarch64-none-linux-gnu/gcc-final-build/gcc/*.015t.cfg` |
| 覆盖率数据 | `build-aarch64-none-linux-gnu/gcc-final-build/gcc/*.gcno` |
| Sysroot | `install-aarch64-none-linux-gnu/aarch64-none-linux-gnu/libc/` |

## 当前已验证的多文件 CFG 面

当前仓库已经验证存在下列 AArch64 CFG 文件：

- `cfgexpand.cc.015t.cfg`
- `function.cc.015t.cfg`
- `calls.cc.015t.cfg`
- `targhooks.cc.015t.cfg`
- `builtins.cc.015t.cfg`
- `gimple-fold.cc.015t.cfg`
- `linux.cc.015t.cfg`
- `aarch64.cc.015t.cfg`
- `c-family/c-opts.cc.015t.cfg`

实际活动配置中：

- canary 使用 `cfgexpand/function/calls/targhooks/aarch64`
- fortify 使用 `c-family/c-opts/builtins/gimple-fold/targhooks/linux`

## 多文件 CFG dump 提醒

如果你要让 DeFuzz 主动 target `cfgexpand.cc` 之外的源码文件，例如：

- `function.cc`
- `calls.cc`
- `targhooks.cc`
- `builtins.cc`
- `gimple-fold.cc`
- `c-family/c-opts.cc`
- `config/linux.cc`
- `config/aarch64/aarch64.cc`

那么这些文件都必须各自生成对应的 `.cfg` dump。

规则是：

- 一个要主动 target 的编译器源码文件
- 对应一个被白名单选中的对象文件
- 对应一个生成出来的 `.cfg`
- 对应一个写入 `cfg_file_paths` 的路径

缺一不可。

## 增量重生成 `.cfg`

如果只是补充新的 target 源文件，通常不需要全量重建整个交叉工具链。可以只重编相关对象，然后重链 `cc1`。

从仓库根目录执行：

```bash
REPO_ROOT=$(pwd)
AARCH64_BUILD="$REPO_ROOT/target_compilers/gcc-v15.2.0-aarch64-cross-compile/build-aarch64-none-linux-gnu/gcc-final-build/gcc"
AARCH64_SRC="$REPO_ROOT/target_compilers/gcc-v15.2.0-aarch64-cross-compile/gcc/gcc"

make -C "$AARCH64_BUILD" -j8 \
  -W "$AARCH64_SRC/cfgexpand.cc" \
  -W "$AARCH64_SRC/function.cc" \
  -W "$AARCH64_SRC/calls.cc" \
  -W "$AARCH64_SRC/targhooks.cc" \
  -W "$AARCH64_SRC/builtins.cc" \
  -W "$AARCH64_SRC/gimple-fold.cc" \
  -W "$AARCH64_SRC/c-family/c-opts.cc" \
  -W "$AARCH64_SRC/config/linux.cc" \
  -W "$AARCH64_SRC/config/aarch64/aarch64.cc" \
  cfgexpand.o function.o calls.o targhooks.o builtins.o gimple-fold.o c-family/c-opts.o linux.o aarch64.o

make -C "$AARCH64_BUILD" -j8 cc1
```

关键点：

- `-W <source-file>` 用来强制对应对象重编
- 最后的 `make ... cc1` 不能省略
- 只更新 `.o` 而不重链 `cc1`，运行时仍可能使用旧结果

## 验证命令

### 检查 CFG 是否齐全

```bash
ls target_compilers/gcc-v15.2.0-aarch64-cross-compile/build-aarch64-none-linux-gnu/gcc-final-build/gcc/{cfgexpand.cc.015t.cfg,function.cc.015t.cfg,calls.cc.015t.cfg,targhooks.cc.015t.cfg,builtins.cc.015t.cfg,gimple-fold.cc.015t.cfg,linux.cc.015t.cfg,aarch64.cc.015t.cfg,c-family/c-opts.cc.015t.cfg}
```

### 检查构建日志中的 CFG 参数

```bash
grep "cfgexpand.cc" target_compilers/gcc-v15.2.0-aarch64-cross-compile/build-logs/gcc-final-build.log | grep fdump
```

## 使用交叉编译器

```bash
export PATH="/path/to/install-aarch64-none-linux-gnu/bin:$PATH"
SYSROOT="/path/to/install-aarch64-none-linux-gnu/aarch64-none-linux-gnu/libc"

# 编译测试程序
aarch64-none-linux-gnu-gcc --sysroot="$SYSROOT" -o test test.c

# 验证输出
file test
# 期望输出: ELF 64-bit LSB executable, ARM aarch64, ...

# 使用 QEMU 运行
qemu-aarch64 -L "$SYSROOT" ./test
```

## 与 DeFuzz 集成

当前项目配置请参考：

- `configs/gcc-v15.2.0-aarch64-canary.yaml`
- `configs/gcc-v15.2.0-aarch64-fortify.yaml`

关键字段：

- `compiler.path`
- `compiler.cflags`
- `compiler.fuzz.qemu_path`
- `compiler.fuzz.qemu_sysroot`
- `compiler.fuzz.cfg_file_paths`
- `targets`

## glibc 构建注意事项

glibc 必须使用交叉编译器构建，否则会产生架构不匹配错误：

```bash
CC="${TARGET}-gcc" \
CXX="${TARGET}-g++" \
AR="${TARGET}-ar" \
RANLIB="${TARGET}-ranlib" \
./configure --host=${TARGET} ...
```

## 常见问题

### Q: glibc 构建失败，架构不匹配

错误信息：

```text
links-dso-program.o: incompatible target x86_64-pc-linux-gnu
```

解决方案：确保 glibc configure 时设置了 `CC/CXX/AR/RANLIB` 环境变量。

### Q: install-gcc 失败，缺少 libbacktrace

错误信息：

```text
No rule to make target '../libbacktrace/.libs/libbacktrace.a'
```

解决方案：在 `install-gcc` 前手动构建 libbacktrace：

```bash
cd gcc-final-build
mkdir -p libbacktrace && cd libbacktrace
/path/to/source/libbacktrace/configure --host=x86_64-pc-linux-gnu
make -j"$(nproc)"
cd ..
make install-gcc
```

### Q: CFG 文件未生成

检查构建日志中目标文件的编译命令：

```bash
grep "cfgexpand.cc" target_compilers/gcc-v15.2.0-aarch64-cross-compile/build-logs/gcc-final-build.log | grep fdump
```

如果没有 `-fdump-tree-cfg-lineno`，说明 Makefile.in 补丁未正确应用，或者对应对象未在白名单中。

### Q: 只有 `cfgexpand.cc.015t.cfg`

说明其它目标文件没有被加入白名单，或者没有触发对应对象重编。按“增量重生成 `.cfg`”章节补做对象重编和 `cc1` 重链。

### Q: 对象已经重编，但行为没变

最常见原因是没有重链 `cc1`。只更新 `.o` 不会自动保证运行时用到的新对象内容。
