# LoongArch64 Cross-Compiler Build Guide (GCC 15.2.0)

本文档描述如何构建带有覆盖率插桩和 CFG dump 的 LoongArch64 交叉编译器。

> **注意**: 本构建脚本基于 `target_compilers/gcc-v15.2.0-aarch64-cross-compile` 的验证通过的构建流程改编。

## 概述

这是一个**交叉编译器**：
- 运行在 x86_64 主机上
- 生成 LoongArch64 目标二进制
- 覆盖率插桩针对**编译器本身**（HOST），而非生成的目标程序

## 与 AArch64 的差异

| 项目 | AArch64 | LoongArch64 |
|------|---------|-------------|
| Target Triplet | `aarch64-none-linux-gnu` | `loongarch64-unknown-linux-gnu` |
| Linux ARCH | `arm64` | `loongarch` |
| 动态链接器 | `ld-linux-aarch64.so.1` | `ld-linux-loongarch-lp64d.so.1` |
| ABI | LP64 | LP64D (硬浮点) |
| 最低内核版本 | 无限制 | 5.19+ (首次支持) |
| QEMU 命令 | `qemu-aarch64` | `qemu-loongarch64` |

## 构建流程

LoongArch64 交叉编译器构建需要 8 个阶段：

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
    libgmp-dev libmpfr-dev libmpc-dev zlib1g-dev python3
```

### QEMU (用于运行 LoongArch64 二进制)

```bash
# Ubuntu 22.04+ (需要 QEMU >= 7.0 才支持 LoongArch64)
sudo apt-get install qemu-user qemu-user-static
```

> **注意**: 如果系统 QEMU 版本过低，可能需要从源码编译 QEMU >= 7.0。

## 目录结构

```
target_compilers/gcc-v15.2.0-loongarch64-cross-compile/
├── gcc/                          # GCC 15.2.0 源码
│   └── gcc/
│       └── Makefile.in           # 已修改（选择性插桩）
├── gmp/                          # GMP 6.3.0
├── mpfr/                         # MPFR 4.2.1
├── mpc/                          # MPC 1.3.1
├── isl/                          # ISL 0.26
├── binutils/                     # Binutils 2.44
├── glibc/                        # glibc 2.40
├── linux/                        # Linux 6.6.70 (LTS)
├── build-loongarch64-unknown-linux-gnu/  # 构建目录
│   ├── host-tools/               # GMP, MPFR 等
│   ├── binutils-build/
│   ├── gcc1-build/
│   ├── gcc2-build/
│   ├── glibc-build/
│   └── gcc-final-build/          # 含 CFG 文件
│       └── gcc/
│           ├── xgcc
│           ├── cfgexpand.cc.015t.cfg
│           └── cfgexpand.gcno
├── install-loongarch64-unknown-linux-gnu/  # 安装目录
│   ├── bin/
│   │   └── loongarch64-unknown-linux-gnu-gcc
│   └── loongarch64-unknown-linux-gnu/
│       └── libc/                 # Sysroot
├── build-logs/
└── build-gcc-instrumented.sh
```

## 构建步骤

```bash
# 1. 进入目录
cd target_compilers/gcc-v15.2.0-loongarch64-cross-compile

# 2. 验证 Makefile.in 已打补丁
grep -q "FUZZ-COVERAGE-INSTRUMENTATION" gcc/gcc/Makefile.in && echo "Patch OK"

# 3. 运行构建脚本
./build-gcc-instrumented.sh
```

构建时间约 1-2 小时（取决于 CPU 核心数）。

## 输出文件

| 文件 | 位置 |
|------|------|
| 交叉编译器 | `install-loongarch64-unknown-linux-gnu/bin/loongarch64-unknown-linux-gnu-gcc` |
| CFG dump | `build-loongarch64-unknown-linux-gnu/gcc-final-build/gcc/cfgexpand.cc.015t.cfg` |
| 覆盖率数据 | `build-loongarch64-unknown-linux-gnu/gcc-final-build/gcc/cfgexpand.gcno` |
| Sysroot | `install-loongarch64-unknown-linux-gnu/loongarch64-unknown-linux-gnu/libc/` |

## 使用交叉编译器

```bash
# 设置环境
export PATH="/path/to/install-loongarch64-unknown-linux-gnu/bin:$PATH"
SYSROOT="/path/to/install-loongarch64-unknown-linux-gnu/loongarch64-unknown-linux-gnu/libc"

# 编译测试程序
loongarch64-unknown-linux-gnu-gcc --sysroot=$SYSROOT -o test test.c

# 验证输出
file test
# 输出: ELF 64-bit LSB executable, LoongArch, ...

# 使用 QEMU 运行
qemu-loongarch64 -L $SYSROOT ./test
```

## 与 de-fuzz 集成

配置示例 (`configs/gcc-v15.2.0-loongarch64-canary.yaml`):

```yaml
config:
  llm: "deepseek"
  isa: "loongarch64"
  strategy: "canary"
  log_level: "info"

  compiler:
    name: "gcc"
    version: "15.2.0"

fuzz:
  use_qemu: true
  qemu_path: "qemu-loongarch64"
  qemu_sysroot: "/path/to/install/loongarch64-unknown-linux-gnu/libc"

compiler:
  path: "/path/to/build/gcc-final-build/gcc/xgcc"
  sysroot: "/path/to/install/loongarch64-unknown-linux-gnu/libc"
  target_triplet: "loongarch64-unknown-linux-gnu"

coverage:
  gcno_dir: "/path/to/build/gcc-final-build/gcc/"
  gcda_dir: "/path/to/build/gcc-final-build/gcc/"
  source_file: "cfgexpand.cc"
  cfg_file_path: "/path/to/build/gcc-final-build/gcc/cfgexpand.cc.015t.cfg"
```

## glibc 构建注意事项

glibc 有两个关键要求：

1. **必须使用交叉编译器**：
```bash
CC="${TARGET}-gcc" \
CXX="${TARGET}-g++" \
AR="${TARGET}-ar" \
RANLIB="${TARGET}-ranlib" \
./configure --host=${TARGET} ...
```

2. **必须使用 -O2 优化**：glibc 不允许无优化编译
```bash
CFLAGS="-O2" CXXFLAGS="-O2" make -j$(nproc)
```

## 常见问题

### Q: glibc 构建失败，提示 "cannot be compiled without optimization"

错误信息：
```
#error "glibc cannot be compiled without optimization"
```

解决方案：确保 glibc 编译时使用 `-O2` 而不是 `-O0`。构建脚本已自动处理此问题。

### Q: Linux headers 安装失败，找不到 loongarch 架构

确保使用的 Linux 内核版本 >= 5.19，LoongArch 从该版本开始被内核支持。

### Q: glibc 构建失败，架构不匹配

错误信息：
```
links-dso-program.o: incompatible target x86_64-pc-linux-gnu
```

确保 glibc configure 时设置了正确的环境变量：

```bash
CC="${TARGET}-gcc" \
CXX="${TARGET}-g++" \
AR="${TARGET}-ar" \
RANLIB="${TARGET}-ranlib" \
./configure --host=${TARGET} ...
```

### Q: install-gcc 失败，缺少 libbacktrace

错误信息：
```
No rule to make target '../libbacktrace/.libs/libbacktrace.a'
```

解决方案：在 install-gcc 前手动构建 libbacktrace：

```bash
cd gcc-final-build
mkdir -p libbacktrace && cd libbacktrace
/path/to/source/gcc/libbacktrace/configure --host=x86_64-pc-linux-gnu
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

### Q: QEMU 报错 "Exec format error"

确保安装了 QEMU LoongArch64 支持：

```bash
# 检查 QEMU 是否支持 loongarch64
qemu-loongarch64 --version
```

如果没有，可能需要从源码编译 QEMU >= 7.0。

## LoongArch64 特殊说明

1. **ABI**: LoongArch64 使用 LP64D ABI（64位长整型和指针，硬件双精度浮点）
2. **指令集**: 支持 LA64v1.0 基础指令集
3. **SIMD**: 可选支持 LSX (128-bit) 和 LASX (256-bit)
4. **浮点**: 默认使用硬件浮点 (FPU64)

## 参考资料

- [GCC LoongArch Options](https://gcc.gnu.org/onlinedocs/gcc/LoongArch-Options.html)
- [LoongArch Documentation](https://loongson.github.io/LoongArch-Documentation/)
- [LoongArch Toolchain Conventions](https://loongson.github.io/LoongArch-Documentation/LoongArch-toolchain-conventions-EN.html)
- [Linux LoongArch Support](https://docs.kernel.org/arch/loongarch/introduction.html)
