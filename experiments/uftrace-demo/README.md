# uftrace 演示

本目录包含 uftrace 工具的使用演示，用于分析编译器函数调用轨迹和发散分析。

## 文件说明

- `test1.c` / `test2.c` - 简单的 C 测试程序
- `trace1/` - 编译 test1.c 时的 uftrace 追踪数据
- `trace2/` - 编译 test2.c 时的 uftrace 追踪数据

## 常用命令

### 1. 记录追踪
```bash
uftrace record -P '.*' -d trace1 gcc test1.c -o test1 -O0
```

### 2. 查看报告
```bash
uftrace report -d trace1 --no-libcall
```

### 3. 对比差异
```bash
uftrace report -d trace1 --diff trace2 --no-libcall
```

### 4. 查看调用树
```bash
uftrace replay -d trace1 -D 3 -F toplev::main
```

### 5. 导出 Chrome Trace 格式
```bash
uftrace dump -d trace1 --chrome > trace1.json
# 在 Chrome 中打开 chrome://tracing 并加载 trace1.json
```

### 6. 查找发散点（最重要）
```bash
# 使用脚本自动查找两次执行的发散点
./find-divergence-v2.sh
```

## 发散分析原理

当我们用不同的测试用例编译时，编译器的执行路径会有所不同。发散点分析的目标是：

1. **找到第一个不同的函数调用** - 这是两次执行开始分叉的地方
2. **分析发散前的上下文** - 了解执行到发散点之前的共同路径
3. **对比发散后的不同路径** - 看两次执行接下来分别调用了哪些函数

这对于 fuzzing 非常有用：
- 当 LLM 生成的测试用例未能覆盖目标代码时
- 我们可以分析发散点，了解为什么执行路径偏离了目标
- 然后引导 LLM 修改代码，使执行路径朝正确方向发展

## 示例输出

```
✓ 发散点在第 4 个函数调用:
  trace1: file_cache::file_cache (深度: 5)
  trace2: diagnostic_option_classifier::init (深度: 5)

发散前的调用上下文（最后10个相同的调用）:
  [1]   main
  [2]       toplev::main
  [3]           pretty_printer::pretty_printer
```

这表明在 `pretty_printer::pretty_printer` 之后，两次编译开始走不同的路径。
