package oracle

import (
	"errors"
	"strings"
	"testing"
)

// scrubMockExecutor is a deterministic Executor for scrub-checker tests.
// Unlike `MockExecutor` (used by the binary-search checker), this mock
// keys responses on the argv token so a single executor can serve both
// scrub-mode probes ("scrub") and binary-search probes ("<n> <m>") in
// integration tests.
type scrubMockExecutor struct {
	// scrubExitCode / scrubStdout / scrubStderr are returned when argv[0]
	// (binary's first arg) is "scrub".
	scrubExitCode int
	scrubStdout   string
	scrubStderr   string
	// scrubErr, if non-nil, is returned instead of a result; used to
	// exercise the executor-failure path.
	scrubErr error
}

func (m *scrubMockExecutor) ExecuteWithInput(binaryPath string, stdin string) (int, string, string, error) {
	return 0, "", "", nil
}

func (m *scrubMockExecutor) ExecuteWithArgs(binaryPath string, args ...string) (int, string, string, error) {
	if len(args) == 1 && args[0] == "scrub" {
		if m.scrubErr != nil {
			return 0, "", "", m.scrubErr
		}
		return m.scrubExitCode, m.scrubStdout, m.scrubStderr, nil
	}
	// Default: pretend everything else exits cleanly; binary-search-mode
	// integration tests will install a different executor.
	return 0, "", "", nil
}

// TestEpilogueCanaryScrubChecker_LeakFails: the canonical positive case.
// stdout contains GUARD_LEAKED → Fail with reg name in evidence.
func TestEpilogueCanaryScrubChecker_LeakFails(t *testing.T) {
	c := &EpilogueCanaryScrubChecker{}
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &scrubMockExecutor{
			scrubExitCode: 1,
			scrubStdout:   "some preamble\nGUARD_LEAKED reg=3 name=t2\nignored trailing line\n",
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail on GUARD_LEAKED, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "reg=3") {
		t.Errorf("Evidence should preserve leak line; got %q", r.Evidence)
	}
	if !strings.Contains(r.Evidence, "name=t2") {
		t.Errorf("Evidence should include reg name; got %q", r.Evidence)
	}
	if r.Detail["leak_line"] == nil {
		t.Errorf("Detail[leak_line] should be populated")
	}
}

// TestEpilogueCanaryScrubChecker_OKPasses: clean scrub → Pass.
func TestEpilogueCanaryScrubChecker_OKPasses(t *testing.T) {
	c := &EpilogueCanaryScrubChecker{}
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &scrubMockExecutor{
			scrubExitCode: 0,
			scrubStdout:   "CANARY_SCRUB_OK\n",
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass on CANARY_SCRUB_OK, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "no caller-saved") {
		t.Errorf("Evidence should describe the pass; got %q", r.Evidence)
	}
}

// TestEpilogueCanaryScrubChecker_NASysreg: the AArch64 sysreg path.
// Template prints "CANARY_SCRUB_NA reason=no_guard_symbol" when
// __stack_chk_guard resolves to NULL via weak linkage.
func TestEpilogueCanaryScrubChecker_NASysreg(t *testing.T) {
	c := &EpilogueCanaryScrubChecker{}
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &scrubMockExecutor{
			scrubExitCode: 0,
			scrubStdout:   "CANARY_SCRUB_NA reason=no_guard_symbol\n",
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on CANARY_SCRUB_NA, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "no_guard_symbol") {
		t.Errorf("Reason should preserve NA reason; got %q", r.Reason)
	}
}

// TestEpilogueCanaryScrubChecker_LeakDominatesOK: belt-and-suspenders —
// even if the template, due to a regression, prints both markers, the
// leak signal must win.
func TestEpilogueCanaryScrubChecker_LeakDominatesOK(t *testing.T) {
	c := &EpilogueCanaryScrubChecker{}
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &scrubMockExecutor{
			scrubExitCode: 1,
			scrubStdout:   "CANARY_SCRUB_OK\nGUARD_LEAKED reg=0 name=rax\n",
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictFail {
		t.Fatalf("Leak must dominate OK; got %s", r.Verdict)
	}
}

// TestEpilogueCanaryScrubChecker_GarbledStdoutNA: empty / unrelated stdout
// → NA, never a Fail (avoid false positives during template churn).
func TestEpilogueCanaryScrubChecker_GarbledStdoutNA(t *testing.T) {
	c := &EpilogueCanaryScrubChecker{}
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &scrubMockExecutor{
			scrubExitCode: 0,
			scrubStdout:   "",
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on empty stdout, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "no recognizable marker") {
		t.Errorf("Reason should explain marker mismatch; got %q", r.Reason)
	}
}

// TestEpilogueCanaryScrubChecker_ExecutorErrorIsError: infrastructure
// failure (executor returned err) → VerdictError, not NA.
func TestEpilogueCanaryScrubChecker_ExecutorErrorIsError(t *testing.T) {
	c := &EpilogueCanaryScrubChecker{}
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &scrubMockExecutor{
			scrubErr: errors.New("qemu died"),
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictError {
		t.Fatalf("expected Error on executor failure, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "qemu died") {
		t.Errorf("Reason should propagate executor error; got %q", r.Reason)
	}
}

// TestEpilogueCanaryScrubChecker_MissingDepsNA: nil context fields → NA,
// never panic. Mirrors the pattern in checker_dynamic_buffer_test.go.
func TestEpilogueCanaryScrubChecker_MissingDepsNA(t *testing.T) {
	c := &EpilogueCanaryScrubChecker{}
	for name, ctx := range map[string]*CheckContext{
		"nil_executor": {BinaryPath: "/fake"},
		"empty_path":   {Executor: &scrubMockExecutor{}},
		"both_missing": {},
	} {
		r := c.Check(ctx)
		if r.Verdict != VerdictNotApplicable {
			t.Errorf("%s: expected NotApplicable, got %s", name, r.Verdict)
		}
	}
}

// TestEpilogueCanaryScrubChecker_DefaultsAreSane: the zero-value
// EpilogueCanaryScrubChecker must work without any field configured —
// this guarantees the registration in canary_oracle.go remains terse.
func TestEpilogueCanaryScrubChecker_DefaultsAreSane(t *testing.T) {
	c := &EpilogueCanaryScrubChecker{}
	if c.ID() != "INV-SP-R03" {
		t.Errorf("default ID() = %q, want INV-SP-R03", c.ID())
	}
	if c.Category() != CategoryDynamic {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryDynamic)
	}
	if c.sourceURL() == "" {
		t.Error("default sourceURL() must be non-empty")
	}
	if c.sensitivity() == "" {
		t.Error("default sensitivity() must be non-empty")
	}
}
