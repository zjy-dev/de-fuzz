//go:build integration

package oracle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// TestIsCrashExit_Integration tests crash exit code detection.
func TestIsCrashExit_Integration(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int
		expected bool
	}{
		{"Normal exit 0", 0, false},
		{"Normal exit 1", 1, false},
		{"SIGILL (128+4)", 132, true},
		{"SIGABRT (128+6)", 134, true},
		{"SIGBUS (128+7)", 135, true},
		{"SIGFPE (128+8)", 136, true},
		{"SIGSEGV (128+11)", 139, true},
		{"SIGTERM (128+15)", 143, false},
		{"SIGKILL (128+9)", 137, false},
		{"Random value 255", 255, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCrashExit(tt.exitCode)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestContainsCrashIndicators_Integration tests crash indicator detection.
func TestContainsCrashIndicators_Integration(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected bool
	}{
		{"Empty output", "", false},
		{"Normal output", "Hello, World!", false},
		{"Segmentation fault", "Segmentation fault (core dumped)", true},
		{"SEGFAULT lowercase", "segfault occurred", true},
		{"Stack smashing", "*** stack smashing detected ***: terminated", true},
		{"Buffer overflow", "buffer overflow detected in function X", true},
		{"Double free", "Error: double free detected", true},
		{"Use after free", "use after free error", true},
		{"Heap corruption", "heap corruption detected", true},
		{"Assertion failed", "Assertion failed: x > 0", true},
		{"Bus error", "Bus error: invalid address", true},
		{"Core dumped", "core dumped", true},
		{"Stack overflow", "stack overflow in recursive call", true},
		{"Abort called", "called abort()", true},
		{"Invalid memory", "invalid memory access", true},
		{"Normal stderr", "This is a normal error message", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsCrashIndicators(tt.output)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsExpectedError_Integration tests expected error detection.
func TestIsExpectedError_Integration(t *testing.T) {
	tests := []struct {
		name     string
		stderr   string
		expected bool
	}{
		{"Empty stderr", "", false},
		{"Compiler warning", "warning: unused variable 'x'", true},
		{"Compiler note", "note: declared here", true},
		{"Real error", "error: undefined reference to 'foo'", false},
		{"Segfault message", "Segmentation fault", false},
		{"Mixed with warning", "warning: something\nerror: something else", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isExpectedError(tt.stderr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestLLMOracle_Integration_WithRealLLM tests oracle with real LLM.
// This test requires LLM API credentials.
// Skipped by default as it requires external API access.
func TestLLMOracle_Integration_WithRealLLM(t *testing.T) {
	// Skip this test as it requires real LLM API credentials
	// Enable manually when testing LLM integration
	t.Skip("Skipping LLM integration test: requires DEEPSEEK_API_KEY")
}

// TestLLMOracle_Integration_NoResults tests oracle with empty results.
func TestLLMOracle_Integration_NoResults(t *testing.T) {
	oracle := &LLMOracle{}

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 1},
		Content: `int main() { return 0; }`,
	}

	bug, err := oracle.Analyze(testSeed, []Result{})
	assert.Error(t, err)
	assert.Nil(t, bug)
	assert.Contains(t, err.Error(), "no execution results")
}

// TestLLMOracle_Integration_MultipleTestCases tests oracle with multiple test case results.
// Skipped by default as it requires external API access.
func TestLLMOracle_Integration_MultipleTestCases(t *testing.T) {
	t.Skip("Skipping LLM integration test: requires DEEPSEEK_API_KEY")
}

// TestLLMOracle_Integration_NonZeroExitOnly tests detection of non-zero exit without crash.
// Skipped by default as it requires external API access.
func TestLLMOracle_Integration_NonZeroExitOnly(t *testing.T) {
	t.Skip("Skipping LLM integration test: requires DEEPSEEK_API_KEY")
}

// TestLLMOracle_Integration_StderrOnly tests detection based on stderr content.
// Skipped by default as it requires external API access.
func TestLLMOracle_Integration_StderrOnly(t *testing.T) {
	t.Skip("Skipping LLM integration test: requires DEEPSEEK_API_KEY")
}

// TestBugStructure_Integration tests Bug struct creation and access.
func TestBugStructure_Integration(t *testing.T) {
	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 42},
		Content: `int main() { return 0; }`,
	}

	results := []Result{
		{Stdout: "output", Stderr: "error", ExitCode: 1},
	}

	bug := &Bug{
		Seed:        testSeed,
		Results:     results,
		Description: "Test bug description",
	}

	assert.Equal(t, uint64(42), bug.Seed.Meta.ID)
	assert.Equal(t, 1, len(bug.Results))
	assert.Equal(t, "output", bug.Results[0].Stdout)
	assert.Equal(t, "error", bug.Results[0].Stderr)
	assert.Equal(t, 1, bug.Results[0].ExitCode)
	assert.Equal(t, "Test bug description", bug.Description)
}
