package executor

import (
	"testing"

	"defuzz/internal/exec"
	"defuzz/internal/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCompiler implements the compiler.Compiler interface for testing
type mockCompiler struct {
	shouldFail bool
}

func (m *mockCompiler) Compile(s *seed.Seed, commandPath string) (string, error) {
	if m.shouldFail {
		return "", assert.AnError
	}
	return "/tmp/test_binary", nil
}

// mockExecutor implements the exec.Executor interface for testing
type mockExecutor struct {
	results []*exec.ExecutionResult
	index   int
}

func (m *mockExecutor) Run(command string, args ...string) (*exec.ExecutionResult, error) {
	if m.index >= len(m.results) {
		return &exec.ExecutionResult{Stdout: "default", Stderr: "", ExitCode: 0}, nil
	}
	result := m.results[m.index]
	m.index++
	return result, nil
}

func TestQemuExecutor_Execute(t *testing.T) {
	t.Run("should execute all test cases successfully", func(t *testing.T) {
		compiler := &mockCompiler{shouldFail: false}
		mockExec := &mockExecutor{
			results: []*exec.ExecutionResult{
				{Stdout: "success", Stderr: "", ExitCode: 0},
				{Stdout: "verbose output", Stderr: "", ExitCode: 0},
			},
		}
		executor := NewQemuExecutor(compiler, "/tmp/compile_command.txt", mockExec)

		testCases := []seed.TestCase{
			{RunningCommand: "./prog", ExpectedResult: "success"},
			{RunningCommand: "./prog -v", ExpectedResult: "verbose output"},
		}

		testSeed := &seed.Seed{
			ID:        "test-seed",
			Content:   "int main() { return 0; }",
			TestCases: testCases,
		}

		results, err := executor.Execute(testSeed)
		require.NoError(t, err)
		assert.Len(t, results, 2)

		assert.Equal(t, "success", results[0].Stdout)
		assert.Equal(t, 0, results[0].ExitCode)

		assert.Equal(t, "verbose output", results[1].Stdout)
		assert.Equal(t, 0, results[1].ExitCode)
	})

	t.Run("should return error when compilation fails", func(t *testing.T) {
		compiler := &mockCompiler{shouldFail: true}
		mockExec := &mockExecutor{}
		executor := NewQemuExecutor(compiler, "/tmp/compile_command.txt", mockExec)

		testSeed := &seed.Seed{
			ID:        "test-seed",
			Content:   "invalid C code",
			TestCases: []seed.TestCase{{RunningCommand: "./prog", ExpectedResult: "success"}},
		}

		_, err := executor.Execute(testSeed)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to compile seed")
	})
}
