# RISC-V 64 GCC 15.2.0 插桩交叉编译器构建指南

本文档说明如何在当前仓库中构建用于 RISC-V 64 的 GCC 15.2.0 插桩交叉编译器，并补充多文件 CFG 与增量重生成流程。

## 概述

这是一个**交叉编译器**：

- 运行在 x86_64 主机上
- 生成 `riscv64-unknown-linux-gnu` 目标程序
- 覆盖率插桩针对**编译器本身**
- 目标 ABI 为 `lp64d`
- 动态链接器为 `ld-linux-riscv64-lp64d.so.1`

当前仓库的主构建脚本为：

- `target_compilers/gcc-v15.2.0-riscv64-cross-compile/build-gcc-instrumented.sh`

## 构建依赖

### 系统依赖

```bash
sudo apt-get install build-essential flex bison texinfo \
    libgmp-dev libmpfr-dev libmpc-dev zlib1g-dev python3 \
    qemu-user qemu-user-static xz-utils
```

### 源码组织方式

当前 RISC-V 构建目录复用了另一套 GCC 15.2.0 源码与依赖目录，通常通过软链接组织：

- `gcc`
- `gmp`
- `mpfr`
- `mpc`
- `isl`
- `binutils`
- `glibc`
- `linux`

正常仓库状态下这些路径已经准备好，一般不需要手工额外下载。

## 构建流程

RISC-V 交叉编译器也需要完整的 8 阶段交叉工具链流程：

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

## 全量构建

```bash
cd target_compilers/gcc-v15.2.0-riscv64-cross-compile

# 可选：确认关键源码目录存在
ls gcc gmp mpfr mpc isl binutils glibc linux >/dev/null

# 全量构建
JOBS=16 ./build-gcc-instrumented.sh
```

说明：

- `JOBS` 控制并行编译度
- 首次构建时间通常约 1-2 小时
- 构建失败后通常可直接重跑

## 目录结构

```text
target_compilers/gcc-v15.2.0-riscv64-cross-compile/
├── build-gcc-instrumented.sh
├── gcc/            -> 软链接或源码目录
├── gmp/            -> 软链接
├── mpfr/           -> 软链接
├── mpc/            -> 软链接
├── isl/            -> 软链接
├── binutils/       -> 软链接
├── glibc/          -> 软链接
├── linux/          -> 软链接
├── build-riscv64-unknown-linux-gnu/
├── install-riscv64-unknown-linux-gnu/
└── build-logs/
```

## 构建输出

| 产物 | 路径 |
|------|------|
| 交叉编译器 | `install-riscv64-unknown-linux-gnu/bin/riscv64-unknown-linux-gnu-gcc` |
| `xgcc` | `build-riscv64-unknown-linux-gnu/gcc-final-build/gcc/xgcc` |
| Sysroot | `install-riscv64-unknown-linux-gnu/riscv64-unknown-linux-gnu/libc/` |
| CFG dump | `build-riscv64-unknown-linux-gnu/gcc-final-build/gcc/*.015t.cfg` |
| 覆盖率数据 | `build-riscv64-unknown-linux-gnu/gcc-final-build/gcc/*.gcno` |

## 当前已验证的多文件 CFG 面

当前仓库已经验证存在下列 RISC-V CFG 文件：

- `cfgexpand.cc.015t.cfg`
- `function.cc.015t.cfg`
- `calls.cc.015t.cfg`
- `targhooks.cc.015t.cfg`
- `builtins.cc.015t.cfg`
- `gimple-fold.cc.015t.cfg`
- `linux.cc.015t.cfg`
- `riscv.cc.015t.cfg`
- `c-family/c-opts.cc.015t.cfg`

其中：

- canary 活动配置使用 `cfgexpand/function/calls/targhooks/riscv`
- fortify 活动配置使用 `c-family/c-opts/builtins/gimple-fold/targhooks/linux`

## 多文件 CFG dump 提醒

如果你要让 DeFuzz 主动 target `cfgexpand.cc` 之外的源码文件，例如：

- `function.cc`
- `calls.cc`
- `targhooks.cc`
- `builtins.cc`
- `gimple-fold.cc`
- `c-family/c-opts.cc`
- `config/linux.cc`
- `config/riscv/riscv.cc`

那么这些文件都必须各自生成对应的 `.cfg` dump。

规则仍然是：

- 一个要主动 target 的源码文件
- 对应一个白名单对象
- 对应一个生成出的 `.cfg`
- 对应一个写入 `cfg_file_paths` 的配置路径

## 增量重生成 `.cfg`

从仓库根目录执行：

```bash
REPO_ROOT=$(pwd)
RISCV_BUILD="$REPO_ROOT/target_compilers/gcc-v15.2.0-riscv64-cross-compile/build-riscv64-unknown-linux-gnu/gcc-final-build/gcc"
RISCV_SRC="$REPO_ROOT/target_compilers/gcc-v15.2.0-riscv64-cross-compile/gcc/gcc"

make -C "$RISCV_BUILD" -j8 \
  -W "$RISCV_SRC/cfgexpand.cc" \
  -W "$RISCV_SRC/function.cc" \
  -W "$RISCV_SRC/calls.cc" \
  -W "$RISCV_SRC/targhooks.cc" \
  -W "$RISCV_SRC/builtins.cc" \
  -W "$RISCV_SRC/gimple-fold.cc" \
  -W "$RISCV_SRC/c-family/c-opts.cc" \
  -W "$RISCV_SRC/config/linux.cc" \
  -W "$RISCV_SRC/config/riscv/riscv.cc" \
  cfgexpand.o function.o calls.o targhooks.o builtins.o gimple-fold.o c-family/c-opts.o linux.o riscv.o

make -C "$RISCV_BUILD" -j8 cc1
```

关键点：

- 对象重编后必须重链 `cc1`
- 否则运行时仍可能使用旧链接结果

## 验证命令

```bash
ls target_compilers/gcc-v15.2.0-riscv64-cross-compile/build-riscv64-unknown-linux-gnu/gcc-final-build/gcc/{cfgexpand.cc.015t.cfg,function.cc.015t.cfg,calls.cc.015t.cfg,targhooks.cc.015t.cfg,builtins.cc.015t.cfg,gimple-fold.cc.015t.cfg,linux.cc.015t.cfg,riscv.cc.015t.cfg,c-family/c-opts.cc.015t.cfg}
```

## 使用方法

```bash
# 编译 RISC-V 程序
riscv64-unknown-linux-gnu-gcc \
  --sysroot=target_compilers/gcc-v15.2.0-riscv64-cross-compile/install-riscv64-unknown-linux-gnu/riscv64-unknown-linux-gnu/libc \
  -o test test.c

# 使用 QEMU 执行
qemu-riscv64 -L target_compilers/gcc-v15.2.0-riscv64-cross-compile/install-riscv64-unknown-linux-gnu/riscv64-unknown-linux-gnu/libc ./test
```

## 与 DeFuzz 集成

当前项目配置请参考：

- `configs/gcc-v15.2.0-riscv64-canary.yaml`
- `configs/gcc-v15.2.0-riscv64-fortify.yaml`

## 常见问题

### Q: 构建脚本报缺少源码目录

先确认以下路径存在：

```bash
ls target_compilers/gcc-v15.2.0-riscv64-cross-compile/{gcc,gmp,mpfr,mpc,isl,binutils,glibc,linux}
```

如果不存在，需要恢复这些源码目录或软链接。

### Q: CFG 文件未生成

先检查日志中对应文件是否带 `-fdump-tree-cfg-lineno`：

```bash
grep "riscv.cc" target_compilers/gcc-v15.2.0-riscv64-cross-compile/build-logs/gcc-final-build.log | grep fdump
```

### Q: 对象已重编，但运行结果没变化

最常见原因是没有重链 `cc1`。

### Q: 目标程序无法运行

先确认：

- `qemu-riscv64` 已安装
- `-L` 指向正确 sysroot
- 编译时和运行时使用的是同一套 `install-riscv64-unknown-linux-gnu/.../libc`
