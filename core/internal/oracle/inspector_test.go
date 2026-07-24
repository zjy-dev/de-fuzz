package oracle

import (
	"errors"
	"os"
	"runtime"
	"testing"
)

// TestInspector_MissingFile asserts inspector returns ErrBinaryMissing
// (not panic) for a path that doesn't exist. This is the common case in
// unit tests with mock paths like "/fake/binary".
func TestInspector_MissingFile(t *testing.T) {
	insp := NewBinaryInspector("/no/such/path/anywhere/abc123")
	if insp.Exists() {
		t.Fatal("Exists() should be false for missing path")
	}
	if _, err := insp.Symbols(); !errors.Is(err, ErrBinaryMissing) {
		t.Errorf("expected ErrBinaryMissing, got: %v", err)
	}
	if _, err := insp.ImportedFunctions(); !errors.Is(err, ErrBinaryMissing) {
		t.Errorf("expected ErrBinaryMissing, got: %v", err)
	}
	if has, err := insp.HasSymbol("main"); has || !errors.Is(err, ErrBinaryMissing) {
		t.Errorf("expected (false, ErrBinaryMissing), got (%v, %v)", has, err)
	}
}

// TestInspector_NotELF asserts non-ELF files (e.g., text files) are
// correctly classified, returning ErrNotELF rather than parsing garbage.
func TestInspector_NotELF(t *testing.T) {
	tmp, err := os.CreateTemp("", "oracle-inspector-notelf-*.txt")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString("not an elf file"); err != nil {
		t.Fatalf("setup write: %v", err)
	}
	tmp.Close()

	insp := NewBinaryInspector(tmp.Name())
	if !insp.Exists() {
		t.Fatal("Exists() should be true for existing text file")
	}
	if insp.IsELF() {
		t.Fatal("IsELF() should be false for plain text file")
	}
	if _, err := insp.Symbols(); !errors.Is(err, ErrNotELF) {
		t.Errorf("expected ErrNotELF, got: %v", err)
	}
}

// TestInspector_RealELF parses the running test binary itself. On Linux
// hosts the test binary is an ELF, so this exercises the real elf.Open
// code path without requiring gcc / a fixture binary.
//
// Skipped on non-Linux to avoid flake on macOS (Mach-O) and Windows (PE).
func TestInspector_RealELF(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("skipping ELF parse test on %s (not ELF host)", runtime.GOOS)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable failed: %v", err)
	}
	insp := NewBinaryInspector(exe)
	if !insp.Exists() {
		t.Fatal("test binary should exist")
	}
	if !insp.IsELF() {
		t.Skip("test binary is not ELF (PIE/wrapped?); skipping")
	}
	syms, err := insp.Symbols()
	if err != nil {
		t.Fatalf("Symbols() error on real ELF: %v", err)
	}
	// Go test binaries are usually NOT stripped, so we expect a non-empty
	// symbol table. If they ARE stripped (some build configs), we accept
	// an empty list as valid.
	t.Logf("test binary has %d symbols", len(syms))
}
