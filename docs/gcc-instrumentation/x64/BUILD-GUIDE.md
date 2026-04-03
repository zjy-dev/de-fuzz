# GCC x64 Native Build Guide

本文档描述如何构建带有覆盖率插桩和 CFG dump 的 x86_64 原生 GCC 编译器，并补充多文件 CFG 扩展时需要保留的操作细节。

## 概述

这是一个**原生编译器**：

- 运行在 x86_64 主机上
- 生成 x86_64 目标二进制
- 覆盖率插桩针对**编译器本身**

虽然当前仓库的主要实测工作集中在 GCC 15.2 交叉编译器上，但 x64 仍遵循同一套多文件 CFG 规则。

## 前置要求

### 系统依赖

```bash
sudo apt-get install build-essential flex bison \
    libgmp-dev libmpfr-dev libmpc-dev texinfo zlib1g-dev

# 覆盖率工具
pip install gcovr
```

### 获取 GCC 源码

```bash
wget https://github.com/gcc-mirror/gcc/archive/refs/tags/releases/gcc-12.2.0.tar.gz
tar xzf gcc-12.2.0.tar.gz
mv gcc-releases-gcc-12.2.0 gcc-source
```

## 应用插桩补丁

在构建前，需要修改 `gcc/Makefile.in`：

```bash
# 参考 docs/gcc-instrumentation/Makefile.in.patch
# 在三个位置添加 FUZZ-COVERAGE-INSTRUMENTATION 标记的代码块
```

关键修改点：

1. 添加 `COVERAGE_WHITELIST`
2. 修改 `ALL_CFLAGS` / `ALL_CXXFLAGS` 使用 `$(CFLAGS-$@)`
3. 添加 `COVERAGE_LDFLAGS`

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
| `*.015t.cfg` | CFG dump 文件 |
| `*.gcno` | 覆盖率插桩数据 |
| `*.gcda` | 覆盖率运行数据（运行时生成） |
| `libgcc.a` | 运行时库 |

## 目录结构

```text
workspace/
├── gcc-source/
│   └── gcc/
│       └── Makefile.in
├── gcc-build/
│   └── gcc/
│       ├── xgcc
│       ├── *.015t.cfg
│       └── *.gcno
├── build-logs/
└── build-gcc-instrumented.sh
```

## 使用编译器

```bash
export GCC_BUILD=/path/to/gcc-build

# 编译测试程序
$GCC_BUILD/gcc/xgcc -B$GCC_BUILD/gcc/ -o test test.c

# 查看覆盖率
gcovr $GCC_BUILD/gcc/
```

## 多文件 CFG 扩展

如果你要让 DeFuzz 主动 target `cfgexpand.cc` 之外的源码文件，例如：

- `function.cc`
- `calls.cc`
- `targhooks.cc`
- `builtins.cc`
- `gimple-fold.cc`
- `c-family/c-opts.cc`
- `linux.cc`

那么这些文件都必须各自生成对应的 `.cfg` dump。

扩展步骤仍然是：

1. 在 `Makefile.in` 的白名单中加入对应对象
2. 为这些对象增加 `CFLAGS-*.o += -fdump-tree-cfg-lineno`
3. 重编相关对象
4. 重链 `cc1`

## 增量重生成提醒

对象重编之后，必须再次重链 `cc1`。否则新生成的 `.o` 不一定进入实际运行的编译器前端。

## 常见问题

### Q: 构建失败，缺少依赖

```bash
sudo apt-get install build-essential flex bison \
    libgmp-dev libmpfr-dev libmpc-dev texinfo zlib1g-dev
```

### Q: CFG 文件未生成

检查构建日志中对应文件的编译命令是否包含 `-fdump-tree-cfg-lineno`：

```bash
grep "cfgexpand.cc" build-logs/build-verbose.log | grep fdump
```

### Q: 只有 `cfgexpand.cc.015t.cfg`

说明当前仍是单文件 CFG 白名单，或多文件对象没有被重编。

### Q: 链接错误 `undefined reference to __gcov_*`

确保 `ALL_LINKERFLAGS` 包含 `$(COVERAGE_LDFLAGS)`，并且：

```text
COVERAGE_LDFLAGS = -lgcov --coverage
```

### Q: 对象已重编，但行为没变化

最常见原因是没有重链 `cc1`。
