   诊断报告：约束求解失败根本原因分析

   问题发现

   从日志可以看到：
   •  98 次迭代中，只有 5 次成功增加了覆盖率（seed 50, 185, 457, 461, 552）
   •  目标 BB74、BB14、BB85 等被反复尝试 8 次后失败
   •  约束求解从未真正成功覆盖目标 BB，只是偶然覆盖了其他新行

   根本原因分析

   1. 核心问题：目标 BB 在当前配置下是不可达的

   查看 expand_used_vars 的 BB74（行 2600）的源码：

   cpp
     if (!lookup_attribute ("no_stack_protector", attribs))
         switch (flag_stack_protect)
           {
           case SPCT_FLAG_ALL:     // flag = 2
           case SPCT_FLAG_STRONG:  // flag = 3
           case SPCT_FLAG_DEFAULT: // flag = 1
           case SPCT_FLAG_EXPLICIT:// flag = 4
           }

   BB74 是 switch (flag_stack_protect) 语句本身。要覆盖它需要：
   •  函数没有 no_stack_protector 属性
   •  编译时使用了 -fstack-protector* 标志

   但你的配置使用的是 -fstack-protector-strong（对应 flag_stack_protect = 3 = SPCT_FLAG_STRONG），这意味着 BB74 
   总是会被覆盖（因为你已经开启了栈保护）。

   实际问题：CFG 分析器标记 BB74 为"未覆盖"，但这可能是因为：
   1. CFG 解析与覆盖率数据不匹配
   2. 行号映射问题
   3. 或者目标行在特定条件分支内（如 SPCT_FLAG_ALL case）

   2. 提示词与任务不匹配

   查看生成的种子代码：

   c
     // id-000457: 尝试通过 union/struct/VLA 组合触发不同路径
     // id-000461: 尝试 VLA + alloca + 函数指针 + PIC

   这些代码变化确实很有创意，但它们影响的是编译器处理变量分配的逻辑，而不是 flag_stack_protect 的 switch 分支。

   3. 约束求解任务的固有局限性

   覆盖 expand_used_vars:BB74 的不同分支需要：
   •  case SPCT_FLAG_ALL: 编译时使用 -fstack-protector-all
   •  case SPCT_FLAG_STRONG: 编译时使用 -fstack-protector-strong（当前配置）
   •  case SPCT_FLAG_DEFAULT: 编译时使用 -fstack-protector
   •  case SPCT_FLAG_EXPLICIT: 编译时使用 -fstack-protector-explicit

   种子代码（C 语言测试用例）无法改变编译器自身的编译选项！

   结论

   因素              │ 评估                 
   ----------------+--------------------
   **代码 Bug**      │ 不是主要问题
   **提示词质量**       │ 一般，但不是根因
   **方案缺陷**        │ ★★★★★ **核心问题**
   **DeepSeek 能力** │ 无法验证，因为任务本身不可行

   方案的根本缺陷：
   1. 许多编译器内部分支由编译器选项（如 -O2, -fstack-protector）控制，而非输入代码
   2. CFG 分析选择的"高价值目标"实际上可能在当前编译配置下不可达
   3. LLM 无论多聪明，都无法通过修改 C 代码来改变编译器选项

   建议

   1. 过滤不可达目标：在目标选择前，分析 BB 是否依赖编译器选项（flag_stack_protect, optimize, flag_pic 等）

   2. 多编译配置 fuzzing：对每个种子使用多种编译选项组合（-O0/-O2/-O3, -fstack-protector/-fstack-protector-all）

   3. 改进 CFG 解析：区分"代码可控"的分支（依赖输入代码结构）和"选项可控"的分支（依赖编译选项）

   4. 聚焦代码结构敏感路径：如 stack_protect_classify_type、defer_stack_allocation 中依赖变量类型/大小的分支