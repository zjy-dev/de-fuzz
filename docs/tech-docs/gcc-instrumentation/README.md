---
title: GCC Instrumentation
description: de-fuzz 中带插桩 GCC 的产物布局与运行期使用约定
priority: HIGH
last_updated: 2026-05-24
status: IMPLEMENTED
related_docs:
  - ../guides/building-instrumented-gcc.md
  - ../architecture/gcc-pipeline.md
---

# GCC Instrumentation

本文档只说明 de-fuzz 当前采用的产物布局和运行期约定。各 ISA 的具体构建步骤见本目录下对应的 `BUILD-GUIDE.md`。

## 当前方案

- `build dir` 存放被测编译器本体和插桩产物
- `install dir` 存放 sysroot、目标运行库、安装后的交叉工具链
- fuzz 运行时实际调用的是 `build dir` 里的 `xgcc`
- `cfg`、`gcno`、`gcda` 都按 `build dir` 布局组织

推荐约定如下：

- `compiler.path` -> `<build>/.../gcc/xgcc`
- `--sysroot` / `qemu_sysroot` -> `<install>/.../libc`
- `cfg` / `gcno` / `gcda` -> `<build>/.../gcc/`

## 目录职责

### build dir

用于存放：

- `xgcc`
- `cc1` 等编译器内部组件
- `.cfg`
- `.gcno`
- `.gcda`

典型路径：

- x64: `<build>/gcc/xgcc`
- cross: `<build>/gcc-final-build/gcc/xgcc`

### install dir

用于存放：

- `<triplet>-gcc`
- sysroot
- `libgcc.a`
- 目标架构运行库
- QEMU `-L` 需要的根目录

典型路径：

- `<install>/bin/<triplet>-gcc`
- `<install>/<triplet>/libc/`

## 运行期推荐

当前不建议把 `install dir` 里的 `<triplet>-gcc` 作为 de-fuzz 的主入口。推荐继续使用：

- `build dir` 里的 `xgcc` 作为 `compiler.path`
- `install dir` 里的 sysroot 和 runtime libraries 作为辅助依赖

交叉编译常见配置：

- `compiler.path = <build>/gcc-final-build/gcc/xgcc`
- `--sysroot = <install>/<triplet>/libc`
- `-B<build>/gcc-final-build/gcc`
- `-B<install>/lib/gcc/<triplet>/<version>`
- `qemu_sysroot = <install>/<triplet>/libc`

## 文件位置

- `.cfg` 是构建期 dump，默认位于 `build dir`
- `.gcno` 是构建期产物，默认位于 `build dir`
- `.gcda` 是运行期产物，当前项目也按 `build dir` 收集

典型路径：

- x64: `<build>/gcc/cfgexpand.cc.015t.cfg`, `<build>/gcc/cfgexpand.gcno`, `<build>/gcc/cfgexpand.gcda`
- cross: `<build>/gcc-final-build/gcc/cfgexpand.cc.015t.cfg`, `<build>/gcc-final-build/gcc/cfgexpand.gcno`, `<build>/gcc-final-build/gcc/cfgexpand.gcda`

## 路径能否调整

项目读取路径可以通过配置修改：

- `compiler.path`
- `gcovr_exec_path`
- `fuzz.cfg_file_path`
- `fuzz.cfg_file_paths`
- `qemu_sysroot`

但 `cfg`、`gcno`、`gcda` 的生成位置默认仍受 GCC build tree 影响。当前最稳妥的做法是：

- 让 GCC 继续在 `build dir` 生成这些文件
- de-fuzz 通过配置读取它们
- 如果需要统一目录，再额外复制或建立链接
