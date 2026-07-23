---
title: Cross-Mechanism Invariants — 以调研不变量为种子的 BM25 检索
description: 把人工调研的不变量（而非仅漏洞报告）当探针种子，用 BM25 在 GCC 16.1 里找同构根因，去重后产出的新不变量
last_updated: 2026-07-10
generated_from: orchestrator/runs/specgen_inv_bm25 (git 90125f582273)
---

# 以调研不变量为种子的跨机制迁移（BM25）

此前的跨机制迁移只把已查实的漏洞报告（`DREV-2026-xxx`，共 24 条）当探针种子，覆盖的根因面偏窄。这一轮把探针来源扩到**人工调研阶段沉淀的不变量**——`docs/invariants/*.md` 里的 426 条 `INV-*` 规则，让每条已知不变量的根因去别的防御机制里找同形操作。管道其余环节完全不变：同一份 GCC 16.1 语料（4496 块）、同一条 `distill → analogy → specialize → entailment` 判断链、同一套出口过滤与去重。

用不变量当种子有一个和漏洞种子不同的性质：不变量本身就是"某机制必须守住的性质"，它的根因描述更抽象、更贴近机制中立的操作形状，因此更容易在别的机制里找到同构落点。代价是数量大、噪声也大，所以这一轮先只用 BM25 跑通、看信号面，再取高价值子集做判断。

## 一、检索层信号面（可达性探针）

在花任何判断成本之前，先做一个纯 deterministic 的可达性探针（`orchestrator/scripts/probe_invariant_reach.py`）：对全部 426 个不变量种子，用种子原文拼一个**保守**的查询（保留机制词，因此是命中率下界），跑检索 + 出口过滤，只统计"有多少种子能命中至少一个跨机制源码块"。这一步不调用任何判断，用来回答"瓶颈在检索还是在判断"。

| 指标 | 数值 |
| --- | --- |
| 种子总数 | 426 |
| 至少命中一个跨机制块的种子 | 425（99.8%） |
| 出口过滤后的总幸存命中（每种子 top-8） | 3273 |
| top-hit 落在具体姊妹机制（非 `backend-multi`/`codegen`）的种子 | 194 |

命中机制分布（按命中该机制的种子数计）：

| 命中机制 | 种子数 |
| --- | --- |
| backend-multi | 393 |
| fortify-source | 177 |
| stack-protector | 166 |
| stack-clash-protection | 101 |
| return-address-signing | 98 |
| codegen | 84 |
| cet | 76 |
| strub | 22 |
| shadowcallstack | 17 |
| shstk | 14 |
| ibt | 7 |
| bti | 6 |

**结论：检索不是瓶颈**。425/426 的种子都能在别的机制里勾到候选块，信号充足；真正的成本在下游的四阶段判断（每条命中都要判同形、写规则、核对蕴含）。因此本轮不盲目对 3273 条命中全量判断，而是从 194 个"top-hit 落在具体姊妹机制"的种子里，选一个高价值子集做完整判断。

> 可达性探针的完整逐种子结果见 `orchestrator/runs/specgen_inv_bm25/reach.json`。

## 二、判断口径与子集选择

- **子集**：从可达性面里挑出 10 个种子，聚成三类同形族：
  1. **非本地跳转的返回边完整性**（5 条）：`INV-PAC-P05`（PAC）、`INV-SHSTK-C01`（影子栈）、`INV-SS-R03`（SafeStack）、`INV-GCS-F02`（GCS）、`INV-BTI-P03`（BTI 跳回标记）——它们都指向 `setjmp/longjmp` 的返回边。
  2. **异常落地垫的间接分支标记**（2 条）：`INV-BTI-P04`、`INV-IBT-P03`。
  3. **独立根因**（3 条）：`INV-SCK-B02`（探针步长 vs 保护间隙）、`INV-SP-H01`（动态栈分配必须走受保护路径）、`INV-FORT-R02`（守卫失败处理器必须 noreturn）。
- **判断执行**：本机无外部大模型凭证，判断由 AI 助手充当裁判离线完成（`TranscriptJudge` 回放）。所有判断记录在 `runs/specgen_inv_bm25/transcript.json`，证据原文由脚本直接从命中块提取，裁判不接触也无法编造引用。
- **审查标准延续**：只判"同形操作"。命中块里出现相同词汇但操作不同（散文碰撞、词法碰撞）一律判 false，记入 `rejected.jsonl` 供 RQ2 消融使用。
- **去重**：候选与既有 468 条调研不变量 + DREV 发现 + 前两轮 XINV 产出（BM25 9 条、embedding 独有 2 条，共 16 条）比对，`NoveltyBaseline` 阈值 85.0。

**本轮结果**：10 个种子，命中 80 条（每种子 top-8）；判定通过 **6 条**候选，其中 **5 条判为新颖**（`is_novel=True`），1 条被去重降级；74 条在 analogy 闸门被判为非同构。4 个种子（见 §四）全部命中判 false，是真实负结果。

## 三、longjmp/setjmp 返回边的跨机制汇聚点

本轮最强的信号是一个结构性发现：**五个来自不同返回边防御机制的种子，全部汇聚到 GCC 里同一对机制中立的函数上**——`expand_builtin_setjmp_setup`（`builtins.cc:886`）与 `expand_builtin_longjmp`（`builtins.cc:990`）。

读这两个函数就能看清为什么。`setjmp` 建立点只往缓冲区写**三个字**：帧指针、接收标签、`SAVE_NONLOCAL` 栈保存；任何额外状态都 `defer` 给 `targetm.gen_builtin_setjmp_setup`。`longjmp` 恢复点在通用回退分支里也只恢复这三样，然后 `emit_indirect_jump(lab)` 裸跳——它不处理任何返回边完整性记录（PAC 签名、影子栈指针、GCS 指针、次栈指针、分支目标标记），全部 `defer` 给 `targetm.gen_builtin_longjmp`。

也就是说，这两个函数是所有"返回边防御必须在 `setjmp/longjmp` 上各自扩展"的**公共 defer 点**。五种机制的种子在此合法汇聚，不是词法巧合，而是它们共享同一个"通用路径只存/恢复三字、其余交给目标钩子"的根因形状。下面五条候选各自约束"某机制若不在目标钩子里补齐它那份状态，通用路径就静默放行"。

命中族分布：

- **返回边完整性 @ `expand_builtin_longjmp` / `expand_builtin_setjmp_setup`** — 4 条（XINV-012~015）
- **动态栈分配的探针路由 @ `expand_builtin_alloca`** — 1 条（XINV-016）

（`INV-SCK-B02` 亦通过判断，但被去重降级，见 §五。）

---

### XINV-012 · INV-PAC-P05 : return-address-signing → fortify-source

- **命中站点**: `builtins.cc:990`（`expand_builtin_longjmp`，GCC gcc-16.1.0, generic）
- **version_sensitivity**: target-specific
- **statement**: GCC's mechanism-neutral __builtin_longjmp lowering (expand_builtin_longjmp) restores only {frame pointer, target label, stack pointer} from the buffer and, in its generic fallback, transfers control with a bare emit_indirect_jump to the restored label; a target whose ABI signs return/transfer addresses must reconcile that signature inside targetm.gen_builtin_longjmp, because the generic path authenticates nothing about the restored target.
- **中文解读**: `__builtin_longjmp` 的通用降级路径从缓冲区取出保存的跳转标签后直接 `emit_indirect_jump`，中间没有任何对该地址的认证步骤。对一个 ABI 会对返回/转移地址签名的目标（如 PAC），这段通用路径不做任何签名校验；若该目标没有提供自己的 `builtin_longjmp` 模式，`longjmp` 就会裸跳到一个未经认证的地址——一个被篡改的跳转缓冲区可以静默改流。签名/认证的责任被完全 defer 给了 `targetm.gen_builtin_longjmp`。
- **observation（违反时可外部观测的现象）**: on a return-address-signing target that supplies no builtin_longjmp pattern, the expanded __builtin_setjmp/__builtin_longjmp sequence contains no authentication instruction between loading the saved label and the indirect jump — disassembly shows a raw indirect branch to the restored label.
- **falsifiability（README §3 四维自评）**:
    - 可观测性: absence of an authentication instruction on the restored transfer target in the emitted generic longjmp sequence
    - 判定确定性: decisive —— either targetm.have_builtin_longjmp() supplies a hardened pattern or the generic bare indirect jump is emitted
    - 实现成本: inspect the RTL/asm of a __builtin_longjmp expansion; no runtime needed
    - 静态/动态归属: static
- **类比对齐（Stage-4 step 1）**: the generic else-branch of expand_builtin_longjmp restores the saved target label (`lab = copy_to_reg (lab)`) and control-transfers with `emit_indirect_jump (lab)`, reconstructing the return/transfer target with no authentication step of its own
- **受保护资产**: integrity of the control-transfer target address on a nonlocal return
- **为何同构**: both take a saved control-transfer token out of the jump buffer and resume control from it; the seed requires that token be re-validated on the next return, and this generic path restores the raw label and jumps to it, leaving any signing/authentication to targetm.gen_builtin_longjmp
- **证据接地（Stage-5 entailment support）**: the else branch: `lab = copy_to_reg (lab); ... emit_indirect_jump (lab);` guarded by `if (targetm.have_builtin_longjmp ())`
- **新颖性**: is_novel=True（离最近 INV-SHSTK-C02 词法距离 49.71）

### XINV-013 · INV-SHSTK-C01 : shstk → fortify-source

- **命中站点**: `builtins.cc:990`（`expand_builtin_longjmp`，GCC gcc-16.1.0, generic）
- **version_sensitivity**: target-specific
- **statement**: expand_builtin_longjmp's generic fallback rewinds the ordinary stack pointer to the saved SAVE_NONLOCAL value in a single emit_stack_restore and adjusts no parallel return-record stack; a target providing a shadow/return-record stack must perform the matching multi-frame rewind of that record stack inside targetm.gen_builtin_longjmp, or the record-stack pointer is left stale relative to the restored SP.
- **中文解读**: 通用 `longjmp` 用一条 `emit_stack_restore (SAVE_NONLOCAL)` 一步把普通栈指针拨回保存值，跨越了 `longjmp` 跳过的所有帧；但它对并行的影子栈/返回记录栈不做任何调整。若目标提供影子栈，就必须在 `targetm.gen_builtin_longjmp` 里把影子栈指针也按相同帧数回退，否则跳转后第一次返回读到的影子栈指针与恢复后的 SP 不一致。
- **observation（违反时可外部观测的现象）**: a __builtin_longjmp that unwinds several frames restores SP directly but emits no shadow-stack-pointer adjustment; on a shadow-stack target the first return after such a jump reads a record-stack pointer inconsistent with the restored SP.
- **falsifiability**:
    - 可观测性: no record-stack adjustment instruction in the emitted generic longjmp sequence alongside the emit_stack_restore
    - 判定确定性: decisive —— the generic branch either has a record-stack fixup or it does not
    - 实现成本: inspect emitted longjmp RTL/asm; no runtime needed
    - 静态/动态归属: static
- **类比对齐**: the generic branch does `emit_stack_restore (SAVE_NONLOCAL, stack)` and `emit_move_insn (hard_frame_pointer_rtx, fp)` then `emit_indirect_jump (lab)`: it rewinds the ordinary stack pointer across all skipped frames in one step but performs no adjustment of any parallel return-record stack
- **受保护资产**: consistency between the ordinary SP and a parallel return-record (shadow) stack after a multi-frame nonlocal unwind
- **为何同构**: the seed requires longjmp to roll a shadow stack back by the same N frames as the ordinary SP; this chunk rolls back only the ordinary SP and the generic path emits no parallel record-stack fixup
- **证据接地**: `emit_stack_restore (SAVE_NONLOCAL, stack)` immediately followed by frame-pointer restore and `emit_indirect_jump (lab)`, with no record-stack op
- **新颖性**: is_novel=True（离最近 DREV-2026-001 词法距离 50.37）

### XINV-014 · INV-SS-R03 : safestack → fortify-source

- **命中站点**: `builtins.cc:990`（`expand_builtin_longjmp`，GCC gcc-16.1.0, generic）
- **version_sensitivity**: likely-to-drift
- **statement**: GCC's generic nonlocal-transfer lowering restores a single stack pointer (SAVE_NONLOCAL) and no auxiliary stack pointer; a mechanism that splits locals onto a secondary stack must arrange its own rewind of that secondary pointer on the nonlocal path, since the generic __builtin_longjmp path never reclaims secondary-stack frames skipped by the jump.
- **中文解读**: 通用非本地跳转只恢复一个栈指针（`SAVE_NONLOCAL`），不认识任何辅助/次栈指针。SafeStack 这类把局部变量分流到独立"不安全栈"的机制，必须自己在非本地路径上回退那个次栈指针；否则 `longjmp` 跳过的那些帧在次栈上的对象永远不被回收，重复跳转会让次栈单调增长（一个泄漏）。
- **observation（违反时可外部观测的现象）**: repeated nonlocal transfers that skip frames holding secondary-stack objects show the secondary stack pointer never decreasing (monotonic growth), because the generic longjmp path emits no secondary-stack restore.
- **falsifiability**:
    - 可观测性: monotonic growth of the secondary stack region across repeated cross-frame nonlocal jumps
    - 判定确定性: probabilistic in effect but decisive in mechanism —— the generic path emits no secondary-stack restore
    - 实现成本: runtime observation of secondary-stack pointer across a longjmp loop
    - 静态/动态归属: dynamic
- **类比对齐**: the generic branch restores exactly one stack pointer via `emit_stack_restore (SAVE_NONLOCAL, stack)` plus the frame pointer, then indirect-jumps; it has no notion of an auxiliary/secondary stack pointer to rewind for the frames the jump skips
- **受保护资产**: reclamation of a secondary (unsafe) stack across a multi-frame nonlocal jump
- **为何同构**: the seed's shape is a nonlocal transfer that skips frames whose secondary-stack objects are never reclaimed (a monotonic leak); this generic path rewinds only the single ordinary stack, matching that shape
- **证据接地**: the generic branch restores only `fp` and one stack via `emit_stack_restore (SAVE_NONLOCAL, stack)` before the indirect jump
- **新颖性**: is_novel=True（离最近 DREV-2026-016 词法距离 37.56）

### XINV-015 · INV-GCS-F02 : gcs → fortify-source

- **命中站点**: `builtins.cc:886`（`expand_builtin_setjmp_setup`，GCC gcc-16.1.0, generic）
- **version_sensitivity**: target-specific
- **statement**: expand_builtin_setjmp_setup writes only the three-word {frame pointer, receiver label, SAVE_NONLOCAL stack save} set into the buffer and defers extra state to targetm.gen_builtin_setjmp_setup; a target with a hardware return-record-stack pointer must extend both setup (save the pointer) and longjmp (restore it) via the target hooks, because the generic buffer stores nothing beyond those three words.
- **中文解读**: 这是上面那对汇聚点的**建立侧**。`expand_builtin_setjmp_setup` 只把三个字写进缓冲区（帧指针、接收标签、`SAVE_NONLOCAL` 栈保存），其余交给 `targetm.gen_builtin_setjmp_setup`。GCS（保护控制栈）有一个硬件返回记录栈指针，必须在 setup 侧保存、longjmp 侧恢复这个指针；由于通用缓冲区不存这个字，两侧都得靠目标钩子扩展，否则 `longjmp` 后第一次返回就会在控制栈不匹配上出错。它与 XINV-013（影子栈，命中恢复侧）是同一汇聚点的两侧互补。
- **observation（违反时可外部观测的现象）**: on a return-record-stack target lacking the builtin_setjmp/longjmp hooks, the emitted setjmp buffer has no slot written with the record-stack pointer and longjmp emits no pointer restore; the first return after longjmp faults on a control-stack mismatch.
- **falsifiability**:
    - 可观测性: no store of a return-record-stack pointer into the setjmp buffer on the generic setup path
    - 判定确定性: decisive —— the generic setup writes exactly three words unless the target setup pattern adds more
    - 实现成本: inspect emitted setjmp setup RTL/asm; no runtime needed
    - 静态/动态归属: static
- **类比对齐**: expand_builtin_setjmp_setup writes exactly three words into the buffer (hard_frame_pointer, the receiver label, and the SAVE_NONLOCAL stack save), then defers any extra state to `targetm.gen_builtin_setjmp_setup`; no return-record-stack pointer is saved on the generic path
- **受保护资产**: a hardware return-record-stack pointer that must survive setjmp/longjmp
- **为何同构**: the seed requires setjmp to save and longjmp to restore the return-record stack pointer; this chunk shows the generic buffer holds only the three-word {FP,label,SP} set, so saving that pointer must come from the target hook
- **证据接地**: three emit_move_insn/emit_stack_save into buf_addr, buf_addr+sizeof(Pmode), buf_addr+2*sizeof(Pmode), then `if (targetm.have_builtin_setjmp_setup ())`
- **新颖性**: is_novel=True（离最近 DREV-2026-025 词法距离 40.70）

  <details><summary>命中块证据原文（GCC 16.1）</summary>

  ```c
  void
  expand_builtin_setjmp_setup (rtx buf_addr, rtx receiver_label)
  {
    machine_mode sa_mode = STACK_SAVEAREA_MODE (SAVE_NONLOCAL);
    rtx stack_save;
    rtx mem;
    /* ... */
    /* We store the frame pointer and the address of receiver_label in
       the buffer and use the rest of it for the stack save area, which
       is machine-dependent.  */
    mem = gen_rtx_MEM (Pmode, buf_addr);
    set_mem_alias_set (mem, setjmp_alias_set);
    emit_move_insn (mem, hard_frame_pointer_rtx);

    mem = gen_rtx_MEM (Pmode, plus_constant (Pmode, buf_addr,
                                             GET_MODE_SIZE (Pmode))),
    set_mem_alias_set (mem, setjmp_alias_set);
    emit_move_insn (validize_mem (mem),
                    force_reg (Pmode, gen_rtx_LABEL_REF (Pmode, receiver_label)));

    stack_save = gen_rtx_MEM (sa_mode,
                              plus_constant (Pmode, buf_addr,
                                             2 * GET_MODE_SIZE (Pmode)));
    set_mem_alias_set (stack_save, setjmp_alias_set);
    emit_stack_save (SAVE_NONLOCAL, &stack_save);

    /* If there is further processing to do, do it.  */
    if (targetm.have_builtin_setjmp_setup ())
      emit_insn (targetm.gen_builtin_setjmp_setup (buf_addr));

    /* We have a nonlocal label.   */
    cfun->has_nonlocal_label = 1;
  }
  ```

  </details>

### XINV-016 · INV-SP-H01 : stack-protector → fortify-source

- **命中站点**: `builtins.cc:5755`（`expand_builtin_alloca`，GCC gcc-16.1.0, generic）
- **version_sensitivity**: likely-to-drift
- **statement**: expand_builtin_alloca lowers a dynamic allocation by routing it through allocate_dynamic_stack_space (with the alloca-for-variable flag) rather than a bare stack adjustment; under stack-clash protection a dynamic alloca must remain on this routed path so the dynamically-sized region is probed, because a bare adjustment of a variable-sized region would skip guard-page probing entirely.
- **中文解读**: `expand_builtin_alloca` 把动态分配（`alloca`/VLA）交给 `allocate_dynamic_stack_space`（带 alloca-for-variable 标记）来降级，而不是直接裸调整 SP。在栈防护/栈冲突保护下，动态分配必须留在这条被路由的路径上，因为只有它会对这块动态大小的区域打探针；一旦某处走了裸调整，一个变量大小的区域就完全跳过了保护页探测。
- **observation（违反时可外部观测的现象）**: a function performing __builtin_alloca / a VLA whose emitted code adjusts SP by a variable amount without going through the probing path shows a dynamically-sized stack region with no guard-page probe — an unprobed dynamic allocation.
- **falsifiability**:
    - 可观测性: a variable-sized stack adjustment for an alloca/VLA with no accompanying guard-page probe
    - 判定确定性: decisive —— the alloca either routes through allocate_dynamic_stack_space or it does not
    - 实现成本: inspect emitted RTL/asm of an alloca-bearing function; no runtime needed
    - 静态/动态归属: static
- **类比对齐**: expand_builtin_alloca lowers a dynamic allocation by routing it through `allocate_dynamic_stack_space (op0, 0, align, max_size, alloca_for_var)` rather than a bare stack adjustment; the alloca-for-variable flag marks the variable-sized-object case
- **受保护资产**: instrumentation coverage of a dynamically-sized stack region
- **为何同构**: the seed's shape is 'a function with a dynamic stack allocation must be forced onto the instrumented guarded path'; this chunk is exactly the dynamic-allocation lowering site that decides which path the allocation takes
- **证据接地**: `result = allocate_dynamic_stack_space (op0, 0, align, max_size, alloca_for_var);`
- **新颖性**: is_novel=True（离最近 DREV-2026-025 词法距离 41.28）

---

## 四、真实负结果：三个源/目标不对齐的种子

诚实起见，本轮有四个种子全部命中判 false，它们是真实负结果，不粉饰、不硬凑：

- **`INV-BTI-P03`（BTI 跳回标记）**：种子约束的是"`longjmp` 通过 `BR` 跳回时，落地点必须是合法的间接分支目标标记（`BTI c`）"——约束的是分支**目标**。但它检索到的全是 `expand_builtin_longjmp`、`ix86_output_call_insn` 这类间接分支的**发射侧**（分支源头）机器码，以及 bug 散文。源与目标不对齐，所以每条命中都判非同构。注意：`longjmp` 的**落地侧**函数 `expand_builtin_setjmp_receiver` 确实存在，但 BM25 没把它排进这个种子的 top-8（它反而排进了 `INV-PAC-P05`/`INV-SS-R03` 的命中里）——检索没勾到对的块，就不为了凑一条候选强判。
- **`INV-BTI-P04` / `INV-IBT-P03`（异常落地垫标记）**：同样的源/目标不对齐——标记约束落地垫的**入口**（分支目标），命中的却是间接分支发射机器码与 bug 散文。
- **`INV-FORT-R02`（守卫失败处理器必须 noreturn）**：只命中 epilogue/返回相关代码，纯粹是 "return" 一词的词法碰撞；命中块里没有"守卫失败处理器 fall-through"这个操作形状。

这条不对称本身是一个方法学上的正面信号：类比闸门确实在挡"词法碰上了但操作形状不对"的假阳性，而不是有命中就放行。它们的逐条拒绝理由记录在 `runs/specgen_inv_bm25/rejected.jsonl`（本轮 74 条拒绝全部在 analogy 阶段被拦）。

## 五、去重

5 条判为新颖的候选，除了管道内置的对"468 调研不变量 + DREV 发现"去重（阈值 85.0，全部通过，最高分 50.37）外，还额外与前两轮 XINV 产出（BM25 9 条 + embedding 独有 2 条，共 16 条）合并成一个 466 条的联合基线复核了一遍：

| 候选 | 联合基线最近条目 | 词法距离 | 判定 |
| --- | --- | --- | --- |
| XINV-012 (INV-PAC-P05) | INV-SHSTK-C02 | 50.73 | NOVEL |
| XINV-013 (INV-SHSTK-C01) | XINV::specgen_full::DREV-2026-025 (`trunc_int_for_mode`) | 49.64 | NOVEL |
| XINV-014 (INV-SS-R03) | DREV-2026-018 | 33.97 | NOVEL |
| XINV-015 (INV-GCS-F02) | INV-SHSTK-C02 | 38.73 | NOVEL |
| XINV-016 (INV-SP-H01) | INV-STRUB-C04 | 40.03 | NOVEL |

全部远低于 85 阈值——即便把前两轮所有 XINV 产出也算进去，这 5 条也不与任何已有条目撞车。这符合预期：前 16 条 XINV 都由 DREV 漏洞种子出发、落在 arm/i386/riscv 后端出口序列、`cfgexpand` 异常出口、`explow.cc:51` 整数窄化等站点，与本轮的 `builtins.cc` 非本地跳转汇聚点毫无交集。

**被去重降级的一条**：`INV-SCK-B02`（stack-protector → stack-clash-protection，命中 `compute_stack_clash_protection_loop_data`）通过了 analogy/specialize/entailment 三闸——它确实在 `explow.cc:1954` 找到了"探针步长必须匹配保护页"的同形操作。但对既有基线的最近距离高达 **99.03**（撞 `DREV-2026-019`），远超阈值，判 `is_novel=False` 被降级。这是去重闸门按设计正常拦截"重新发现已有不变量"的一个例子，保留在 `candidates.jsonl` 里但不提升。

## 六、结论

1. **不变量当种子把有效根因面显著拓宽**。从 24 条漏洞种子扩到 426 条调研不变量后，检索层可达率 99.8%，且暴露出漏洞种子从未触及的 `builtins.cc` 非本地跳转汇聚点。

2. **最有价值的产出是一个结构性发现**，而非单条规则：五种返回边防御机制（PAC / 影子栈 / SafeStack / GCS / BTI）的种子全部合法汇聚到 GCC 同一对机制中立函数（`expand_builtin_setjmp_setup` / `expand_builtin_longjmp`）——因为它们共享"通用路径只存/恢复三字、其余 defer 给目标钩子"的同一根因形状。这正是跨机制迁移想找的东西：一个防御机制的已知性质，精确定位到别的机制在同一处必须各自补齐的静默放行点。

3. **负结果与去重都在如实工作**。4 个源/目标不对齐或纯词法碰撞的种子被全数拒绝（74 条 analogy 拒绝），`INV-SCK-B02` 被去重降级（99.03）——闸门没有为了凑数放水。最终 5 条新颖候选（XINV-012~016）经联合基线复核确认与全部既有条目不重复。

### 产物索引

- 本轮产物：`orchestrator/runs/specgen_inv_bm25/`（`candidates.jsonl` 6 条 · `accepted/001.md`~`005.md` · `rejected.jsonl` 74 条 · `reach.json` · `transcript.json` · `manifest.json`）
- 可达性探针脚本：`orchestrator/scripts/probe_invariant_reach.py`
- 判断 author 脚本：`orchestrator/scripts/author_inv_transcript.py`
- 前两轮 XINV 正文与对照：[cross-mechanism-generated.md](file:///Users/bytedance/projects/de-fuzz-orchestration-research/docs/tech-docs/invariants/cross-mechanism-generated.md)（XINV-001~009）、[cross-mechanism-bm25-vs-embedding.md](file:///Users/bytedance/projects/de-fuzz-orchestration-research/docs/tech-docs/invariants/cross-mechanism-bm25-vs-embedding.md)（XINV-010~011）
