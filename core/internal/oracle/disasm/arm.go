package disasm

import (
	"strings"

	armasm "golang.org/x/arch/arm/armasm"
)

// fromARMInst converts a decoded armasm.Inst into our neutral Inst.
// All architecture quirks live here: condition-code stripping (we
// classify by the base mnemonic), sign-folded memory offsets, and
// pre/post-indexing handling.
//
// We deliberately do NOT enumerate every conditional variant of every
// opcode. Instead we strip the trailing condition / S suffix from the
// mnemonic and classify on the prefix; this keeps the table compact
// and forward-compatible with new condition codes.
func fromARMInst(pc uint64, raw armasm.Inst) Inst {
	full := raw.String()
	// raw.Op.String() returns "LDR.EQ" / "EOR.S.NE" / etc.; we want
	// the bare verb for classification ("ldr", "eor"). Lowercased so
	// evidence strings render consistently.
	base := normalizeARMOp(raw.Op.String())

	out := Inst{
		PC:       pc,
		Len:      raw.Len,
		Op:       classifyARMOp(base),
		Mnemonic: base,
		Raw:      raw,
	}

	// Walk the args. ARM convention is "Args[0] = destination" for
	// data-processing and load instructions, "Args[0] = source" for
	// store instructions; we honor that here.
	storeStyle := isARMStore(base)
	cmpStyle := isARMCompare(base)

	for idx, a := range raw.Args {
		if a == nil {
			break
		}
		switch v := a.(type) {
		case armasm.Reg:
			name := strings.ToLower(v.String())
			if idx == 0 && !storeStyle && !cmpStyle {
				out.DstReg = name
			} else {
				out.SrcRegs = append(out.SrcRegs, name)
			}
		case armasm.Mem:
			out.HasMem = true
			out.Mem = Mem{
				Base:   strings.ToLower(v.Base.String()),
				Offset: int64(v.Sign) * int64(v.Offset),
			}
			if v.Index != 0 {
				out.Mem.Index = strings.ToLower(v.Index.String())
				out.Mem.HasIndex = true
			}
		}
	}

	// On stores, Args[0] is the value being written; we filed it as
	// SrcRegs above, which is correct for the ReadRegs/WrittenRegs
	// surface (a store writes memory, not a register).
	_ = full // reserved for future debug evidence
	return out
}

// normalizeARMOp converts armasm's "LDR.EQ" / "EOR.S.NE" into "ldr" /
// "eor" — the common base mnemonic. Suffixes after the first '.' are
// always condition codes or the flag-setting `S` modifier; both are
// orthogonal to load/store/compare classification.
func normalizeARMOp(opname string) string {
	if i := strings.IndexByte(opname, '.'); i >= 0 {
		opname = opname[:i]
	}
	return strings.ToLower(opname)
}

// classifyARMOp maps the base mnemonic to our coarse Op enum.
// Conditional variants are handled by `normalizeARMOp` stripping
// suffixes before this is called.
func classifyARMOp(base string) Op {
	switch base {
	case "ldr", "ldrb", "ldrh", "ldrd", "ldrsb", "ldrsh", "ldm", "ldmia", "ldmdb",
		"ldmib", "ldmda", "pop", "vldr":
		return OpLoad
	case "str", "strb", "strh", "strd", "stm", "stmia", "stmdb", "stmib", "stmda",
		"push", "vstr":
		return OpStore
	case "mov", "movw", "movt", "movs", "mvn":
		return OpMove
	case "cmp", "cmn", "tst", "teq":
		return OpCompare
	case "b", "bl", "bx", "blx", "ret":
		return OpBranch
	case "add", "adds", "sub", "subs", "rsb", "and", "ands", "orr", "orrs",
		"eor", "eors", "bic", "lsl", "lsr", "asr", "ror":
		return OpArithmetic
	default:
		return OpOther
	}
}

// isARMStore reports whether the base mnemonic places its first
// register operand on the *source* side (i.e., Args[0] is the value
// being written, not a destination). This is the convention for
// STR / STRB / STRH / STRD / STM / PUSH on ARM.
func isARMStore(base string) bool {
	switch base {
	case "str", "strb", "strh", "strd", "stm", "stmia", "stmdb", "stmib",
		"stmda", "push", "vstr":
		return true
	}
	return false
}

// isARMCompare reports whether the base mnemonic is a flag-setting
// instruction with no register destination (CMP / CMN / TST / TEQ).
// All operands are sources for these.
func isARMCompare(base string) bool {
	switch base {
	case "cmp", "cmn", "tst", "teq":
		return true
	}
	return false
}
