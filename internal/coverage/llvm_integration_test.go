package coverage

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	defexec "github.com/zjy-dev/de-fuzz/internal/exec"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// TestLLVMCoverage_Integration_EndToEnd runs the full LLVM coverage path against
// a real clang/llvm-cov/llvm-profdata toolchain. It is skipped when any of the
// required tools are unavailable.
func TestLLVMCoverage_Integration_EndToEnd(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not found; skipping LLVM integration test")
	}
	if _, err := exec.LookPath("llvm-profdata"); err != nil {
		t.Skip("llvm-profdata not found; skipping LLVM integration test")
	}
	if _, err := exec.LookPath("llvm-cov"); err != nil {
		t.Skip("llvm-cov not found; skipping LLVM integration test")
	}

	tmp := t.TempDir()
	profileDir := filepath.Join(tmp, "profraw")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Build a tiny instrumented "compiler under test": a program whose own source
	// coverage we measure. Here it stands in for the instrumented clang.
	srcPath := filepath.Join(tmp, "uut.c")
	src := `#include <stdlib.h>
int classify(int n) {
    if (n > 0) { return 1; }
    else { return 0; }
}
int main(int argc, char **argv) {
    return classify(argc);
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(tmp, "uut")
	build := exec.Command(clang, "-fprofile-instr-generate", "-fcoverage-mapping",
		"-O0", srcPath, "-o", binPath)
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("failed to build instrumented binary: %v (%s)", err, out)
	}

	compileFunc := func(s *seed.Seed) error {
		// "Compiling a seed" == running the instrumented UUT once.
		os.Setenv("LLVM_PROFILE_FILE", filepath.Join(profileDir, "seed-%p.profraw"))
		run := exec.Command(binPath, "x")
		run.Env = append(os.Environ(),
			"LLVM_PROFILE_FILE="+filepath.Join(profileDir, "seed-%p.profraw"))
		_ = run.Run()
		return nil
	}

	l := NewLLVMCoverage(LLVMCoverageConfig{
		Executor:        defexec.NewCommandExecutor(),
		CompileFunc:     compileFunc,
		CompilerBinary:  binPath,
		ProfileDir:      profileDir,
		ProfdataCommand: "llvm-profdata",
		CovCommand:      "llvm-cov",
		TotalReportPath: filepath.Join(tmp, "total.json"),
		SeedReportDir:   tmp,
	})

	s := &seed.Seed{}
	s.Meta.ID = 1
	report, err := l.Measure(s)
	if err != nil {
		t.Fatalf("Measure() error = %v", err)
	}

	lines, err := l.ExtractCoveredLinesFiltered(report)
	if err != nil {
		t.Fatalf("ExtractCoveredLinesFiltered() error = %v", err)
	}
	if len(lines) == 0 {
		t.Error("expected non-empty covered lines from instrumented binary")
	}

	inc, err := l.HasIncreased(report)
	if err != nil {
		t.Fatalf("HasIncreased() error = %v", err)
	}
	if !inc {
		t.Error("first seed should report a coverage increase")
	}
	if err := l.Merge(report); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
}
