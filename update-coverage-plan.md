## Idea

现在的 coverage 模块太粗了, 我想借鉴一篇叫 HLPFuzz 的论文的思路来修改 coverage 模块和 fuzz 整体的逻辑, 它基本的思想是 LLM 驱动的渐进式的约束求解. 我简化一下其思路, 如下:
1. 维护一个mapping, 每一次有新的代码行被覆盖后, 要存下该代码行和首次覆盖它的种子id的映射
2. 运行初始种子, 覆盖一些代码, 并维护好mapping, 将初始种子持久化到磁盘
3. 进入循环, 每次循环先查看未覆盖的代码中, 哪个基本块的后继最多, 就选中该基本块去做约束求解
4. 为了用 LLM 约束求解该基本块, 首先要在 prompt 中提供该基本块所在函数的代码(context), 并用符号标注出该函数中已覆盖行, 未覆盖行, 目标基本块所在的行
5. 将离同函数中目标基本块的前驱(如存在必然已经被覆盖)对应的种子作为 shot(示例) 写入 prompt
6. 将 prompt 发送给 llm, 让其根据 shot 变异出种子, 期望该种子能覆盖目标基本块
7.  编译变异出的种子, 判断是否能覆盖目标基本块, 如果能就维护好 mapping, 如果意外的覆盖了其他非目标基本块(甚至不包括目标基本块), 也要进行 mapping 的维护. 然后继续 fuzz 流程将该种子塞入预言机. 如果该种子通过了预言机且没有任何覆盖率的增加, 那不要持久化该种子, 否则持久化到磁盘.
8. 如果变异出的种子覆盖了目标基本块, 回到 1
9. 如果变异出的种子没有覆盖目标基本块, 就对示例种子和变异出的种子进行发散分析(Divergence analysis), 定位到它们的call trace在哪个函数开始不同(如果方便的话最好能定位到哪一行开始不同), 然后将这些信息(原来的prompt中的shot和context, 变异出的种子, call trace diff到的函数)再次发给llm去变异, 回到 8

## 技术

### mapping 

用 json 持久化到磁盘, 读写方便即可

### 选取后继最多的基本块

采用 **GCC CFG Dump** 方案 (静态分析阶段)

1.  **构建时注入**：在 **构建目标编译器** (Build Target Compiler) 时，仅对 **关注的源文件** (Target Files) 添加 `-fdump-tree-cfg-lineno` 编译参数。
    *   **实现方式**：可以通过修改 Makefile 或在构建命令中针对特定文件覆盖 `CFLAGS`。
    *   **产物**：GCC 会为每个被标记的源文件生成一个独立的 `.cfg` 文件 (例如 `cfgexpand.cc.015t.cfg`)。
2.  **收集资产**：构建完成后，收集所有生成的 `.cfg` 文件。这些文件仅包含我们关注的函数和文件的控制流信息。
3.  **初始化加载**：
    *   Fuzzer 启动时，读取配置文件中的 `targets` 列表。
    *   仅加载与 `targets` 列表匹配的 `.cfg` 文件。
    *   构建全局映射表：`File:Line -> BasicBlockID` 以及 `BasicBlockID -> SuccessorCount`。
4.  **运行时查询**：在 Fuzz 循环中，当发现某行代码未覆盖时，直接查询内存中的映射表即可得知该行所在基本块的后继数量。


### call trace 发散分析

采用 **uftrace** 进行 **函数级** 发散分析（不做行号级别）。

#### 方案验证 (2024-12-24)

在 `experiments/uftrace-demo/` 目录下进行了验证实验：

| 测试用例 | 内容 |
|----------|------|
| test1.c | `return a + b;` |
| test2.c | `return a * b;` |

**验证结果：**
- 总调用数：~16000 个函数调用
- 解析阶段起点：第 8280 个调用 (`c_parser_peek_token`)
- **发散点**：解析阶段第 3746 个调用
  - trace1: `gen_addsi3` (生成加法指令)
  - trace2: `optimize_insn_for_speed_p` (乘法走优化分支)

#### 技术实现

1.  **录制 Trace**：
    ```bash
    uftrace record -P '.*' -d trace_dir gcc -c seed.c -o /dev/null
    ```
    - `-P '.*'`: 动态追踪所有函数（编译器未用 `-pg` 编译）
    - uftrace 自动跟踪 fork 的子进程 (cc1, as, ld)

2.  **提取 cc1 进程 ID**：
    ```bash
    grep "cc1" trace_dir/task.txt | head -1 | awk '{print $3}' | cut -d= -f2
    ```

3.  **导出调用序列**（过滤噪声）：
    ```bash
    uftrace replay -d trace_dir --no-libcall | \
        grep "\[$CC1_PID\]" | \
        grep -v "linux:schedule"
    ```

4.  **跳过初始化阶段**：
    - 从第一个 `c_parser_*` 或 `parse` 函数开始比较
    - 避免编译器启动时的非确定性初始化顺序干扰

5.  **查找发散点**：
    - 逐行比较两个调用序列
    - 记录第一个函数名不同的位置
    - 返回发散点前后的函数调用上下文

#### 不做行号级别的原因

1. 函数级已足够定位问题 —— LLM 只需要知道「在哪个函数开始走不同路径」
2. uftrace 的 `--srcline` 需要 DWARF 调试信息，会大幅增加 trace 文件大小
3. 行号定位需要额外的 Tree-sitter 集成，增加复杂度
4. 发散通常发生在分支决策函数，函数名本身就是很好的语义信息

