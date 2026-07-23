---
title: Cross-Mechanism Generated Invariants (Innovation A)
description: RAG 跨防御机制类比迁移管道产出的新不变量 —— 按防御机制整理
last_updated: 2026-07-03
generated_from: orchestrator/runs/specgen_full (git 90125f582273)
---

# 跨机制生成的不变量（创新点 A）

这份文档记录了通过 RAG 跨机制迁移管道（`orchestrator/defuzz_loop/specgen`）挖掘出的新不变量。

区别于 `README.md` 中的人工总结，这些规则是自动生成的：我们将已知漏洞的根因（DREV-2026-xxx 种子）作为探针，在 GCC 16.1 源码中寻找其他防御机制的同构缺陷。发现匹配后，将其转化为目标机制的新规则，并进行“证据蕴含”与“可证伪面”两项静态验证。

## 执行口径

- **探针种子**：`findings/` 目录下的 24 份已查实报告（跳过了前置数据畸形的 021 号种子）。
- **检索范围**：GCC 16.1 源码与 Bugzilla，共切分为 4496 个语料块。使用 BM25 算法（top_k=8，去重阈值 85.0）。
- **四阶段判断**：
  1. Distill：提取机制中立的根因特征。
  2. Analogy：验证源码中是否存在同构操作。
  3. Specialize：生成目标机制专属的规则与可观测现象。
  4. Entailment：核对规则是否被源码原文严格支持。
- **审查标准**：规则必须有代码原文支撑（防止幻觉），且具备明确的可观测现象（不要求构造具体的 PoC）。
- **本轮结果**：通过 9 条，拒绝 96 条（均判定为 BM25 词法碰撞，在 Analogy 阶段拦截）。

> 注：由于缺少外部大模型 API 凭证，本次执行中的判断工作由 AI 助手充当裁判（LLM Judge）完成。所有判断依据均记录在 `runs/specgen_full/transcript.json`，证据原文由脚本直接从代码块提取，以保证客观性。

## 按防御机制整理

机制分布:

- **出口敏感寄存器/状态擦除 (zero-call-used-regs · CMSE secure-entry · SME state)** — 6 条
- **stack-clash-protection — 帧大小位宽完整性** — 1 条
- **RISC-V 后端 — 大偏移地址物化完整性** — 2 条

---

### 出口敏感寄存器/状态擦除 (zero-call-used-regs · CMSE secure-entry · SME state)

本组不变量都属于同一族防御目标: **一个敏感值（调用者可见寄存器、CMSE 安全态、SME ZA/ZT0 状态）在控制流离开函数的某条出口边上没有被擦除**。种子 DREV-2026-001（stack-protector：秘密残留在返回边寄存器）与 DREV-2026-020（zero-call-used-regs：异常出口特判导致正常返回边漏清）分别作为探针，在其它后端的出口序列发射点命中同形操作。

#### XINV-001 · DREV-2026-001 : stack-protector → backend-multi

- **命中站点**: `config/arm/arm.cc:26305` （GCC gcc-16.1.0, target `arm`）
- **version_sensitivity**: target-specific
- **statement**: In thumb_exit, every return form that can be reached for a cmse_nonsecure_entry function must emit the secure-state register clearing before the branch that transfers control out of the function; a return path that reaches its exit branch (bx/bxns/pop pc) without having emitted the clear leaves callee register state readable to the non-secure caller.
- **中文解读**: 在 ARM 架构的 `thumb_exit` 函数中，存在一个快速返回的路径（直接使用 `bx` 指令跳出）。这个路径漏掉了对敏感寄存器的清零操作，导致机密状态（如 CMSE 安全态）在函数返回后被泄露给调用者。
- **observation（违反时可外部观测的现象）**: In the disassembly of a cmse_nonsecure_entry function, an exit form (e.g. the pops_needed==0 `bx`/`bxns` path) reaches its return branch with no preceding CLRM / msr APSR_nzcvq / mov-immediate scrub of the call-clobbered registers, so those registers still hold in-function values at the secure-to-nonsecure boundary.
- **falsifiability（README §3 四维自评）**:
    - 可观测性: static: for each return form emitted by thumb_exit, check whether a register-clearing insn precedes the exit branch on that path in the emitted assembly; a bare branch with no scrub is the violation.
    - 判定确定性: deterministic given the function type and target flags — the chosen exit form and whether a scrub precedes it are fixed by the compile, not runtime state.
    - 实现成本: low: inspect the epilogue insn sequence of the affected function; no whole-program analysis.
    - 静态/动态归属: static
- **类比对齐（Stage-4 step 1）**: thumb_exit()'s `if (pops_needed == 0)` branch emits the return instruction (`bx`/`bxns %r`) and returns; the register-scrub step (msr APSR_nzcvq) is emitted only on the IS_CMSE_ENTRY sub-path, so the plain `bx` exit form leaves registers unscrubbed on the return edge
- **受保护资产**: the contents of caller-visible / secret-holding registers live at the point control leaves the function
- **为何同构**: the seed's root cause is a sensitive value surviving past the return edge because the exit path emits no overwriting instruction; thumb_exit is exactly an exit-sequence emitter with a form-specific early-return path (`pops_needed==0` -> bx) that reaches the branch without emitting a scrub, the same shape of missing-clobber-on-exit
- **证据接地（Stage-5 entailment support）**: the `if (pops_needed == 0)` block: it emits `bxns`/`bx %r` and returns, with the msr APSR_nzcvq scrub only inside the IS_CMSE_ENTRY sub-branch
- **新颖性**: is_novel=True (离最近种子 DREV-2026-016 词法距离 68.88)

  <details><summary>命中块证据原文（GCC 16.1）</summary>

  ```c
     Note: do not forget to update length attribute of corresponding insn pattern
     when changing assembly output (eg. length attribute of epilogue_insns when
     updating Armv8-M Baseline Security Extensions register clearing
     sequences).  */
  static void
  thumb_exit (FILE *f, int reg_containing_return_addr)
  {
    unsigned regs_available_for_popping;
    unsigned regs_to_pop;
    int pops_needed;
    unsigned available;
    unsigned required;
    machine_mode mode;
    int size;
    int restore_a4 = FALSE;
  
    /* Compute the registers we need to pop.  */
    regs_to_pop = 0;
    pops_needed = 0;
  
    if (reg_containing_return_addr == -1)
      {
        regs_to_pop |= 1 << LR_REGNUM;
        ++pops_needed;
      }
  
    if (TARGET_BACKTRACE)
      {
        /* Restore the (ARM) frame pointer and stack pointer.  */
        regs_to_pop |= (1 << ARM_HARD_FRAME_POINTER_REGNUM) | (1 << SP_REGNUM);
        pops_needed += 2;
      }
  
    /* If there is nothing to pop then just emit the BX instruction and
       return.  */
    if (pops_needed == 0)
      {
        if (crtl->calls_eh_return)
  	asm_fprintf (f, "\tadd\t%r, %r\n", SP_REGNUM, ARM_EH_STACKADJ_REGNUM);
  
        if (IS_CMSE_ENTRY (arm_current_func_type ()))
  	{
  	  /* For Armv8.1-M, this is cleared as part of the CLRM instruction
  	     emitted by cmse_nonsecure_entry_clear_before_return ().  */
  	  if (!TARGET_HAVE_FPCXT_CMSE)
  	    asm_fprintf (f, "\tmsr\tAPSR_nzcvq, %r\n",
  			 reg_containing_return_addr);
  	  asm_fprintf (f, "\tbxns\t%r\n", reg_containing_return_addr);
  	}
        else
  	asm_fprintf (f, "\tbx\t%r\n", reg_containing_return_addr);
        return;
      }
    /* Otherwise if we are not supporting interworking and we have not created
       a backtrace structure and the function was not entered in ARM mode then
       just pop the return address straight into the PC.  */
    else if (!TARGET_INTERWORK
  	   && !TARGET_BACKTRACE
  	   && !is_called_in_ARM_mode (current_function_decl)
  	   && !crtl->calls_eh_return
  	   && !IS_CMSE_ENTRY (arm_current_func_type ()))
      {
        asm_fprintf (f, "\tpop\t{%r}\n", PC_REGNUM);
        return;
      }
  
    /* Find out how many of the (return) argument registers we can corrupt.  */
    regs_available_for_popping = 0;
  
    /* If returning via __builtin_eh_return, the bottom three registers
       all contain information needed for the return.  */
    if (crtl->calls_eh_return)
      size = 12;
    else
      {
        /* If we can deduce the registers used from the function's
  	 return value.  This is more reliable that examining
  	 df_regs_ever_live_p () because that will be set if the register is
  	 ever used in the function, not just if the register is used
  	 to hold a return value.  */
  
        if (crtl->return_rtx != 0)
  	mode = GET_MODE (crtl->return_rtx);
        else
  	mode = DECL_MODE (DECL_RESULT (current_function_decl));
  
        size = GET_MODE_SIZE (mode);
  
        if (size == 0)
  	{
  	  /* In a void function we can use any argument register.
  	     In a function that returns a structure on the stack
  	     we can use the second and third argument registers.  */
  	  if (mode == VOIDmode)
  	    regs_available_for_popping =
  	      (1 << ARG_REGISTER (1))
  	      | (1 << ARG_REGISTER (2))
  	      | (1 << ARG_REGISTER (3));
  	  else
  	    regs_available_for_popping =
  	      (1 << ARG_REGISTER (2))
  	      | (1 << ARG_REGISTER (3));
  	}
        else if (size <= 4)
  	regs_available_for_popping =
  	  (1 << ARG_REGISTER (2))
  	  | (1 << ARG_REGISTER (3));
        else if (size <= 8)
  	regs_available_for_popping =
  	  (1 << ARG_REGISTER (3));
      }
  
    /* Match registers to be popped with registers into which we pop them.  */
    for (available = regs_available_for_popping,
         required  = regs_to_pop;
         required != 0 && available != 0;
         available &= ~(available & - available),
         required  &= ~(required  & - required))
      -- pops_needed;
  
    /* If we have any popping registers left over, remove them.  */
    if (available > 0)
      regs_available_for_popping &= ~available;
  
    /* Otherwise if we need another popping register we can use
       the fourth argument register.  */
    else if (pops_needed)
      {
        /* If we have not found any free argument registers and
  	 reg a4 contains the return address, we must move it.  */
        if (regs_available_for_popping == 0
  	  && reg_containing_return_addr == LAST_ARG_REGNUM)
  	{
  	  asm_fprintf (f, "\tmov\t%r, %r\n", LR_REGNUM, LAST_ARG_REGNUM);
  	  reg_containing_return_addr = LR_REGNUM;
  	}
        else if (size > 12)
  	{
  	  /* Register a4 is being used to hold part of the return value,
  	     but we have dire need of a free, low register.  */
  	  restore_a4 = TRUE;
  
  	  asm_fprintf (f, "\tmov\t%r, %r\n",IP_REGNUM, LAST_ARG_REGNUM);
  	}
  
        if (reg_containing_return_addr != LAST_ARG_REGNUM)
  	{
  	  /* The fourth argument register is available.  */
  	  regs_available_for_popping |= 1 << LAST_ARG_REGNUM;
  
  	  --pops_needed;
  	}
      }
  
    /* Pop as many registers as we can.  */
    thumb_pop (f, regs_available_for_popping);
  
    /* Process the registers we popped.  */
    if (reg_containing_return_addr == -1)
      {
        /* The return address was popped into the lowest numbered register.  */
        regs_to_pop &= ~(1 << LR_REGNUM);
  
        reg_containing_return_addr =
  	number_of_first_bit_set (regs_available_for_popping);
  
        /* Remove this register for the mask of available registers, so that
           the return address will not be corrupted by further pops.  */
        regs_available_for_popping &= ~(1 << reg_containing_return_addr);
      }
  
    /* If we popped other registers then handle them here.  */
    if (regs_available_for_popping)
      {
        int frame_pointer;
  
        /* Work out which register currently contains the frame pointer.  */
        frame_pointer = number_of_first_bit_set (regs_available_for_popping);
  
        /* Move it into the correct place.  */
        asm_fprintf (f, "\tmov\t%r, %r\n",
  		   ARM_HARD_FRAME_POINTER_REGNUM, frame_pointer);
  
        /* (Temporarily) remove it from the mask of popped registers.  */
        regs_available_for_popping &= ~(1 << frame_pointer);
        regs_to_pop &= ~(1 << ARM_HARD_FRAME_POINTER_REGNUM);
  
        if (regs_available_for_popping)
  	{
  	  int stack_pointer;
  
  	  /* We popped the stack pointer as well,
  	     find the register that contains it.  */
  	  stack_pointer = number_of_first_bit_set (regs_available_for_popping);
  
  	  /* Move it into the stack register.  */
  	  asm_fprintf (f, "\tmov\t%r, %r\n", SP_REGNUM, stack_pointer);
  
  	  /* At this point we have popped all necessary registers, so
  	     do not worry about restoring regs_available_for_popping
  	     to its correct value:
  
  	     assert (pops_needed == 0)
  	     assert (regs_available_for_popping == (1 << frame_pointer))
  	     assert (regs_to_pop == (1 << STACK_POINTER))  */
  	}
        else
  	{
  	  /* Since we have just move the popped value into the frame
  	     pointer, the popping register is available for reuse, and
  	     we know that we still have the stack pointer left to pop.  */
  	  regs_available_for_popping |= (1 << frame_pointer);
  	}
      }
  
    /* If we still have registers left on the stack, but we no longer have
       any registers into which we can pop them, then we must move the return
       address into the link register and make available the register that
       contained it.  */
    if (regs_available_for_popping == 0 && pops_needed > 0)
      {
        regs_available_for_popping |= 1 << reg_containing_return_addr;
  
        asm_fprintf (f, "\tmov\t%r, %r\n", LR_REGNUM,
  		   reg_containing_return_addr);
  
        reg_containing_return_addr = LR_REGNUM;
      }
  
    /* If we have registers left on the stack then pop some more.
       We know that at most we will want to pop FP and SP.  */
    if (pops_needed > 0)
      {
        int  popped_into;
        int  move_to;
  
        thumb_pop (f, regs_available_for_popping);
  
        /* We have popped either FP or SP.
  	 Move whichever one it is into the correct register.  */
        popped_into = number_of_first_bit_set (regs_available_for_popping);
        move_to     = number_of_first_bit_set (regs_to_pop);
  
        asm_fprintf (f, "\tmov\t%r, %r\n", move_to, popped_into);
        --pops_needed;
      }
  
    /* If we still have not popped everything then we must have only
       had one register available to us and we are now popping the SP.  */
    if (pops_needed > 0)
      {
        int  popped_into;
  
        thumb_pop (f, regs_available_for_popping);
  
        popped_into = number_of_first_bit_set (regs_available_for_popping);
  
        asm_fprintf (f, "\tmov\t%r, %r\n", SP_REGNUM, popped_into);
        /*
  	assert (regs_to_pop == (1 << STACK_POINTER))
  	assert (pops_needed == 1)
        */
      }
  
    /* If necessary restore the a4 register.  */
    if (restore_a4)
      {
        if (reg_containing_return_addr != LR_REGNUM)
  	{
  	  asm_fprintf (f, "\tmov\t%r, %r\n", LR_REGNUM, LAST_ARG_REGNUM);
  	  reg_containing_return_addr = LR_REGNUM;
  	}
  
        asm_fprintf (f, "\tmov\t%r, %r\n", LAST_ARG_REGNUM, IP_REGNUM);
      }
  
    if (crtl->calls_eh_return)
      asm_fprintf (f, "\tadd\t%r, %r\n", SP_REGNUM, ARM_EH_STACKADJ_REGNUM);
  
    /* Return to caller.  */
    if (IS_CMSE_ENTRY (arm_current_func_type ()))
      {
        /* This is for the cases where LR is not being used to contain the return
           address.  It may therefore contain information that we might not want
  	 to leak, hence it must be cleared.  The value in R0 will never be a
  	 secret at this point, so it is safe to use it, see the clearing code
  	 in cmse_nonsecure_entry_clear_before_return ().  */
        if (reg_containing_return_addr != LR_REGNUM)
  	asm_fprintf (f, "\tmov\tlr, r0\n");
  
        /* For Armv8.1-M, this is cleared as part of the CLRM instruction emitted
  	 by cmse_nonsecure_entry_clear_before_return ().  */
        if (!TARGET_HAVE_FPCXT_CMSE)
  	asm_fprintf (f, "\tmsr\tAPSR_nzcvq, %r\n", reg_containing_return_addr);
        asm_fprintf (f, "\tbxns\t%r\n", reg_containing_return_addr);
      }
    else
      asm_fprintf (f, "\tbx\t%r\n", reg_containing_return_addr);
  }
  ```

  </details>

#### XINV-002 · DREV-2026-001 : stack-protector → backend-multi

- **命中站点**: `config/aarch64/aarch64.cc:31923` （GCC gcc-16.1.0, target `aarch64`）
- **version_sensitivity**: target-specific
- **statement**: In aarch64_mode_emit_local_sme_state, every transition into a mode on which ZA/ZT0 must not carry the previous function's sensitive state must emit the corresponding zeroing (initial_zero_za / sme_zero_zt0); a prev_mode arm that returns without emitting the zeroing for a state the function actually holds leaves that SME register state live past the edge on which it should be destroyed.
- **中文解读**: AArch64 架构在进行 SME（可扩展矩阵扩展）状态切换时，某些前置模式的提前返回分支漏发了清零指令（`zero {za/zt0}`），导致敏感的寄存器数据在不该存活的代码边界后依然残留。
- **observation（违反时可外部观测的现象）**: For a function that has ZA (or ZT0) state, a mode-transition edge is emitted with no `zero {za}` / `zero {zt0}` instruction on a path where the previous mode's state is no longer valid, so the SME register file still contains the stale sensitive contents after the transition.
- **falsifiability（README §3 四维自评）**:
    - 可观测性: static: enumerate the (prev_mode, mode) arms; for each arm that returns, check the arm emits a zeroing insn whenever the function has the corresponding state; a returning arm with a live state and no zero is the violation.
    - 判定确定性: deterministic: the emitted arm and its zeroing are a pure function of the mode pair and aarch64_cfun_has_state().
    - 实现成本: low: local inspection of the emitted SME transition sequence.
    - 静态/动态归属: static
- **类比对齐（Stage-4 step 1）**: aarch64_mode_emit_local_sme_state() zeroes sensitive SME state via gen_aarch64_initial_zero_za()/gen_aarch64_sme_zero_zt0() only inside `if (aarch64_cfun_has_state("za"/"zt0"))`, and several prev_mode arms `return` earlier without emitting any zeroing
- **受保护资产**: the ZA / ZT0 SME register-file state, which is sensitive and must not survive across the mode-transition edge it is dead on
- **为何同构**: same shape as the seed: clearing of a sensitive register is guarded by a liveness/state predicate and skipped on some control-flow arms, so on those arms the sensitive value survives past the edge where it should have been destroyed
- **证据接地（Stage-5 entailment support）**: `if (aarch64_cfun_has_state("za")) emit_insn (gen_aarch64_initial_zero_za ());` and the several prev_mode arms that `return;` without emitting a zero
- **新颖性**: is_novel=True (离最近种子 DREV-2026-016 词法距离 74.28)

  <details><summary>命中块证据原文（GCC 16.1）</summary>

  ```c
  static void
  aarch64_mode_emit_local_sme_state (aarch64_local_sme_state mode,
  				   aarch64_local_sme_state prev_mode)
  {
    /* Back-propagation should ensure that we're always starting from
       a known mode.  */
    gcc_assert (prev_mode != aarch64_local_sme_state::ANY);
  
    if (prev_mode == aarch64_local_sme_state::INACTIVE_CALLER)
      {
        /* Commit any uncommitted lazy save.  This leaves ZA either active
  	 and zero (lazy save case) or off (normal case).
  
  	 The sequence is:
  
  	     mrs <temp>, tpidr2_el0
  	     cbz <temp>, no_save
  	     bl __arm_tpidr2_save
  	     msr tpidr2_el0, xzr
  	     zero { za }       // Only if ZA is live
  	     zero { zt0 }      // Only if ZT0 is live
  	 no_save:  */
        auto tmp_reg = gen_reg_rtx (DImode);
        emit_insn (gen_aarch64_read_tpidr2 (tmp_reg));
        auto label = gen_label_rtx ();
        rtx branch = aarch64_gen_compare_zero_and_branch (EQ, tmp_reg, label);
        auto jump = emit_jump_insn (branch);
        JUMP_LABEL (jump) = label;
        emit_insn (gen_aarch64_tpidr2_save ());
        emit_insn (gen_aarch64_clear_tpidr2 ());
        if (mode == aarch64_local_sme_state::ACTIVE_LIVE
  	  || mode == aarch64_local_sme_state::ACTIVE_DEAD)
  	{
  	  if (aarch64_cfun_has_state ("za"))
  	    emit_insn (gen_aarch64_initial_zero_za ());
  	  if (aarch64_cfun_has_state ("zt0"))
  	    emit_insn (gen_aarch64_sme_zero_zt0 ());
  	}
        emit_label (label);
      }
  
    if (mode == aarch64_local_sme_state::ACTIVE_LIVE
        || mode == aarch64_local_sme_state::ACTIVE_DEAD)
      {
        if (prev_mode == aarch64_local_sme_state::INACTIVE_LOCAL)
  	{
  	  /* Make ZA active after being inactive.
  
  	     First handle the case in which the lazy save we set up was
  	     committed by a callee.  If the function's source-level ZA state
  	     is live then we must conditionally restore it from the lazy
  	     save buffer.  Otherwise we can just force PSTATE.ZA to 1.  */
  	  if (mode == aarch64_local_sme_state::ACTIVE_LIVE)
  	    emit_insn (gen_aarch64_restore_za (aarch64_get_tpidr2_ptr ()));
  	  else
  	    emit_insn (gen_aarch64_smstart_za ());
  
  	  /* Now handle the case in which the lazy save was not committed.
  	     In that case, ZA still contains the current function's ZA state,
  	     and we just need to cancel the lazy save.  */
  	  emit_insn (gen_aarch64_clear_tpidr2 ());
  
  	  /* Restore the ZT0 state, if we have some.  */
  	  if (aarch64_cfun_has_state ("zt0"))
  	    aarch64_restore_zt0 (true);
  
  	  return;
  	}
  
        if (prev_mode == aarch64_local_sme_state::SAVED_LOCAL)
  	{
  	  /* Retrieve the current function's ZA state from the lazy save
  	     buffer.  */
  	  aarch64_restore_za (aarch64_get_tpidr2_ptr ());
  
  	  /* Restore the ZT0 state, if we have some.  */
  	  if (aarch64_cfun_has_state ("zt0"))
  	    aarch64_restore_zt0 (true);
  	  return;
  	}
  
        if (prev_mode == aarch64_local_sme_state::INACTIVE_CALLER
  	  || prev_mode == aarch64_local_sme_state::OFF)
  	{
  	  /* INACTIVE_CALLER means that we are enabling ZA for the first
  	     time in this function.  The code above means that ZA is either
  	     active and zero (if we committed a lazy save) or off.  Handle
  	     the latter case by forcing ZA on.
  
  	     OFF means that PSTATE.ZA is guaranteed to be 0.  We just need
  	     to force it to 1.
  
  	     Both cases leave ZA zeroed.  */
  	  emit_insn (gen_aarch64_smstart_za ());
  
  	  /* Restore the ZT0 state, if we have some.  */
  	  if (prev_mode == aarch64_local_sme_state::OFF
  	      && aarch64_cfun_has_state ("zt0"))
  	    aarch64_restore_zt0 (true);
  	  return;
  	}
  
        if (prev_mode == aarch64_local_sme_state::ACTIVE_DEAD
  	  || prev_mode == aarch64_local_sme_state::ACTIVE_LIVE)
  	/* A simple change in liveness, such as in a CFG structure where
  	   ZA is only conditionally defined.  No code is needed.  */
  	return;
  
        gcc_unreachable ();
      }
  
    if (mode == aarch64_local_sme_state::INACTIVE_LOCAL)
      {
        if (prev_mode == aarch64_local_sme_state::ACTIVE_LIVE
  	  || prev_mode == aarch64_local_sme_state::ACTIVE_DEAD
  	  || prev_mode == aarch64_local_sme_state::INACTIVE_CALLER)
  	{
  	  /* Save the ZT0 state, if we have some.  */
  	  if (aarch64_cfun_has_state ("zt0"))
  	    aarch64_save_zt0 ();
  
  	  /* A transition from ACTIVE_LIVE to INACTIVE_LOCAL is the usual
  	     case of setting up a lazy save buffer before a call.
  	     A transition from INACTIVE_CALLER is similar, except that
  	     the contents of ZA are known to be zero.
  
  	     A transition from ACTIVE_DEAD means that ZA is live at the
  	     point of the transition, but is dead on at least one incoming
  	     edge.  (That is, ZA is only conditionally initialized.)
  	     For efficiency, we want to set up a lazy save even for
  	     dead contents, since forcing ZA off would make later code
  	     restore ZA from the lazy save buffer.  */
  	  emit_insn (gen_aarch64_write_tpidr2 (aarch64_get_tpidr2_ptr ()));
  	  return;
  	}
  
        if (prev_mode == aarch64_local_sme_state::SAVED_LOCAL
  	  || prev_mode == aarch64_local_sme_state::OFF)
  	/* We're simply discarding the information about which inactive
  	   state applies.  */
  	return;
  
        gcc_unreachable ();
      }
  
    if (mode == aarch64_local_sme_state::INACTIVE_CALLER
        || mode == aarch64_local_sme_state::OFF)
      {
        /* Save the ZT0 state, if we have some.  */
        if ((prev_mode == aarch64_local_sme_state::ACTIVE_LIVE
  	   || prev_mode == aarch64_local_sme_state::ACTIVE_DEAD)
  	  && mode == aarch64_local_sme_state::OFF
  	  && aarch64_cfun_has_state ("zt0"))
  	aarch64_save_zt0 ();
  
        /* The transition to INACTIVE_CALLER is used before returning from
  	 new("za") functions.  Any state in ZA belongs to the current
  	 function rather than a caller, but that state is no longer
  	 needed.  Clear any pending lazy save and turn ZA off.
  
  	 The transition to OFF is used before calling a private-ZA function.
  	 We committed any incoming lazy save above, so at this point any
  	 contents in ZA belong to the current function.  */
        if (prev_mode == aarch64_local_sme_state::INACTIVE_LOCAL)
  	emit_insn (gen_aarch64_clear_tpidr2 ());
  
        if (prev_mode != aarch64_local_sme_state::OFF
  	  && prev_mode != aarch64_local_sme_state::SAVED_LOCAL)
  	emit_insn (gen_aarch64_smstop_za ());
  
        return;
      }
  
    if (mode == aarch64_local_sme_state::SAVED_LOCAL)
      {
        /* This is a transition to an exception handler.  */
        gcc_assert (prev_mode == aarch64_local_sme_state::OFF
  		  || prev_mode == aarch64_local_sme_state::INACTIVE_LOCAL);
        return;
      }
  
    gcc_unreachable ();
  }
  ```

  </details>

#### XINV-003 · DREV-2026-020 : zero-call-used-regs → stack-protector

- **命中站点**: `cfgexpand.cc:6632` （GCC gcc-16.1.0, target `generic`）
- **version_sensitivity**: stable
- **statement**: Any exit-edge-keyed hardening that a pass installs at the single exit block built by construct_exit_block must independently cover the abnormal predecessor edges that this function deliberately does NOT redirect into that block; treating the constructed exit block as the sole function-exit site silently omits every EDGE_ABNORMAL exit.
- **中文解读**: 编译器在构建函数的单一“常规出口块”时，会故意绕过异常边（如尾调用、非局部跳转）。如果防御机制（如寄存器擦除）只部署在常规出口块上，就会导致走异常出口路径的代码完全处于“裸奔”状态。
- **observation（违反时可外部观测的现象）**: A function with an abnormal exit edge (sibling call, non-local-goto, EH) reaches program exit through an edge that was not redirected into the common exit block, so any epilogue-time scrub/guard emitted only at the exit block is absent on that abnormal exit — visible as a return/branch out of the function with no preceding hardening insns while the normal-return path has them.
- **falsifiability（README §3 四维自评）**:
    - 可观测性: static: compare the insn sequence on abnormal exit edges against the normal exit block; a hardening sequence present on the normal exit but absent on the abnormal exit edge is the violation.
    - 判定确定性: deterministic: which edges are abnormal and whether they are redirected is fixed at expand time.
    - 实现成本: medium: requires enumerating exit-block predecessors and the abnormal edges skipped by the redirect loop.
    - 静态/动态归属: static
- **类比对齐（Stage-4 step 1）**: construct_exit_block()'s loop `if (!(e->flags & EDGE_ABNORMAL)) redirect_edge_succ(e, exit_block); else ix++;` routes only non-abnormal predecessor edges through the single constructed exit block, leaving abnormal exit edges bypassing it
- **受保护资产**: any exit-edge-keyed hardening (register scrub, epilogue guard) that a mechanism attaches at the common function-exit block
- **为何同构**: same shape as the seed: an abnormal exit form is given special handling (excluded from the common exit path) so a protective step placed on the common exit edge silently does not cover the independent abnormal exit edges
- **证据接地（Stage-5 entailment support）**: `if (!(e->flags & EDGE_ABNORMAL)) redirect_edge_succ (e, exit_block); else ix++;` in the predecessor loop
- **新颖性**: is_novel=True (离最近种子 DREV-2026-016 词法距离 45.62)

  <details><summary>命中块证据原文（GCC 16.1）</summary>

  ```c
  static void
  construct_exit_block (void)
  {
    rtx_insn *head = get_last_insn ();
    rtx_insn *end;
    basic_block exit_block;
    edge e, e2;
    unsigned ix;
    edge_iterator ei;
    basic_block prev_bb = EXIT_BLOCK_PTR_FOR_FN (cfun)->prev_bb;
    rtx_insn *orig_end = BB_END (prev_bb);
  
    rtl_profile_for_bb (EXIT_BLOCK_PTR_FOR_FN (cfun));
  
    /* Make sure the locus is set to the end of the function, so that
       epilogue line numbers and warnings are set properly.  */
    if (LOCATION_LOCUS (cfun->function_end_locus) != UNKNOWN_LOCATION)
      input_location = cfun->function_end_locus;
  
    /* Generate rtl for function exit.  */
    expand_function_end ();
  
    end = get_last_insn ();
    if (head == end)
      return;
    /* While emitting the function end we could move end of the last basic
       block.  */
    BB_END (prev_bb) = orig_end;
    while (NEXT_INSN (head) && NOTE_P (NEXT_INSN (head)))
      head = NEXT_INSN (head);
    /* But make sure exit_block starts with RETURN_LABEL, otherwise the
       bb count counting will be confused.  Any instructions before that
       label are emitted for the case where PREV_BB falls through into the
       exit block, so append those instructions to prev_bb in that case.  */
    if (NEXT_INSN (head) != return_label)
      {
        while (NEXT_INSN (head) != return_label)
  	{
  	  if (!NOTE_P (NEXT_INSN (head)))
  	    BB_END (prev_bb) = NEXT_INSN (head);
  	  head = NEXT_INSN (head);
  	}
      }
    exit_block = create_basic_block (NEXT_INSN (head), end, prev_bb);
    exit_block->count = EXIT_BLOCK_PTR_FOR_FN (cfun)->count;
    add_bb_to_loop (exit_block, EXIT_BLOCK_PTR_FOR_FN (cfun)->loop_father);
  
    ix = 0;
    while (ix < EDGE_COUNT (EXIT_BLOCK_PTR_FOR_FN (cfun)->preds))
      {
        e = EDGE_PRED (EXIT_BLOCK_PTR_FOR_FN (cfun), ix);
        if (!(e->flags & EDGE_ABNORMAL))
  	redirect_edge_succ (e, exit_block);
        else
  	ix++;
      }
  
    e = make_single_succ_edge (exit_block, EXIT_BLOCK_PTR_FOR_FN (cfun),
  			     EDGE_FALLTHRU);
    FOR_EACH_EDGE (e2, ei, EXIT_BLOCK_PTR_FOR_FN (cfun)->preds)
      if (e2 != e)
        {
  	exit_block->count -= e2->count ();
        }
    update_bb_for_insn (exit_block);
  }
  ```

  </details>

#### XINV-004 · DREV-2026-020 : zero-call-used-regs → stack-protector

- **命中站点**: `cfgexpand.cc:4428` （GCC gcc-16.1.0, target `generic`）
- **version_sensitivity**: stable
- **statement**: Because expand_gimple_tailcall removes the ordinary succ edges and creates the tail/sibling-call exit as an EDGE_ABNORMAL|EDGE_SIBCALL edge, any pass that enumerates 'return edges' to place a register-clearing or exit guard must include EDGE_SIBCALL exits; an enumeration restricted to normal return edges leaves the tail-call exit unscrubbed.
- **中文解读**: 尾调用（Sibling/Tail call）在编译器内部被标记为“异常边”。如果安全机制只遍历“常规返回边”来清理寄存器，就会漏掉尾调用出口，导致调用者的寄存器值未被擦除就转移给了下一个函数。
- **observation（违反时可外部观测的现象）**: A function containing a tail/sibling call transfers control to the callee via a sibcall exit that carries none of the zero-call-used-regs / exit-hardening insns present on that function's ordinary return, so call-used registers still hold the caller-frame values at the sibcall transfer.
- **falsifiability（README §3 四维自评）**:
    - 可观测性: static: check that any exit-edge-keyed hardening covers edges flagged EDGE_SIBCALL, not only ordinary return edges; a sibcall exit with no scrub while normal returns are scrubbed is the violation.
    - 判定确定性: deterministic: the sibcall exit edge and its flags are created unconditionally here for a tail call.
    - 实现成本: low: local — inspect the edge flags and the emitted exit sequence for the tail-call block.
    - 静态/动态归属: static
- **类比对齐（Stage-4 step 1）**: expand_gimple_tailcall() removes the non-eh/non-abnormal (fallthru) succ edges and then `make_edge(bb, EXIT..., EDGE_ABNORMAL | EDGE_SIBCALL)`, turning the sibling-call exit into an abnormal edge distinct from ordinary return edges
- **受保护资产**: register-clearing / exit hardening keyed on ordinary return edges, which the sibcall exit now bypasses as an abnormal edge
- **为何同构**: the seed's abnormal exit form is precisely the sibling/tail-call exit; this is the site that constructs that exit as an EDGE_ABNORMAL edge, so any clearing enumerated over normal return edges omits it — the seed's root-cause shape at its origin site
- **证据接地（Stage-5 entailment support）**: `e = make_edge (bb, EXIT_BLOCK_PTR_FOR_FN (cfun), EDGE_ABNORMAL | EDGE_SIBCALL);` after the loop removing the non-abnormal succ edges
- **新颖性**: is_novel=True (离最近种子 DREV-2026-016 词法距离 61.49)

  <details><summary>命中块证据原文（GCC 16.1）</summary>

  ```c
  static basic_block
  expand_gimple_tailcall (basic_block bb, gcall *stmt, bool *can_fallthru,
  			rtx_insn *asan_epilog_seq)
  {
    rtx_insn *last2, *last, *first = get_last_insn ();
    edge e;
    edge_iterator ei;
    profile_probability probability;
  
    last2 = last = expand_gimple_stmt (stmt);
  
    for (last = NEXT_INSN (last); last; last = NEXT_INSN (last))
      if (CALL_P (last) && SIBLING_CALL_P (last))
        goto found;
  
    maybe_dump_rtl_for_gimple_stmt (stmt, last2);
  
    *can_fallthru = true;
    return NULL;
  
   found:
  
    if (asan_epilog_seq)
      {
        /* We need to emit a copy of the asan_epilog_seq before
  	 the insns emitted by expand_gimple_stmt above.  The sequence
  	 can contain labels, which need to be remapped.  */
        hash_map<rtx, rtx> label_map;
        start_sequence ();
        emit_note (NOTE_INSN_DELETED);
        for (rtx_insn *insn = asan_epilog_seq; insn; insn = NEXT_INSN (insn))
  	switch (GET_CODE (insn))
  	  {
  	  case INSN:
  	  case CALL_INSN:
  	  case JUMP_INSN:
  	    emit_copy_of_insn_after (insn, get_last_insn ());
  	    break;
  	  case CODE_LABEL:
  	    label_map.put ((rtx) insn, (rtx) emit_label (gen_label_rtx ()));
  	    break;
  	  case BARRIER:
  	    emit_barrier ();
  	    break;
  	  default:
  	    gcc_unreachable ();
  	  }
        for (rtx_insn *insn = get_insns (); insn; insn = NEXT_INSN (insn))
  	if (JUMP_P (insn))
  	  {
  	    subrtx_ptr_iterator::array_type array;
  	    FOR_EACH_SUBRTX_PTR (iter, array, &PATTERN (insn), ALL)
  	      {
  		rtx *loc = *iter;
  		if (LABEL_REF_P (*loc))
  		  {
  		    rtx *lab = label_map.get ((rtx) label_ref_label (*loc));
  		    gcc_assert (lab);
  		    set_label_ref_label (*loc, as_a <rtx_insn *> (*lab));
  		  }
  	      }
  	    if (JUMP_LABEL (insn))
  	      {
  		rtx *lab = label_map.get (JUMP_LABEL (insn));
  		gcc_assert (lab);
  		JUMP_LABEL (insn) = *lab;
  	      }
  	  }
        asan_epilog_seq = NEXT_INSN (get_insns ());
        end_sequence ();
        emit_insn_before (asan_epilog_seq, NEXT_INSN (first));
      }
  
    /* ??? Wouldn't it be better to just reset any pending stack adjust?
       Any instructions emitted here are about to be deleted.  */
    do_pending_stack_adjust ();
  
    /* Remove any non-eh, non-abnormal edges that don't go to exit.  */
    /* ??? I.e. the fallthrough edge.  HOWEVER!  If there were to be
       EH or abnormal edges, we shouldn't have created a tail call in
       the first place.  So it seems to me we should just be removing
       all edges here, or redirecting the existing fallthru edge to
       the exit block.  */
  
    probability = profile_probability::never ();
  
    for (ei = ei_start (bb->succs); (e = ei_safe_edge (ei)); )
      {
        if (!(e->flags & (EDGE_ABNORMAL | EDGE_EH)))
  	{
  	  if (e->dest != EXIT_BLOCK_PTR_FOR_FN (cfun))
  	    e->dest->count -= e->count ();
  	  probability += e->probability;
  	  expand_remove_edge (e);
  	}
        else
  	ei_next (&ei);
      }
  
    /* This is somewhat ugly: the call_expr expander often emits instructions
       after the sibcall (to perform the function return).  These confuse the
       find_many_sub_basic_blocks code, so we need to get rid of these.  */
    last = NEXT_INSN (last);
    gcc_assert (BARRIER_P (last));
  
    *can_fallthru = false;
    while (NEXT_INSN (last))
      {
        /* For instance an sqrt builtin expander expands if with
  	 sibcall in the then and label for `else`.  */
        if (LABEL_P (NEXT_INSN (last)))
  	{
  	  *can_fallthru = true;
  	  break;
  	}
        delete_insn (NEXT_INSN (last));
      }
  
    e = make_edge (bb, EXIT_BLOCK_PTR_FOR_FN (cfun), EDGE_ABNORMAL
  		 | EDGE_SIBCALL);
    e->probability = probability;
    head_end_for_bb[bb->index].second = last;
    update_bb_for_insn_chain (head_end_for_bb[bb->index].first,
  			    head_end_for_bb[bb->index].second, bb);
  
    if (NEXT_INSN (last))
      {
        bb = create_basic_block (NEXT_INSN (last), get_last_insn (), bb);
  
        last = BB_END (bb);
        if (BARRIER_P (last))
  	BB_END (bb) = PREV_INSN (last);
      }
  
    maybe_dump_rtl_for_gimple_stmt (stmt, last2);
  
    return bb;
  }
  ```

  </details>

#### XINV-005 · DREV-2026-020 : zero-call-used-regs → backend-multi

- **命中站点**: `config/i386/i386.cc:4063` （GCC gcc-16.1.0, target `x86_64`）
- **version_sensitivity**: target-specific
- **statement**: The per-register clearing loop in ix86_zero_call_used_regs must be reached on every exit edge for which register scrubbing was requested; if the upstream logic that decides where to invoke this clearing bails out globally on encountering one abnormal exit form, the independent normal-return edges of the same function silently lose their scrub even though this loop is fully capable of emitting it.
- **中文解读**: 在 x86 后端的寄存器擦除循环中，如果上层逻辑因为遇到了一个异常出口而“全局放弃”擦除，会导致同一个函数内的“常规正常返回路径”也无辜地丢失了擦除保护。
- **observation（违反时可外部观测的现象）**: In a function compiled with -fzero-call-used-regs that also has an abnormal exit form, the ordinary `ret` paths are emitted with no register-zeroing insns (xor/mov 0 sequence) preceding them, i.e. call-used registers still hold in-function values at the normal return, matching the abnormal-exit-triggered global bailout shape.
- **falsifiability（README §3 四维自评）**:
    - 可观测性: static: for a -fzero-call-used-regs function with a mixed set of exit forms, check each normal return is preceded by the zeroing insns this loop emits; a normal return with no scrub is the violation.
    - 判定确定性: deterministic given the -fzero-call-used-regs mode and the function's exit forms.
    - 实现成本: low: inspect the emitted epilogue/return sequences of the affected function.
    - 静态/动态归属: static
- **类比对齐（Stage-4 step 1）**: ix86_zero_call_used_regs()'s `for (regno = 0; regno < FIRST_PSEUDO_REGISTER; regno++)` loop emits the zeroing insn for each register in need_zeroed_hardregs, skipping any register via `continue`; this is the register-clearing sequence whose emission on an exit edge the seed's bailout suppresses
- **受保护资产**: call-used hard registers that must be scrubbed before every exit edge so no callee value leaks to the caller
- **为何同构**: this is the x86 sibling of the very mechanism the seed found broken on aarch64: the clearing is driven by a per-register walk over need_zeroed_hardregs, so if an upstream exit-edge enumeration bails out for one abnormal exit form the same normal-return edges lose their scrub — the identical root-cause shape in another target
- **证据接地（Stage-5 entailment support）**: the `for (regno = 0; regno < FIRST_PSEUDO_REGISTER; regno++)` loop emitting `gen_rtx_SET (reg, CONST0_RTX (mode))` for each requested register
- **新颖性**: is_novel=True (离最近种子 DREV-2026-016 词法距离 72.31)

  <details><summary>命中块证据原文（GCC 16.1）</summary>

  ```c
     NEED_ZEROED_HARDREGS.  Return the ZEROED_HARDREGS that are actually
     zeroed.  */
  static HARD_REG_SET
  ix86_zero_call_used_regs (HARD_REG_SET need_zeroed_hardregs)
  {
    HARD_REG_SET zeroed_hardregs;
    bool all_sse_zeroed = false;
    int all_st_zeroed_num = 0;
    bool all_mm_zeroed = false;
  
    CLEAR_HARD_REG_SET (zeroed_hardregs);
  
    /* first, let's see whether we can zero all vector registers together.  */
    rtx zero_all_vec_insn = zero_all_vector_registers (need_zeroed_hardregs);
    if (zero_all_vec_insn)
      {
        emit_insn (zero_all_vec_insn);
        all_sse_zeroed = true;
        if (TARGET_64BIT && TARGET_AVX512F)
  	{
  	  rtx zero = CONST0_RTX (V4SFmode);
  	  for (unsigned int regno = XMM16_REG;
  	       regno <= XMM31_REG;
  	       regno++)
  	    {
  	      rtx reg = gen_rtx_REG (V4SFmode, regno);
  	      emit_move_insn (reg, zero);
  	    }
  	}
      }
  
    /* mm/st registers are shared registers set, we should follow the following
       rules to clear them:
  			MMX exit mode	      x87 exit mode
  	-------------|----------------------|---------------
  	uses x87 reg | clear all MMX	    | clear all x87
  	uses MMX reg | clear individual MMX | clear all x87
  	x87 + MMX    | clear all MMX	    | clear all x87
  
       first, we should decide which mode (MMX mode or x87 mode) the function
       exit with.  */
  
    bool exit_with_mmx_mode = (crtl->return_rtx
  			     && (MMX_REG_P (crtl->return_rtx)));
  
    if (!exit_with_mmx_mode)
      /* x87 exit mode, we should zero all st registers together.  */
      {
        all_st_zeroed_num = zero_all_st_registers (need_zeroed_hardregs);
  
        if (all_st_zeroed_num > 0)
  	for (unsigned int regno = FIRST_STACK_REG; regno <= LAST_STACK_REG; regno++)
  	  /* x87 stack registers that hold the return value should be excluded.
  	     x87 returns in the top (two for complex values) register.  */
  	  if (all_st_zeroed_num == 8
  	      || !((all_st_zeroed_num >= 6 && regno == REGNO (crtl->return_rtx))
  		   || (all_st_zeroed_num == 6
  		       && (regno == (REGNO (crtl->return_rtx) + 1)))))
  	    SET_HARD_REG_BIT (zeroed_hardregs, regno);
      }
    else
      /* MMX exit mode, check whether we can zero all mm registers.  */
      {
        unsigned int exit_mmx_regno = REGNO (crtl->return_rtx);
        all_mm_zeroed = zero_all_mm_registers (need_zeroed_hardregs,
  					     exit_mmx_regno);
        if (all_mm_zeroed)
  	for (unsigned int regno = FIRST_MMX_REG; regno <= LAST_MMX_REG; regno++)
  	  if (regno != exit_mmx_regno)
  	    SET_HARD_REG_BIT (zeroed_hardregs, regno);
      }
  
    /* Now, generate instructions to zero all the other registers.  */
  
    for (unsigned int regno = 0; regno < FIRST_PSEUDO_REGISTER; regno++)
      {
        if (!TEST_HARD_REG_BIT (need_zeroed_hardregs, regno))
  	continue;
        if (!zero_call_used_regno_p (regno, all_sse_zeroed,
  				   exit_with_mmx_mode && !all_mm_zeroed))
  	continue;
  
        SET_HARD_REG_BIT (zeroed_hardregs, regno);
  
        machine_mode mode = zero_call_used_regno_mode (regno);
  
        rtx reg = gen_rtx_REG (mode, regno);
        rtx tmp = gen_rtx_SET (reg, CONST0_RTX (mode));
  
        switch (mode)
  	{
  	case E_SImode:
  	  if (!TARGET_USE_MOV0 || optimize_insn_for_size_p ())
  	    {
  	      rtx clob = gen_rtx_CLOBBER (VOIDmode,
  					  gen_rtx_REG (CCmode,
  						       FLAGS_REG));
  	      tmp = gen_rtx_PARALLEL (VOIDmode, gen_rtvec (2,
  							   tmp,
  							   clob));
  	    }
  	  /* FALLTHRU.  */
  
  	case E_V4SFmode:
  	case E_HImode:
  	case E_V2SImode:
  	  emit_insn (tmp);
  	  break;
  
  	default:
  	  gcc_unreachable ();
  	}
      }
    return zeroed_hardregs;
  }
  ```

  </details>

#### XINV-006 · DREV-2026-020 : zero-call-used-regs → backend-multi

- **命中站点**: `config/arm/arm.cc:26305` （GCC gcc-16.1.0, target `arm`）
- **version_sensitivity**: target-specific
- **statement**: In thumb_exit, per-exit-form special handling (the pops_needed==0 `bx` path and the non-interwork `pop {pc}` path) must not be used to short-circuit register-clearing that other exit forms of the same function still require; an early return taken for one exit form must not suppress the scrub owed to the remaining exit edges.
- **中文解读**: ARM 架构的 `thumb_exit` 针对特定返回形式（如 `pop {pc}`）有提前返回的逻辑，这“短路”了后续的清理流程，导致这些特定出口漏掉了本应执行的寄存器擦除操作。
- **observation（违反时可外部观测的现象）**: A Thumb function whose exit is emitted through thumb_exit's early `bx`/`pop pc` return form shows no call-used-register scrub before the branch, while a sibling exit form in the same function that falls through to the general pop/restore path does emit it — the asymmetry is visible directly in the epilogue assembly.
- **falsifiability（README §3 四维自评）**:
    - 可观测性: static: for each exit form thumb_exit can emit, confirm the required scrub precedes the exit branch; an early-return form reaching its branch with no scrub while another form has one is the violation.
    - 判定确定性: deterministic: the exit form chosen and whether a scrub precedes it are fixed by the function type and target flags.
    - 实现成本: low: local inspection of the Thumb epilogue sequence.
    - 静态/动态归属: static
- **类比对齐（Stage-4 step 1）**: thumb_exit() has form-specific early returns: the `pops_needed==0` arm emits `bx`/`bxns` and returns, and the !TARGET_INTERWORK arm emits `pop {pc}` and returns, each leaving before the general pop/restore path — so per-exit-form handling short-circuits the remaining exit logic
- **受保护资产**: register state that a full exit sequence would restore/scrub before control leaves via a given return form
- **为何同构**: the seed's shape is that special handling for one exit form causes an early bailout that skips the sequence required on other exit edges; thumb_exit's per-form early `return`s are the same short-circuit-per-exit-form structure
- **证据接地（Stage-5 entailment support）**: the `if (pops_needed == 0)` early-return `bx` path and the `else if (!TARGET_INTERWORK ...)` `pop {pc}` early-return path
- **新颖性**: is_novel=True (离最近种子 DREV-2026-016 词法距离 55.07)

  <details><summary>命中块证据原文（GCC 16.1）</summary>

  ```c
     Note: do not forget to update length attribute of corresponding insn pattern
     when changing assembly output (eg. length attribute of epilogue_insns when
     updating Armv8-M Baseline Security Extensions register clearing
     sequences).  */
  static void
  thumb_exit (FILE *f, int reg_containing_return_addr)
  {
    unsigned regs_available_for_popping;
    unsigned regs_to_pop;
    int pops_needed;
    unsigned available;
    unsigned required;
    machine_mode mode;
    int size;
    int restore_a4 = FALSE;
  
    /* Compute the registers we need to pop.  */
    regs_to_pop = 0;
    pops_needed = 0;
  
    if (reg_containing_return_addr == -1)
      {
        regs_to_pop |= 1 << LR_REGNUM;
        ++pops_needed;
      }
  
    if (TARGET_BACKTRACE)
      {
        /* Restore the (ARM) frame pointer and stack pointer.  */
        regs_to_pop |= (1 << ARM_HARD_FRAME_POINTER_REGNUM) | (1 << SP_REGNUM);
        pops_needed += 2;
      }
  
    /* If there is nothing to pop then just emit the BX instruction and
       return.  */
    if (pops_needed == 0)
      {
        if (crtl->calls_eh_return)
  	asm_fprintf (f, "\tadd\t%r, %r\n", SP_REGNUM, ARM_EH_STACKADJ_REGNUM);
  
        if (IS_CMSE_ENTRY (arm_current_func_type ()))
  	{
  	  /* For Armv8.1-M, this is cleared as part of the CLRM instruction
  	     emitted by cmse_nonsecure_entry_clear_before_return ().  */
  	  if (!TARGET_HAVE_FPCXT_CMSE)
  	    asm_fprintf (f, "\tmsr\tAPSR_nzcvq, %r\n",
  			 reg_containing_return_addr);
  	  asm_fprintf (f, "\tbxns\t%r\n", reg_containing_return_addr);
  	}
        else
  	asm_fprintf (f, "\tbx\t%r\n", reg_containing_return_addr);
        return;
      }
    /* Otherwise if we are not supporting interworking and we have not created
       a backtrace structure and the function was not entered in ARM mode then
       just pop the return address straight into the PC.  */
    else if (!TARGET_INTERWORK
  	   && !TARGET_BACKTRACE
  	   && !is_called_in_ARM_mode (current_function_decl)
  	   && !crtl->calls_eh_return
  	   && !IS_CMSE_ENTRY (arm_current_func_type ()))
      {
        asm_fprintf (f, "\tpop\t{%r}\n", PC_REGNUM);
        return;
      }
  
    /* Find out how many of the (return) argument registers we can corrupt.  */
    regs_available_for_popping = 0;
  
    /* If returning via __builtin_eh_return, the bottom three registers
       all contain information needed for the return.  */
    if (crtl->calls_eh_return)
      size = 12;
    else
      {
        /* If we can deduce the registers used from the function's
  	 return value.  This is more reliable that examining
  	 df_regs_ever_live_p () because that will be set if the register is
  	 ever used in the function, not just if the register is used
  	 to hold a return value.  */
  
        if (crtl->return_rtx != 0)
  	mode = GET_MODE (crtl->return_rtx);
        else
  	mode = DECL_MODE (DECL_RESULT (current_function_decl));
  
        size = GET_MODE_SIZE (mode);
  
        if (size == 0)
  	{
  	  /* In a void function we can use any argument register.
  	     In a function that returns a structure on the stack
  	     we can use the second and third argument registers.  */
  	  if (mode == VOIDmode)
  	    regs_available_for_popping =
  	      (1 << ARG_REGISTER (1))
  	      | (1 << ARG_REGISTER (2))
  	      | (1 << ARG_REGISTER (3));
  	  else
  	    regs_available_for_popping =
  	      (1 << ARG_REGISTER (2))
  	      | (1 << ARG_REGISTER (3));
  	}
        else if (size <= 4)
  	regs_available_for_popping =
  	  (1 << ARG_REGISTER (2))
  	  | (1 << ARG_REGISTER (3));
        else if (size <= 8)
  	regs_available_for_popping =
  	  (1 << ARG_REGISTER (3));
      }
  
    /* Match registers to be popped with registers into which we pop them.  */
    for (available = regs_available_for_popping,
         required  = regs_to_pop;
         required != 0 && available != 0;
         available &= ~(available & - available),
         required  &= ~(required  & - required))
      -- pops_needed;
  
    /* If we have any popping registers left over, remove them.  */
    if (available > 0)
      regs_available_for_popping &= ~available;
  
    /* Otherwise if we need another popping register we can use
       the fourth argument register.  */
    else if (pops_needed)
      {
        /* If we have not found any free argument registers and
  	 reg a4 contains the return address, we must move it.  */
        if (regs_available_for_popping == 0
  	  && reg_containing_return_addr == LAST_ARG_REGNUM)
  	{
  	  asm_fprintf (f, "\tmov\t%r, %r\n", LR_REGNUM, LAST_ARG_REGNUM);
  	  reg_containing_return_addr = LR_REGNUM;
  	}
        else if (size > 12)
  	{
  	  /* Register a4 is being used to hold part of the return value,
  	     but we have dire need of a free, low register.  */
  	  restore_a4 = TRUE;
  
  	  asm_fprintf (f, "\tmov\t%r, %r\n",IP_REGNUM, LAST_ARG_REGNUM);
  	}
  
        if (reg_containing_return_addr != LAST_ARG_REGNUM)
  	{
  	  /* The fourth argument register is available.  */
  	  regs_available_for_popping |= 1 << LAST_ARG_REGNUM;
  
  	  --pops_needed;
  	}
      }
  
    /* Pop as many registers as we can.  */
    thumb_pop (f, regs_available_for_popping);
  
    /* Process the registers we popped.  */
    if (reg_containing_return_addr == -1)
      {
        /* The return address was popped into the lowest numbered register.  */
        regs_to_pop &= ~(1 << LR_REGNUM);
  
        reg_containing_return_addr =
  	number_of_first_bit_set (regs_available_for_popping);
  
        /* Remove this register for the mask of available registers, so that
           the return address will not be corrupted by further pops.  */
        regs_available_for_popping &= ~(1 << reg_containing_return_addr);
      }
  
    /* If we popped other registers then handle them here.  */
    if (regs_available_for_popping)
      {
        int frame_pointer;
  
        /* Work out which register currently contains the frame pointer.  */
        frame_pointer = number_of_first_bit_set (regs_available_for_popping);
  
        /* Move it into the correct place.  */
        asm_fprintf (f, "\tmov\t%r, %r\n",
  		   ARM_HARD_FRAME_POINTER_REGNUM, frame_pointer);
  
        /* (Temporarily) remove it from the mask of popped registers.  */
        regs_available_for_popping &= ~(1 << frame_pointer);
        regs_to_pop &= ~(1 << ARM_HARD_FRAME_POINTER_REGNUM);
  
        if (regs_available_for_popping)
  	{
  	  int stack_pointer;
  
  	  /* We popped the stack pointer as well,
  	     find the register that contains it.  */
  	  stack_pointer = number_of_first_bit_set (regs_available_for_popping);
  
  	  /* Move it into the stack register.  */
  	  asm_fprintf (f, "\tmov\t%r, %r\n", SP_REGNUM, stack_pointer);
  
  	  /* At this point we have popped all necessary registers, so
  	     do not worry about restoring regs_available_for_popping
  	     to its correct value:
  
  	     assert (pops_needed == 0)
  	     assert (regs_available_for_popping == (1 << frame_pointer))
  	     assert (regs_to_pop == (1 << STACK_POINTER))  */
  	}
        else
  	{
  	  /* Since we have just move the popped value into the frame
  	     pointer, the popping register is available for reuse, and
  	     we know that we still have the stack pointer left to pop.  */
  	  regs_available_for_popping |= (1 << frame_pointer);
  	}
      }
  
    /* If we still have registers left on the stack, but we no longer have
       any registers into which we can pop them, then we must move the return
       address into the link register and make available the register that
       contained it.  */
    if (regs_available_for_popping == 0 && pops_needed > 0)
      {
        regs_available_for_popping |= 1 << reg_containing_return_addr;
  
        asm_fprintf (f, "\tmov\t%r, %r\n", LR_REGNUM,
  		   reg_containing_return_addr);
  
        reg_containing_return_addr = LR_REGNUM;
      }
  
    /* If we have registers left on the stack then pop some more.
       We know that at most we will want to pop FP and SP.  */
    if (pops_needed > 0)
      {
        int  popped_into;
        int  move_to;
  
        thumb_pop (f, regs_available_for_popping);
  
        /* We have popped either FP or SP.
  	 Move whichever one it is into the correct register.  */
        popped_into = number_of_first_bit_set (regs_available_for_popping);
        move_to     = number_of_first_bit_set (regs_to_pop);
  
        asm_fprintf (f, "\tmov\t%r, %r\n", move_to, popped_into);
        --pops_needed;
      }
  
    /* If we still have not popped everything then we must have only
       had one register available to us and we are now popping the SP.  */
    if (pops_needed > 0)
      {
        int  popped_into;
  
        thumb_pop (f, regs_available_for_popping);
  
        popped_into = number_of_first_bit_set (regs_available_for_popping);
  
        asm_fprintf (f, "\tmov\t%r, %r\n", SP_REGNUM, popped_into);
        /*
  	assert (regs_to_pop == (1 << STACK_POINTER))
  	assert (pops_needed == 1)
        */
      }
  
    /* If necessary restore the a4 register.  */
    if (restore_a4)
      {
        if (reg_containing_return_addr != LR_REGNUM)
  	{
  	  asm_fprintf (f, "\tmov\t%r, %r\n", LR_REGNUM, LAST_ARG_REGNUM);
  	  reg_containing_return_addr = LR_REGNUM;
  	}
  
        asm_fprintf (f, "\tmov\t%r, %r\n", LAST_ARG_REGNUM, IP_REGNUM);
      }
  
    if (crtl->calls_eh_return)
      asm_fprintf (f, "\tadd\t%r, %r\n", SP_REGNUM, ARM_EH_STACKADJ_REGNUM);
  
    /* Return to caller.  */
    if (IS_CMSE_ENTRY (arm_current_func_type ()))
      {
        /* This is for the cases where LR is not being used to contain the return
           address.  It may therefore contain information that we might not want
  	 to leak, hence it must be cleared.  The value in R0 will never be a
  	 secret at this point, so it is safe to use it, see the clearing code
  	 in cmse_nonsecure_entry_clear_before_return ().  */
        if (reg_containing_return_addr != LR_REGNUM)
  	asm_fprintf (f, "\tmov\tlr, r0\n");
  
        /* For Armv8.1-M, this is cleared as part of the CLRM instruction emitted
  	 by cmse_nonsecure_entry_clear_before_return ().  */
        if (!TARGET_HAVE_FPCXT_CMSE)
  	asm_fprintf (f, "\tmsr\tAPSR_nzcvq, %r\n", reg_containing_return_addr);
        asm_fprintf (f, "\tbxns\t%r\n", reg_containing_return_addr);
      }
    else
      asm_fprintf (f, "\tbx\t%r\n", reg_containing_return_addr);
  }
  ```

  </details>

---

### stack-clash-protection — 帧大小位宽完整性

种子 DREV-2026-025（fortify-source：安全关键大小值被窄化为定宽类型）作为探针，命中了 stack-clash 探测循环所依赖的通用位宽收窄点。

#### XINV-007 · DREV-2026-025 : fortify-source → stack-clash-protection

- **命中站点**: `explow.cc:51` （GCC gcc-16.1.0, target `generic`）
- **version_sensitivity**: stable
- **statement**: A stack-frame or guard-probe size constant that stack-clash protection forces into a machine mode via trunc_int_for_mode must be represented in a mode whose precision strictly exceeds the significant bit width of that size, so bit (width-1) is never set; otherwise the sign-extension block in trunc_int_for_mode (`c ^= sign; c -= sign;`) flips a large positive frame size to a negative value and the probe-loop bound derived from it no longer covers every guard page of the frame.
- **中文解读**: 当栈帧大小极大、刚好触及当前数据类型的有符号上限时，编译器的强制截断操作（`trunc_int_for_mode`）会把一个极大的正数翻转成负数。这会导致 Stack-clash 的探测循环边界变成负的，从而直接跳过安全探测，留下巨大的栈帧漏洞。
- **observation（违反时可外部观测的现象）**: In the emitted prologue for a function whose frame size approaches or exceeds the signed maximum of the mode used to carry it, the stack-clash probe count or SP-adjustment immediate appears negative or wildly wrong (a sign-flipped magnitude), or the probe loop is skipped entirely, leaving guard pages of an oversized frame untouched.
- **falsifiability（README §3 四维自评）**:
    - 可观测性: Compile a function whose frame size sits just above the signed limit of the carrying mode, disassemble the prologue, and check that the SP-adjustment / probe-loop bound immediate equals the true frame size rather than a sign-extended (negative/wrapped) value.
    - 判定确定性: Decisive: the emitted immediate either equals the true frame size or it is the sign-extended value; there is no ambiguity.
    - 实现成本: One crafted large-frame function plus objdump of the prologue; static inspection, no execution required.
    - 静态/动态归属: static
- **类比对齐（Stage-4 step 1）**: The sign-extension block `if (width < HOST_BITS_PER_WIDE_INT) { sign <<= width-1; c &= (sign<<1)-1; c ^= sign; c -= sign; }` masks the value to `width = GET_MODE_PRECISION(smode)` bits and sign-extends it back to HOST_WIDE_INT.
- **受保护资产**: the integer constant/immediate (frame size, offset, or bound) that RTL forces into a given machine mode
- **为何同构**: Both narrow a wider signed value to a fixed bit width and then sign-extend it, so a value whose bit at (width-1) is set flips sign and yields a wrong magnitude for whatever bound/size/offset consumes it.
- **证据接地（Stage-5 entailment support）**: The block `if (width < HOST_BITS_PER_WIDE_INT) { HOST_WIDE_INT sign = 1; sign <<= width - 1; c &= (sign << 1) - 1; c ^= sign; c -= sign; }` is exactly the mask-then-sign-extend to `width = GET_MODE_PRECISION(smode)` that the statement constrains.
- **新颖性**: is_novel=True (离最近种子 DREV-2026-019 词法距离 66.60)

  <details><summary>命中块证据原文（GCC 16.1）</summary>

  ```c
  HOST_WIDE_INT
  trunc_int_for_mode (HOST_WIDE_INT c, machine_mode mode)
  {
    /* Not scalar_int_mode because we also allow pointer bound modes.  */
    scalar_mode smode = as_a <scalar_mode> (mode);
    int width = GET_MODE_PRECISION (smode);
  
    /* You want to truncate to a _what_?  */
    gcc_assert (SCALAR_INT_MODE_P (mode));
  
    /* Canonicalize BImode to 0 and STORE_FLAG_VALUE.  */
    if (smode == BImode)
      return c & 1 ? STORE_FLAG_VALUE : 0;
  
    /* Sign-extend for the requested mode.  */
  
    if (width < HOST_BITS_PER_WIDE_INT)
      {
        HOST_WIDE_INT sign = 1;
        sign <<= width - 1;
        c &= (sign << 1) - 1;
        c ^= sign;
        c -= sign;
      }
  
    return c;
  }
  ```

  </details>

---

### RISC-V 后端 — 大偏移地址物化完整性

同一个 DREV-2026-025 探针在 RISC-V 后端的大帧偏移地址拆分/物化路径上命中同形的“定宽表示越界导致值翻转”操作，影响所有依赖精确帧偏移的机制（含 stack-clash 与 fortify 的帧内寻址）。

#### XINV-008 · DREV-2026-025 : fortify-source → backend-multi

- **命中站点**: `config/riscv/riscv.cc:3037` （GCC gcc-16.1.0, target `riscv64`）
- **version_sensitivity**: target-specific
- **statement**: When riscv_add_offset splits a large frame/stack offset into a CONST_HIGH_PART placed in Pmode plus a CONST_LOW_PART, the sign-extended high part added to the base must, together with the low part, reproduce the exact original signed offset across the whole representable range; if the high part's sign-extension is not corrected for a low part whose bit 11 is set, the reassembled address differs from the intended offset by 0x1000.
- **中文解读**: RISC-V 架构在处理超大栈帧偏移时，会将其拆分为高位和低12位。如果低12位恰好是个负数（符号位为1），会引发错误的符号扩展。如果不加以修正，最终算出的内存地址会偏差 0x1000，导致依赖精确地址的保护机制失效。
- **observation（违反时可外部观测的现象）**: A memory access at a large constant stack/frame offset that requires the high/low split (near the point where the low 12-bit part's sign bit is set) is emitted as a lui+addi sequence whose reconstructed address is off by 0x1000 from the intended slot, so the load/store targets an adjacent, wrong stack location.
- **falsifiability（README §3 四维自评）**:
    - 可观测性: Cross-compile a RISC-V function that accesses a stack slot at an offset requiring the high/low split with the low part's sign bit set, disassemble, and verify the lui+addi pair reconstructs exactly the intended byte offset.
    - 判定确定性: Decisive: the reconstructed offset either equals the source offset or it is off by 0x1000.
    - 实现成本: RISC-V cross-compiler plus objdump; static inspection of the addressing sequence.
    - 静态/动态归属: static
- **类比对齐（Stage-4 step 1）**: The large-offset split `high = gen_int_mode(CONST_HIGH_PART(offset), Pmode); offset = CONST_LOW_PART(offset);` with the in-code comment that `CONST_HIGH_PART` may overflow and 'we need to force a sign-extension check' before the high part is added into the address.
- **受保护资产**: the computed target address (base register + reconstructed large offset) used for a memory access
- **为何同构**: Both split/convert a wider signed quantity into a narrower part plus sign-extension and then reassemble it for an address/bound computation, where an incorrect sign-extension of the high part produces a wrong final address.
- **证据接地（Stage-5 entailment support）**: `high = gen_int_mode (CONST_HIGH_PART (offset), Pmode); offset = CONST_LOW_PART (offset);` plus the comment noting CONST_HIGH_PART may overflow and needs a sign-extension check is the high/low split the statement constrains.
- **新颖性**: is_novel=True (离最近种子 DREV-2026-019 词法距离 63.34)

  <details><summary>命中块证据原文（GCC 16.1）</summary>

  ```c
  static rtx
  riscv_add_offset (rtx temp, rtx reg, HOST_WIDE_INT offset)
  {
    if (!SMALL_OPERAND (offset))
      {
        rtx high;
  
        /* Leave OFFSET as a 16-bit offset and put the excess in HIGH.
  	 The addition inside the macro CONST_HIGH_PART may cause an
  	 overflow, so we need to force a sign-extension check.  */
        high = gen_int_mode (CONST_HIGH_PART (offset), Pmode);
        offset = CONST_LOW_PART (offset);
        high = riscv_force_temporary (temp, high);
        reg = riscv_force_temporary (temp, gen_rtx_PLUS (Pmode, high, reg));
      }
    return plus_constant (Pmode, reg, offset);
  }
  ```

  </details>

#### XINV-009 · DREV-2026-025 : fortify-source → backend-multi

- **命中站点**: `config/riscv/riscv.cc:16438` （GCC gcc-16.1.0, target `riscv64`）
- **version_sensitivity**: target-specific
- **statement**: Each SImode lowpart that synthesize_add_extended tags SUBREG_PROMOTED / SRP_SIGNED before widening it back to DImode must hold a value that fits the signed 32-bit range implied by that promotion; if an intermediate add result has bit 31 set unexpectedly, the SRP_SIGNED promotion sign-extends it and the reconstructed 64-bit sum diverges from operand1 + operand2.
- **中文解读**: RISC-V 在用 32 位指令合成 64 位加法时，如果 32 位中间结果的最高位（第31位）为 1，会被错误地当做负数进行符号扩展（扩展到 64 位）。这会导致最终计算出的地址偏离 2^32，使得安全机制访问到错误的内存位置。
- **observation（违反时可外部观测的现象）**: For an add of a large constant whose two-s12 split intermediates cross the 32-bit sign boundary, the destination 64-bit register holds a value that is off by a multiple of 2^32 (a sign-extended intermediate) rather than the true base+offset, observable as a wrong address/value at the emitted SET.
- **falsifiability（README §3 四维自评）**:
    - 可观测性: Cross-compile a RISC-V add of a constant that forces the SUM_OF_TWO_S12 split with an intermediate crossing the signed-32 boundary, disassemble, and check the reconstructed 64-bit result equals operand1 + operand2.
    - 判定确定性: Decisive: the reconstructed sum either equals base+offset or differs by a 2^32 multiple from the sign-extension.
    - 实现成本: RISC-V cross-compiler plus objdump; static inspection of the synthesized add sequence.
    - 静态/动态归属: static
- **类比对齐（Stage-4 step 1）**: `temp = gen_lowpart(SImode, temp); SUBREG_PROMOTED_VAR_P(temp) = 1; SUBREG_PROMOTED_SET(temp, SRP_SIGNED);` narrows the DImode add result to a 32-bit SImode subreg tagged as signed-promoted, repeated for each synthesized add stage.
- **受保护资产**: the synthesized 64-bit address/add result reconstructed from a narrowed 32-bit signed intermediate
- **为何同构**: Both narrow a wider (64-bit) value to a fixed 32-bit signed representation and rely on sign-extension (SRP_SIGNED) when widening it back, so a value with bit 31 set is sign-extended and the reconstructed address/offset is wrong.
- **证据接地（Stage-5 entailment support）**: `temp = gen_lowpart (SImode, temp); SUBREG_PROMOTED_VAR_P (temp) = 1; SUBREG_PROMOTED_SET (temp, SRP_SIGNED);` (repeated for each stage) is the SImode signed-promoted narrowing the statement constrains.
- **新颖性**: is_novel=True (离最近种子 DREV-2026-001 词法距离 38.83)

  <details><summary>命中块证据原文（GCC 16.1）</summary>

  ```c
  bool
  synthesize_add_extended (rtx operands[3])
  {
  
  /*  If operands[2] is a 12-bit signed immediate,
      no synthesis needs to be done.  */
  
    if (SMALL_OPERAND (INTVAL (operands[2])))
      return false;
  
    HOST_WIDE_INT ival = INTVAL (operands[2]);
    int budget1 = riscv_const_insns (operands[2], true);
    int budget2 = riscv_const_insns (GEN_INT (-INTVAL (operands[2])), true);
  
  /*  If operands[2] can be split into two 12-bit signed immediates,
      split add into two adds.  */
  
    if (SUM_OF_TWO_S12 (ival))
      {
        HOST_WIDE_INT saturated = HOST_WIDE_INT_M1U << (IMM_BITS - 1);
  
        if (ival >= 0)
  	saturated = ~saturated;
  
        ival -= saturated;
  
        /* The first add may be an FP relative address during reload.  FP
  	 may be replaced with (sp + C).  We don't want that to already
  	 be saturated as (sp + C) would then exceed a simm12 field.  So
  	 emit the smaller offset first and the saturated constant last.  */
        rtx temp = gen_reg_rtx (DImode);
        emit_insn (gen_addsi3_extended (temp, operands[1], GEN_INT (ival)));
        temp = gen_lowpart (SImode, temp);
        SUBREG_PROMOTED_VAR_P (temp) = 1;
        SUBREG_PROMOTED_SET (temp, SRP_SIGNED);
        emit_insn (gen_rtx_SET (operands[0], temp));
        rtx t = gen_reg_rtx (DImode);
        emit_insn (gen_addsi3_extended (t, operands[0], GEN_INT (saturated)));
        t = gen_lowpart (SImode, t);
        SUBREG_PROMOTED_VAR_P (t) = 1;
        SUBREG_PROMOTED_SET (t, SRP_SIGNED);
        emit_move_insn (operands[0], t);
        return true;
      }
  
  
  /*  If the negated value is cheaper to synthesize, subtract that from
      operands[1]. */
  
    if (budget2 < budget1)
      {
        rtx tmp = gen_reg_rtx (SImode);
        emit_insn (gen_rtx_SET (tmp, GEN_INT (-INTVAL (operands[2]))));
  
        rtx t = gen_reg_rtx (DImode);
        emit_insn (gen_subsi3_extended (t, operands[1], tmp));
        t = gen_lowpart (SImode, t);
        SUBREG_PROMOTED_VAR_P (t) = 1;
        SUBREG_PROMOTED_SET (t, SRP_SIGNED);
        emit_move_insn (operands[0], t);
        return true;
      }
  
    rtx tsrc = force_reg (SImode, operands[2]);
    rtx tdest = gen_reg_rtx (DImode);
    emit_insn (gen_addsi3_extended (tdest, operands[1], tsrc));
    tdest = gen_lowpart (SImode, tdest);
    SUBREG_PROMOTED_VAR_P (tdest) = 1;
    SUBREG_PROMOTED_SET (tdest, SRP_SIGNED);
    emit_move_insn (operands[0], tdest);
    return true;
  
  }
  ```

  </details>

## 溯源

- 结构化产物: `orchestrator/runs/specgen_full/candidates.jsonl`（含 accepted/rejected 全量）、`accepted/*.md`（单条卡片）。
- 判断转录: `orchestrator/runs/specgen_full/transcript.json`（distill / analogy / specialize / entailment 四表）。
- run manifest: `orchestrator/runs/specgen_full/manifest.json` (git 90125f582273, corpus 4496)。

