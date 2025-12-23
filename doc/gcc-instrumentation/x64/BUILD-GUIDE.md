# GCC x64 Native Build Guide

本文档描述如何构建带有覆盖率插桩和 CFG dump 的 x86_64 原生 GCC 编译器。

## 概述

这是一个**原生编译器**：
- 运行在 x86_64 主机上
- 生成 x86_64 目标二进制
- 覆盖率插桩针对**编译器本身**的 `cfgexpand.cc` 模块

## 前置要求

### 系统依赖

```bash
# Ubuntu/Debian
sudo apt-get install build-essential flex bison \
    libgmp-dev libmpfr-dev libmpc-dev texinfo

# 覆盖率工具
pip install gcovr
```

### 获取 GCC 源码

```bash
# 从 GitHub 下载 GCC 12.2.0
wget https://github.com/gcc-mirror/gcc/archive/refs/tags/releases/gcc-12.2.0.tar.gz
tar xzf gcc-12.2.0.tar.gz
mv gcc-releases-gcc-12.2.0 gcc-source
```

## 应用插桩补丁

在构建前，需要修改 `gcc/Makefile.in`：

```bash
# 参考 doc/gcc-instrumentation/Makefile.in.patch
# 在三个位置添加 FUZZ-COVERAGE-INSTRUMENTATION 标记的代码块
```

关键修改点：
1. **Line ~163**: 添加 `COVERAGE_WHITELIST` 和 `CFLAGS-cfgexpand.o`
2. **Line ~1066**: 修改 `ALL_CFLAGS` 使用 `$(CFLAGS-$@)`
3. **Line ~1089**: 添加 `COVERAGE_LDFLAGS`

## 构建步骤

```bash
# 1. 进入工作目录
cd /path/to/workspace

# 2. 确保 GCC 源码已修改
grep -q "FUZZ-COVERAGE-INSTRUMENTATION" gcc-source/gcc/Makefile.in

# 3. 运行构建脚本
./build-gcc-instrumented.sh gcc-source gcc-build
```

构建时间约 30-60 分钟。

## 输出文件

构建完成后，关键文件位于 `gcc-build/gcc/`：

| 文件 | 说明 |
|------|------|
| `xgcc` | GCC 编译器可执行文件 |
| `cfgexpand.cc.015t.cfg` | CFG dump 文件 |
| `cfgexpand.gcno` | 覆盖率插桩数据 |
| `cfgexpand.gcda` | 覆盖率运行数据（运行时生成） |
| `libgcc.a` | 运行时库 |

## 目录结构

```
workspace/
├── gcc-source/                    # GCC 源码（已修改）
│   └── gcc/
│       └── Makefile.in           # 包含插桩修改
├── gcc-build/                     # 构建目录
│   └── gcc/
│       ├── xgcc                  # 编译器
│       ├── cfgexpand.*.cfg       # CFG dump
│       └── cfgexpand.gcno        # 覆盖率
├── build-logs/                    # 日志
│   ├── configure.log
│   └── build-verbose.log
└── build-gcc-instrumented.sh     # 构建脚本
```

## 使用编译器

```bash
# 设置路径
export GCC_BUILD=/path/to/gcc-build

# 编译测试程序
$GCC_BUILD/gcc/xgcc -B$GCC_BUILD/gcc/ -o test test.c

# 查看覆盖率
gcovr $GCC_BUILD/gcc/
```

## 常见问题

### Q: 构建失败，缺少依赖

```bash
# 安装所有依赖
sudo apt-get install build-essential flex bison \
    libgmp-dev libmpfr-dev libmpc-dev texinfo zlib1g-dev
```

### Q: CFG 文件未生成

检查构建日志中 cfgexpand.cc 的编译命令是否包含 `-fdump-tree-cfg-lineno`：

```bash
grep "cfgexpand.cc" build-logs/build-verbose.log | grep fdump
```

### Q: 链接错误 undefined reference to `__gcov_*`

确保 `ALL_LINKERFLAGS` 包含 `$(COVERAGE_LDFLAGS)`，并且 `COVERAGE_LDFLAGS = -lgcov --coverage`。
