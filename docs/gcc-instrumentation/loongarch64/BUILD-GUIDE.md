# LoongArch64 Cross-Compiler Build Guide (GCC 15.2.0)

本文档描述如何构建带有覆盖率插桩和 CFG dump 的 LoongArch64 交叉编译器，并补充当前仓库已经验证过的多文件 CFG 与增量重生成流程。

> **注意**: 本构建脚本基于 `target_compilers/gcc-v15.2.0-aarch64-cross-compile` 的验证通过流程改编。

## 概述

这是一个**交叉编译器**：

- 运行在 x86_64 主机上
- 生成 LoongArch64 目标二进制
- 覆盖率插桩针对**编译器本身**（HOST），而非生成的目标程序

当前仓库中的主构建路径是：

- `target_compilers/gcc-v15.2.0-loongarch64-cross-compile/build-gcc-instrumented.sh`

## 与 AArch64 的差异

| 项目 | AArch64 | LoongArch64 |
|------|---------|-------------|
| Target Triplet | `aarch64-none-linux-gnu` | `loongarch64-unknown-linux-gnu` |
| Linux ARCH | `arm64` | `loongarch` |
| 动态链接器 | `ld-linux-aarch64.so.1` | `ld-linux-loongarch-lp64d.so.1` |
| ABI | LP64 | LP64D (硬浮点) |
| 最低内核版本 | 无限制 | 5.19+ |
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
sudo apt-get install build-essential flex bison texinfo \
    libgmp-dev libmpfr-dev libmpc-dev zlib1g-dev python3 \
    qemu-user qemu-user-static xz-utils
```

### QEMU

```bash
qemu-loongarch64 --version
```

如果系统 QEMU 版本过低，可能需要从源码编译 QEMU >= 7.0。

## 目录结构

```text
target_compilers/gcc-v15.2.0-loongarch64-cross-compile/
├── gcc/
├── gmp/
├── mpfr/
├── mpc/
├── isl/
├── binutils/
├── glibc/
├── linux/
├── build-loongarch64-unknown-linux-gnu/
│   ├── host-tools/
│   ├── binutils-build/
│   ├── gcc1-build/
│   ├── gcc2-build/
│   ├── glibc-build/
│   └── gcc-final-build/
│       └── gcc/
├── install-loongarch64-unknown-linux-gnu/
├── build-logs/
└── build-gcc-instrumented.sh
```

## 全量构建步骤

```bash
cd target_compilers/gcc-v15.2.0-loongarch64-cross-compile

# 验证 Makefile.in 已打补丁
grep -q "FUZZ-COVERAGE-INSTRUMENTATION" gcc/gcc/Makefile.in && echo "Patch OK"

# 全量构建
JOBS=16 ./build-gcc-instrumented.sh
```

构建时间通常约 1-2 小时，取决于 CPU、磁盘和系统环境。

## 输出文件

| 文件 | 位置 |
|------|------|
| 交叉编译器 | `install-loongarch64-unknown-linux-gnu/bin/loongarch64-unknown-linux-gnu-gcc` |
| `xgcc` | `build-loongarch64-unknown-linux-gnu/gcc-final-build/gcc/xgcc` |
| CFG dump | `build-loongarch64-unknown-linux-gnu/gcc-final-build/gcc/*.015t.cfg` |
| 覆盖率数据 | `build-loongarch64-unknown-linux-gnu/gcc-final-build/gcc/*.gcno` |
| Sysroot | `install-loongarch64-unknown-linux-gnu/loongarch64-unknown-linux-gnu/libc/` |

## 当前已验证的多文件 CFG 面

当前仓库已经验证存在下列 LoongArch64 CFG 文件：

- `cfgexpand.cc.015t.cfg`
- `function.cc.015t.cfg`
- `calls.cc.015t.cfg`
- `targhooks.cc.015t.cfg`
- `builtins.cc.015t.cfg`
- `gimple-fold.cc.015t.cfg`
- `linux.cc.015t.cfg`
- `c-family/c-opts.cc.015t.cfg`

当前活动配置中：

- canary 使用 `cfgexpand/function/calls/targhooks`
- fortify 使用 `c-family/c-opts/builtins/gimple-fold/targhooks/linux`

注意：

- 当前还没有把 LoongArch64 后端专有源码对象加入活动白名单
- 如果未来要 target LoongArch64 后端专有函数，需要额外扩展白名单并重新生成 `.cfg`

## 多文件 CFG dump 提醒

如果你要让 DeFuzz 主动 target `cfgexpand.cc` 之外的源码文件，例如：

- `function.cc`
- `calls.cc`
- `targhooks.cc`
- `builtins.cc`
- `gimple-fold.cc`
- `c-family/c-opts.cc`
- `config/linux.cc`

那么这些文件都必须各自生成对应的 `.cfg` dump。

## 增量重生成 `.cfg`

从仓库根目录执行：

```bash
REPO_ROOT=$(pwd)
LOONG_BUILD="$REPO_ROOT/target_compilers/gcc-v15.2.0-loongarch64-cross-compile/build-loongarch64-unknown-linux-gnu/gcc-final-build/gcc"
LOONG_SRC="$REPO_ROOT/target_compilers/gcc-v15.2.0-loongarch64-cross-compile/gcc/gcc"

make -C "$LOONG_BUILD" -j8 \
  -W "$LOONG_SRC/cfgexpand.cc" \
  -W "$LOONG_SRC/function.cc" \
  -W "$LOONG_SRC/calls.cc" \
  -W "$LOONG_SRC/targhooks.cc" \
  -W "$LOONG_SRC/builtins.cc" \
  -W "$LOONG_SRC/gimple-fold.cc" \
  -W "$LOONG_SRC/c-family/c-opts.cc" \
  -W "$LOONG_SRC/config/linux.cc" \
  cfgexpand.o function.o calls.o targhooks.o builtins.o gimple-fold.o c-family/c-opts.o linux.o

make -C "$LOONG_BUILD" -j8 cc1
```

关键点：

- 这里只重建当前已验证使用的对象
- 对象重编后必须重链 `cc1`

## 验证命令

```bash
ls target_compilers/gcc-v15.2.0-loongarch64-cross-compile/build-loongarch64-unknown-linux-gnu/gcc-final-build/gcc/{cfgexpand.cc.015t.cfg,function.cc.015t.cfg,calls.cc.015t.cfg,targhooks.cc.015t.cfg,builtins.cc.015t.cfg,gimple-fold.cc.015t.cfg,linux.cc.015t.cfg,c-family/c-opts.cc.015t.cfg}
```

## 使用交叉编译器

```bash
export PATH="/path/to/install-loongarch64-unknown-linux-gnu/bin:$PATH"
SYSROOT="/path/to/install-loongarch64-unknown-linux-gnu/loongarch64-unknown-linux-gnu/libc"

# 编译测试程序
loongarch64-unknown-linux-gnu-gcc --sysroot="$SYSROOT" -o test test.c

# 验证输出
file test
# 输出: ELF 64-bit LSB executable, LoongArch, ...

# 使用 QEMU 运行
qemu-loongarch64 -L "$SYSROOT" ./test
```

## 与 DeFuzz 集成

当前项目配置请参考：

- `configs/gcc-v15.2.0-loongarch64-canary.yaml`
- `configs/gcc-v15.2.0-loongarch64-fortify.yaml`

## glibc 构建注意事项

glibc 有两个关键要求：

1. **必须使用交叉编译器**

```bash
CC="${TARGET}-gcc" \
CXX="${TARGET}-g++" \
AR="${TARGET}-ar" \
RANLIB="${TARGET}-ranlib" \
./configure --host=${TARGET} ...
```

2. **必须使用 `-O2` 优化**

```bash
CFLAGS="-O2" CXXFLAGS="-O2" make -j"$(nproc)"
```

## 常见问题

### Q: glibc 构建失败，提示 `cannot be compiled without optimization`

错误信息：

```text
#error "glibc cannot be compiled without optimization"
```

解决方案：确保 glibc 编译时使用 `-O2` 而不是 `-O0`。构建脚本会自动处理，但手工补构时要注意。

### Q: Linux headers 安装失败，找不到 loongarch 架构

确保使用的 Linux 内核版本 >= 5.19。

### Q: glibc 构建失败，架构不匹配

错误信息：

```text
links-dso-program.o: incompatible target x86_64-pc-linux-gnu
```

确保 glibc configure 时设置了正确的交叉编译工具链环境变量。

### Q: install-gcc 失败，缺少 libbacktrace

错误信息：

```text
No rule to make target '../libbacktrace/.libs/libbacktrace.a'
```

解决方案：在 `install-gcc` 前手动构建 libbacktrace：

```bash
cd gcc-final-build
mkdir -p libbacktrace && cd libbacktrace
/path/to/source/gcc/libbacktrace/configure --host=x86_64-pc-linux-gnu
make -j"$(nproc)"
cd ..
make install-gcc
```

### Q: CFG 文件未生成

检查构建日志中目标文件的编译命令：

```bash
grep "cfgexpand.cc" target_compilers/gcc-v15.2.0-loongarch64-cross-compile/build-logs/gcc-final-build.log | grep fdump
```

如果没有 `-fdump-tree-cfg-lineno`，说明 Makefile.in 补丁未正确应用，或对应对象未进入白名单。

### Q: LoongArch64 后端函数为什么还不能 CFG-guided target

因为当前活动白名单没有 LoongArch64 后端专有对象，所以虽然基础 canary/fortify 面已经可用，但后端专有函数还没有对应 `.cfg`。

### Q: QEMU 报错 `Exec format error`

先确认：

```bash
qemu-loongarch64 --version
```

再确认运行时使用的 `-L` sysroot 与编译时一致。

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
