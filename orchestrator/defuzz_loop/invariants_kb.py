"""Distilled invariant knowledge for the LLM-driven oracle.

The deterministic Go oracle decides every invariant it can express as a sound
programmatic check. A handful of FORTIFY invariants resist that on some targets:
their decision needs a per-ISA disassembly backend the core only implements for
x86_64 (O01/O02/O03), or a judgement the symbol level cannot make (W01 cannot
tell "no fortify sink" from "wrapper inlined away"). On aarch64 those four punt
to NOT_APPLICABLE / ERROR.

This module holds a compact, self-contained statement of exactly those invariants
so the LLM oracle can adjudicate them from the seed source plus the binary's
aarch64 disassembly and symbol table — the evidence the deterministic backend
would have used had it supported the ISA. Text is distilled from
docs/tech-docs/invariants/fortify-source.md (the SSOT); keep them in sync.
"""

from __future__ import annotations

from pydantic import BaseModel


class InvariantBrief(BaseModel):
    """One invariant's adjudication brief for the LLM oracle."""

    id: str
    title: str
    statement: str
    violation_signal: str
    pass_signal: str
    na_signal: str


# The four FORTIFY invariants the deterministic oracle cannot decide on aarch64.
# Each brief is written so a verdict can be reached from (seed source + aarch64
# disassembly of fortify call sites + symbol table) alone.
_FORTIFY_BRIEFS: dict[str, InvariantBrief] = {
    "INV-FORT-W01": InvariantBrief(
        id="INV-FORT-W01",
        title="__fortify_function wrapper must lower to a real __<family>_chk call",
        statement=(
            "glibc's __fortify_function wrappers are static __always_inline; after "
            "the caller inlines them they must still forward to __<family>_chk(..., "
            "dstlen) with the caller's __builtin_object_size context intact. If the "
            "inliner drops the BOS context, the wrapper collapses back to the bare "
            "libc function and the overflow check disappears (kernel a28a6e860c6c "
            "worked around exactly this for Clang <= 12)."
        ),
        violation_signal=(
            "The seed calls a fortify-protected libc function on an undersized "
            "destination (e.g. memcpy/strcpy/sprintf/snprintf into a buffer smaller "
            "than the copy), the build used -D_FORTIFY_SOURCE>=2 -O>=2, yet the "
            "disassembly shows ONLY a branch to the bare function (e.g. `bl memcpy`) "
            "with no `__memcpy_chk`/`__strcpy_chk`/`__sprintf_chk` symbol referenced "
            "anywhere. The wrapper was inlined away -> FAIL."
        ),
        pass_signal=(
            "The disassembly/symbol table references at least one `__<family>_chk` "
            "symbol for the protected call -> PASS."
        ),
        na_signal=(
            "The seed compiles to no fortify-protected libc sink at all (no "
            "memcpy/strcpy/printf-family call that fortify would wrap), so the "
            "invariant is simply not exercised -> NOT_APPLICABLE."
        ),
    ),
    "INV-FORT-O01": InvariantBrief(
        id="INV-FORT-O01",
        title="BOS must not return (size_t)-1 for a struct's last-member array",
        statement=(
            "When an array is the last member of a struct (flexible or fixed), "
            "__builtin_object_size(p->arr, 1) must return that field's static byte "
            "size, never (size_t)-1. If it returns -1, the __<family>_chk dstlen "
            "argument is SIZE_MAX, the runtime check `dstlen < len` is always false, "
            "and any overflow of that field passes silently (GCC PR101836)."
        ),
        violation_signal=(
            "The seed copies into a struct's last-member array via a DIRECT field "
            "reference with at most a compile-time-constant offset (p->arr or "
            "&p->arr[CONST]), writes MORE bytes than the field's real capacity, the "
            "build used -D_FORTIFY_SOURCE>=2 -O>=2, AND at the "
            "`__memcpy_chk`/`__strcpy_chk` call site the dstlen argument (x2/x3 on "
            "aarch64) is the immediate -1 / 0xFFFFFFFFFFFFFFFF (`mov x3, #-1` / "
            "`movn x3, #0`) instead of the field's real static size -> FAIL. The -1 "
            "must be UNEXPECTED: it only counts when the size was statically knowable."
        ),
        pass_signal=(
            "The dstlen argument at the chk call site is the field's real static "
            "size (a sensible small constant matching the source) -> PASS."
        ),
        na_signal=(
            "No __<family>_chk call site targets a struct last-member array; OR the "
            "destination pointer is formed with a RUNTIME-VARIABLE offset (e.g. "
            "p->arr + (cond?4:3), base + i) so -1 is the documented, correct "
            "'unknown size' result, not the bug; OR the copy length is bounded and no "
            "overflow is attempted -> NOT_APPLICABLE."
        ),
    ),
    "INV-FORT-O02": InvariantBrief(
        id="INV-FORT-O02",
        title="counted_by BDOS must equal count * sizeof(elem)",
        statement=(
            "For `struct S { int n; T arr[] __attribute__((counted_by(n))); }`, the "
            "__builtin_dynamic_object_size of p->arr under -D_FORTIFY_SOURCE=3 must "
            "be p->n * sizeof(T). Known bugs return 0 for nested-pointer access "
            "(Clang <= 19.1.2) or a whole-struct value off by exactly 4 bytes "
            "(Clang <= 19.1.3), letting an in-window overflow pass."
        ),
        violation_signal=(
            "The seed writes more than count*sizeof(elem) bytes into a counted_by "
            "flexible array, and the dstlen computed at the chk call site is 0 "
            "(short-circuits the check) or is 4 bytes larger than count*sizeof(elem) "
            "-> FAIL."
        ),
        pass_signal=(
            "The dstlen at the chk call site is computed as exactly "
            "count*sizeof(elem) (a multiply/shift of the count field) -> PASS."
        ),
        na_signal=(
            "No counted_by attribute / BDOS path is exercised, or the toolchain is "
            "GCC without counted_by support -> NOT_APPLICABLE."
        ),
    ),
    "INV-FORT-O03": InvariantBrief(
        id="INV-FORT-O03",
        title="BDOS must read the latest value of a reassigned size variable",
        statement=(
            "When BDOS references a size variable that a local reassigns before the "
            "fortify call, it must use the value live AT the call point, not a stale "
            "predecessor-block value. GCC 14 (PR113514) used a value 8 bytes too "
            "large for `f.bar[argc][40]` after argc was reassigned, making an "
            "overflow of f.bar pass the chk comparison."
        ),
        violation_signal=(
            "The seed reassigns a size variable just before an overflowing fortify "
            "call, and the dstlen at the chk call site is computed from the OLD "
            "(pre-reassignment) value — i.e. larger than the real writable region — "
            "rather than the value live at the call -> FAIL."
        ),
        pass_signal=(
            "The dstlen reflects the size variable's value live at the call point "
            "(matches the reassigned value / real region size) -> PASS."
        ),
        na_signal=(
            "No reassigned-size BDOS path is exercised by the seed -> NOT_APPLICABLE."
        ),
    ),
}


def llm_oracle_target_ids() -> frozenset[str]:
    """Invariant IDs the LLM oracle is allowed to adjudicate (FORTIFY, aarch64)."""
    return frozenset(_FORTIFY_BRIEFS)


def brief_for(invariant_id: str) -> InvariantBrief | None:
    return _FORTIFY_BRIEFS.get(invariant_id)
