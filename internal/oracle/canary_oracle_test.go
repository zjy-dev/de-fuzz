package oracle

import (
	"strconv"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// MockExecutor simulates binary execution for testing.
type MockExecutor struct {
	// CrashThreshold is the input length at which the "binary" starts crashing
	CrashThreshold int
	// CrashExitCode is the exit code returned when crashing
	CrashExitCode int
	// SecondCrashThreshold is an optional second threshold for SIGABRT
	// (simulating canary -> ret -> buf layout where SIGSEGV comes before SIGABRT)
	SecondCrashThreshold int
	// SecondCrashExitCode is the exit code for the second crash (e.g., SIGABRT)
	SecondCrashExitCode int
}

func (m *MockExecutor) ExecuteWithInput(binaryPath string, stdin string) (exitCode int, stdout string, stderr string, err error) {
	inputLen := len(stdin)
	return m.checkCrash(inputLen)
}

func (m *MockExecutor) ExecuteWithArgs(binaryPath string, args ...string) (exitCode int, stdout string, stderr string, err error) {
	// Parse fill_size (second argument) as the value to test
	// buf_size (first argument) is ignored in mock since it's fixed
	inputLen := 0
	if len(args) >= 2 {
		// Use fill_size (second arg)
		inputLen, _ = strconv.Atoi(args[1])
	} else if len(args) == 1 {
		// Backward compatibility: single arg test
		inputLen, _ = strconv.Atoi(args[0])
	}
	return m.checkCrash(inputLen)
}

func (m *MockExecutor) checkCrash(inputLen int) (exitCode int, stdout string, stderr string, err error) {
	// Check second threshold first (if set) - this simulates the canary -> ret -> buf case
	if m.SecondCrashThreshold > 0 && inputLen >= m.SecondCrashThreshold {
		return m.SecondCrashExitCode, "", "", nil
	}

	// Check first threshold
	if m.CrashThreshold > 0 && inputLen >= m.CrashThreshold {
		return m.CrashExitCode, "", "", nil
	}

	return 0, "", "", nil
}

func TestCanaryOracle_NoCrash(t *testing.T) {
	// Scenario: Program never crashes - canary protection working or no buffer overflow
	orc := &CanaryOracle{
		MaxBufferSize:  100,
		DefaultBufSize: 64,
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 0, // Never crash
		},
	}

	s := &seed.Seed{}
	bug, err := orc.Analyze(s, ctx, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug != nil {
		t.Errorf("expected no bug, got: %s", bug.Description)
	}
}

func TestCanaryOracle_SafeWithSIGABRT(t *testing.T) {
	// Scenario: Program crashes with SIGABRT (canary check) - this is SAFE
	orc := &CanaryOracle{
		MaxBufferSize:  200,
		DefaultBufSize: 64,
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 100,
			CrashExitCode:  ExitCodeSIGABRT, // 134
		},
	}

	s := &seed.Seed{}
	bug, err := orc.Analyze(s, ctx, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug != nil {
		t.Errorf("expected no bug (SIGABRT is safe), got: %s", bug.Description)
	}
}

func TestCanaryOracle_BugWithSIGSEGV(t *testing.T) {
	// Scenario: Program crashes with SIGSEGV (ret modified) - this is a BUG!
	orc := &CanaryOracle{
		MaxBufferSize:  200,
		DefaultBufSize: 64,
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 100,
			CrashExitCode:  ExitCodeSIGSEGV, // 139
		},
	}

	s := &seed.Seed{}
	bug, err := orc.Analyze(s, ctx, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug == nil {
		t.Fatal("expected bug, got nil")
	}
	if bug.Description == "" {
		t.Error("expected bug description, got empty string")
	}
	t.Logf("Bug description: %s", bug.Description)
}

func TestCanaryOracle_CVE2023_4039_Pattern(t *testing.T) {
	// Scenario: canary -> ret -> buf layout (CVE-2023-4039)
	// As buffer grows: normal -> SIGSEGV (ret modified) -> SIGABRT (canary check)
	// The oracle should detect SIGSEGV at the smaller threshold
	orc := &CanaryOracle{
		MaxBufferSize:  200,
		DefaultBufSize: 64,
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold:       50,              // First crash at 50 bytes (SIGSEGV)
			CrashExitCode:        ExitCodeSIGSEGV, // 139
			SecondCrashThreshold: 100,             // Second crash at 100 bytes (SIGABRT)
			SecondCrashExitCode:  ExitCodeSIGABRT, // 134
		},
	}

	s := &seed.Seed{}
	bug, err := orc.Analyze(s, ctx, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug == nil {
		t.Fatal("expected bug (SIGSEGV before SIGABRT), got nil")
	}
	t.Logf("Bug description: %s", bug.Description)
}

func TestCanaryOracle_BinarySearchAccuracy(t *testing.T) {
	// Test that binary search finds the exact crash threshold
	orc := &CanaryOracle{
		MaxBufferSize:  1000,
		DefaultBufSize: 64,
	}

	exactThreshold := 337 // Arbitrary threshold

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: exactThreshold,
			CrashExitCode:  ExitCodeSIGSEGV,
		},
	}

	minCrash, exitCode := orc.binarySearchCrash(ctx)

	if minCrash != exactThreshold {
		t.Errorf("expected crash at %d, got %d", exactThreshold, minCrash)
	}
	if exitCode != ExitCodeSIGSEGV {
		t.Errorf("expected exit code %d, got %d", ExitCodeSIGSEGV, exitCode)
	}
}

func TestCanaryOracle_MissingContext(t *testing.T) {
	orc := &CanaryOracle{
		MaxBufferSize:  100,
		DefaultBufSize: 64,
	}
	s := &seed.Seed{}

	// Test with nil context
	_, err := orc.Analyze(s, nil, nil)
	if err == nil {
		t.Error("expected error with nil context")
	}

	// Test with missing executor
	ctx := &AnalyzeContext{BinaryPath: "/fake/binary"}
	_, err = orc.Analyze(s, ctx, nil)
	if err == nil {
		t.Error("expected error with missing executor")
	}

	// Test with missing binary path
	ctx = &AnalyzeContext{Executor: &MockExecutor{}}
	_, err = orc.Analyze(s, ctx, nil)
	if err == nil {
		t.Error("expected error with missing binary path")
	}
}

func TestCanaryOracle_Registration(t *testing.T) {
	// Test that canary oracle is registered
	orc, err := New("canary", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("Failed to create canary oracle: %v", err)
	}
	if orc == nil {
		t.Error("Canary oracle should not be nil")
	}
}

func TestCanaryOracle_CustomMaxBufferSize(t *testing.T) {
	options := map[string]interface{}{
		"max_buffer_size": 8192,
	}

	orc, err := NewCanaryOracle(options, nil, nil, "")
	if err != nil {
		t.Fatalf("Failed to create canary oracle: %v", err)
	}

	canaryOrc, ok := orc.(*CanaryOracle)
	if !ok {
		t.Fatal("Expected *CanaryOracle type")
	}

	if canaryOrc.MaxBufferSize != 8192 {
		t.Errorf("expected MaxBufferSize 8192, got %d", canaryOrc.MaxBufferSize)
	}
}
