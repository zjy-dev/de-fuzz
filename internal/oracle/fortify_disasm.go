package oracle

import (
	"debug/elf"
	"fmt"

	"golang.org/x/arch/x86/x86asm"
)

// FortifyChkCallSite is one observed `call __<family>_chk` instruction
// in an executable section, together with the dstlen immediate that the
// preceding code paths could load into the dstlen-argument register.
//
// The struct is intentionally narrow: it carries only the facts the
// O01 / O02 / O03 checkers consume. The "dstlen immediate" recovery is
// best-effort — see `recoverDstlenImmediate` — and `DstlenImmediate`
// being -1 means "could not statically prove a constant".
type FortifyChkCallSite struct {
	// CallerFunc is the name of the enclosing function symbol; empty
	// when the call site lies in a gap between symbols (rare).
	CallerFunc string
	// SiteAddr is the absolute address of the `call` instruction.
	SiteAddr uint64
	// Family is the libc function family (`memcpy` / `snprintf` / ...).
	Family string
	// ChkSymbol is the resolved chk symbol name (`__memcpy_chk` ...).
	ChkSymbol string
	// DstlenImmediate is the BOS / BDOS-supplied destination size that
	// the wrapper put into the chk-arg register before the call. -1
	// means the static recovery failed (non-constant load, indirect
	// register, control-flow merge, or a non-x86 binary).
	DstlenImmediate int64
	// DstlenIsAllOnes is true when the recovered immediate is the
	// `(size_t)-1` sentinel value that signals BOS-fallback (the
	// INV-FORT-O01 silent-bypass shape).
	DstlenIsAllOnes bool
}

// SupportsFortifyDisasm reports whether the (machine, class) pair has a
// disassembly backend wired up for FORTIFY checkers. Today only x86_64
// is supported — aarch64 / riscv64 / loongarch64 take the NA path with a
// reason that mentions the missing backend.
//
// This is the single switch the static checkers consult before calling
// `FindFortifyChkCallSites`.
func SupportsFortifyDisasm(machine elf.Machine, class elf.Class) bool {
	switch machine {
	case elf.EM_X86_64:
		return class == elf.ELFCLASS64
	default:
		return false
	}
}

// FindFortifyChkCallSites walks every executable section, locates every
// PC-relative `call` to a `__<family>_chk` PLT entry, and recovers the
// dstlen immediate (when statically provable).
//
// The implementation is deliberately conservative: it only looks at the
// last `MOV imm64 -> RCX` (System-V AMD64 calls memcpy_chk-family with
// `(dst, src, len, dstlen)` mapped to `(rdi, rsi, rdx, rcx)`) within a
// short instruction window before the `call`. This is enough to spot
// the INV-FORT-O01 `(size_t)-1` and the INV-FORT-O02 / O03 fixed-value
// shapes from the documented PRs, and it deliberately does not chase
// def-use chains across basic blocks (which the plan explicitly
// downgrades to "disasm_confidence" Detail).
//
// `inspector` is required (the checkers should pre-flight via
// `BinaryInspector.Exists`); a nil `inspector` returns nil.
func FindFortifyChkCallSites(inspector BinaryInspector) ([]FortifyChkCallSite, error) {
	if inspector == nil {
		return nil, fmt.Errorf("nil inspector")
	}
	machine, err := inspector.Machine()
	if err != nil {
		return nil, err
	}
	class, err := inspector.Class()
	if err != nil {
		return nil, err
	}
	if !SupportsFortifyDisasm(machine, class) {
		return nil, nil // NA at the call-site level; checker maps to NA.
	}

	funcs, err := inspector.FunctionSymbols()
	if err != nil {
		return nil, err
	}
	execs, err := inspector.ExecutableSections()
	if err != nil {
		return nil, err
	}

	// Build a name lookup for chk symbols (by resolved address). We
	// learn the call target name from the imported symbol set: a
	// `call rel32` reaches the PLT stub for the resolved name, and the
	// PLT stub is itself a defined function symbol in `.plt`. Rather
	// than chase PLT relocations, we accept any call whose computed
	// target address falls inside a function whose name is a chk
	// symbol. That set is already in `funcs` (the PLT stubs are
	// STT_FUNC defined symbols).
	chkAddrToName := make(map[uint64]string)
	for _, fn := range funcs {
		if family := chkSymbolFamilyName(fn.Name); family != "" {
			chkAddrToName[fn.Addr] = fn.Name
		}
	}
	// A binary with no `_chk` PLT stubs has nothing to find.
	if len(chkAddrToName) == 0 {
		return nil, nil
	}

	var out []FortifyChkCallSite
	for _, sec := range execs {
		walkSection(sec, funcs, chkAddrToName, &out)
	}
	return out, nil
}

// walkSection disassembles `sec` linearly and emits one
// FortifyChkCallSite per recognised chk-call.
//
// "Linear sweep" disassembly is brittle on x86 in general but fine for
// the GCC / Clang `.text` sections we inspect: glibc-linked seed
// templates do not embed jump tables in `.text`, and the executable
// sections are ELF-aligned so inter-function padding is `nop` /
// `endbr64`-and-`nop` regions that decode cleanly.
func walkSection(sec ExecSection, funcs []FunctionSymbol, chkAddrs map[uint64]string, out *[]FortifyChkCallSite) {
	// NOTE: one of three independent x86 linear-sweep decoders in the
	// oracle (see also decodeX86 in disasm/disasm.go and
	// EnumerateIndirectBranches in x86dasm.go). Candidate for future
	// consolidation.
	const window = 16 // instructions kept in the rolling buffer

	// Sliding window of recent decoded instructions; we use it to find
	// the most recent immediate-load into RCX before a call.
	buf := make([]past, 0, window)

	off := 0
	for off < len(sec.Data) {
		inst, err := x86asm.Decode(sec.Data[off:], 64)
		if err != nil || inst.Len == 0 {
			off++ // skip a byte, keep scanning
			continue
		}
		addr := sec.Addr + uint64(off)

		// Track immediate loads into RCX (the AMD64 4th argument slot).
		entry := past{inst: inst, addr: addr, size: inst.Len}
		if dst, imm, ok := decodeImmediateLoad(inst); ok {
			entry.dstrc = dst
			entry.imm = imm
			entry.hasIm = true
		}

		if inst.Op == x86asm.CALL {
			// `call rel32` — operand 0 is a Rel relative to the
			// instruction *end*; convert to absolute.
			if rel, ok := inst.Args[0].(x86asm.Rel); ok {
				target := resolveRelTarget(addr, inst.Len, rel)
				if name, isChk := chkAddrs[target]; isChk {
					site := FortifyChkCallSite{
						SiteAddr:        addr,
						Family:          chkSymbolFamilyName(name),
						ChkSymbol:       name,
						DstlenImmediate: -1,
					}
					if fn, ok := findEnclosingFunction(funcs, addr); ok {
						site.CallerFunc = fn.Name
					}
					if imm, hit := lookbackRCXImm(buf); hit {
						site.DstlenImmediate = imm
						if uint64(imm) == ^uint64(0) || imm == int64(-1) {
							site.DstlenIsAllOnes = true
						}
					}
					*out = append(*out, site)
				}
			}
		}

		// Maintain the rolling window.
		if len(buf) == window {
			copy(buf, buf[1:])
			buf = buf[:window-1]
		}
		buf = append(buf, entry)

		off += inst.Len
	}
}

// decodeImmediateLoad recognises:
//   - `mov RCX, imm32`  (REX.W + C7 /0 imm32 → sign-extended)
//   - `mov RCX, imm64`  (REX.W + B9 imm64)
//   - `mov ECX, imm32`  (B9 imm32 — zero-extended; OK because chk arg
//     comparisons use the full register and a high-zero immediate is
//     also informative)
//   - `xor RCX, RCX` / `xor ECX, ECX`  → imm == 0
//
// Returns (destReg, immediate, recognised).
func decodeImmediateLoad(inst x86asm.Inst) (x86asm.Reg, int64, bool) {
	if inst.Op == x86asm.MOV && len(inst.Args) >= 2 {
		dst, ok := inst.Args[0].(x86asm.Reg)
		if !ok {
			return 0, 0, false
		}
		if dst != x86asm.RCX && dst != x86asm.ECX {
			return 0, 0, false
		}
		switch v := inst.Args[1].(type) {
		case x86asm.Imm:
			return dst, int64(v), true
		}
	}
	if inst.Op == x86asm.XOR && len(inst.Args) >= 2 {
		dst, dok := inst.Args[0].(x86asm.Reg)
		src, sok := inst.Args[1].(x86asm.Reg)
		if dok && sok && dst == src && (dst == x86asm.RCX || dst == x86asm.ECX) {
			return dst, 0, true
		}
	}
	return 0, 0, false
}

// lookbackRCXImm scans the instruction window from newest to oldest and
// returns the most recent immediate that landed in RCX/ECX. Any branch
// or call between the load and the chk-call invalidates the recovery
// (the def-use chain crossed a basic block); we mark such cases as
// not-recovered so the checker can report `disasm_confidence=low`.
func lookbackRCXImm(buf []past) (int64, bool) {
	for i := len(buf) - 1; i >= 0; i-- {
		op := buf[i].inst.Op
		if op == x86asm.JMP || op == x86asm.CALL || op == x86asm.RET ||
			op == x86asm.JE || op == x86asm.JNE || op == x86asm.JL ||
			op == x86asm.JG || op == x86asm.JLE || op == x86asm.JGE ||
			op == x86asm.JA || op == x86asm.JB || op == x86asm.JAE || op == x86asm.JBE {
			return 0, false
		}
		if buf[i].hasIm {
			return buf[i].imm, true
		}
	}
	return 0, false
}

// past mirrors the inner-scope type used by walkSection so that
// lookbackRCXImm can be a free function (Go does not allow methods on
// inner types). Kept package-private.
type past struct {
	inst  x86asm.Inst
	addr  uint64
	size  int
	dstrc x86asm.Reg
	imm   int64
	hasIm bool
}
