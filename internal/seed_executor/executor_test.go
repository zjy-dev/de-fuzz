package executor

import (
	"testing"

	"defuzz/internal/seed"
	"defuzz/internal/vm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCompiler implements the compiler.Compiler interface for testing
type mockCompiler struct {
	shouldFail bool
}

func (m *mockCompiler) Compile(s *seed.Seed) (string, error) {
	if m.shouldFail {
		return "", assert.AnError
	}
	return "/tmp/test_binary", nil
}

// mockVM implements the vm.VM interface for testing
type mockVM struct {
	results []*vm.ExecutionResult
	index   int
}

func (m *mockVM) Create() error { return nil }
func (m *mockVM) Run(binaryPath, runScriptPath string) (*vm.ExecutionResult, error) {
	if m.index >= len(m.results) {
		return &vm.ExecutionResult{Stdout: "default", Stderr: "", ExitCode: 0}, nil
	}
	result := m.results[m.index]
	m.index++
	return result, nil
}
func (m *mockVM) Stop() error { return nil }

func TestQemuExecutor_Execute(t *testing.T) {
	t.Run("should execute all test cases successfully", func(t *testing.T) {
		compiler := &mockCompiler{shouldFail: false}
		executor := NewQemuExecutor(compiler)

		testCases := []seed.TestCase{
			{RunningCommand: "./prog", ExpectedResult: "success"},
			{RunningCommand: "./prog -v", ExpectedResult: "verbose output"},
		}

		testSeed := &seed.Seed{
			ID:        "test-seed",
			Content:   "int main() { return 0; }",
			Makefile:  "all:\n\tgcc source.c -o prog",
			TestCases: testCases,
		}

		mockVMResults := []*vm.ExecutionResult{
			{Stdout: "success", Stderr: "", ExitCode: 0},
			{Stdout: "verbose output", Stderr: "", ExitCode: 0},
		}
		mockVM := &mockVM{results: mockVMResults}

		results, err := executor.Execute(testSeed, mockVM)
		require.NoError(t, err)
		assert.Len(t, results, 2)

		assert.Equal(t, "success", results[0].Stdout)
		assert.Equal(t, 0, results[0].ExitCode)

		assert.Equal(t, "verbose output", results[1].Stdout)
		assert.Equal(t, 0, results[1].ExitCode)
	})

	t.Run("should return error when compilation fails", func(t *testing.T) {
		compiler := &mockCompiler{shouldFail: true}
		executor := NewQemuExecutor(compiler)

		testSeed := &seed.Seed{
			ID:        "test-seed",
			Content:   "invalid C code",
			TestCases: []seed.TestCase{{RunningCommand: "./prog", ExpectedResult: "success"}},
		}

		mockVM := &mockVM{}

		_, err := executor.Execute(testSeed, mockVM)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to compile seed")
	})
}
