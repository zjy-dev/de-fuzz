# DeFuzz Paper Outline (annotated)

- Abstract（摘要）
  - 编译器防御是内存安全的最后防线，其有效性受底层 ISA 影响。当后端假设被破坏时，防御会静默失效。DeFuzz 提出三点方案：机器可验证的预言机、跨机制不变量生成流水线，以及显式编排的代理循环。

- 1. Introduction（引言）
  - 上层内存漏洞难以绝迹，编译器防御成为安全兜底。但防御实现本身会出错，且跨架构的攻击面正随 ISA 增多而扩大。
  - 1.1 Silent failure（静默失效）
    - CVE-2023-4039 表明：AArch64 上的金丝雀位置错误导致防御被绕过。此类失效编译正常、功能无误，但安全契约已破。
  - 1.2 Two coupled challenges（两个相互纠缠的挑战）
    - 机制 × ISA 矩阵构成庞大搜索空间。崩溃或差分预言机无法察觉静默失效，形成预言机盲区（oracle gap）。
  - 1.3 Our approach（我们的方法）
    - 提取安全不变量构建预言机；通过类比迁移生成跨机制不变量；用确定性流水线驱动代理循环，并通过判定器路由简化 ISA 选择。
  - 1.4 Contributions（贡献）
    - 四大贡献：静默失效预言机、系统化安全不变量、显式编排的代理系统，以及覆盖率引导的代理循环。

- 2. Background and Motivation（背景与动机）
  - 2.1 Compiler defenses and the mechanism × ISA matrix（编译器防御与机制 × ISA 矩阵）
    - 编译器通过插入检查或元数据实现防御。防御效果受栈布局和寄存器分配等影响，每个后端实现相互独立。
  - 2.2 Silent failure: a real case（静默失效：一个真实案例）
    - CVE-2023-4039 和 PR-96191 证明：针对特定目标的修复往往遗漏后备架构，导致同类失效在其他目标上复现。
  - 2.3 Why existing methods miss it（现有方法为何漏检）
    - 静态分析受限已知模式；传统模糊测试缺乏判定能力；自由运行的代理牺牲了可复现性和可审计性。
  - 2.4 Scope and threat model（研究范围与威胁模型）
    - 聚焦 GCC/LLVM 自身的防御实现。只描述违例原理，不构造漏洞利用。语料库锁定 GCC 16.1。

- 3. System Overview（系统概览）
  - 范式转为"测试编译器"。编排器调度检查器，生成代理构造种子，确定性流水线执行判定。架构包含 Go 核心服务与 Python LangGraph 编排器。

- 4. Security Invariants for Compiler Defenses（编译器防御的安全不变量）
  - 4.1 What is a security invariant（什么是安全不变量）
    - 防御生效的必要属性。其定义与具体的检测手段解耦，仅描述可观测的现象。
  - 4.2 Survey methodology and sources（调研方法与来源）
    - 从源码注释、ABI 文档及历史补丁中提取数据，采用统一的数据格式以供下游使用。
  - 4.3 A bottom-up taxonomy（自底向上的分类法）
    - 对 468 条不变量进行自底向上聚类，提取出三种跨机制复现的根因族。
  - 4.4 Machine-checkable form: static vs dynamic（可机器验证形态：静态与动态）
    - 根据可观测性和成本，将不变量验证拆分为静态（分析二进制）与动态（运行时观测）两种形式。

- 5. Cross-Mechanism Invariant Generation（跨机制不变量生成）
  - 5.1 Motivation: abstract-failure-mode transfer（动机：抽象失效模式迁移）
    - 放弃 API 相似度检索，转而利用抽象的失效模式，将一个机制的已知根因迁移到其他机制。
  - 5.2 Pipeline: distill → analogy → specialize → entailment（流水线：蒸馏 → 类比 → 实例化 → 蕴含）
    - 提取根因后，通过类比门过滤非同构代码（拦截了 94% 的词法碰撞），再实例化为具体的不变量。
  - 5.3 Retrieval: BM25 and embedding are complementary（检索：BM25 与嵌入互补）
    - BM25 与稠密检索互补。24 个探针在 GCC 源码中生成了 11 条全新的并集不变量。
  - 5.4 Grounding gates and reproducibility（锚定门与可复现性）
    - 结果须通过两道静态锚定门和新颖性检查。缓存查询向量以消除稠密检索的随机性。

- 6. Oracle: From Invariants to Verdicts（预言机：从不变量到判定）
  - 6.1 Checker design（判定器设计）
    - 判定器返回四态结果。仅当确定性检查失败或执行差异复现时才报告缺陷，杜绝大模型幻觉。
  - 6.2 Mechanism aggregator and flag profiles（机制聚合器与标志配置）
    - 聚合单项判定结果。支持开启/关闭防御标志进行差分裁决。GCC 与 LLVM 共享这套判定器。
  - 6.3 Checker metadata as a single source of truth（判定器元数据作为单一真源）
    - 判定器的 ISA 绑定及性能属性作为唯一真实数据源，使后续的路由分发成为查表操作。

- 7. Agentic Loop（代理循环）
  - 7.1 Design principles（设计原则）
    - 流水线硬编码，代理仅在固定位置执行语义判断；各节点通过共享状态通信，结论基于确定性证据。
  - 7.2 Explicit orchestration and the blackboard（显式编排与共享黑板）
    - 代理通过版本化的黑板（共享状态）进行闭环通信，确保执行轨迹可复现、可消融、可审计。
  - 7.3 Three agents（三个代理）
    - 生成代理构造种子；反馈代理分析覆盖率提供指导；最小化代理利用 C-Reduce 和模型引导精简触发代码。
  - 7.4 Checker-routed seeds, ISA-bound checkers（判定器路由的种子、ISA 绑定的判定器）
    - 代理只选择适用的判定器，由系统自动展开到对应的 ISA。静态判定器常开机制提供安全兜底。

- 8. Implementation（工程实现）
  - Go 提供构建、覆盖率及预言机服务；Python 负责图流转与状态管理。每次运行生成独立目录供后续复现。

- 9. Evaluation（实验评估）
  - Setup（实验设置）
    - 目标为 GCC 16.1 和 LLVM，覆盖四种主流 ISA，使用 QEMU 进行跨架构执行。
  - RQ1 — Real bugs（RQ1 — 真实漏洞）
    - 统计 DeFuzz 在真实编译器中发现的静默失效缺陷及 CVE 确认情况。
  - RQ2 — Invariant generation quality（RQ2 — 不变量生成质量）
    - 评估流水线生成的 11 条新不变量质量，验证类比门的过滤效果。
  - RQ3 — Ablation（RQ3 — 消融实验）
    - 验证反馈机制、预言机锚定及代理连通性对漏洞发现率的贡献。
  - RQ4 — Explicit orchestration（RQ4 — 显式编排）
    - 验证基于黑板状态的精确复现能力及系统稳定性。
  - RQ5 — Agentic vs. fuzz loop（RQ5 — 代理循环对比模糊测试循环）
    - 评估代理循环在定位静默失效上相较传统模糊测试的效率提升。

- 10. Discussion and Limitations（讨论与局限）
  - 局限于违例检测而不构造利用；稠密检索需要缓存维持确定性；判定器路由本质上是一种性能优化。

- 11. Related Work（相关工作）
  - Compiler bug finding（编译器漏洞发现）
    - 传统模糊测试工具依赖崩溃预言机，无法发现静默失效。
  - Silent/security bugs in compilers（编译器中的静默/安全漏洞）
    - 现有研究仅对编译器安全漏洞进行了分类，未提供自动化检测方案。
  - LLM/RAG property generation（基于 LLM/RAG 的属性生成）
    - 对比同类工具，本文摒弃了基于实体的检索，转而使用抽象失效模式迁移。
  - LLM-agent fuzzing（LLM 代理模糊测试）
    - 与自由运行的代理不同，本文采用了显式编排和预言机锚定。

- 12. Conclusion（结论）
  - 预言机锚定和显式编排使代理寻洞成为一种可靠的方法，有效解决了机制 × ISA 矩阵中的静默失效难题。
