package disasm

import (
	"strings"

	arm64asm "golang.org/x/arch/arm64/arm64asm"
)

// fromAArch64Inst converts an arm64asm.Inst into our neutral Inst.
//
// Note: arm64asm hides the immediate offset of MemImmediate behind an
// unexported field; the only stable way to recover it is via the
// String() representation. For the current consumers (V01 / S01 don't
// run on AArch64 today; AArch64 stack-canary checking is covered by
// the existing aarch64-specific `stack_protect_test_<mode>` analysis
// in INV-SP-S02) we only need Mem.Base, which IS exposed. Offset
// parsing can be added when a real consumer needs it.
func fromAArch64Inst(pc uint64, raw arm64asm.Inst) Inst {
	base := strings.ToLower(raw.Op.String())

	out := Inst{
		PC:       pc,
		Len:      4,
		Op:       classifyAArch64Op(base),
		Mnemonic: base,
		Raw:      raw,
	}

	storeStyle := isAArch64Store(base)
	cmpStyle := isAArch64Compare(base)

	for idx, a := range raw.Args {
		if a == nil {
			break
		}
		switch v := a.(type) {
		case arm64asm.Reg:
			name := strings.ToLower(v.String())
			if idx == 0 && !storeStyle && !cmpStyle {
				out.DstReg = name
			} else {
				out.SrcRegs = append(out.SrcRegs, name)
			}
		case arm64asm.RegSP:
			name := strings.ToLower(v.String())
			out.SrcRegs = append(out.SrcRegs, name)
		case arm64asm.MemImmediate:
			out.HasMem = true
			out.Mem = Mem{Base: strings.ToLower(v.Base.String())}
		case arm64asm.MemExtend:
			out.HasMem = true
			out.Mem = Mem{
				Base:     strings.ToLower(v.Base.String()),
				Index:    strings.ToLower(v.Index.String()),
				HasIndex: true,
			}
		}
	}
	return out
}

func classifyAArch64Op(base string) Op {
	switch base {
	case "ldr", "ldrb", "ldrh", "ldrsb", "ldrsh", "ldrsw", "ldp", "ldpsw",
		"ldur", "ldurb", "ldurh", "ldursb", "ldursh", "ldursw":
		return OpLoad
	case "str", "strb", "strh", "stp", "stur", "sturb", "sturh":
		return OpStore
	case "mov", "movz", "movk", "movn":
		return OpMove
	case "cmp", "cmn", "tst":
		return OpCompare
	case "b", "bl", "br", "blr", "ret", "cbz", "cbnz", "tbz", "tbnz":
		return OpBranch
	case "add", "adds", "sub", "subs", "and", "ands", "orr", "eor", "eon",
		"lsl", "lsr", "asr":
		return OpArithmetic
	default:
		return OpOther
	}
}

func isAArch64Store(base string) bool {
	switch base {
	case "str", "strb", "strh", "stp", "stur", "sturb", "sturh":
		return true
	}
	return false
}

func isAArch64Compare(base string) bool {
	switch base {
	case "cmp", "cmn", "tst":
		return true
	}
	return false
}
