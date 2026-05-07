package oracle

import (
	"testing"
)

// TestDynamicBufferSearchChecker_NoCrash: search finds no crash → NA, not Fail.
func TestDynamicBufferSearchChecker_NoCrash(t *testing.T) {
	c := &DynamicBufferSearchChecker{
		InvariantID:    "INV-TEST-DYN",
		MechanismLabel: "Test",
		MaxFillSize:    100,
		DefaultBufSize: 64,
		SentinelMarker: SentinelMarker,
	}
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor:   &MockExecutor{CrashThreshold: 0},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable when no crash observed, got %s", r.Verdict)
	}
	if r.Detail["min_crash_size"] != -1 {
		t.Errorf("Detail[min_crash_size] = %v, want -1", r.Detail["min_crash_size"])
	}
}

// TestDynamicBufferSearchChecker_SIGABRTPasses: SIGABRT (134) → Pass
// (mechanism's __*_chk_fail caught the overflow).
func TestDynamicBufferSearchChecker_SIGABRTPasses(t *testing.T) {
	c := &DynamicBufferSearchChecker{
		MaxFillSize: 200, DefaultBufSize: 64, SentinelMarker: SentinelMarker,
		InvariantID: "INV-X", MechanismLabel: "Mech",
	}
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor:   &MockExecutor{CrashThreshold: 100, CrashExitCode: ExitCodeSIGABRT},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass on SIGABRT, got %s (evidence=%s)", r.Verdict, r.Evidence)
	}
}

// TestDynamicBufferSearchChecker_SIGSEGVWithSentinelFails: classic bypass.
func TestDynamicBufferSearchChecker_SIGSEGVWithSentinelFails(t *testing.T) {
	c := &DynamicBufferSearchChecker{
		MaxFillSize: 200, DefaultBufSize: 64, SentinelMarker: SentinelMarker,
		InvariantID: "INV-X", MechanismLabel: "Mech",
	}
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 100, CrashExitCode: ExitCodeSIGSEGV, ReturnSentinel: true,
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail on SIGSEGV+sentinel, got %s", r.Verdict)
	}
	if r.Detail["polarity_sensitive"] != true {
		t.Error("dynamic checker must mark itself polarity_sensitive")
	}
}

// TestDynamicBufferSearchChecker_SIGSEGVWithoutSentinelNA: indirect crash
// inside seed() → NA (likely false positive due to spill corruption).
func TestDynamicBufferSearchChecker_SIGSEGVWithoutSentinelNA(t *testing.T) {
	c := &DynamicBufferSearchChecker{
		MaxFillSize: 200, DefaultBufSize: 64, SentinelMarker: SentinelMarker,
		InvariantID: "INV-X", MechanismLabel: "Mech",
	}
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 50, CrashExitCode: ExitCodeSIGSEGV, ReturnSentinel: false,
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NotApplicable on SIGSEGV without sentinel, got %s", r.Verdict)
	}
}

// TestDynamicBufferSearchChecker_CacheReuse: a second invocation in the
// same CheckContext must not call the executor again — it must reuse the
// cached search result (verifies the dynamic-result cache pattern that lets
// multiple dynamic checkers share one binary search).
func TestDynamicBufferSearchChecker_CacheReuse(t *testing.T) {
	probes := 0
	exec := &countingExecutor{
		inner:    &MockExecutor{CrashThreshold: 100, CrashExitCode: ExitCodeSIGABRT},
		probes:   &probes,
	}
	c := &DynamicBufferSearchChecker{
		MaxFillSize: 200, DefaultBufSize: 64, SentinelMarker: SentinelMarker,
		InvariantID: "INV-X", MechanismLabel: "Mech",
	}
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor:   exec,
		Cache:      make(map[string]any),
	}
	c.Check(ctx)
	first := probes
	if first == 0 {
		t.Fatal("first run should issue probes")
	}
	c.Check(ctx)
	if probes != first {
		t.Errorf("second Check() in same ctx must reuse cache (probes before=%d after=%d)", first, probes)
	}
}

// TestDynamicBufferSearchChecker_NoExecutorNA: missing dependencies → NA, not panic.
func TestDynamicBufferSearchChecker_NoExecutorNA(t *testing.T) {
	c := &DynamicBufferSearchChecker{
		MaxFillSize: 200, DefaultBufSize: 64, SentinelMarker: SentinelMarker,
		InvariantID: "INV-X", MechanismLabel: "Mech",
	}
	r := c.Check(&CheckContext{BinaryPath: ""})
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NA on missing deps, got %s", r.Verdict)
	}
}

// TestDynamicBufferSearchChecker_MaxFillSizeError: invalid config → Error.
func TestDynamicBufferSearchChecker_MaxFillSizeError(t *testing.T) {
	c := &DynamicBufferSearchChecker{
		MaxFillSize: 0, DefaultBufSize: 64, SentinelMarker: SentinelMarker,
		InvariantID: "INV-X", MechanismLabel: "Mech",
	}
	ctx := &CheckContext{BinaryPath: "/fake", Executor: &MockExecutor{}}
	r := c.Check(ctx)
	if r.Verdict != VerdictError {
		t.Fatalf("expected Error on invalid MaxFillSize, got %s", r.Verdict)
	}
}

// countingExecutor wraps another Executor and counts ExecuteWithArgs calls.
type countingExecutor struct {
	inner  Executor
	probes *int
}

func (c *countingExecutor) ExecuteWithInput(p, s string) (int, string, string, error) {
	return c.inner.ExecuteWithInput(p, s)
}
func (c *countingExecutor) ExecuteWithArgs(p string, args ...string) (int, string, string, error) {
	*c.probes++
	return c.inner.ExecuteWithArgs(p, args...)
}
