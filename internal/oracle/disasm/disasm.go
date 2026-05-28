// Package disasm provides a thin, architecture-neutral disassembly layer
// over `golang.org/x/arch`. It exists so that invariant checkers
// (`internal/oracle/checker_*.go`) don't have to switch on architecture
// at every call site, and so that unit tests can construct synthetic
// instruction streams without touching real ELF files.
//
// Design constraints, in order of priority:
//
//  1. Stable surface for checkers. Checkers see one `Inst` shape; the
//     architecture-specific quirks (Thumb mode discovery, ARM PC-relative
//     literal pools, x86 ModR/M variants) are kept inside this package.
//
//  2. Minimal API. We only expose the predicates and accessors that the
//     stack-canary / IBT invariants actually need today — `IsLoad`,
//     `IsStore`, `IsCompare`, `MemBase`, `MemOffset`, `WrittenRegs`,
//     `ReadRegs`, plus the raw mnemonic for evidence strings. Adding more
//     is cheap; pre-emptively exposing all of `armasm.Args` is not.
//
//  3. Best-effort, never panic. ELF inspection commonly hits stripped
//     binaries, foreign architectures, Mach-O hosts. `Decode` returns
//     a typed error and an empty slice on unrecoverable inputs so the
//     caller can VerdictNotApplicable cleanly.
//
//  4. Reusable across mechanisms. Stack-canary V01/S01 are the first
//     consumers, but the same predicates serve future IBT checkers
//     (e.g., scanning function bodies for unintended ENDBR landing
//     pads — INV-IBT-B03) and future FORTIFY checkers.
//
// Architecture coverage today: ARM/Thumb (primary, where V01 lives),
// AArch64, x86_64. Other backends are returned as `ErrUnsupportedArch`
// and consumers must downgrade to NA.
package disasm

import (
	"debug/elf"
	"errors"
	"fmt"

	armasm "golang.org/x/arch/arm/armasm"
	arm64asm "golang.org/x/arch/arm64/arm64asm"
	x86asm "golang.org/x/arch/x86/x86asm"
)

// Arch is the abstract instruction-set family the consumer cares about.
// Mode-level distinctions (ARM vs Thumb, 32 vs 64 bit) are folded in.
type Arch int

const (
	// ArchUnknown is the zero value; Decode rejects it with ErrUnsupportedArch.
	ArchUnknown Arch = iota
	// ArchARM is 32-bit ARM (`A32` / `armv7-a` style), 4-byte instructions.
	ArchARM
	// ArchThumb is the 16/32-bit Thumb encoding used on Cortex-M and on
	// interworking ARM. INV-SP-V01 (GCC 9.x Cortex-M4 nano.specs) lives
	// here.
	ArchThumb
	// ArchAArch64 is 64-bit ARM (`A64`), fixed 4-byte instructions.
	ArchAArch64
	// ArchAMD64 is x86_64. Variable-length instructions; we use
	// x86asm.Decode64.
	ArchAMD64
	// ArchX86 is 32-bit x86. We use x86asm.Decode32.
	ArchX86
)

// String returns a stable token for log / report output.
func (a Arch) String() string {
	switch a {
	case ArchARM:
		return "arm"
	case ArchThumb:
		return "thumb"
	case ArchAArch64:
		return "aarch64"
	case ArchAMD64:
		return "amd64"
	case ArchX86:
		return "x86"
	default:
		return fmt.Sprintf("Arch(%d)", int(a))
	}
}

// ErrUnsupportedArch is returned by Decode when the requested Arch is
// not handled by this package. Checkers should map this to
// VerdictNotApplicable.
var ErrUnsupportedArch = errors.New("disasm: unsupported architecture")

// ArchFromELF maps an `elf.Machine` (and class) to a disasm.Arch.
// The Thumb / ARM split is NOT decidable from the ELF header alone —
// callers that need Thumb must pass it explicitly via `Decode` instead
// of going through this helper.
//
// Returns ArchUnknown (and a non-nil error) for machines this package
// does not yet handle.
func ArchFromELF(machine elf.Machine, class elf.Class) (Arch, error) {
	switch machine {
	case elf.EM_ARM:
		return ArchARM, nil
	case elf.EM_AARCH64:
		return ArchAArch64, nil
	case elf.EM_X86_64:
		return ArchAMD64, nil
	case elf.EM_386:
		return ArchX86, nil
	default:
		return ArchUnknown, fmt.Errorf("%w: %s", ErrUnsupportedArch, machine)
	}
}

// Op is a stable, architecture-neutral classification of an instruction's
// effect. We keep the granularity coarse on purpose: most invariant
// checkers want to ask "is this a load?", "is this a compare?",
// "does this write a register?", not "is this a UQADD16".
type Op int

const (
	// OpUnknown is the catch-all; checkers fall back to inspecting
	// the raw mnemonic string when they hit this.
	OpUnknown Op = iota
	// OpLoad reads a value from memory into a register
	// (LDR / LDRB / LDRH / LDP / MOV mem→reg / etc.).
	OpLoad
	// OpStore writes a register value to memory
	// (STR / STRB / STP / MOV reg→mem / PUSH on x86).
	OpStore
	// OpMove transfers a value register-to-register or imm-to-register
	// without touching memory (MOV reg,reg / MOVW / MVN).
	OpMove
	// OpCompare sets flags based on a comparison
	// (CMP / CMN / TST / TEQ / EORS-with-discarded-result).
	OpCompare
	// OpBranch is any control-flow change (B / BL / RET / JMP / CALL).
	OpBranch
	// OpArithmetic covers ADD / SUB / EOR / AND / ORR when the result
	// is consumed (i.e., not the OpCompare flag-only variant).
	OpArithmetic
	// OpOther is everything else.
	OpOther
)

// String returns a stable token for log / report output.
func (o Op) String() string {
	switch o {
	case OpLoad:
		return "load"
	case OpStore:
		return "store"
	case OpMove:
		return "move"
	case OpCompare:
		return "compare"
	case OpBranch:
		return "branch"
	case OpArithmetic:
		return "arith"
	case OpOther:
		return "other"
	default:
		return "unknown"
	}
}

// Mem is the simplified memory-operand view used by checkers.
// Only fields actually consumed today are populated; everything else
// stays in `Raw` for the rare checker that needs deep inspection.
type Mem struct {
	// Base is the symbolic name of the base register
	// ("r3", "sp", "x0", "rsp", ...). Empty if not applicable.
	Base string
	// Index is the symbolic name of the index register, if any.
	Index string
	// Offset is the constant offset; meaning is architecture-specific
	// (always added on ARM regardless of `Sign`, where we already
	// pre-applied `Sign` for the caller).
	Offset int64
	// HasIndex is true if Index is meaningful.
	HasIndex bool
}

// Inst is the architecture-neutral instruction view exposed to
// checkers. It is a *slim subset* of the underlying x/arch struct;
// fields not yet needed are deliberately absent.
type Inst struct {
	// PC is the address (or section-relative offset) of the instruction's
	// first byte. Caller-supplied via `Decode`'s base argument.
	PC uint64
	// Len is the encoded length in bytes (4 for ARM/AArch64,
	// 2 or 4 for Thumb, 1..15 for x86).
	Len int
	// Op is the coarse classification (see comments on Op constants).
	Op Op
	// Mnemonic is the lowercased instruction name
	// ("ldr", "str", "eors", "cmp", "mov", "ret", ...). Always populated.
	Mnemonic string
	// DstReg is the symbolic name of the primary destination register
	// for OpLoad / OpMove / OpArithmetic. Empty otherwise.
	DstReg string
	// SrcRegs holds the symbolic names of source-only registers
	// (excluding any register that's also in Mem.Base / Mem.Index).
	// Order is preserved from the underlying x/arch decoding.
	SrcRegs []string
	// HasMem is true if exactly one operand is a memory access.
	// (Multi-mem instructions like LDM / STM are rare in epilogues
	// and currently fall under OpOther + HasMem=false.)
	HasMem bool
	// Mem is the memory operand when HasMem is true.
	Mem Mem
	// Raw is the underlying x/arch instruction value, type-erased.
	// Use only when the fields above are insufficient; document the
	// type assertion at the call site.
	Raw any
}

// String returns a textual rendering suitable for evidence strings.
// We fall through to the underlying x/arch String() when one is
// available so the format matches what users see in objdump.
func (i Inst) String() string {
	if s, ok := i.Raw.(fmt.Stringer); ok {
		return s.String()
	}
	return i.Mnemonic
}

// Decode disassembles `code` starting at byte offset 0 (PC = base) for
// the given Arch. It returns one `Inst` per decoded instruction. On
// the first decode error it stops and returns the instructions decoded
// so far plus the error; callers that want a partial result can ignore
// the error.
//
// `base` is the starting PC for the first instruction; it is added to
// each subsequent Inst.PC by the instruction's encoded length. For ELF
// callers, pass the section's `Addr` plus the function's offset within
// the section.
func Decode(arch Arch, base uint64, code []byte) ([]Inst, error) {
	switch arch {
	case ArchARM:
		return decodeARM(base, code, armasm.ModeARM)
	case ArchThumb:
		return decodeARM(base, code, armasm.ModeThumb)
	case ArchAArch64:
		return decodeAArch64(base, code)
	case ArchAMD64:
		return decodeX86(base, code, 64)
	case ArchX86:
		return decodeX86(base, code, 32)
	default:
		return nil, ErrUnsupportedArch
	}
}

// decodeARM walks `code` two or four bytes at a time depending on the
// Thumb mode flag. Thumb decoding occasionally needs 4 bytes for a
// 32-bit Thumb encoding; armasm.Decode handles that by setting Len.
func decodeARM(base uint64, code []byte, mode armasm.Mode) ([]Inst, error) {
	out := make([]Inst, 0, len(code)/4)
	pc := base
	for off := 0; off < len(code); {
		// armasm wants at least 4 bytes for ARM, can take 2 for Thumb;
		// pass the remainder and let it refuse if too short.
		raw, err := armasm.Decode(code[off:], mode)
		if err != nil {
			return out, fmt.Errorf("disasm/arm: decode at %#x: %w", pc, err)
		}
		if raw.Len == 0 {
			return out, fmt.Errorf("disasm/arm: zero-length decode at %#x", pc)
		}
		out = append(out, fromARMInst(pc, raw))
		off += raw.Len
		pc += uint64(raw.Len)
	}
	return out, nil
}

func decodeAArch64(base uint64, code []byte) ([]Inst, error) {
	const insnLen = 4
	out := make([]Inst, 0, len(code)/insnLen)
	pc := base
	for off := 0; off+insnLen <= len(code); off += insnLen {
		raw, err := arm64asm.Decode(code[off:])
		if err != nil {
			return out, fmt.Errorf("disasm/aarch64: decode at %#x: %w", pc, err)
		}
		out = append(out, fromAArch64Inst(pc, raw))
		pc += insnLen
	}
	return out, nil
}

func decodeX86(base uint64, code []byte, bits int) ([]Inst, error) {
	out := make([]Inst, 0, len(code)/4)
	pc := base
	for off := 0; off < len(code); {
		raw, err := x86asm.Decode(code[off:], bits)
		if err != nil {
			return out, fmt.Errorf("disasm/x86: decode at %#x: %w", pc, err)
		}
		if raw.Len == 0 {
			return out, fmt.Errorf("disasm/x86: zero-length decode at %#x", pc)
		}
		out = append(out, fromX86Inst(pc, raw))
		off += raw.Len
		pc += uint64(raw.Len)
	}
	return out, nil
}
