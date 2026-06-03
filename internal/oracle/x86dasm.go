package oracle

import (
	"bytes"
	"debug/elf"
	"fmt"

	"golang.org/x/arch/x86/x86asm"
)

// x86dasm.go centralizes the x86/x86_64 instruction-decode helpers used by
// the IBT static checkers (INV-IBT-P02 / P06 / N01 / N02 and friends).
//
// Why a thin wrapper over `x86asm.Decode`:
//
//  1. Most checkers need the same primitives — "find every indirect branch",
//     "is this byte sequence an ENDBR?", "does this call target setjmp?" —
//     so we centralize them here instead of letting each checker reach into
//     x86asm directly.
//  2. Decoding is reasonably fast but not free; results for a binary's
//     `.text` are cached on the CheckContext via DecodeIndirectBranches so
//     that P02/P06/N01 share the work.
//  3. We keep the surface narrow on purpose: anything more sophisticated
//     (full CFG, jump-table reconstruction) lives inside the checker that
//     needs it.

// IndirectBranch records one decoded indirect call/jmp instruction.
//
// Address fields are in the same reference frame as `ExecSection.Addr`
// (i.e., absolute virtual addresses for ET_EXEC/ET_DYN and
// section-relative for ET_REL).
type IndirectBranch struct {
	// Section name the branch lives in (e.g., ".text").
	Section string
	// Addr is the address of the first byte of the branch instruction
	// (post-prefix). For NOTRACK branches this is the byte AFTER the
	// `0x3E` prefix; use HasNotrack / PrefixAddr to access the prefix.
	Addr uint64
	// PrefixAddr is the address of the leading prefix byte if any
	// (currently only `0x3E` NOTRACK is recognised); equals Addr when no
	// prefix is present.
	PrefixAddr uint64
	// Length is the total instruction length in bytes (including any
	// recognised NOTRACK prefix).
	Length int
	// IsCall is true for `call*`, false for `jmp*`.
	IsCall bool
	// HasNotrack indicates a leading `0x3E` (NOTRACK) prefix.
	HasNotrack bool
	// TargetKnown reports whether we could derive a static target
	// address from the instruction encoding alone.
	TargetKnown bool
	// Target is the resolved target address (only meaningful when
	// TargetKnown). Common cases that resolve:
	//   - `call rel32` / `jmp rel32`   (direct relative)
	// Indirect via register / memory remains TargetKnown=false; callers
	// that need jump-table targets must do their own data-flow.
	Target uint64
	// Inst is the decoded x86asm.Inst (kept for callers that need fine
	// detail like operand kinds).
	Inst x86asm.Inst
}

// DecodeMode picks the operand size used by x86asm.Decode (32 or 64).
func decodeMode(machine elf.Machine) int {
	if machine == elf.EM_X86_64 {
		return 64
	}
	return 32
}

// notrackPrefix is the single-byte CET NOTRACK prefix.
const notrackPrefix = 0x3E

// IsEndbrAt reports whether the 4 bytes at `data[off:off+4]` form an
// ENDBR matching `pattern`.
func IsEndbrAt(data []byte, off int, pattern []byte) bool {
	if off < 0 || off+len(pattern) > len(data) {
		return false
	}
	return bytes.Equal(data[off:off+len(pattern)], pattern)
}

// IndirectBranchCacheKey is the CheckContext.Cache slot for the decoded
// indirect-branch list. Keyed by section name set so a binary with one
// `.text` is decoded once per Analyze.
const IndirectBranchCacheKey = "oracle.x86dasm.indirect_branches"

// EnumerateIndirectBranches walks every executable section's bytes and
// returns every indirect call/jmp instruction it can decode. Direct
// `call rel32` / `jmp rel32` are NOT included; they're not the subject of
// any IBT invariant.
//
// The decoder is best-effort: bytes that don't decode as a valid
// instruction are skipped one byte at a time. This is acceptable because
// `.text` is overwhelmingly real instructions and we only care about the
// indirect-branch subset; the false-positive risk is bounded by the
// follow-up "is this target an ENDBR" check that each consuming checker
// performs.
func EnumerateIndirectBranches(machine elf.Machine, execs []ExecSection) []IndirectBranch {
	mode := decodeMode(machine)
	var out []IndirectBranch
	for _, sec := range execs {
		i := 0
		for i < len(sec.Data) {
			// Recognise NOTRACK prefix first.
			hasNotrack := false
			prefixAddr := sec.Addr + uint64(i)
			start := i
			if sec.Data[i] == notrackPrefix && i+1 < len(sec.Data) {
				hasNotrack = true
				start = i + 1
			}
			inst, err := x86asm.Decode(sec.Data[start:], mode)
			if err != nil || inst.Len == 0 {
				// Skip one byte and retry.
				i++
				continue
			}
			ib, ok := classifyBranch(sec, sec.Addr+uint64(start), inst)
			if ok {
				ib.HasNotrack = hasNotrack
				if hasNotrack {
					ib.PrefixAddr = prefixAddr
					ib.Length = (start - i) + inst.Len
				}
				out = append(out, ib)
			}
			// Advance past either: prefix(1)+insn or just insn.
			step := inst.Len
			if hasNotrack {
				step += start - i
			}
			if step <= 0 {
				step = 1
			}
			i += step
		}
	}
	return out
}

// classifyBranch decides whether a decoded instruction is an indirect
// call/jmp; if it's a direct relative branch with a knowable target we
// also record the resolved target (used by the IFUNC / setjmp checkers).
//
// Returns (zero-value, false) when the instruction is not a branch we
// care about.
func classifyBranch(sec ExecSection, addr uint64, inst x86asm.Inst) (IndirectBranch, bool) {
	ib := IndirectBranch{
		Section:    sec.Name,
		Addr:       addr,
		PrefixAddr: addr,
		Length:     inst.Len,
		Inst:       inst,
	}
	switch inst.Op {
	case x86asm.CALL:
		ib.IsCall = true
	case x86asm.JMP:
		ib.IsCall = false
	default:
		return IndirectBranch{}, false
	}
	if len(inst.Args) == 0 {
		return IndirectBranch{}, false
	}
	switch a := inst.Args[0].(type) {
	case x86asm.Rel:
		// Direct relative branch — we still report it because the
		// setjmp / IFUNC checkers need to resolve direct calls.
		ib.TargetKnown = true
		ib.Target = addr + uint64(inst.Len) + uint64(int64(a))
		return ib, true
	case x86asm.Reg, x86asm.Mem:
		// Indirect — target unknown statically.
		ib.TargetKnown = false
		return ib, true
	default:
		return IndirectBranch{}, false
	}
}

// CachedIndirectBranches returns the per-Analyze cached decode result if
// present, otherwise computes & stores it. When ctx.Cache is nil the
// helper still works but does not memoize.
func CachedIndirectBranches(ctx *CheckContext, machine elf.Machine, execs []ExecSection) []IndirectBranch {
	if ctx == nil {
		return EnumerateIndirectBranches(machine, execs)
	}
	if v, ok := ctx.CacheGet(IndirectBranchCacheKey); ok {
		if cached, ok := v.([]IndirectBranch); ok {
			return cached
		}
	}
	branches := EnumerateIndirectBranches(machine, execs)
	ctx.CacheSet(IndirectBranchCacheKey, branches)
	return branches
}

// FormatBranchAddr is a tiny helper for putting branch addrs into bug
// descriptions in a stable hex format.
func FormatBranchAddr(addr uint64) string { return fmt.Sprintf("0x%x", addr) }
