# GCC Instrumentation Documentation

本目录包含为编译器模糊测试构建带插桩 GCC 的文档和脚本。

## 目录结构

```
doc/gcc-instrumentation/
├── README.md                    # 本文件
├── Makefile.in.patch            # Makefile.in 修改说明
├── x64/                         # x86_64 原生编译器
│   ├── BUILD-GUIDE.md           # 构建指南
│   └── build-gcc-instrumented.sh
└── aarch64/                     # AArch64 交叉编译器
    ├── BUILD-GUIDE.md           # 构建指南
    └── build-gcc-instrumented.sh
```

## 插桩说明

我们对 GCC 进行**选择性插桩**，只为 `cfgexpand.cc` 添加：

1. **覆盖率插桩** (`-fprofile-arcs -ftest-coverage`)
   - 生成 `.gcno` 和 `.gcda` 文件
   - 用于测量编译器代码覆盖率

2. **CFG dump** (`-fdump-tree-cfg-lineno`)
   - 生成 `.cfg` 文件
   - 包含控制流图信息，用于 CFG-guided fuzzing

### 为什么选择性插桩？

- **减少开销**：只插桩关键路径 (cfgexpand.cc)，避免全量插桩的性能损失
- **聚焦测试**：cfgexpand.cc 是 GIMPLE → RTL 转换的核心，是编译器 bug 的高发区域
- **简化分析**：更小的 CFG 文件便于分析和约束求解

## 快速开始

### x86_64 原生编译器

```bash
# 1. 下载 GCC 源码
wget https://github.com/gcc-mirror/gcc/archive/refs/tags/releases/gcc-12.2.0.tar.gz
tar xzf gcc-12.2.0.tar.gz

# 2. 应用补丁（参考 Makefile.in.patch）
# 编辑 gcc-releases-gcc-12.2.0/gcc/Makefile.in

# 3. 构建
cd doc/gcc-instrumentation/x64
./build-gcc-instrumented.sh /path/to/gcc-source /path/to/build
```

### AArch64 交叉编译器

```bash
# 1. 下载 ARM GNU Toolchain 源码
wget https://developer.arm.com/.../arm-gnu-toolchain-src-snapshot-12.2.rel1.tar.xz
tar xf arm-gnu-toolchain-src-snapshot-12.2.rel1.tar.xz

# 2. 应用补丁
# 编辑 .../arm-gnu-toolchain-src-snapshot-12.2.rel1/gcc/Makefile.in

# 3. 构建
cd doc/gcc-instrumentation/aarch64
./build-gcc-instrumented.sh /path/to/source /path/to/build /path/to/install
```

## 与 de-fuzz 集成

de-fuzz 项目使用这些插桩编译器进行模糊测试：

1. 编译器在编译测试用例时生成 `.gcda` 覆盖率数据
2. de-fuzz 读取覆盖率数据计算覆盖率增量
3. CFG 文件用于 CFG-guided seed 生成

配置示例 (`configs/gcc-v12.2.0-x64.yaml`):

```yaml
compiler:
  path: /path/to/gcc-build/gcc/xgcc
  args: ["-B/path/to/gcc-build/gcc/"]
  
coverage:
  gcno_dir: /path/to/gcc-build/gcc/
  gcda_dir: /path/to/gcc-build/gcc/
  source_file: cfgexpand.cc
```

## 支持的 GCC 版本

- GCC 12.2.0 (已测试)
- 其他 GCC 12.x 版本应该也可以工作

## 参考资料

- [GCC Internals](https://gcc.gnu.org/onlinedocs/gccint/)
- [Gcov Documentation](https://gcc.gnu.org/onlinedocs/gcc/Gcov.html)
- [ARM GNU Toolchain](https://developer.arm.com/Tools%20and%20Software/GNU%20Toolchain)
