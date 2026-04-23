# PPC64LE Cross-Compiler Build Guide (GCC 15.2.0)

本文档描述如何在当前仓库中构建带有覆盖率插桩和 CFG dump 的 **ppc64le / PowerPC64 Little Endian** GCC 15.2.0 交叉编译器，并说明 DeFuzz 新增的 canary 目标面。

## 概述

这是一个**交叉编译器**：

- 运行在 x86_64 主机上
- 生成 `powerpc64le-linux-gnu` 目标二进制
- 覆盖率插桩针对**编译器本身**（HOST）
- 目标 ABI 为 **ELFv2**
- 目标 CPU 基线至少为 **POWER8**
- 动态链接器为 `/lib64/ld64.so.2`

当前仓库中的主构建脚本为：

- `target_compilers/gcc-v15.2.0-ppc64le-cross-compile/build-gcc-instrumented.sh`

## 与现有目标的关系

PPC64LE 目录沿用和 `riscv64` / `loongarch64` 一样的组织方式：

- GCC / glibc / binutils / gmp / mpfr / mpc / isl / linux 源码目录通过符号链接复用
- 实际共享的源树来自 `target_compilers/gcc-v15.2.0-aarch64-cross-compile/`
- 但构建目录、安装目录、sysroot、日志目录都独立

## 目标后端与新增 CFG 面

本次 ppc64le canary 支持新增了两个后端对象到插桩白名单：

- `rs6000.o` → `gcc/gcc/config/rs6000/rs6000.cc`
- `rs6000-logue.o` → `gcc/gcc/config/rs6000/rs6000-logue.cc`

这两个文件分别覆盖：

- stack protector guard/fail 目标 hook
- ELFv2 栈帧布局、prologue / epilogue、寄存器保存区与 alloca / local 区域的关系

## 构建流程

PPC64LE 交叉编译器同样需要完整 8 阶段流程：

| 阶段 | 说明 |
|------|------|
| 1 | Host Tools (GMP, MPFR, MPC, ISL) |
| 2 | Binutils |
| 3 | Linux Headers |
| 4 | GCC Stage 1 |
| 5 | glibc Headers + CSUs |
| 6 | GCC Stage 2 + libgcc |
| 7 | Full glibc |
| 8 | Final GCC（含覆盖率和 CFG dump） |

## 系统依赖

```bash
sudo apt-get install build-essential flex bison texinfo \
    libgmp-dev libmpfr-dev libmpc-dev zlib1g-dev python3 \
    qemu-user qemu-user-static xz-utils
```

## 目录结构

```text
target_compilers/gcc-v15.2.0-ppc64le-cross-compile/
├── gcc/            -> 共享源码树（符号链接）
├── gmp/            -> 符号链接
├── mpfr/           -> 符号链接
├── mpc/            -> 符号链接
├── isl/            -> 符号链接
├── binutils/       -> 符号链接
├── glibc/          -> 符号链接
├── linux/          -> 符号链接
├── build-powerpc64le-linux-gnu/
├── install-powerpc64le-linux-gnu/
├── build-logs/
└── build-gcc-instrumented.sh
```

## 全量构建

```bash
cd target_compilers/gcc-v15.2.0-ppc64le-cross-compile

# 可选：确认共享源码目录存在
ls gcc gmp mpfr mpc isl binutils glibc linux >/dev/null

# 全量构建
JOBS=16 ./build-gcc-instrumented.sh
```

说明：

- `JOBS` 控制并行编译度
- 首次构建通常需要 1-2 小时甚至更久
- glibc 阶段会显式使用 `-mcpu=power8 -mabi=elfv2 -mno-gnu-attribute`
- bootstrap 阶段会把 `usr/lib` 内容镜像到 `lib64` 视图，避免 ppc64le 的 `ld64.so.2` / crt 搜索路径不一致

## 输出文件

| 产物 | 路径 |
|------|------|
| 交叉编译器 | `install-powerpc64le-linux-gnu/bin/powerpc64le-linux-gnu-gcc` |
| `xgcc` | `build-powerpc64le-linux-gnu/gcc-final-build/gcc/xgcc` |
| Sysroot | `install-powerpc64le-linux-gnu/powerpc64le-linux-gnu/libc/` |
| CFG dump | `build-powerpc64le-linux-gnu/gcc-final-build/gcc/*.015t.cfg` |
| 覆盖率数据 | `build-powerpc64le-linux-gnu/gcc-final-build/gcc/*.gcno` |

## 构建完成后应存在的 canary CFG 文件

PPC64LE canary 活动配置需要这些 `.cfg`：

- `cfgexpand.cc.015t.cfg`
- `function.cc.015t.cfg`
- `calls.cc.015t.cfg`
- `targhooks.cc.015t.cfg`
- `rs6000.cc.015t.cfg`
- `rs6000-logue.cc.015t.cfg`

对应 DeFuzz 配置文件：

- `configs/gcc-v15.2.0-ppc64le-canary.yaml`

## 增量重生成 `.cfg`

从仓库根目录执行：

```bash
REPO_ROOT=$(pwd)
PPC64LE_BUILD="$REPO_ROOT/target_compilers/gcc-v15.2.0-ppc64le-cross-compile/build-powerpc64le-linux-gnu/gcc-final-build/gcc"
PPC64LE_SRC="$REPO_ROOT/target_compilers/gcc-v15.2.0-ppc64le-cross-compile/gcc/gcc"

make -C "$PPC64LE_BUILD" -j8 \
  -W "$PPC64LE_SRC/cfgexpand.cc" \
  -W "$PPC64LE_SRC/function.cc" \
  -W "$PPC64LE_SRC/calls.cc" \
  -W "$PPC64LE_SRC/targhooks.cc" \
  -W "$PPC64LE_SRC/config/rs6000/rs6000.cc" \
  -W "$PPC64LE_SRC/config/rs6000/rs6000-logue.cc" \
  cfgexpand.o function.o calls.o targhooks.o rs6000.o rs6000-logue.o

make -C "$PPC64LE_BUILD" -j8 cc1
```

关键点：

- `rs6000-logue.cc` 不是 generic 中端文件，必须单独进白名单 / 单独重编
- 对象重编后**必须重链 `cc1`**

## 验证命令

```bash
ls target_compilers/gcc-v15.2.0-ppc64le-cross-compile/build-powerpc64le-linux-gnu/gcc-final-build/gcc/{cfgexpand.cc.015t.cfg,function.cc.015t.cfg,calls.cc.015t.cfg,targhooks.cc.015t.cfg,rs6000.cc.015t.cfg,rs6000-logue.cc.015t.cfg}
```

## 使用交叉编译器

```bash
export PATH="/path/to/install-powerpc64le-linux-gnu/bin:$PATH"
SYSROOT="/path/to/install-powerpc64le-linux-gnu/powerpc64le-linux-gnu/libc"

powerpc64le-linux-gnu-gcc \
  --sysroot="$SYSROOT" \
  -mcpu=power8 -mabi=elfv2 \
  -o test test.c

file test
# 期望输出: ELF 64-bit LSB executable, PowerPC or cisco 7500, version 1 (SYSV), ...

qemu-ppc64le -L "$SYSROOT" ./test
```

## 与 DeFuzz 集成

当前 ppc64le canary 配置请参考：

- `configs/gcc-v15.2.0-ppc64le-canary.yaml`
- `initial_seeds/ppc64le/canary/function_template.c`
- `initial_seeds/ppc64le/canary/understanding.md`

## 常见问题

### Q: glibc configure 报 POWER8 or newer is required on powerpc64le

说明编译器目标 CPU 不是 POWER8 基线。检查：

- GCC configure 是否带 `--with-cpu=power8`
- glibc 构建是否带 `-mcpu=power8`

### Q: glibc configure 报缺少 `-mno-gnu-attribute`

PPC64LE glibc 会检查这个选项。确保 glibc 阶段的 `CC` 带：

```bash
-mno-gnu-attribute
```

### Q: 只有 generic `.cfg`，没有 `rs6000.cc.015t.cfg` / `rs6000-logue.cc.015t.cfg`

通常原因是：

1. `Makefile.in` 白名单没有 `rs6000.o` / `rs6000-logue.o`
2. 对应对象没有被重编
3. 重编后没有重链 `cc1`

### Q: 运行目标程序时找不到动态链接器

PPC64LE glibc 动态链接器是：

```text
/lib64/ld64.so.2
```

运行时请确保：

```bash
qemu-ppc64le -L "$SYSROOT" ./test
```
