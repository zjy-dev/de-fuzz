# RISC-V 64 GCC 15.2.0 插桩交叉编译器构建指南

## 概述

本指南说明如何构建用于 RISC-V 64 的 GCC 15.2.0 插桩交叉编译器。

- **目标架构**: riscv64-unknown-linux-gnu
- **ABI**: lp64d (64位整数，双精度浮点)
- **动态链接器**: ld-linux-riscv64-lp64d.so.1

## 依赖

复用 `gcc-v15.2.0-loongarch64-cross-compile/` 中的源码（通过软链接）：
- GCC 15.2.0（已应用 FUZZ-COVERAGE-INSTRUMENTATION 补丁）
- GMP, MPFR, MPC, ISL, Binutils 2.44, glibc 2.40, Linux headers

## 构建步骤

```bash
cd target_compilers/gcc-v15.2.0-riscv64-cross-compile
./build-gcc-instrumented.sh
```

## 构建输出

| 产物 | 路径 |
|------|------|
| 交叉编译器 | `install-riscv64-unknown-linux-gnu/bin/riscv64-unknown-linux-gnu-gcc` |
| xgcc (直接调用) | `build-riscv64-unknown-linux-gnu/gcc-final-build/gcc/xgcc` |
| Sysroot | `install-riscv64-unknown-linux-gnu/riscv64-unknown-linux-gnu/libc/` |
| CFG dump | `build-riscv64-unknown-linux-gnu/gcc-final-build/gcc/cfgexpand.cc.015t.cfg` |
| 覆盖率数据 | `build-riscv64-unknown-linux-gnu/gcc-final-build/gcc/cfgexpand.gcno` |

## 使用方法

```bash
# 编译 RISC-V 程序
riscv64-unknown-linux-gnu-gcc \
  --sysroot=.../install-riscv64-unknown-linux-gnu/riscv64-unknown-linux-gnu/libc \
  -fstack-protector-strong -O0 -o test test.c

# 使用 QEMU 执行
qemu-riscv64 -L .../install-riscv64-unknown-linux-gnu/riscv64-unknown-linux-gnu/libc ./test
```

## 项目配置

参见 `configs/gcc-v15.2.0-riscv64-canary.yaml`
