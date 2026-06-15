package disasm

import (
	"debug/elf"
	"errors"
	"testing"
)

// armBytes encodes 4 little-endian ARM A32 instructions chosen to
// exercise every classification path:
//
//	ldr r0, [r1]      e5 91 00 00  →  e5910000
//	str r0, [r1]      e5 81 00 00  →  e5810000
//	cmp r0, r1        e1 50 00 01  →  e1500001
//	bx  lr            e1 2f ff 1e  →  e12fff1e
//
// Bytes are written little-endian (LSB first) as in an actual ELF.
func armBytes() []byte {
	return []byte{
		0x00, 0x00, 0x91, 0xe5, // ldr r0, [r1]
		0x00, 0x00, 0x81, 0xe5, // str r0, [r1]
		0x01, 0x00, 0x50, 0xe1, // cmp r0, r1
		0x1e, 0xff, 0x2f, 0xe1, // bx lr
	}
}

func TestDecode_ARM_Classification(t *testing.T) {
	insts, err := Decode(ArchARM, 0x1000, armBytes())
	if err != nil {
		t.Fatalf("Decode(ARM) failed: %v", err)
	}
	if len(insts) != 4 {
		t.Fatalf("expected 4 instructions, got %d", len(insts))
	}
	wantOps := []Op{OpLoad, OpStore, OpCompare, OpBranch}
	wantPCs := []uint64{0x1000, 0x1004, 0x1008, 0x100c}
	for i, w := range wantOps {
		if insts[i].Op != w {
			t.Errorf("inst[%d].Op = %s, want %s (mnemonic=%q)", i, insts[i].Op, w, insts[i].Mnemonic)
		}
		if insts[i].PC != wantPCs[i] {
			t.Errorf("inst[%d].PC = %#x, want %#x", i, insts[i].PC, wantPCs[i])
		}
		if insts[i].Len != 4 {
			t.Errorf("inst[%d].Len = %d, want 4", i, insts[i].Len)
		}
	}
}

// TestDecode_ARM_LoadDestAndMem verifies that an ARM load populates
// DstReg and Mem.Base correctly — the foundational dataflow that V01
// will rely on.
func TestDecode_ARM_LoadDestAndMem(t *testing.T) {
	insts, err := Decode(ArchARM, 0, armBytes()[:4]) // just the LDR
	if err != nil {
		t.Fatalf("Decode(ARM) failed: %v", err)
	}
	got := insts[0]
	if got.DstReg != "r0" {
		t.Errorf("DstReg = %q, want r0", got.DstReg)
	}
	if !got.HasMem {
		t.Fatalf("HasMem = false; want true")
	}
	if got.Mem.Base != "r1" {
		t.Errorf("Mem.Base = %q, want r1", got.Mem.Base)
	}
}

// TestDecode_ARM_StoreNoDestReg verifies the store-style convention:
// Args[0] of STR is the value being written, NOT a destination, so
// DstReg must be empty.
func TestDecode_ARM_StoreNoDestReg(t *testing.T) {
	insts, err := Decode(ArchARM, 0, armBytes()[4:8]) // just the STR
	if err != nil {
		t.Fatalf("Decode(ARM) failed: %v", err)
	}
	got := insts[0]
	if got.DstReg != "" {
		t.Errorf("STR DstReg should be empty, got %q", got.DstReg)
	}
	if got.Op != OpStore {
		t.Errorf("STR Op = %s, want store", got.Op)
	}
	if !got.HasMem || got.Mem.Base != "r1" {
		t.Errorf("STR Mem.Base = %q, want r1 (HasMem=%v)", got.Mem.Base, got.HasMem)
	}
}

// TestNormalizeARMOp covers the suffix-stripping behavior driving the
// classifyARMOp lookup. Without this, "LDR.EQ" would never hit the
// "ldr" branch.
func TestNormalizeARMOp(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"LDR", "ldr"},
		{"LDR.EQ", "ldr"},
		{"EOR.S.NE", "eor"},
		{"MOVW", "movw"},
	}
	for _, c := range cases {
		if got := normalizeARMOp(c.in); got != c.want {
			t.Errorf("normalizeARMOp(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDecode_AArch64_Ret verifies a trivial AArch64 instruction
// decodes (RET = d6 5f 03 c0).
func TestDecode_AArch64_Ret(t *testing.T) {
	code := []byte{0xc0, 0x03, 0x5f, 0xd6} // ret
	insts, err := Decode(ArchAArch64, 0, code)
	if err != nil {
		t.Fatalf("Decode(AArch64) failed: %v", err)
	}
	if len(insts) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(insts))
	}
	if insts[0].Op != OpBranch {
		t.Errorf("RET Op = %s, want branch", insts[0].Op)
	}
}

// TestDecode_X86_RetAndCmp covers x86_64 RET (0xc3) and CMP
// (48 39 c8 = `cmp rax, rcx`).
func TestDecode_X86_RetAndCmp(t *testing.T) {
	code := []byte{
		0x48, 0x39, 0xc8, // cmp rax, rcx
		0xc3, // ret
	}
	insts, err := Decode(ArchAMD64, 0, code)
	if err != nil {
		t.Fatalf("Decode(AMD64) failed: %v", err)
	}
	if len(insts) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(insts))
	}
	if insts[0].Op != OpCompare {
		t.Errorf("CMP Op = %s, want compare", insts[0].Op)
	}
	if insts[1].Op != OpBranch {
		t.Errorf("RET Op = %s, want branch", insts[1].Op)
	}
}

// TestDecode_X86_MovMemIsLoadOrStore verifies the positional
// re-classification logic: `MOV reg, [mem]` must report OpLoad and
// `MOV [mem], reg` must report OpStore even though the mnemonic alone
// is ambiguous.
func TestDecode_X86_MovMemIsLoadOrStore(t *testing.T) {
	// 48 8b 07  →  mov rax, qword ptr [rdi]   (load)
	loadCode := []byte{0x48, 0x8b, 0x07}
	insts, err := Decode(ArchAMD64, 0, loadCode)
	if err != nil {
		t.Fatalf("Decode load failed: %v", err)
	}
	if insts[0].Op != OpLoad {
		t.Errorf("mov reg,[mem] Op = %s, want load", insts[0].Op)
	}
	if !insts[0].HasMem {
		t.Errorf("mov reg,[mem] HasMem = false; want true")
	}

	// 48 89 07  →  mov qword ptr [rdi], rax   (store)
	storeCode := []byte{0x48, 0x89, 0x07}
	insts, err = Decode(ArchAMD64, 0, storeCode)
	if err != nil {
		t.Fatalf("Decode store failed: %v", err)
	}
	if insts[0].Op != OpStore {
		t.Errorf("mov [mem],reg Op = %s, want store", insts[0].Op)
	}
	if !insts[0].HasMem {
		t.Errorf("mov [mem],reg HasMem = false; want true")
	}
}

// TestArchFromELF spot-checks the elf.Machine → Arch mapping plus the
// sentinel error path.
func TestArchFromELF(t *testing.T) {
	cases := []struct {
		machine elf.Machine
		want    Arch
		wantErr bool
	}{
		{elf.EM_ARM, ArchARM, false},
		{elf.EM_AARCH64, ArchAArch64, false},
		{elf.EM_X86_64, ArchAMD64, false},
		{elf.EM_386, ArchX86, false},
		{elf.EM_MIPS, ArchUnknown, true},
	}
	for _, c := range cases {
		got, err := ArchFromELF(c.machine, elf.ELFCLASS64)
		if c.wantErr {
			if err == nil {
				t.Errorf("ArchFromELF(%s) expected error, got nil", c.machine)
			}
			if !errors.Is(err, ErrUnsupportedArch) {
				t.Errorf("ArchFromELF(%s) error = %v, want ErrUnsupportedArch wrap", c.machine, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ArchFromELF(%s) unexpected error: %v", c.machine, err)
		}
		if got != c.want {
			t.Errorf("ArchFromELF(%s) = %s, want %s", c.machine, got, c.want)
		}
	}
}

// TestDecode_UnknownArch verifies ArchUnknown is rejected with
// ErrUnsupportedArch (so checkers can downgrade to NA).
func TestDecode_UnknownArch(t *testing.T) {
	_, err := Decode(ArchUnknown, 0, []byte{0x00})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUnsupportedArch) {
		t.Errorf("error = %v, want ErrUnsupportedArch", err)
	}
}

// TestArchString is a stable-output guard for log formatting.
func TestArchString(t *testing.T) {
	cases := map[Arch]string{
		ArchARM:     "arm",
		ArchThumb:   "thumb",
		ArchAArch64: "aarch64",
		ArchAMD64:   "amd64",
		ArchX86:     "x86",
	}
	for a, want := range cases {
		if got := a.String(); got != want {
			t.Errorf("Arch(%d).String() = %q, want %q", int(a), got, want)
		}
	}
}

// TestOpString is a stable-output guard for the coarse Op classifier
// (used in evidence strings).
func TestOpString(t *testing.T) {
	cases := map[Op]string{
		OpLoad: "load", OpStore: "store", OpMove: "move",
		OpCompare: "compare", OpBranch: "branch",
		OpArithmetic: "arith", OpOther: "other",
	}
	for op, want := range cases {
		if got := op.String(); got != want {
			t.Errorf("Op(%d).String() = %q, want %q", int(op), got, want)
		}
	}
}
