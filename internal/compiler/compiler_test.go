package compiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/exec"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// MockExecutor is a mock implementation of exec.Executor for testing.
type MockExecutor struct {
	RunFunc func(command string, args ...string) (*exec.ExecutionResult, error)
}

func (m *MockExecutor) Run(command string, args ...string) (*exec.ExecutionResult, error) {
	if m.RunFunc != nil {
		return m.RunFunc(command, args...)
	}
	return &exec.ExecutionResult{ExitCode: 0}, nil
}

func TestNewGCCCompiler(t *testing.T) {
	cfg := GCCCompilerConfig{
		GCCPath:    "/usr/bin/gcc",
		WorkDir:    "/tmp/test",
		PrefixPath: "/usr/lib/gcc",
		CFlags:     []string{"-Wall", "-O2"},
	}

	compiler := NewGCCCompiler(cfg)

	assert.NotNil(t, compiler)
	assert.Equal(t, "/usr/bin/gcc", compiler.gccPath)
	assert.Equal(t, "/tmp/test", compiler.workDir)
	assert.Equal(t, "/usr/lib/gcc", compiler.prefixPath)
	assert.Equal(t, []string{"-Wall", "-O2"}, compiler.cflags)
}

func TestGCCCompiler_GetWorkDir(t *testing.T) {
	cfg := GCCCompilerConfig{
		WorkDir: "/custom/work/dir",
	}
	compiler := NewGCCCompiler(cfg)

	assert.Equal(t, "/custom/work/dir", compiler.GetWorkDir())
}

func TestGCCCompiler_Compile_Success(t *testing.T) {
	workDir, err := os.MkdirTemp("", "compiler_test_")
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	cfg := GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: workDir,
	}
	compiler := NewGCCCompiler(cfg)

	// Replace executor with mock
	compiler.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			assert.Equal(t, "gcc", command)
			return &exec.ExecutionResult{
				ExitCode: 0,
				Stdout:   "",
				Stderr:   "",
			}, nil
		},
	}

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 1},
		Content: "int main() { return 0; }",
	}

	result, err := compiler.Compile(testSeed)

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Contains(t, result.BinaryPath, "seed_1")
}

func TestGCCCompiler_Compile_RecordsCommandMetadata(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "build dir")
	require.NoError(t, os.MkdirAll(workDir, 0755))

	cfg := GCCCompilerConfig{
		GCCPath:    "/usr/bin/gcc",
		WorkDir:    workDir,
		PrefixPath: "/opt/tool chain/libexec",
		CFlags:     []string{"-Wall", "-O0"},
	}
	compiler := NewGCCCompiler(cfg)

	var capturedCommand string
	var capturedArgs []string
	compiler.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			capturedCommand = command
			capturedArgs = append([]string(nil), args...)
			return &exec.ExecutionResult{
				ExitCode: 0,
				Stdout:   "ok",
				Stderr:   "warning",
			}, nil
		},
	}

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 9},
		Content: "int main() { return 0; }",
		CFlags:  []string{"-fstack-protector-all"},
	}

	result, err := compiler.Compile(testSeed)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "/usr/bin/gcc", capturedCommand)
	assert.Equal(t, capturedCommand, result.CompilerPath)
	assert.Equal(t, capturedArgs, result.Args)
	assert.Equal(t, []string{"-B/opt/tool chain/libexec"}, result.PrefixFlags)
	assert.Equal(t, []string{"-Wall", "-O0"}, result.ConfigCFlags)
	assert.Equal(t, []string{"-fstack-protector-all"}, result.SeedCFlags)
	assert.Equal(t, []string{"-B/opt/tool chain/libexec", "-Wall", "-O0", "-fstack-protector-all"}, result.EffectiveFlags)
	assert.Contains(t, result.Command, "/usr/bin/gcc")
	assert.Contains(t, result.Command, "'-B/opt/tool chain/libexec'")
	assert.Contains(t, result.Command, "'"+filepath.Join(workDir, "seed_9.c")+"'")
	assert.Equal(t, "-o", result.Args[len(result.Args)-2])
	assert.Equal(t, filepath.Join(workDir, "seed_9"), result.Args[len(result.Args)-1])
}

func TestGCCCompiler_Compile_Failure(t *testing.T) {
	workDir, err := os.MkdirTemp("", "compiler_test_")
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	cfg := GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: workDir,
	}
	compiler := NewGCCCompiler(cfg)

	// Replace executor with mock that simulates compile error
	compiler.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			return &exec.ExecutionResult{
				ExitCode: 1,
				Stdout:   "",
				Stderr:   "error: expected ';' before 'return'",
			}, nil
		},
	}

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 2},
		Content: "int main() { return 0 }", // Missing semicolon
	}

	result, err := compiler.Compile(testSeed)

	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.Stderr, "error")
}

func TestGCCCompiler_SourceFileWritten(t *testing.T) {
	workDir, err := os.MkdirTemp("", "compiler_test_")
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	cfg := GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: workDir,
	}
	compiler := NewGCCCompiler(cfg)

	compiler.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			return &exec.ExecutionResult{ExitCode: 0}, nil
		},
	}

	sourceCode := "int main() { return 42; }"
	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 5},
		Content: sourceCode,
	}

	_, err = compiler.Compile(testSeed)
	require.NoError(t, err)

	// Verify source file was written
	sourceFile := filepath.Join(workDir, "seed_5.c")
	content, err := os.ReadFile(sourceFile)
	require.NoError(t, err)
	assert.Equal(t, sourceCode, string(content))
}

func TestCompileResult_ToCompilationRecord(t *testing.T) {
	result := &CompileResult{
		BinaryPath:     "/tmp/seed_1",
		Success:        true,
		Stdout:         "stdout",
		Stderr:         "stderr",
		Command:        "gcc source.c -o seed_1",
		CompilerPath:   "gcc",
		Args:           []string{"source.c", "-o", "seed_1"},
		PrefixFlags:    []string{"-B/tmp/gcc"},
		ConfigCFlags:   []string{"-Wall"},
		SeedCFlags:     []string{"-O2"},
		EffectiveFlags: []string{"-B/tmp/gcc", "-Wall", "-O2"},
	}

	record := result.ToCompilationRecord(1, "/tmp/corpus/id-000001/source.c")

	require.NotNil(t, record)
	assert.Equal(t, uint64(1), record.SeedID)
	assert.Equal(t, "/tmp/corpus/id-000001/source.c", record.SourcePath)
	assert.Equal(t, result.Command, record.Command)
	assert.Equal(t, result.Args, record.Args)
	assert.Equal(t, result.EffectiveFlags, record.EffectiveFlags)
	assert.False(t, record.RecordedAt.IsZero())
}

func TestNewCrossGCCCompiler(t *testing.T) {
	cfg := CrossGCCCompilerConfig{
		GCCCompilerConfig: GCCCompilerConfig{
			GCCPath: "/usr/bin/aarch64-linux-gnu-gcc",
			WorkDir: "/tmp/cross",
		},
		TargetArch: "aarch64",
		Sysroot:    "/usr/aarch64-linux-gnu",
	}

	compiler := NewCrossGCCCompiler(cfg)

	assert.NotNil(t, compiler)
	assert.Equal(t, "aarch64", compiler.GetTargetArch())
	assert.Equal(t, "/usr/aarch64-linux-gnu", compiler.sysroot)
}
