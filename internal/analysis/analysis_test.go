package analysis

import (
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLLM implements a simple LLM for testing
type mockLLM struct {
	response   string
	shouldFail bool
}

func (m *mockLLM) GetCompletion(prompt string) (string, error) { return m.response, nil }
func (m *mockLLM) GetCompletionWithSystem(system, prompt string) (string, error) {
	return m.response, nil
}
func (m *mockLLM) Understand(prompt string) (string, error)                  { return m.response, nil }
func (m *mockLLM) Generate(understanding, prompt string) (*seed.Seed, error) { return nil, nil }
func (m *mockLLM) Mutate(understanding, prompt string, s *seed.Seed) (*seed.Seed, error) {
	return nil, nil
}
func (m *mockLLM) Analyze(understanding, prompt string, s *seed.Seed, feedback string) (string, error) {
	if m.shouldFail {
		return "", assert.AnError
	}
	return m.response, nil
}

func TestLLMAnalyzer_AnalyzeResult(t *testing.T) {
	analyzer := NewLLMAnalyzer()

	t.Run("should return nil when no anomalies detected", func(t *testing.T) {
		testCases := []seed.TestCase{
			{RunningCommand: "./prog", ExpectedResult: "success"},
		}
		testSeed := &seed.Seed{
			ID:        "test-seed",
			Content:   "int main() { return 0; }",
			TestCases: testCases,
		}

		results := []executor.ExecutionResult{
			{Stdout: "success", Stderr: "", ExitCode: 0},
		}

		mockLLM := &mockLLM{response: "no issues"}
		builder := prompt.NewBuilder()

		bug, err := analyzer.AnalyzeResult(testSeed, results, mockLLM, builder, "context")
		require.NoError(t, err)
		assert.Nil(t, bug)
	})

	t.Run("should detect crash and return bug", func(t *testing.T) {
		testCases := []seed.TestCase{
			{RunningCommand: "./prog", ExpectedResult: "success"},
		}
		testSeed := &seed.Seed{
			ID:        "test-seed",
			Content:   "int main() { return 0; }",
			TestCases: testCases,
		}

		results := []executor.ExecutionResult{
			{Stdout: "", Stderr: "segmentation fault", ExitCode: 139},
		}

		mockLLM := &mockLLM{response: "crash detected"}
		builder := prompt.NewBuilder()

		bug, err := analyzer.AnalyzeResult(testSeed, results, mockLLM, builder, "context")
		require.NoError(t, err)
		require.NotNil(t, bug)
		assert.Equal(t, testSeed, bug.Seed)
		assert.Equal(t, results, bug.Results)
		assert.Equal(t, "crash detected", bug.Description)
	})

	t.Run("should detect unexpected exit code", func(t *testing.T) {
		testCases := []seed.TestCase{
			{RunningCommand: "./prog", ExpectedResult: "success"},
		}
		testSeed := &seed.Seed{
			ID:        "test-seed",
			Content:   "int main() { return 1; }",
			TestCases: testCases,
		}

		results := []executor.ExecutionResult{
			{Stdout: "failed", Stderr: "", ExitCode: 1},
		}

		mockLLM := &mockLLM{response: "Exit code indicates error"}
		builder := prompt.NewBuilder()

		bug, err := analyzer.AnalyzeResult(testSeed, results, mockLLM, builder, "context")
		require.NoError(t, err)
		require.NotNil(t, bug)
		assert.Contains(t, bug.Description, "Exit code indicates error")
	})

	t.Run("should handle LLM analysis failure gracefully", func(t *testing.T) {
		testCases := []seed.TestCase{
			{RunningCommand: "./prog", ExpectedResult: "success"},
		}
		testSeed := &seed.Seed{
			ID:        "test-seed",
			Content:   "int main() { return 1; }",
			TestCases: testCases,
		}

		results := []executor.ExecutionResult{
			{Stdout: "failed", Stderr: "", ExitCode: 1},
		}

		mockLLM := &mockLLM{shouldFail: true}
		builder := prompt.NewBuilder()

		bug, err := analyzer.AnalyzeResult(testSeed, results, mockLLM, builder, "context")
		require.NoError(t, err)
		require.NotNil(t, bug)
		assert.Contains(t, bug.Description, "Execution anomalies detected")
	})
}

func TestContainsCrashIndicators(t *testing.T) {
	testCases := []struct {
		name     string
		output   string
		expected bool
	}{
		{"segmentation fault", "Process terminated with segmentation fault", true},
		{"segfault", "Segfault occurred at address 0x123", true},
		{"buffer overflow", "Buffer overflow detected", true},
		{"normal output", "Program executed successfully", false},
		{"case insensitive", "SEGMENTATION FAULT", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := containsCrashIndicators(tc.output)
			assert.Equal(t, tc.expected, result)
		})
	}
}
