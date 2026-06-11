package coverage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/exec"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// writeFile is a small test helper to create a file with string content.
func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// fakeExecutor records commands and simulates llvm-cov export by copying a
// preset JSON payload into the redirected output path.
type fakeExecutor struct {
	commands    []string
	exportJSON  string // payload to write as the llvm-cov export result
	demangleOut string // stdout for demangler calls
}

func (f *fakeExecutor) Run(command string, args ...string) (*exec.ExecutionResult, error) {
	full := command + " " + strings.Join(args, " ")
	f.commands = append(f.commands, full)

	// Simulate an llvm-cov export: "... > /path/to/report.json"
	if strings.Contains(full, "export") && strings.Contains(full, ">") {
		idx := strings.LastIndex(full, ">")
		out := strings.TrimSpace(full[idx+1:])
		if err := os.WriteFile(out, []byte(f.exportJSON), 0644); err != nil {
			return &exec.ExecutionResult{}, err
		}
		return &exec.ExecutionResult{}, nil
	}

	// Simulate a demangler call (returns preset stdout).
	if f.demangleOut != "" && (strings.Contains(command, "cxxfilt") || strings.Contains(command, "c++filt")) {
		return &exec.ExecutionResult{Stdout: f.demangleOut}, nil
	}

	return &exec.ExecutionResult{}, nil
}

const sampleExportJSON = `{
  "version": "3.1.0",
  "type": "llvm.coverage.json.export",
  "data": [
    {
      "files": [
        {
          "filename": "/src/foo.cpp",
          "segments": [
            [10, 1, 3, true, true, false],
            [11, 1, 0, true, true, false],
            [12, 1, 2, true, true, false]
          ]
        }
      ],
      "functions": []
    }
  ]
}`

func newTestLLVMCoverage(t *testing.T, fe *fakeExecutor, profileDir, totalPath, seedDir string) *LLVMCoverage {
	t.Helper()
	return NewLLVMCoverage(LLVMCoverageConfig{
		Executor:        fe,
		CompileFunc:     func(s *seed.Seed) error { return nil },
		CompilerBinary:  "/fake/clang",
		ProfileDir:      profileDir,
		ProfdataCommand: "llvm-profdata",
		CovCommand:      "llvm-cov",
		TotalReportPath: totalPath,
		SeedReportDir:   seedDir,
	})
}

func TestLLVMCoverage_MeasureCompiled(t *testing.T) {
	tmp := t.TempDir()
	fe := &fakeExecutor{exportJSON: sampleExportJSON}
	l := newTestLLVMCoverage(t, fe, tmp, filepath.Join(tmp, "total.json"), tmp)

	s := &seed.Seed{}
	s.Meta.ID = 7
	report, err := l.MeasureCompiled(s)
	if err != nil {
		t.Fatalf("MeasureCompiled() error = %v", err)
	}
	if report == nil {
		t.Fatal("MeasureCompiled() returned nil report")
	}
	// Report file must exist.
	if _, err := os.Stat(filepath.Join(tmp, "7.json")); err != nil {
		t.Errorf("seed report not created: %v", err)
	}
}

func TestLLVMCoverage_MeasureCompiled_ZeroID(t *testing.T) {
	tmp := t.TempDir()
	fe := &fakeExecutor{exportJSON: sampleExportJSON}
	l := newTestLLVMCoverage(t, fe, tmp, filepath.Join(tmp, "total.json"), tmp)
	if _, err := l.MeasureCompiled(&seed.Seed{}); err == nil {
		t.Error("MeasureCompiled() should error on seed ID 0")
	}
}

func TestLLVMCoverage_Clean(t *testing.T) {
	tmp := t.TempDir()
	// Real executor so `find -delete` actually runs.
	profraw := filepath.Join(tmp, "a.profraw")
	profdata := filepath.Join(tmp, "a.profdata")
	keep := filepath.Join(tmp, "keep.ll")
	for _, f := range []string{profraw, profdata, keep} {
		if err := writeFile(f, "x"); err != nil {
			t.Fatal(err)
		}
	}
	l := NewLLVMCoverage(LLVMCoverageConfig{
		Executor:        exec.NewCommandExecutor(),
		ProfileDir:      tmp,
		CovCommand:      "llvm-cov",
		TotalReportPath: filepath.Join(tmp, "total.json"),
		SeedReportDir:   tmp,
	})
	if err := l.Clean(); err != nil {
		t.Fatalf("Clean() error = %v", err)
	}
	if _, err := os.Stat(profraw); !os.IsNotExist(err) {
		t.Error(".profraw should be deleted")
	}
	if _, err := os.Stat(profdata); !os.IsNotExist(err) {
		t.Error(".profdata should be deleted")
	}
	if _, err := os.Stat(keep); os.IsNotExist(err) {
		t.Error(".ll should be preserved")
	}
}

func TestLLVMCoverage_HasIncreased_FirstSeed(t *testing.T) {
	tmp := t.TempDir()
	fe := &fakeExecutor{exportJSON: sampleExportJSON}
	l := newTestLLVMCoverage(t, fe, tmp, filepath.Join(tmp, "total.json"), tmp)

	s := &seed.Seed{}
	s.Meta.ID = 1
	report, err := l.MeasureCompiled(s)
	if err != nil {
		t.Fatal(err)
	}
	inc, err := l.HasIncreased(report)
	if err != nil {
		t.Fatalf("HasIncreased() error = %v", err)
	}
	if !inc {
		t.Error("first seed should report an increase")
	}
}

func TestLLVMCoverage_Merge_And_NoIncrease(t *testing.T) {
	tmp := t.TempDir()
	fe := &fakeExecutor{exportJSON: sampleExportJSON}
	totalPath := filepath.Join(tmp, "total.json")
	l := newTestLLVMCoverage(t, fe, tmp, totalPath, tmp)

	s := &seed.Seed{}
	s.Meta.ID = 1
	report, err := l.MeasureCompiled(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Merge(report); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if _, err := os.Stat(totalPath); err != nil {
		t.Fatalf("total.json not created: %v", err)
	}

	// Same report again -> no increase.
	s2 := &seed.Seed{}
	s2.Meta.ID = 2
	report2, err := l.MeasureCompiled(s2)
	if err != nil {
		t.Fatal(err)
	}
	inc, err := l.HasIncreased(report2)
	if err != nil {
		t.Fatal(err)
	}
	if inc {
		t.Error("identical coverage should NOT report an increase after merge")
	}
}

func TestLLVMCoverage_Resume_NotZero(t *testing.T) {
	tmp := t.TempDir()
	totalPath := filepath.Join(tmp, "total.json")
	// Pre-existing total simulates a resumed session.
	pre := &llvmTotalReport{CoveredLines: map[string][]int{"/src/foo.cpp": {10, 12}}}
	if err := writeLLVMTotal(totalPath, pre); err != nil {
		t.Fatal(err)
	}

	fe := &fakeExecutor{exportJSON: sampleExportJSON}
	l := newTestLLVMCoverage(t, fe, tmp, totalPath, tmp)

	stats, err := l.GetStats()
	if err != nil {
		t.Fatalf("GetStats() error = %v", err)
	}
	if stats.TotalCoveredLines != 2 {
		t.Errorf("resumed coverage lines = %d, want 2", stats.TotalCoveredLines)
	}
}

func TestLLVMCoverage_GetStats_NoTotal(t *testing.T) {
	tmp := t.TempDir()
	fe := &fakeExecutor{exportJSON: sampleExportJSON}
	l := newTestLLVMCoverage(t, fe, tmp, filepath.Join(tmp, "missing.json"), tmp)
	stats, err := l.GetStats()
	if err != nil {
		t.Fatalf("GetStats() error = %v", err)
	}
	if stats.TotalCoveredLines != 0 {
		t.Errorf("stats without total should be zero, got %d", stats.TotalCoveredLines)
	}
}

// Ensure LLVMCoverage satisfies the coverage interfaces.
var (
	_ Coverage              = (*LLVMCoverage)(nil)
	_ PreCompileCoverage    = (*LLVMCoverage)(nil)
	_ PostCompileCoverage   = (*LLVMCoverage)(nil)
	_ FilteredLineExtractor = (*LLVMCoverage)(nil)
)

func TestLLVMCoverage_ExtractCoveredLinesFiltered_NoTargets(t *testing.T) {
	tmp := t.TempDir()
	fe := &fakeExecutor{exportJSON: sampleExportJSON}
	l := newTestLLVMCoverage(t, fe, tmp, filepath.Join(tmp, "total.json"), tmp)
	s := &seed.Seed{}
	s.Meta.ID = 1
	report, err := l.MeasureCompiled(s)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := l.ExtractCoveredLinesFiltered(report)
	if err != nil {
		t.Fatal(err)
	}
	// With no targets configured, all covered lines (10, 12) pass through.
	if len(lines) != 2 {
		t.Errorf("expected 2 covered lines, got %d: %v", len(lines), lines)
	}
	if fmt.Sprint(lines) == "" {
		t.Error("unexpected empty lines")
	}
}
