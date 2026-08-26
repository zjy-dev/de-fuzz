---
title: Cross-Mechanism Invariants — BM25 vs Embedding 检索去重比对
description: 同一跨机制迁移管道下 BM25 词法检索与 doubao-embedding-vision 稠密检索两条独立产出的去重对照
last_updated: 2026-07-03
generated_from: orchestrator/runs/specgen_full (BM25) · orchestrator/runs/specgen_embed (embedding)，git 90125f582273
---

# BM25 与 Embedding 检索的跨机制不变量去重比对

我们把同一套跨机制迁移管道（`orchestrator/defuzz_loop/specgen`）跑了两遍，只换检索后端：一遍用 BM25 词法检索，一遍用 `doubao-embedding-vision` 稠密向量检索。除排序模型外，两条路径的其它环节完全一致——同样的 24 个探针种子、同样的 4496 块 GCC 16.1 语料、同一份 `query_terms()` 查询文本、同样的出口过滤与四阶段判断链。这样做的目的是隔离变量：两份产出的差异只能归因于"排序模型不同"，而不是输入或判据不同。

这份文档把两份产出放在一起做去重对照，回答三个问题：哪些不变量是两种检索都能命中的（稳健信号）、哪些是某一种检索独有的（模型偏好差异）、以及稠密检索到底补上了什么词法检索够不到的东西。

## 去重口径

我们按两个维度做匹配：

1. **主键维度 `(seed_id, chunk_id)`**：种子相同、命中的源码块也相同，视为同一条不变量的两次独立发现。这是主判据。
2. **陈述文本维度 `statement`**：对主键相同的条目，核对两边生成的 `statement` 是否描述同一个根因约束（防止同块不同解读被误判为重合）。

需要说明的是，`config/arm/arm.cc:26305:output`（即 `thumb_exit`）这一块同时被种子 001 和种子 020 命中，属于两个不同的 `(seed_id, chunk_id)` 键，我们按两条独立记录处理，不做合并。

## 总览

| 类别 | 数量 | 说明 |
| --- | --- | --- |
| 两种检索交集 | 5 | seed 与 chunk 完全一致，`statement` 同构 |
| BM25 独有 | 4 | embedding 对相应种子未把该块排进 top-k |
| Embedding 独有 | 2 | BM25 对相应种子未命中，稠密检索新增 |
| **并集（去重后总数）** | **11** | 9 (BM25) + 2 (embedding 新增) |

BM25 路径共 9 条，embedding 路径共 7 条；其中 5 条重合，故并集为 `9 + 7 - 5 = 11`。两条独有的 embedding 命中经去重（`NoveltyBaseline`，阈值 85.0）后均判定 `is_novel=True`，不与既有种子或手册规则撞车。

---

## 一、两种检索的交集（5 条 · 稳健信号）

这 5 条不变量在词法与语义两种排序下都能浮出水面，说明它们的根因特征在源码里既有强词面锚点、又有强语义相似度，是跨机制迁移里最可靠的一批。它们的完整条目已收录在 [cross-mechanism-generated.md](file:///Users/bytedance/projects/research/de-fuzz/feat-specgen-rag-invariants/docs/tech-docs/invariants/cross-mechanism-generated.md)，此处只列对照索引。

| 种子 | 命中机制 | 命中块 | 对应 XINV |
| --- | --- | --- | --- |
| DREV-2026-001 | backend-multi | `config/arm/arm.cc:26305` (`thumb_exit`) | XINV-001 |
| DREV-2026-020 | stack-protector | `cfgexpand.cc:6632` (`construct_exit_block`) | XINV-003 |
| DREV-2026-020 | stack-protector | `cfgexpand.cc:4428` (`expand_gimple_tailcall`) | XINV-004 |
| DREV-2026-020 | backend-multi | `config/arm/arm.cc:26305` (`thumb_exit`) | XINV-006 |
| DREV-2026-025 | stack-clash-protection | `explow.cc:51` (`trunc_int_for_mode`) | XINV-007 |

值得注意的是，这 5 条覆盖了全部三类根因族：出口寄存器/状态残留（前两行）、异常出口边漏清（`construct_exit_block` / `expand_gimple_tailcall`）、以及整数窄化的符号扩展错误（`trunc_int_for_mode`）。也就是说，每一类根因都至少有一条被双路径交叉验证。

---

## 二、BM25 独有（4 条 · 词法检索优势）

这 4 条 BM25 命中了、embedding 对相应种子却没排进 top-k。共同点是命中块里有**高区分度的标识符**（`ix86_zero_call_used_regs`、`SUBREG_PROMOTED`、`CONST_HIGH_PART`、`aarch64_..._sme_state`），BM25 的子词切分把这些长标识符拆成强锚点直接命中，而稠密向量把它们平滑进了整体语义、排名反而被更"泛化相似"的块挤下去。

| 种子 | 命中机制 | 命中块 | 对应 XINV | embedding 为何漏掉 |
| --- | --- | --- | --- | --- |
| DREV-2026-001 | backend-multi | `config/aarch64/aarch64.cc:31923` (`aarch64_mode_emit_local_sme_state`) | XINV-002 | SME 状态清零的语义与"寄存器擦除"整体相近，向量排名被其它出口序列块稀释 |
| DREV-2026-020 | backend-multi | `config/i386/i386.cc:4063` (`ix86_zero_call_used_regs`) | XINV-005 | 命中靠 `zero_call_used` 词面锚点；语义上与大量"逐寄存器循环"块难分 |
| DREV-2026-025 | backend-multi | `config/riscv/riscv.cc:3037` (`riscv_add_offset`) | XINV-008 | 靠 `CONST_HIGH_PART`/`CONST_LOW_PART` 词面命中；高低位拆分语义分散 |
| DREV-2026-025 | backend-multi | `config/riscv/riscv.cc:16438` (`synthesize_add_extended`) | XINV-009 | 靠 `SUBREG_PROMOTED`/`SRP_SIGNED` 词面命中；同上 |

结论：对于根因**锚定在具体 API 名/宏名**上的场景，BM25 仍不可替代。

---

## 三、Embedding 独有（2 条 · 稠密检索新增）

这 2 条是本轮 embedding 检索的净收益——BM25 对相应种子没能命中这两块，稠密向量凭语义相似度把它们拉了进来。两条都通过了 analogy / specialize / entailment 三道闸门，且 `is_novel=True`。

### XINV-010 · DREV-2026-001 : stack-protector → backend-multi（embedding 独有）

- **命中站点**: `config/arm/arm.cc:27531` （GCC gcc-16.1.0, target `arm`）
- **version_sensitivity**: target-specific
- **statement**: For a cmse_nonsecure_entry function, cmse_nonsecure_entry_clear_before_return must add every call-clobbered and VFP register that is not part of the result to to_clear_bitmap and emit the corresponding clearing before the return branch; any call-clobbered register omitted from to_clear_bitmap (other than the result registers and the deliberately excluded scratch/argument registers) reaches the secure-to-non-secure return edge still holding its in-function value and is readable by the non-secure caller.
- **中文解读**: 这是 CMSE 安全入口函数专门的"退出前清寄存器"发射点。它先构造一张 `to_clear_bitmap`（把参数寄存器、IP、VFP D 寄存器、以及用户指定为 caller-saved 的寄存器全部标记进去），再在返回分支前把这些寄存器清掉。不变量约束的是：凡是 call-clobbered 且不属于返回值的寄存器，都必须进这张位图并被清除；一旦漏标，它就会带着函数内的秘密值跨过 secure→non-secure 返回边，被非安全世界的调用者读到。这和种子 001（栈保护场景里秘密残留在返回边寄存器）是同一个"退出前必须覆盖写"的根因形状，只是落在 CMSE 这个不同机制上。
- **observation（违反时可外部观测的现象）**: In the emitted body of a cmse_nonsecure_entry function, a call-clobbered register that held an intermediate secret is not overwritten (no CLRM / mov-immediate / VFP-clear) between its last secret-bearing definition and the function's return branch, so its value is observable after the bxns back to the non-secure world.
- **falsifiability**:
    - 可观测性: static —— 对 cmse_nonsecure_entry 函数，把 call-clobbered 寄存器集合与 `to_clear_bitmap` 及返回分支前实际发射的清除指令做差集；一个在函数体内被定义、却没有清除指令的 call-clobbered 非结果寄存器即为违反。
    - 判定确定性: deterministic —— 清除集合与发射的清除序列在编译期即固定，不依赖运行时状态。
    - 实现成本: low —— 只需检查单个受影响函数的 epilogue/返回序列。
    - 静态/动态归属: static
- **新颖性**: is_novel=True（离最近种子 DREV-2026-016 词法距离 83.63，逼近但未越过 85 阈值）

  <details><summary>命中块证据原文（GCC 16.1）</summary>

  ```c
  void
  cmse_nonsecure_entry_clear_before_return (void)
  {
    bool clear_vfpregs = TARGET_HARD_FLOAT || TARGET_HAVE_FPCXT_CMSE;
    int regno, maxregno = clear_vfpregs ? LAST_VFP_REGNUM : IP_REGNUM;
    uint32_t padding_bits_to_clear = 0;
    auto_sbitmap to_clear_bitmap (maxregno + 1);
    rtx r1_reg, result_rtl, clearing_reg = NULL_RTX;
    tree result_type;

    bitmap_clear (to_clear_bitmap);
    bitmap_set_range (to_clear_bitmap, R0_REGNUM, NUM_ARG_REGS);
    bitmap_set_bit (to_clear_bitmap, IP_REGNUM);
    /* ... */
    for (regno = NUM_ARG_REGS; regno <= maxregno; regno++)
      {
        if (IN_RANGE (regno, FIRST_VFP_REGNUM, D7_VFP_REGNUM))
          continue;
        if (IN_RANGE (regno, IP_REGNUM, PC_REGNUM))
          continue;
        if (!callee_saved_reg_p (regno)
            && (!IN_RANGE (regno, FIRST_VFP_REGNUM, LAST_VFP_REGNUM)
                || TARGET_HARD_FLOAT))
          bitmap_set_bit (to_clear_bitmap, regno);
      }
    /* ... 排除返回值寄存器后 ... */
    clearing_reg = gen_rtx_REG (SImode, TARGET_THUMB1 ? R0_REGNUM : LR_REGNUM);
    r1_reg = gen_rtx_REG (SImode, R0_REGNUM + 1);
    cmse_clear_registers (to_clear_bitmap, &padding_bits_to_clear, 1, r1_reg,
                          clearing_reg);
  }
  ```

  </details>

> 与 XINV-001 的关系：XINV-001（BM25）命中的是 `thumb_exit` 里 `pops_needed==0` 的**快返回路径漏清**；XINV-010（embedding）命中的是 CMSE 的**清除集合构造函数本身**。两者是同一族防御（CMSE 退出擦除）的两个不同发射点，互补而非重复——BM25 抓到了"哪条出口边漏清"，embedding 抓到了"清除集合本身可能漏标哪个寄存器"。

### XINV-011 · DREV-2026-025 : fortify-source → backend-multi（embedding 独有）

- **命中站点**: `config/riscv/riscv.cc:14845` （GCC gcc-16.1.0, target `riscv64`）
- **version_sensitivity**: target-specific
- **statement**: In riscv_expand_sstrunc the saturation bounds must be computed at the full Xmode width from the destination mode's precision (narrow_max = (1<<(narrow_prec-1))-1, narrow_min = -narrow_max-1) and the source must be sign-extended to Xmode before the LT/AND clamp; if the clamp compared a value that had already been truncated to the narrow mode (rather than the Xmode sign-extended source), a source whose bit (narrow_prec-1) is set would be mis-sign-extended and the saturated result would take the wrong branch, yielding a narrowed value that no longer represents min(max(src, narrow_min), narrow_max).
- **中文解读**: 这是 RISC-V 后端的有符号饱和截断展开器。正确做法是：先按目标窄模式的精度算出饱和上下界（`narrow_max = (1<<(prec-1))-1`、`narrow_min = -narrow_max-1`），再把源值**符号扩展到 Xmode 全宽**，然后用 `LT`/`AND` 做钳位。不变量约束的是：参与钳位比较的必须是这个 Xmode 符号扩展后的源值，而不是已经被截断到窄模式的值——如果拿一个提前截断、符号已翻转的操作数去钳位，源值里 bit `(prec-1)` 一旦置位就会被错误符号扩展，饱和结果会走错分支，最终得到的窄化值不再等于 `min(max(src, narrow_min), narrow_max)`。这和种子 025（fortify 里宽有符号量窄化时符号扩展出错、导出边界失真）是同一个"带符号窄化的钳位由符号扩展主导"的根因形状，只是从 fortify 头部裁剪路径迁移到了后端饱和截断路径。
- **observation（违反时可外部观测的现象）**: For a saturating truncation whose source has the destination mode's sign bit set, the produced value differs from the true signed saturation min(max(src, narrow_min), narrow_max) —— e.g. a large positive source saturates to a negative narrow value —— because the clamp consumed a prematurely-narrowed, sign-flipped operand instead of the Xmode sign-extended source.
- **falsifiability**:
    - 可观测性: static —— 检查喂给 `LT` 比较和 `AND` 掩码的操作数是否为 `src` 的 Xmode `SIGN_EXTEND`，且 `narrow_min`/`narrow_max` 由目标模式的 `GET_MODE_PRECISION` 导出；若钳位作用在符号扩展之前就被截断的值上，即为违反。
    - 判定确定性: deterministic —— 操作数位宽与所用扩展方式由 RTL 展开固定，与运行时数值无关。
    - 实现成本: low —— 只需检查这一个展开器的操作数链。
    - 静态/动态归属: static
- **新颖性**: is_novel=True（离最近种子 DREV-2026-001 词法距离 79.16）

  <details><summary>命中块证据原文（GCC 16.1）</summary>

  ```c
  void
  riscv_expand_sstrunc (rtx dest, rtx src)
  {
    machine_mode mode = GET_MODE (dest);
    unsigned narrow_prec = GET_MODE_PRECISION (mode).to_constant ();
    HOST_WIDE_INT narrow_max = ((int64_t)1 << (narrow_prec - 1)) - 1; // 127
    HOST_WIDE_INT narrow_min = -narrow_max - 1; // -128

    rtx xmode_src = riscv_extend_to_xmode_reg (src, GET_MODE (src), SIGN_EXTEND);
    /* Step-1: lt = src < max, gt = min < src, mask = lt & gt  */
    emit_move_insn (xmode_narrow_min, gen_int_mode (narrow_min, Xmode));
    emit_move_insn (xmode_narrow_max, gen_int_mode (narrow_max, Xmode));
    riscv_emit_binary (LT, xmode_lt, xmode_src, xmode_narrow_max);
    riscv_emit_binary (LT, xmode_gt, xmode_narrow_min, xmode_src);
    riscv_emit_binary (AND, xmode_mask, xmode_lt, xmode_gt);
    /* ... Step-2..5：sat_mask/trunc_mask 混合，最后取 lowpart 写回 dest ... */
    emit_move_insn (dest, gen_lowpart (mode, xmode_dest));
  }
  ```

  </details>

> 与 XINV-008 / XINV-009 的关系：这三条都属于种子 025 在 RISC-V 后端的"窄化/符号扩展"根因族。XINV-008（`riscv_add_offset`）和 XINV-009（`synthesize_add_extended`，BM25 命中）落在**大偏移地址物化**上；XINV-011（`riscv_expand_sstrunc`，embedding 命中）落在**有符号饱和截断**上。同一根因形状在后端的三个不同发射点各命中一次，稠密检索补齐了饱和截断这个词面锚点较弱的分支。

---

## 四、分析与结论

1. **交集即稳健信号**。5 条交集覆盖全部三类根因族，说明这三类根因在 GCC 源码里同时具备强词面锚点和强语义相似度，是跨机制迁移中最可靠的候选，适合优先提升为正式规则。

2. **两种检索是互补而非替代关系**。BM25 独有的 4 条都锚定在高区分度标识符（`ix86_zero_call_used_regs`、`SUBREG_PROMOTED`、`CONST_HIGH_PART`）上——这是词法检索的主场；embedding 独有的 2 条则靠语义相似度拉入了词面锚点较弱、但根因同构的块（CMSE 清除集合构造、RISC-V 饱和截断）。把两条路径的并集（11 条）作为最终候选池，比任何单一检索都更完整。

3. **dense 检索的净收益是 2 条，且都不重复**。经 `NoveltyBaseline`（阈值 85.0）去重后，两条 embedding 独有命中均 `is_novel=True`，且经人工核对与既有 XINV-001~009 分属不同发射点（分别是 CMSE 清除集合本身、RISC-V 饱和截断），不构成语义重复。其中 XINV-010 的最近距离 83.63 已逼近阈值——它与 CMSE 出口族高度相关但仍被判为新规则，这个边界值本身值得在后续调阈值时留意。

4. **稠密检索有非确定性代价**。`doubao-embedding-vision` 对同一 query 每次返回略有差异的向量，会让 top-k 边界（rank 8~9）的 marginal 块在多次运行间轮换。我们通过给 `EmbeddingRetriever` 增加 query 向量缓存（`cache/query_vectors.json`，按 query 原文 keying）锁定排序，之后 `pending=0` 可跨运行复现。这是词法检索天然没有、稠密检索必须额外处理的工程点。

### 产物索引

- BM25 路径产物：`orchestrator/runs/specgen_full/`（`candidates.jsonl` 9 条 · `transcript.json` · `manifest.json`）
- Embedding 路径产物：`orchestrator/runs/specgen_embed/`（`candidates.jsonl` 7 条 · `accepted/001.md`~`007.md` · `cache/query_vectors.json`）
- BM25 不变量正文：[cross-mechanism-generated.md](file:///Users/bytedance/projects/research/de-fuzz/feat-specgen-rag-invariants/docs/tech-docs/invariants/cross-mechanism-generated.md)（XINV-001~009）
- 中文摘要：[cross-mechanism-generated-zh-summary.md](file:///Users/bytedance/projects/research/de-fuzz/feat-specgen-rag-invariants/docs/tech-docs/invariants/cross-mechanism-generated-zh-summary.md)
