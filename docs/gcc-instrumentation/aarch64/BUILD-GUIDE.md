# AArch64 Cross-Compiler Build Guide

本文档描述如何构建带有覆盖率插桩和 CFG dump 的 AArch64 交叉编译器。

## 概述

这是一个**交叉编译器**：
- 运行在 x86_64 主机上
- 生成 AArch64 (ARM64) 目标二进制
- 覆盖率插桩针对**编译器本身**（HOST），而非生成的目标程序

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
    libgmp-dev libmpfr-dev libmpc-dev zlib1g-dev
```

### 获取 ARM GNU Toolchain 源码

```bash
# 从 ARM 官网下载
wget https://developer.arm.com/-/media/Files/downloads/gnu/12.2.rel1/srcrel/arm-gnu-toolchain-src-snapshot-12.2.rel1.tar.xz
tar xf arm-gnu-toolchain-src-snapshot-12.2.rel1.tar.xz
```

## 应用插桩补丁

修改源码中的 `gcc/Makefile.in`：

```bash
# 补丁位置
arm-gnu-toolchain-src-snapshot-12.2.rel1/arm-gnu-toolchain-src-snapshot-12.2.rel1/gcc/Makefile.in

# 参考 doc/gcc-instrumentation/Makefile.in.patch
# 在三个位置添加 FUZZ-COVERAGE-INSTRUMENTATION 标记的代码块
```

## 构建步骤

```bash
# 1. 准备工作目录
cd /path/to/workspace

# 2. 验证补丁已应用
grep -q "FUZZ-COVERAGE-INSTRUMENTATION" \
    arm-gnu-toolchain-src-snapshot-12.2.rel1/arm-gnu-toolchain-src-snapshot-12.2.rel1/gcc/Makefile.in

# 3. 运行构建脚本
./build-gcc-instrumented.sh \
    ./arm-gnu-toolchain-src-snapshot-12.2.rel1 \
    ./build-aarch64 \
    ./install-aarch64
```

构建时间约 1-2 小时。

## 输出文件

构建完成后，关键文件位置：

| 文件 | 位置 |
|------|------|
| 交叉编译器 | `install-aarch64/bin/aarch64-none-linux-gnu-gcc` |
| CFG dump | `build-aarch64/gcc-final-build/gcc/cfgexpand.cc.015t.cfg` |
| 覆盖率数据 | `build-aarch64/gcc-final-build/gcc/cfgexpand.gcno` |
| Sysroot | `install-aarch64/aarch64-none-linux-gnu/libc/` |

## 目录结构

```
workspace/
├── arm-gnu-toolchain-src-snapshot-12.2.rel1/    # 源码
│   └── arm-gnu-toolchain-src-snapshot-12.2.rel1/
│       └── gcc/
│           └── Makefile.in                      # 已修改
├── build-aarch64/                               # 构建目录
│   ├── host-tools/                              # GMP, MPFR 等
│   ├── binutils-build/
│   ├── gcc1-build/
│   ├── gcc2-build/
│   ├── glibc-build/
│   └── gcc-final-build/                         # 含 CFG 文件
│       └── gcc/
│           ├── xgcc
│           ├── cfgexpand.cc.015t.cfg
│           └── cfgexpand.gcno
├── install-aarch64/                             # 安装目录
│   ├── bin/
│   │   └── aarch64-none-linux-gnu-gcc
│   └── aarch64-none-linux-gnu/
│       └── libc/                                # Sysroot
├── build-logs/
└── build-gcc-instrumented.sh
```

## 使用交叉编译器

```bash
# 设置环境
export PATH="/path/to/install-aarch64/bin:$PATH"
SYSROOT="/path/to/install-aarch64/aarch64-none-linux-gnu/libc"

# 编译测试程序
aarch64-none-linux-gnu-gcc --sysroot=$SYSROOT -o test test.c

# 验证输出
file test
# 输出: ELF 64-bit LSB executable, ARM aarch64, ...
```

## glibc 构建注意事项

glibc 必须使用交叉编译器构建，否则会产生架构不匹配错误：

```bash
# 正确方式：显式指定交叉编译工具链
CC="${TARGET}-gcc" \
CXX="${TARGET}-g++" \
AR="${TARGET}-ar" \
RANLIB="${TARGET}-ranlib" \
./configure --host=${TARGET} ...
```

## 常见问题

### Q: glibc 构建失败，架构不匹配

错误信息：
```
links-dso-program.o: incompatible target x86_64-pc-linux-gnu
```

解决方案：确保 glibc configure 时设置了 CC/CXX/AR/RANLIB 环境变量。

### Q: install-gcc 失败，缺少 libbacktrace

错误信息：
```
No rule to make target '../libbacktrace/.libs/libbacktrace.a'
```

解决方案：在 install-gcc 前手动构建 libbacktrace：

```bash
cd gcc-final-build
mkdir -p libbacktrace && cd libbacktrace
/path/to/source/libbacktrace/configure --host=x86_64-pc-linux-gnu
make -j$(nproc)
cd ..
make install-gcc
```

### Q: CFG 文件未生成

检查构建日志中 cfgexpand.cc 的编译命令：

```bash
grep "cfgexpand.cc" build-logs/gcc-final-build.log | grep fdump
```

如果没有 `-fdump-tree-cfg-lineno`，说明 Makefile.in 补丁未正确应用。
