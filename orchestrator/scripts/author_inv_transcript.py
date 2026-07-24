"""Author the offline judgment transcript for the 10-seed invariant BM25 run.

Distill was authored by hand (already in transcript.json). This script fills the
analogy / specialize / entailment sections, grounded in the REAL retrieved chunk
text recorded in pending.json. The bar is strict: analogy holds for exactly one
canonical, best-grounded site per surviving seed; every other hit is a lexical
collision (shared "stack"/"frame"/"return"/"indirect-branch" vocabulary, or a
bugzilla prose match) and is recorded false with a reason for the RQ2 ablation.

Four of the ten seeds are honest negatives, decided by what BM25 actually
returned (not by a pre-drawn plan):

- INV-BTI-P04 / INV-IBT-P03 (EH landing-pad marker) and INV-BTI-P03 (setjmp
  landing-pad marker) retrieve only indirect-branch *emission* / source-side
  machinery and bug prose — the marker constrains the branch TARGET, the hits
  are the branch SOURCE — so every hit is rejected as non-isomorphic.
- INV-FORT-R02 (guard-failure handler must be noreturn) retrieves only
  epilogue/return code that collides on the word "return"; no guard-failure
  handler shape is present.

That asymmetry is a real result, not a gap to paper over. The six positives are
the five-way longjmp/setjmp return-edge convergence at expand_builtin_longjmp /
_setjmp_setup, the dynamic-alloca lowering at expand_builtin_alloca, and the
stack-clash probe-stride derivation at compute_stack_clash_protection_loop_data.
"""

from __future__ import annotations

import json
from pathlib import Path

TRANSCRIPT = Path("runs/specgen_inv_bm25/transcript.json")
PENDING = Path("runs/specgen_inv_bm25/pending.json")

# (seed_id, chunk_id) -> the single canonical isomorphic site + authored judgment.
# Every other retrieved hit for these seeds is a lexical collision (false).
TRUE_ANALOGY: dict[tuple[str, str], dict] = {
    ("INV-PAC-P05", "builtins.cc:990:expand_builtin_longjmp"): {
        "aligned_operation": (
            "the generic else-branch of expand_builtin_longjmp restores the saved "
            "target label (`lab = copy_to_reg (lab)`) and control-transfers with "
            "`emit_indirect_jump (lab)`, reconstructing the return/transfer target "
            "with no authentication step of its own"
        ),
        "protected_asset": "integrity of the control-transfer target address on a nonlocal return",
        "why_analogous": (
            "both take a saved control-transfer token out of the jump buffer and resume "
            "control from it; the seed requires that token be re-validated on the next "
            "return, and this generic path restores the raw label and jumps to it, leaving "
            "any signing/authentication to targetm.gen_builtin_longjmp"
        ),
    },
    ("INV-SHSTK-C01", "builtins.cc:990:expand_builtin_longjmp"): {
        "aligned_operation": (
            "the generic branch does `emit_stack_restore (SAVE_NONLOCAL, stack)` and "
            "`emit_move_insn (hard_frame_pointer_rtx, fp)` then `emit_indirect_jump (lab)`: "
            "it rewinds the ordinary stack pointer across all skipped frames in one step "
            "but performs no adjustment of any parallel return-record stack"
        ),
        "protected_asset": (
            "consistency between the ordinary SP and a parallel return-record (shadow) "
            "stack after a multi-frame nonlocal unwind"
        ),
        "why_analogous": (
            "the seed requires longjmp to roll a shadow stack back by the same N frames "
            "as the ordinary SP; this chunk rolls back only the ordinary SP and the "
            "generic path emits no parallel record-stack fixup"
        ),
    },
    ("INV-SS-R03", "builtins.cc:990:expand_builtin_longjmp"): {
        "aligned_operation": (
            "the generic branch restores exactly one stack pointer via "
            "`emit_stack_restore (SAVE_NONLOCAL, stack)` plus the frame pointer, then "
            "indirect-jumps; it has no notion of an auxiliary/secondary stack pointer to "
            "rewind for the frames the jump skips"
        ),
        "protected_asset": "reclamation of a secondary (unsafe) stack across a multi-frame nonlocal jump",
        "why_analogous": (
            "the seed's shape is a nonlocal transfer that skips frames whose secondary-stack "
            "objects are never reclaimed (a monotonic leak); this generic path rewinds only "
            "the single ordinary stack, matching that shape"
        ),
    },
    ("INV-GCS-F02", "builtins.cc:886:expand_builtin_setjmp_setup"): {
        "aligned_operation": (
            "expand_builtin_setjmp_setup writes exactly three words into the buffer "
            "(hard_frame_pointer, the receiver label, and the SAVE_NONLOCAL stack save), "
            "then defers any extra state to `targetm.gen_builtin_setjmp_setup`; no "
            "return-record-stack pointer is saved on the generic path"
        ),
        "protected_asset": "a hardware return-record-stack pointer that must survive setjmp/longjmp",
        "why_analogous": (
            "the seed requires setjmp to save and longjmp to restore the return-record "
            "stack pointer; this chunk shows the generic buffer holds only the three-word "
            "{FP,label,SP} set, so saving that pointer must come from the target hook"
        ),
    },
    ("INV-SCK-B02", "explow.cc:1954:compute_stack_clash_protection_loop_data"): {
        "aligned_operation": (
            "compute_stack_clash_protection_loop_data derives the probe stride as "
            "`*probe_interval = 1 << param_stack_clash_protection_probe_interval` and rounds "
            "the allocation down to that stride (`AND size, -probe_interval`); the residual is "
            "`size - rounded_size`, and the loop probes once per probe_interval bytes"
        ),
        "protected_asset": "the guarantee that every guard page under a growing allocation is touched by a probe",
        "why_analogous": (
            "the seed (generic probe_stack_range) probes every STACK_CHECK_PROBE_INTERVAL bytes "
            "with an interval not strictly tied to the guard page; this chunk is the same shape "
            "under stack-clash — a page-walking probe loop whose stride is a separate parameter "
            "that must stay matched to the guard/probe range or a stride-sized gap skips a page"
        ),
    },
    ("INV-SP-H01", "builtins.cc:5755:expand_builtin_alloca"): {
        "aligned_operation": (
            "expand_builtin_alloca lowers a dynamic allocation by routing it through "
            "`allocate_dynamic_stack_space (op0, 0, align, max_size, alloca_for_var)` rather "
            "than a bare stack adjustment; the alloca-for-variable flag marks the "
            "variable-sized-object case"
        ),
        "protected_asset": "instrumentation coverage of a dynamically-sized stack region",
        "why_analogous": (
            "the seed's shape is 'a function with a dynamic stack allocation must be forced "
            "onto the instrumented guarded path'; this chunk is exactly the dynamic-allocation "
            "lowering site that decides which path the allocation takes"
        ),
    },
}

SPECIALIZE: dict[tuple[str, str], dict] = {
    ("INV-PAC-P05", "builtins.cc:990:expand_builtin_longjmp"): {
        "statement": (
            "GCC's mechanism-neutral __builtin_longjmp lowering (expand_builtin_longjmp) "
            "restores only {frame pointer, target label, stack pointer} from the buffer and, "
            "in its generic fallback, transfers control with a bare emit_indirect_jump to the "
            "restored label; a target whose ABI signs return/transfer addresses must reconcile "
            "that signature inside targetm.gen_builtin_longjmp, because the generic path "
            "authenticates nothing about the restored target."
        ),
        "observation": (
            "on a return-address-signing target that supplies no builtin_longjmp pattern, the "
            "expanded __builtin_setjmp/__builtin_longjmp sequence contains no authentication "
            "instruction between loading the saved label and the indirect jump — disassembly "
            "shows a raw indirect branch to the restored label"
        ),
        "version_sensitivity": "target-specific",
        "falsifiability": {
            "observability": "absence of an authentication instruction on the restored transfer target in the emitted generic longjmp sequence",
            "determinism": "decisive: either targetm.have_builtin_longjmp() supplies a hardened pattern or the generic bare indirect jump is emitted",
            "cost": "inspect the RTL/asm of a __builtin_longjmp expansion; no runtime needed",
            "static_or_dynamic": "static",
        },
    },
    ("INV-SHSTK-C01", "builtins.cc:990:expand_builtin_longjmp"): {
        "statement": (
            "expand_builtin_longjmp's generic fallback rewinds the ordinary stack pointer to "
            "the saved SAVE_NONLOCAL value in a single emit_stack_restore and adjusts no "
            "parallel return-record stack; a target providing a shadow/return-record stack "
            "must perform the matching multi-frame rewind of that record stack inside "
            "targetm.gen_builtin_longjmp, or the record-stack pointer is left stale relative "
            "to the restored SP."
        ),
        "observation": (
            "a __builtin_longjmp that unwinds several frames restores SP directly but emits no "
            "shadow-stack-pointer adjustment; on a shadow-stack target the first return after "
            "such a jump reads a record-stack pointer inconsistent with the restored SP"
        ),
        "version_sensitivity": "target-specific",
        "falsifiability": {
            "observability": "no record-stack adjustment instruction in the emitted generic longjmp sequence alongside the emit_stack_restore",
            "determinism": "decisive: the generic branch either has a record-stack fixup or it does not",
            "cost": "inspect emitted longjmp RTL/asm; no runtime needed",
            "static_or_dynamic": "static",
        },
    },
    ("INV-SS-R03", "builtins.cc:990:expand_builtin_longjmp"): {
        "statement": (
            "GCC's generic nonlocal-transfer lowering restores a single stack pointer "
            "(SAVE_NONLOCAL) and no auxiliary stack pointer; a mechanism that splits locals "
            "onto a secondary stack must arrange its own rewind of that secondary pointer on "
            "the nonlocal path, since the generic __builtin_longjmp path never reclaims "
            "secondary-stack frames skipped by the jump."
        ),
        "observation": (
            "repeated nonlocal transfers that skip frames holding secondary-stack objects show "
            "the secondary stack pointer never decreasing (monotonic growth), because the "
            "generic longjmp path emits no secondary-stack restore"
        ),
        "version_sensitivity": "likely-to-drift",
        "falsifiability": {
            "observability": "monotonic growth of the secondary stack region across repeated cross-frame nonlocal jumps",
            "determinism": "probabilistic in effect but decisive in mechanism: the generic path emits no secondary-stack restore",
            "cost": "runtime observation of secondary-stack pointer across a longjmp loop",
            "static_or_dynamic": "dynamic",
        },
    },
    ("INV-GCS-F02", "builtins.cc:886:expand_builtin_setjmp_setup"): {
        "statement": (
            "expand_builtin_setjmp_setup writes only the three-word {frame pointer, receiver "
            "label, SAVE_NONLOCAL stack save} set into the buffer and defers extra state to "
            "targetm.gen_builtin_setjmp_setup; a target with a hardware return-record-stack "
            "pointer must extend both setup (save the pointer) and longjmp (restore it) via "
            "the target hooks, because the generic buffer stores nothing beyond those three "
            "words."
        ),
        "observation": (
            "on a return-record-stack target lacking the builtin_setjmp/longjmp hooks, the "
            "emitted setjmp buffer has no slot written with the record-stack pointer and "
            "longjmp emits no pointer restore; the first return after longjmp faults on a "
            "control-stack mismatch"
        ),
        "version_sensitivity": "target-specific",
        "falsifiability": {
            "observability": "no store of a return-record-stack pointer into the setjmp buffer on the generic setup path",
            "determinism": "decisive: the generic setup writes exactly three words unless the target setup pattern adds more",
            "cost": "inspect emitted setjmp setup RTL/asm; no runtime needed",
            "static_or_dynamic": "static",
        },
    },
    ("INV-SCK-B02", "explow.cc:1954:compute_stack_clash_protection_loop_data"): {
        "statement": (
            "compute_stack_clash_protection_loop_data sets the probe stride to "
            "`1 << param_stack_clash_protection_probe_interval` and rounds the allocation down "
            "to a multiple of it before the probe loop; that stride must stay no larger than "
            "the guard page the probe defends, because the loop touches one page per stride and "
            "a stride wider than the guard leaves a page between two probes untouched — the same "
            "weakness the generic probe_stack_range interval has when it is not tied to the "
            "guard size."
        ),
        "observation": (
            "when the probe interval parameter exceeds the guard page size, an allocation larger "
            "than the guard but smaller than the probe stride is rounded to zero loop "
            "iterations (or skips a page), so the prologue advances SP across a guard page with "
            "no emit_stack_probe touching it"
        ),
        "version_sensitivity": "target-specific",
        "falsifiability": {
            "observability": "a stack-clash probe loop whose stride exceeds the guard page, leaving a guard page with no probe between two iterations",
            "determinism": "decisive given the probe_interval and guard-size parameters",
            "cost": "inspect the emitted probe loop's stride vs the configured guard size; no runtime needed",
            "static_or_dynamic": "static",
        },
    },
    ("INV-SP-H01", "builtins.cc:5755:expand_builtin_alloca"): {
        "statement": (
            "expand_builtin_alloca lowers a dynamic allocation by routing it through "
            "allocate_dynamic_stack_space (with the alloca-for-variable flag) rather than a "
            "bare stack adjustment; under stack-clash protection a dynamic alloca must remain "
            "on this routed path so the dynamically-sized region is probed, because a bare "
            "adjustment of a variable-sized region would skip guard-page probing entirely."
        ),
        "observation": (
            "a function performing __builtin_alloca / a VLA whose emitted code adjusts SP by a "
            "variable amount without going through the probing path shows a dynamically-sized "
            "stack region with no guard-page probe — an unprobed dynamic allocation"
        ),
        "version_sensitivity": "likely-to-drift",
        "falsifiability": {
            "observability": "a variable-sized stack adjustment for an alloca/VLA with no accompanying guard-page probe",
            "determinism": "decisive: the alloca either routes through allocate_dynamic_stack_space or it does not",
            "cost": "inspect emitted RTL/asm of an alloca-bearing function; no runtime needed",
            "static_or_dynamic": "static",
        },
    },
}

ENTAILMENT: dict[tuple[str, str], dict] = {
    ("INV-PAC-P05", "builtins.cc:990:expand_builtin_longjmp"): {
        "entailed": True,
        "support": "the else branch: `lab = copy_to_reg (lab); ... emit_indirect_jump (lab);` guarded by `if (targetm.have_builtin_longjmp ())`",
        "reason": "the chunk shows exactly the generic restore-and-indirect-jump fallback and the target-hook it defers to, which the statement constrains",
    },
    ("INV-SHSTK-C01", "builtins.cc:990:expand_builtin_longjmp"): {
        "entailed": True,
        "support": "`emit_stack_restore (SAVE_NONLOCAL, stack)` immediately followed by frame-pointer restore and `emit_indirect_jump (lab)`, with no record-stack op",
        "reason": "the single-step ordinary-SP rewind with no parallel record-stack fixup is directly visible in the generic branch",
    },
    ("INV-SS-R03", "builtins.cc:990:expand_builtin_longjmp"): {
        "entailed": True,
        "support": "the generic branch restores only `fp` and one stack via `emit_stack_restore (SAVE_NONLOCAL, stack)` before the indirect jump",
        "reason": "the chunk restores exactly one stack pointer and no auxiliary stack, matching the statement's claim",
    },
    ("INV-GCS-F02", "builtins.cc:886:expand_builtin_setjmp_setup"): {
        "entailed": True,
        "support": "three emit_move_insn/emit_stack_save into buf_addr, buf_addr+sizeof(Pmode), buf_addr+2*sizeof(Pmode), then `if (targetm.have_builtin_setjmp_setup ())`",
        "reason": "the chunk shows the buffer holds exactly three words and defers extra state to the target hook, grounding the statement",
    },
    ("INV-SCK-B02", "explow.cc:1954:compute_stack_clash_protection_loop_data"): {
        "entailed": True,
        "support": "`*probe_interval = 1 << param_stack_clash_protection_probe_interval;` and `*rounded_size = simplify_gen_binary (AND, Pmode, size, GEN_INT (-*probe_interval));` with `*residual = size - rounded_size`",
        "reason": "the chunk computes exactly the probe stride and page-rounding the statement constrains, so the stride/guard relation is grounded in this code",
    },
    ("INV-SP-H01", "builtins.cc:5755:expand_builtin_alloca"): {
        "entailed": True,
        "support": "`result = allocate_dynamic_stack_space (op0, 0, align, max_size, alloca_for_var);`",
        "reason": "the chunk shows the dynamic-allocation routing the statement is about (through allocate_dynamic_stack_space, not a bare adjustment)",
    },
}


def _false_reason(chunk_id: str) -> str:
    if chunk_id.startswith("bugzilla:"):
        return "bugzilla prose match, not a code operation isomorphic to the seed root cause"
    return (
        "lexical collision on shared stack/frame/return/indirect-branch vocabulary; "
        "no operation of the same shape as the seed root cause is present in this chunk"
    )


def main() -> None:
    data = json.loads(TRANSCRIPT.read_text(encoding="utf-8"))
    pending = json.loads(PENDING.read_text(encoding="utf-8"))

    analogy: dict[str, dict] = {}
    specialize: dict[str, dict] = {}
    entailment: dict[str, dict] = {}

    n_true = 0
    for item in pending:
        if item["task"] != "analogy":
            continue
        key = item["key"]
        seed, chunk = key.split("::", 1)
        pair = (seed, chunk)
        if pair in TRUE_ANALOGY:
            n_true += 1
            j = TRUE_ANALOGY[pair]
            analogy[key] = {"does_analogy_hold": True, **j}
            specialize[key] = SPECIALIZE[pair]
            entailment[key] = ENTAILMENT[pair]
        else:
            analogy[key] = {
                "does_analogy_hold": False,
                "aligned_operation": "",
                "protected_asset": "",
                "why_analogous": _false_reason(chunk),
            }

    data["analogy"] = analogy
    data["specialize"] = specialize
    data["entailment"] = entailment
    TRANSCRIPT.write_text(
        json.dumps(data, indent=2, ensure_ascii=False), encoding="utf-8"
    )
    print(f"analogy entries authored : {len(analogy)}  (true={n_true})")
    print(f"specialize entries       : {len(specialize)}")
    print(f"entailment entries       : {len(entailment)}")


if __name__ == "__main__":
    main()
