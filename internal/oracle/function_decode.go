package oracle

import (
	"errors"
	"fmt"

	"github.com/zjy-dev/de-fuzz/internal/oracle/disasm"
)

// errFunctionOutOfRange is returned by decodeFunction when a FunctionSymbol's
// [Addr, Addr+Size) range cannot be located inside any executable section.
// This is non-fatal — the caller should skip the function and continue.
var errFunctionOutOfRange = errors.New("function range not contained in any executable section")

// decodeFunction reads `fn`'s bytes from the appropriate executable section
// and decodes them through the disasm package. It returns the decoded
// instruction stream plus the architecture used.
//
// Failure modes (caller decides verdict):
//   - errUnsupportedArch from disasm.ArchFromELF when the ELF e_machine is
//     not handled (MIPS / RISC-V / SPARC / ...). Callers downgrade to NA.
//   - errFunctionOutOfRange when the symbol's Addr is not contained in any
//     executable section we read (e.g., relocatable object with .Addr=0
//     and a stale symbol).
//
// Thumb caveat: an ELF header alone cannot tell us if a function is ARM or
// Thumb on EM_ARM (the bit-0 of `st_value` encodes that, and we don't
// surface that today). Callers that care must pass `forceArch` explicitly;
// callers passing `disasm.ArchUnknown` get the heuristic from
// `disasm.ArchFromELF` which assumes plain ARM. INV-SP-V01 / S01 are
// designed to detect the buggy pattern in either mode and rely on the
// instruction-stream shape rather than mode-specific encodings, so we
// accept the imprecision for now.
func decodeFunction(insp BinaryInspector, fn FunctionSymbol, forceArch disasm.Arch) ([]disasm.Inst, disasm.Arch, error) {
	machine, err := insp.Machine()
	if err != nil {
		return nil, disasm.ArchUnknown, err
	}
	class, err := insp.Class()
	if err != nil {
		return nil, disasm.ArchUnknown, err
	}
	arch := forceArch
	if arch == disasm.ArchUnknown {
		arch, err = disasm.ArchFromELF(machine, class)
		if err != nil {
			return nil, disasm.ArchUnknown, err
		}
	}

	execs, err := insp.ExecutableSections()
	if err != nil {
		return nil, arch, err
	}
	for _, sec := range execs {
		// Function fully contained in this section?
		if fn.Addr < sec.Addr {
			continue
		}
		off := fn.Addr - sec.Addr
		end := off + fn.Size
		if end > uint64(len(sec.Data)) {
			continue
		}
		insts, derr := disasm.Decode(arch, fn.Addr, sec.Data[off:end])
		if derr != nil {
			// Partial decode: still useful for downstream pattern scans.
			return insts, arch, fmt.Errorf("decode %q: %w", fn.Name, derr)
		}
		return insts, arch, nil
	}
	return nil, arch, errFunctionOutOfRange
}
