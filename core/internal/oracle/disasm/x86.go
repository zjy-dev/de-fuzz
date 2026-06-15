package disasm

import (
	"strings"

	x86asm "golang.org/x/arch/x86/x86asm"
)

// fromX86Inst converts an x86asm.Inst into our neutral Inst.
//
// Intel-order quirk: x86 args are written `dst, src`, so Args[0] is the
// destination for most data-movement ops. The exception that matters
// most for canary checkers is `MOV [mem], reg` — a store — where
// Args[0] is a Mem and Args[1] is the source register. We classify
// stores positionally (does Args[0] resolve to memory?) rather than
// by mnemonic, because the same `MOV` opcode covers both directions.
//
// Compare-class instructions (`CMP`, `TEST`) treat both args as
// sources; we follow that here.
func fromX86Inst(pc uint64, raw x86asm.Inst) Inst {
	base := strings.ToLower(raw.Op.String())

	out := Inst{
		PC:       pc,
		Len:      raw.Len,
		Op:       classifyX86Op(base),
		Mnemonic: base,
		Raw:      raw,
	}

	cmpStyle := isX86Compare(base)
	// On x86 a store is "Args[0] is Mem"; record that decision before
	// the per-arg loop so register dispatch knows where DstReg lives.
	storeStyle := false
	if len(raw.Args) > 0 {
		if _, ok := raw.Args[0].(x86asm.Mem); ok {
			storeStyle = true
		}
	}

	for idx, a := range raw.Args {
		if a == nil {
			break
		}
		switch v := a.(type) {
		case x86asm.Reg:
			name := strings.ToLower(v.String())
			if idx == 0 && !storeStyle && !cmpStyle {
				out.DstReg = name
			} else {
				out.SrcRegs = append(out.SrcRegs, name)
			}
		case x86asm.Mem:
			out.HasMem = true
			out.Mem = Mem{
				Base:   strings.ToLower(v.Base.String()),
				Offset: v.Disp,
			}
			if v.Index != 0 {
				out.Mem.Index = strings.ToLower(v.Index.String())
				out.Mem.HasIndex = true
			}
		}
	}

	// Re-classify: a `MOV [mem], reg` decoded as OpMove should be
	// an OpStore for our purposes, since the consumer is asking
	// "does this write to memory?".
	if storeStyle && (out.Op == OpMove || out.Op == OpOther) && out.HasMem {
		out.Op = OpStore
	}
	// Conversely `MOV reg, [mem]` is a load.
	if !storeStyle && out.HasMem && out.Op == OpMove {
		out.Op = OpLoad
	}
	return out
}

// classifyX86Op handles the unambiguous cases. MOV is left as OpMove
// here and re-classified to OpLoad / OpStore by `fromX86Inst` once we
// see whether memory is on the source or destination side.
func classifyX86Op(base string) Op {
	switch base {
	case "lea":
		// LEA computes an address but never reads memory; treat as
		// arithmetic for our coarse classification.
		return OpArithmetic
	case "mov", "movzx", "movsx", "movsxd", "movabs":
		return OpMove
	case "push":
		return OpStore
	case "pop":
		return OpLoad
	case "cmp", "test":
		return OpCompare
	case "jmp", "je", "jne", "jz", "jnz", "ja", "jae", "jb", "jbe",
		"jg", "jge", "jl", "jle", "jo", "jno", "js", "jns", "jp", "jnp",
		"jc", "jnc", "call", "ret", "retq":
		return OpBranch
	case "add", "sub", "and", "or", "xor", "shl", "shr", "sar", "imul",
		"mul", "neg", "not", "inc", "dec", "adc", "sbb":
		return OpArithmetic
	default:
		return OpOther
	}
}

func isX86Compare(base string) bool {
	switch base {
	case "cmp", "test":
		return true
	}
	return false
}
