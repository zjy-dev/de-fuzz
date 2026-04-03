# GCC Instrumentation Documentation

本目录说明 DeFuzz 如何构建带覆盖率插桩和 CFG dump 的 GCC，并重点记录当前仓库已经验证过的多文件 CFG 方案。

## 目录结构

```text
docs/gcc-instrumentation/
├── README.md
├── Makefile.in.patch
├── x64/BUILD-GUIDE.md
├── aarch64/BUILD-GUIDE.md
├── riscv64/BUILD-GUIDE.md
└── loongarch64/BUILD-GUIDE.md
```

## 1. 当前项目的真实规则

DeFuzz 现在不是“单个 `cfgexpand.cc` CFG 文件也能覆盖所有 target functions”的模型，而是严格的一文件一 CFG 模型。

规则是：

- 如果要 target `cfgexpand.cc` 的函数，就必须有 `cfgexpand.cc.*.cfg`
- 如果要 target `function.cc` 的函数，就必须有 `function.cc.*.cfg`
- 如果要 target `builtins.cc`、`gimple-fold.cc`、`c-family/c-opts.cc`、`linux.cc`、后端文件等函数，也都必须分别有自己的 `.cfg`

也就是说：

- 一个要主动 target 的编译器源码文件
- 对应一个被插桩并带 `-fdump-tree-cfg-lineno` 的目标对象
- 对应一个生成出来的 `*.cfg`
- 对应一个写入 `cfg_file_path` 或 `cfg_file_paths` 的配置路径

缺哪一步，analyzer 都不能真正把那个文件里的函数当成 CFG-guided target。

## 2. 当前插桩内容

当前机制包含两类插桩：

1. 覆盖率插桩
   - `-fprofile-arcs -ftest-coverage`
   - 生成 `.gcno` 和 `.gcda`
   - 用于统计编译器源码覆盖率
2. CFG dump
   - `-fdump-tree-cfg-lineno`
   - 生成 `*.cfg`
   - 用于 CFG-guided target selection 和约束求解

## 3. 当前验证过的 GCC 15.2 白名单

当前仓库中已经验证过的 GCC 15.2 白名单对象为：

- `cfgexpand.o`
- `function.o`
- `calls.o`
- `targhooks.o`
- `builtins.o`
- `gimple-fold.o`
- `c-family/c-opts.o`
- `linux.o`
- `aarch64.o`
- `riscv.o`

这已经覆盖了当前活动配置中的：

- canary 多文件 CFG 目标面
- fortify 多文件 CFG 目标面

如果未来要把 LoongArch64 后端专有源码也纳入 CFG-guided target surface，需要额外把对应对象加入白名单并重新生成 `.cfg`。

## 4. 当前已经验证存在的多文件 CFG

### AArch64

- `cfgexpand.cc.015t.cfg`
- `function.cc.015t.cfg`
- `calls.cc.015t.cfg`
- `targhooks.cc.015t.cfg`
- `builtins.cc.015t.cfg`
- `gimple-fold.cc.015t.cfg`
- `linux.cc.015t.cfg`
- `aarch64.cc.015t.cfg`
- `c-family/c-opts.cc.015t.cfg`

### RISC-V

- `cfgexpand.cc.015t.cfg`
- `function.cc.015t.cfg`
- `calls.cc.015t.cfg`
- `targhooks.cc.015t.cfg`
- `builtins.cc.015t.cfg`
- `gimple-fold.cc.015t.cfg`
- `linux.cc.015t.cfg`
- `riscv.cc.015t.cfg`
- `c-family/c-opts.cc.015t.cfg`

### LoongArch64

- `cfgexpand.cc.015t.cfg`
- `function.cc.015t.cfg`
- `calls.cc.015t.cfg`
- `targhooks.cc.015t.cfg`
- `builtins.cc.015t.cfg`
- `gimple-fold.cc.015t.cfg`
- `linux.cc.015t.cfg`
- `c-family/c-opts.cc.015t.cfg`

## 5. 重要的可复现性约束

### 5.1 改白名单后必须重链 `cc1`

只重编某个对象文件不够。因为运行时真正执行的是 `cc1`，所以在对象重编之后必须再执行一次：

```bash
make -C <gcc-final-build/gcc> -j<N> cc1
```

### 5.2 `JOBS` 控制构建并行度

当前交叉编译构建脚本都支持：

```bash
JOBS=16 ./build-gcc-instrumented.sh
```

这控制的是 GCC 构建阶段的并行编译，不是 DeFuzz 运行时的 replay 并发。

### 5.3 不要用“删 target functions”替代“补 `.cfg`”

当前项目规则是：

- 所有配置里的 target functions 都视为必需，
- 如果目标文件没有 `.cfg`，应该补齐 `.cfg`，
- 不应仅为了让当前运行通过而把函数从配置里注释掉。

## 6. 与 DeFuzz 的对接方式

单文件兼容模式：

```yaml
compiler:
  fuzz:
    cfg_file_path: /path/to/gcc-final-build/gcc/cfgexpand.cc.015t.cfg
```

推荐的多文件模式：

```yaml
compiler:
  fuzz:
    cfg_file_paths:
      - /path/to/gcc-final-build/gcc/cfgexpand.cc.015t.cfg
      - /path/to/gcc-final-build/gcc/function.cc.015t.cfg
      - /path/to/gcc-final-build/gcc/calls.cc.015t.cfg
      - /path/to/gcc-final-build/gcc/targhooks.cc.015t.cfg
```

`cfg_file_paths` 应与 `targets` 中列出的源码文件保持一致。

## 7. 阅读顺序建议

- 想看补丁规则：`Makefile.in.patch`
- 想从零构建：对应 ISA 的 `BUILD-GUIDE.md`
- 想增量补 `.cfg`：优先看各 ISA 的 `BUILD-GUIDE.md` 里的“增量重生成”章节

## 8. 参考资料

- [GCC Internals](https://gcc.gnu.org/onlinedocs/gccint/)
- [Gcov Documentation](https://gcc.gnu.org/onlinedocs/gcc/Gcov.html)
