package fuzz

import (
	"fmt"
	"os"
	"testing"

	"defuzz/internal/analysis"
	"defuzz/internal/config"
	"defuzz/internal/llm"
	"defuzz/internal/prompt"
	"defuzz/internal/seed"
	"defuzz/internal/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock implementations for all dependencies

type mockLLM struct {
	shouldErrorOnGenerate bool
	shouldErrorOnMutate   bool
}

func (m *mockLLM) Understand(prompt string) (string, error) {
	return "mock understanding", nil
}
func (m *mockLLM) Generate(ctx, seedType string) (*seed.Seed, error) {
	if m.shouldErrorOnGenerate {
		return nil, fmt.Errorf("llm generate error")
	}
	return &seed.Seed{Type: seedType, Content: "generated content", Makefile: "generated makefile"}, nil
}
func (m *mockLLM) Mutate(ctx string, s *seed.Seed) (*seed.Seed, error) {
	if m.shouldErrorOnMutate {
		return nil, fmt.Errorf("llm mutate error")
	}
	return &seed.Seed{ID: s.ID + "-mutated", Type: s.Type, Content: "mutated content"}, nil
}
func (m *mockLLM) Analyze(ctx string, s *seed.Seed, feedback string) (string, error) {
	return "", nil // Not used directly by fuzzer
}

type mockCompiler struct {
	shouldFailCompile bool
}

func (m *mockCompiler) Compile(s *seed.Seed, v vm.VM) (*vm.ExecutionResult, error) {
	if m.shouldFailCompile {
		return &vm.ExecutionResult{ExitCode: 1}, nil
	}
	return &vm.ExecutionResult{ExitCode: 0}, nil
}

type mockVM struct {
	shouldErrorOnRun bool
}

func (m *mockVM) Create() error { return nil }
func (m *mockVM) Run(command ...string) (*vm.ExecutionResult, error) {
	if m.shouldErrorOnRun {
		return nil, fmt.Errorf("vm run error")
	}
	return &vm.ExecutionResult{ExitCode: 0, Stdout: "vm output"}, nil
}
func (m *mockVM) Stop() error { return nil }

type mockAnalyzer struct {
	shouldFindBug bool
}

func (m *mockAnalyzer) AnalyzeResult(s *seed.Seed, result *vm.ExecutionResult, l llm.LLM, pb *prompt.Builder, ctx string) (*analysis.Bug, error) {
	if m.shouldFindBug {
		return &analysis.Bug{Seed: s}, nil
	}
	return nil, nil
}

type mockReporter struct {
	saveCalled bool
}

func (m *mockReporter) Save(bug *analysis.Bug) error {
	m.saveCalled = true
	return nil
}

// setupFuzzerWithTempDir creates a fuzzer and a temporary directory for its output.
func setupFuzzerWithTempDir(t *testing.T) (*Fuzzer, func()) {
	tempDir, err := os.MkdirTemp("", "fuzzer_test_")
	require.NoError(t, err)

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(tempDir)
	require.NoError(t, err)

	cfg := &config.Config{
		Fuzzer: config.FuzzerConfig{
			ISA:          "test_isa",
			Strategy:     "test_strategy",
			InitialSeeds: 1,
			BugQuota:     1,
		},
	}
	fuzzer := NewFuzzer(
		cfg,
		prompt.NewBuilder(),
		&mockLLM{},
		seed.NewInMemoryPool(),
		&mockCompiler{},
		&mockVM{},
		&mockAnalyzer{},
		&mockReporter{},
	)

	cleanup := func() {
		os.Chdir(oldWd)
		os.RemoveAll(tempDir)
	}

	return fuzzer, cleanup
}

func TestFuzzer_Generate(t *testing.T) {
	f, cleanup := setupFuzzerWithTempDir(t)
	defer cleanup()

	err := f.Generate()
	require.NoError(t, err)

	assert.FileExists(t, "initial_seeds/test_isa/test_strategy/understanding.md")
	assert.DirExists(t, "initial_seeds/test_isa/test_strategy/001_c")
}

func TestFuzzer_Fuzz(t *testing.T) {
	t.Run("should run and find a bug", func(t *testing.T) {
		f, cleanup := setupFuzzerWithTempDir(t)
		defer cleanup()

		require.NoError(t, f.Generate())

		f.analyzer = &mockAnalyzer{shouldFindBug: true}
		reporter := &mockReporter{}
		f.reporter = reporter

		err := f.Fuzz()
		require.NoError(t, err)

		assert.True(t, reporter.saveCalled)
		assert.Equal(t, 1, f.bugCount)
		assert.Equal(t, 1, f.seedPool.Len())
	})

	t.Run("should run and handle compilation failure", func(t *testing.T) {
		f, cleanup := setupFuzzerWithTempDir(t)
		defer cleanup()
		require.NoError(t, f.Generate())

		f.compiler = &mockCompiler{shouldFailCompile: true}
		reporter := &mockReporter{}
		f.reporter = reporter

		err := f.Fuzz()
		require.NoError(t, err)

		assert.False(t, reporter.saveCalled)
		assert.Equal(t, 0, f.bugCount)
		assert.Equal(t, 0, f.seedPool.Len())
	})
}
