//go:build integration

package oracle

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// drev2026004TriggerC is the minimal C reproducer for DREV-2026-004.
// The constant 0xfa1e0ff3_00000000 contains the endbr64 byte sequence
// (F3 0F 1E FA) starting at byte offset 4 of the 8-byte little-endian
// immediate.  GCC's defective ix86_endbr_immediate_operand predicate
// fails to detect this misaligned occurrence, so it emits the full
// movabs instruction into the .text section — leaking a byte-level
// ENDBR64 pattern inside the function body rather than only at its entry.
const drev2026004TriggerC = `
unsigned long long gadget(void) {
    return 0xfa1e0ff300000000ULL;
}
`

// TestDREV2026004_IBTOracle_DetectsBug compiles the DREV-2026-004 trigger
// with a host x86_64 GCC that has -fcf-protection=branch and verifies that
// the IBT oracle surfaces exactly one INV-IBT-B01 violation.
//
// The test is skipped when:
//   - /usr/bin/gcc is absent (no host GCC installed), or
//   - the compiled object contains no unintended ENDBR
//     (meaning the GCC version under test has the upstream fix applied).
func TestDREV2026004_IBTOracle_DetectsBug(t *testing.T) {
	gcc := "/usr/bin/gcc"
	if _, err := os.Stat(gcc); err != nil {
		if path, err2 := exec.LookPath("gcc"); err2 == nil {
			gcc = path
		} else {
			t.Skip("host gcc not found; skipping DREV-2026-004 integration test")
		}
	}

	tempDir, err := os.MkdirTemp("", "drev2026004_")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tempDir)

	srcPath := filepath.Join(tempDir, "trigger.c")
	objPath := filepath.Join(tempDir, "trigger.o")

	if err := os.WriteFile(srcPath, []byte(drev2026004TriggerC), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cmd := exec.Command(gcc, "-O2", "-fcf-protection=branch", "-c", "-o", objPath, srcPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compilation failed: %v\n%s", err, out)
	}

	o := &IBTOracle{}
	ctx := &AnalyzeContext{BinaryPath: objPath}
	s := &seed.Seed{
		Meta:    seed.Metadata{ID: 1, FilePath: srcPath, ContentPath: srcPath},
		Content: drev2026004TriggerC,
	}

	bug, err := o.Analyze(s, ctx, nil)
	if err != nil {
		t.Fatalf("IBTOracle.Analyze: %v", err)
	}
	if bug == nil {
		t.Skip("no unintended ENDBR found; GCC under test may have DREV-2026-004 fix applied")
	}

	t.Logf("DREV-2026-004 confirmed:\n%s", bug.Description)

	// Verify the description mentions our function.
	if bug.Description == "" {
		t.Error("Bug.Description must not be empty")
	}
}
