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
	// ReturnSentinel controls whether the sentinel marker is returned in stdout
	// True = seed() returned normally (true bypass), False = crashed inside seed() (false positive)
	ReturnSentinel bool
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
		stdout = ""
		if m.ReturnSentinel {
			stdout = SentinelMarker + "\n"
		}
		return m.SecondCrashExitCode, stdout, "", nil
	}

	// Check first threshold
	if m.CrashThreshold > 0 && inputLen >= m.CrashThreshold {
		stdout = ""
		if m.ReturnSentinel {
			stdout = SentinelMarker + "\n"
		}
		return m.CrashExitCode, stdout, "", nil
	}

	// No crash - always return sentinel (seed completed successfully)
	return 0, SentinelMarker + "\n", "", nil
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
	// Scenario: Program crashes with SIGSEGV (ret modified) AND sentinel present - this is a BUG!
	orc := &CanaryOracle{
		MaxBufferSize:  200,
		DefaultBufSize: 64,
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 100,
			CrashExitCode:  ExitCodeSIGSEGV, // 139
			ReturnSentinel: true,            // seed() returned before crash
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
			ReturnSentinel:       true,            // seed() returned before crash (true bypass)
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
			ReturnSentinel: true,
		},
	}

	minCrash, exitCode, hasSentinel := orc.binarySearchCrash(ctx)

	if minCrash != exactThreshold {
		t.Errorf("expected crash at %d, got %d", exactThreshold, minCrash)
	}
	if exitCode != ExitCodeSIGSEGV {
		t.Errorf("expected exit code %d, got %d", ExitCodeSIGSEGV, exitCode)
	}
	if !hasSentinel {
		t.Error("expected hasSentinel to be true")
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

func TestCanaryOracle_FalsePositive_NoSentinel(t *testing.T) {
	// Scenario: SIGSEGV without sentinel - crash happened inside seed()
	// This is a false positive (indirect crash due to corrupted local variables)
	// The oracle should NOT report this as a bug
	orc := &CanaryOracle{
		MaxBufferSize:  200,
		DefaultBufSize: 64,
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 74, // Small overflow that corrupts local vars
			CrashExitCode:  ExitCodeSIGSEGV,
			ReturnSentinel: false, // seed() did NOT return - crashed inside
		},
	}

	s := &seed.Seed{}
	bug, err := orc.Analyze(s, ctx, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug != nil {
		t.Errorf("expected no bug (false positive), got: %s", bug.Description)
	}
}

func TestCanaryOracle_TrueBypass_WithSentinel(t *testing.T) {
	// Scenario: SIGSEGV with sentinel - seed() returned then crashed
	// This is a true canary bypass - the oracle SHOULD report this as a bug
	orc := &CanaryOracle{
		MaxBufferSize:  200,
		DefaultBufSize: 64,
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 100,
			CrashExitCode:  ExitCodeSIGSEGV,
			ReturnSentinel: true, // seed() returned normally before crash
		},
	}

	s := &seed.Seed{}
	bug, err := orc.Analyze(s, ctx, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug == nil {
		t.Fatal("expected bug (true bypass with sentinel), got nil")
	}
	t.Logf("True bypass detected: %s", bug.Description)
}

func TestCanaryOracle_SIGBUS_NoSentinel_FalsePositive(t *testing.T) {
	// Scenario: SIGBUS without sentinel should also be treated as false positive
	orc := &CanaryOracle{
		MaxBufferSize:  200,
		DefaultBufSize: 64,
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 80,
			CrashExitCode:  ExitCodeSIGBUS,
			ReturnSentinel: false,
		},
	}

	s := &seed.Seed{}
	bug, err := orc.Analyze(s, ctx, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug != nil {
		t.Errorf("expected no bug (SIGBUS without sentinel), got: %s", bug.Description)
	}
}

// ============================================================================
// Negative CFlags Tests (for seeds that disable canary protection)
// ============================================================================

func TestCanaryOracle_NegativeCase_SIGSEGV_NoBug(t *testing.T) {
	// Scenario: Seed has -fno-stack-protector, SIGSEGV is expected (not a bug)
	orc := &CanaryOracle{
		MaxBufferSize:  200,
		DefaultBufSize: 64,
		NegativeCFlags: []string{"-fno-stack-protector"},
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 100,
			CrashExitCode:  ExitCodeSIGSEGV,
			ReturnSentinel: true,
		},
	}

	// Seed with -fno-stack-protector (negative case)
	s := &seed.Seed{
		CFlags: []string{"-fno-stack-protector"},
	}
	bug, err := orc.Analyze(s, ctx, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug != nil {
		t.Errorf("expected no bug for negative case with SIGSEGV, got: %s", bug.Description)
	}
}

func TestCanaryOracle_NegativeCase_SIGBUS_NoBug(t *testing.T) {
	// Scenario: Seed has -fno-stack-protector, SIGBUS is expected (not a bug)
	orc := &CanaryOracle{
		MaxBufferSize:  200,
		DefaultBufSize: 64,
		NegativeCFlags: []string{"-fno-stack-protector"},
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 100,
			CrashExitCode:  ExitCodeSIGBUS,
			ReturnSentinel: true,
		},
	}

	s := &seed.Seed{
		CFlags: []string{"-fno-stack-protector"},
	}
	bug, err := orc.Analyze(s, ctx, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug != nil {
		t.Errorf("expected no bug for negative case with SIGBUS, got: %s", bug.Description)
	}
}

func TestCanaryOracle_NegativeCase_SIGABRT_NoBug(t *testing.T) {
	// Scenario: Seed has -fno-stack-protector but still got SIGABRT
	// This is unexpected but we don't report it as a security bug
	orc := &CanaryOracle{
		MaxBufferSize:  200,
		DefaultBufSize: 64,
		NegativeCFlags: []string{"-fno-stack-protector"},
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 100,
			CrashExitCode:  ExitCodeSIGABRT,
			ReturnSentinel: true,
		},
	}

	s := &seed.Seed{
		CFlags: []string{"-fno-stack-protector"},
	}
	bug, err := orc.Analyze(s, ctx, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug != nil {
		t.Errorf("expected no bug for negative case with SIGABRT, got: %s", bug.Description)
	}
}

func TestCanaryOracle_PositiveCase_StillReportsBug(t *testing.T) {
	// Scenario: Seed without negative CFlags should still report SIGSEGV as bug
	orc := &CanaryOracle{
		MaxBufferSize:  200,
		DefaultBufSize: 64,
		NegativeCFlags: []string{"-fno-stack-protector"},
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/fake/binary",
		Executor: &MockExecutor{
			CrashThreshold: 100,
			CrashExitCode:  ExitCodeSIGSEGV,
			ReturnSentinel: true,
		},
	}

	// Seed without -fno-stack-protector (positive case)
	s := &seed.Seed{
		CFlags: []string{"-fstack-protector-strong"},
	}
	bug, err := orc.Analyze(s, ctx, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug == nil {
		t.Fatal("expected bug for positive case with SIGSEGV, got nil")
	}
	t.Logf("Bug correctly detected: %s", bug.Description)
}

func TestCanaryOracle_isNegativeCase(t *testing.T) {
	orc := &CanaryOracle{
		NegativeCFlags: []string{"-fno-stack-protector", "-fno-stack-protector-all"},
	}

	tests := []struct {
		name     string
		seed     *seed.Seed
		expected bool
	}{
		{
			name:     "nil seed",
			seed:     nil,
			expected: false,
		},
		{
			name:     "empty CFlags",
			seed:     &seed.Seed{CFlags: []string{}},
			expected: false,
		},
		{
			name:     "positive case",
			seed:     &seed.Seed{CFlags: []string{"-fstack-protector-strong"}},
			expected: false,
		},
		{
			name:     "negative case exact match",
			seed:     &seed.Seed{CFlags: []string{"-fno-stack-protector"}},
			expected: true,
		},
		{
			name:     "negative case with other flags",
			seed:     &seed.Seed{CFlags: []string{"-O2", "-fno-stack-protector", "-Wall"}},
			expected: true,
		},
		{
			name:     "multiple negative flags",
			seed:     &seed.Seed{CFlags: []string{"-fno-stack-protector-all"}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := orc.isNegativeCase(tt.seed)
			if result != tt.expected {
				t.Errorf("isNegativeCase() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestCanaryOracle_NegativeCFlagsFromOptions(t *testing.T) {
	// Test parsing negative_cflags from options map
	options := map[string]interface{}{
		"max_buffer_size": 1024,
		"negative_cflags": []interface{}{"-fno-stack-protector", "-fno-stack-protector-all"},
	}

	orc, err := NewCanaryOracle(options, nil, nil, "")
	if err != nil {
		t.Fatalf("Failed to create canary oracle: %v", err)
	}

	canaryOrc, ok := orc.(*CanaryOracle)
	if !ok {
		t.Fatal("Expected *CanaryOracle type")
	}

	if len(canaryOrc.NegativeCFlags) != 2 {
		t.Errorf("expected 2 negative CFlags, got %d", len(canaryOrc.NegativeCFlags))
	}

	expectedFlags := []string{"-fno-stack-protector", "-fno-stack-protector-all"}
	for i, flag := range expectedFlags {
		if canaryOrc.NegativeCFlags[i] != flag {
			t.Errorf("expected NegativeCFlags[%d] = %q, got %q", i, flag, canaryOrc.NegativeCFlags[i])
		}
	}
}
