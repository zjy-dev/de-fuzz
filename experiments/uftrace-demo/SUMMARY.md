# uftrace 发散分析总结

## 核心概念

**发散点（Divergence Point）**：当使用两个不同的测试用例编译时，编译器执行路径开始不同的那个函数调用。

## 为什么重要？

在 Coverage-Guided Fuzzing 中：
1. 我们选定一个目标基本块（想要覆盖的代码）
2. LLM 生成一个测试用例尝试覆盖它
3. 如果失败了，我们需要知道**为什么执行路径偏离了目标**
4. 发散点告诉我们：**从哪个函数开始，执行走向了错误的方向**

## uftrace 工作流程

```bash
# 1. 记录基准执行轨迹（覆盖了目标附近代码的种子）
uftrace record -P '.*' -d trace_base gcc base.c -o /dev/null

# 2. 记录变异后的执行轨迹（LLM 生成的新种子）
uftrace record -P '.*' -d trace_mut gcc mutated.c -o /dev/null

# 3. 导出并对比调用序列，找到发散点
uftrace replay -d trace_base --no-libcall -t 1us > base_calls.txt
uftrace replay -d trace_mut --no-libcall -t 1us > mut_calls.txt

# 4. 用脚本找到第一个不同的函数调用
./find-divergence-v2.sh
```

## 输出示例

```
✓ 发散点在第 4 个函数调用:
  trace1: file_cache::file_cache (深度: 5)
  trace2: diagnostic_option_classifier::init (深度: 5)

发散前的调用上下文（最后10个相同的调用）:
  [1]   main
  [2]       toplev::main
  [3]           pretty_printer::pretty_printer

发散后的不同路径:
  trace1 接下来调用:
    [4]           file_cache::file_cache
    [5]           diagnostic_option_classifier::init
    ...
  
  trace2 接下来调用:
    [4]           diagnostic_option_classifier::init
    [5]           get_terminal_width
    ...
```

## 如何利用发散点？

1. **定位问题函数**：发散点告诉我们在哪个函数做出了不同的决策
2. **用 Tree-sitter 获取源码**：找到该函数的源代码范围
3. **引导 LLM**：在 Prompt 中加入：
   ```
   上一次生成的测试用例在 `pretty_printer::pretty_printer` 后
   没有调用 `file_cache::file_cache`，而是调用了 
   `diagnostic_option_classifier::init`。
   
   请修改测试用例，使编译器在该位置选择调用 `file_cache::file_cache`
   ```

## 集成到 de-fuzz

参见 `update-coverage-code-plan.md` 第 4 节：

```go
type DivergenceAnalyzer interface {
    Analyze(baseSeed, mutatedSeed *seed.Seed, compilerPath string) (*DivergencePoint, error)
}
```

实现会：
1. 调用 uftrace 记录两次编译
2. 解析输出找到发散点
3. 用 Tree-sitter 定位源码位置
4. 返回给 Engine 用于生成 refined prompt
