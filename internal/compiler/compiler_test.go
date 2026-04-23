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
		FlagProfile: &seed.FlagProfile{
			Name: "policy-strong__threshold-8__pic-default__guard-default",
			AxisValues: map[string]string{
				"policy":     "strong",
				"threshold":  "8",
				"pic_mode":   "default",
				"guard_mode": "default",
			},
			Flags: []string{"-fstack-protector-strong", "--param=ssp-buffer-size=8"},
		},
	}

	result, err := compiler.Compile(testSeed)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "/usr/bin/gcc", capturedCommand)
	assert.Equal(t, capturedCommand, result.CompilerPath)
	assert.Equal(t, capturedArgs, result.Args)
	assert.Equal(t, []string{"-B/opt/tool chain/libexec"}, result.PrefixFlags)
	assert.Equal(t, []string{"-Wall", "-O0"}, result.ConfigCFlags)
	assert.Equal(t, "policy-strong__threshold-8__pic-default__guard-default", result.ProfileName)
	assert.Equal(t, []string{"-fstack-protector-strong", "--param=ssp-buffer-size=8"}, result.ProfileFlags)
	assert.Equal(t, []string{"-fstack-protector-all"}, result.SeedCFlags)
	assert.False(t, result.SourceDisablesSSP)
	assert.False(t, result.SourceRequestsSSP)
	assert.False(t, result.UsesAlloca)
	assert.False(t, result.UsesVLA)
	assert.Equal(t, "", result.NegativeReason)
	assert.Empty(t, result.AppliedLLMCFlags)
	assert.Equal(t, []string{"-fstack-protector-all"}, result.DroppedLLMCFlags)
	assert.False(t, result.LLMCFlagsApplied)
	assert.Equal(t, []string{"-B/opt/tool chain/libexec", "-Wall", "-O0", "-fstack-protector-strong", "--param=ssp-buffer-size=8"}, result.EffectiveFlags)
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
		BinaryPath:        "/tmp/seed_1",
		Success:           true,
		Stdout:            "stdout",
		Stderr:            "stderr",
		Command:           "gcc source.c -o seed_1",
		CompilerPath:      "gcc",
		Args:              []string{"source.c", "-o", "seed_1"},
		PrefixFlags:       []string{"-B/tmp/gcc"},
		ConfigCFlags:      []string{"-Wall"},
		ProfileName:       "policy-strong__threshold-8__pic-default__guard-default",
		ProfileFlags:      []string{"-fstack-protector-strong", "--param=ssp-buffer-size=8"},
		ProfileAxes:       map[string]string{"policy": "strong"},
		SourceDisablesSSP: true,
		SourceRequestsSSP: false,
		UsesAlloca:        true,
		UsesVLA:           true,
		NegativeReason:    "source_no_stack_protector_attr",
		SeedCFlags:        []string{"-O2"},
		AppliedLLMCFlags:  []string{"-O2"},
		DroppedLLMCFlags:  []string{"-fno-stack-protector"},
		LLMCFlagsApplied:  false,
		EffectiveFlags:    []string{"-B/tmp/gcc", "-Wall", "-fstack-protector-strong", "--param=ssp-buffer-size=8"},
	}

	record := result.ToCompilationRecord(1, "/tmp/corpus/id-000001/source.c")

	require.NotNil(t, record)
	assert.Equal(t, uint64(1), record.SeedID)
	assert.Equal(t, "/tmp/corpus/id-000001/source.c", record.SourcePath)
	assert.Equal(t, result.Command, record.Command)
	assert.Equal(t, result.Args, record.Args)
	assert.Equal(t, result.EffectiveFlags, record.EffectiveFlags)
	assert.Equal(t, result.ProfileName, record.ProfileName)
	assert.True(t, record.SourceDisablesSSP)
	assert.Equal(t, "source_no_stack_protector_attr", record.NegativeReason)
	assert.False(t, record.LLMCFlagsApplied)
	assert.False(t, record.RecordedAt.IsZero())
}

func TestGCCCompiler_Compile_RecordsSourceDisableEvidence(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "build")
	require.NoError(t, os.MkdirAll(workDir, 0755))

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

	testSeed := &seed.Seed{
		Meta: seed.Metadata{ID: 15},
		Content: `__attribute__((no_stack_protector))
void seed(int buf_size, int fill_size) {
    char vla[buf_size];
    char *p = alloca(buf_size);
    (void)p;
}`,
		FlagProfile: &seed.FlagProfile{
			Name:              "negative-control__fno-stack-protector",
			Flags:             []string{"-fno-stack-protector"},
			IsNegativeControl: true,
		},
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.SourceDisablesSSP)
	assert.True(t, result.UsesAlloca)
	assert.True(t, result.UsesVLA)
	assert.Equal(t, "negative_profile", result.NegativeReason)
}

func TestGCCCompiler_Compile_DisablesLLMFlagsWhenConfigured(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "build")
	require.NoError(t, os.MkdirAll(workDir, 0755))

	cfg := GCCCompilerConfig{
		GCCPath:          "/usr/bin/gcc",
		WorkDir:          workDir,
		CFlags:           []string{"-Wall"},
		DisableLLMCFlags: true,
	}
	compiler := NewGCCCompiler(cfg)

	var capturedArgs []string
	compiler.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			capturedArgs = append([]string(nil), args...)
			return &exec.ExecutionResult{ExitCode: 0}, nil
		},
	}

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 11},
		Content: "int main() { return 0; }",
		CFlags:  []string{"-fno-stack-protector"},
		FlagProfile: &seed.FlagProfile{
			Name:  "policy-strong__threshold-8__pic-default__guard-default",
			Flags: []string{"-fstack-protector-strong", "--param=ssp-buffer-size=8"},
		},
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.LLMCFlagsApplied)
	assert.Empty(t, result.AppliedLLMCFlags)
	assert.Equal(t, []string{"-fno-stack-protector"}, result.DroppedLLMCFlags)
	assert.Equal(t, []string{"-Wall", "-fstack-protector-strong", "--param=ssp-buffer-size=8", filepath.Join(workDir, "seed_11.c"), "-o", filepath.Join(workDir, "seed_11")}, capturedArgs)
	assert.Equal(t, []string{"-Wall", "-fstack-protector-strong", "--param=ssp-buffer-size=8"}, result.EffectiveFlags)
}

func TestGCCCompiler_Compile_FiltersConflictingCanaryLLMFlags(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "build")
	require.NoError(t, os.MkdirAll(workDir, 0755))

	cfg := GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: workDir,
		CFlags:  []string{"-Wall"},
	}
	compiler := NewGCCCompiler(cfg)

	var capturedArgs []string
	compiler.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			capturedArgs = append([]string(nil), args...)
			return &exec.ExecutionResult{ExitCode: 0}, nil
		},
	}

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 13},
		Content: "int main() { return 0; }",
		CFlags:  []string{"-O2", "-fno-stack-protector", "--param=ssp-buffer-size=1", "-mstack-protector-guard=global"},
		FlagProfile: &seed.FlagProfile{
			Name: "policy-strong__threshold-8__pic-default__guard-default",
			AxisValues: map[string]string{
				"policy":     "strong",
				"threshold":  "8",
				"pic_mode":   "default",
				"guard_mode": "default",
			},
			Flags: []string{"-fstack-protector-strong", "--param=ssp-buffer-size=8"},
		},
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.LLMCFlagsApplied)
	assert.Equal(t, []string{"-O2"}, result.AppliedLLMCFlags)
	assert.Equal(t, []string{"-fno-stack-protector", "--param=ssp-buffer-size=1", "-mstack-protector-guard=global"}, result.DroppedLLMCFlags)
	assert.Equal(t, []string{"-Wall", "-fstack-protector-strong", "--param=ssp-buffer-size=8", "-O2", filepath.Join(workDir, "seed_13.c"), "-o", filepath.Join(workDir, "seed_13")}, capturedArgs)
}

func TestGCCCompiler_Compile_FiltersConflictingLoongArchLayoutLLMFlags(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "build")
	require.NoError(t, os.MkdirAll(workDir, 0755))

	cfg := GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: workDir,
		CFlags:  []string{"-Wall"},
	}
	compiler := NewGCCCompiler(cfg)

	var capturedArgs []string
	compiler.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			capturedArgs = append([]string(nil), args...)
			return &exec.ExecutionResult{ExitCode: 0}, nil
		},
	}

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 17},
		Content: "int main() { return 0; }",
		CFlags:  []string{"-O2", "-fpack-struct=1", "-fshort-enums"},
		FlagProfile: &seed.FlagProfile{
			Name: "policy-strong__threshold-8__pic-default__guard-default__layout-default",
			AxisValues: map[string]string{
				"policy":      "strong",
				"threshold":   "8",
				"pic_mode":    "default",
				"guard_mode":  "default",
				"layout_mode": "default",
			},
			Flags: []string{"-fstack-protector-strong", "--param=ssp-buffer-size=8"},
		},
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.LLMCFlagsApplied)
	assert.Equal(t, []string{"-O2"}, result.AppliedLLMCFlags)
	assert.Equal(t, []string{"-fpack-struct=1", "-fshort-enums"}, result.DroppedLLMCFlags)
	assert.Equal(t, []string{"-Wall", "-fstack-protector-strong", "--param=ssp-buffer-size=8", "-O2", filepath.Join(workDir, "seed_17.c"), "-o", filepath.Join(workDir, "seed_17")}, capturedArgs)
}

func TestGCCCompiler_Compile_AllowsLayoutLLMFlagsWhenProfileDoesNotReserveThem(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "build")
	require.NoError(t, os.MkdirAll(workDir, 0755))

	cfg := GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: workDir,
		CFlags:  []string{"-Wall"},
	}
	compiler := NewGCCCompiler(cfg)

	var capturedArgs []string
	compiler.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			capturedArgs = append([]string(nil), args...)
			return &exec.ExecutionResult{ExitCode: 0}, nil
		},
	}

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 18},
		Content: "int main() { return 0; }",
		CFlags:  []string{"-O2", "-fshort-enums"},
		FlagProfile: &seed.FlagProfile{
			Name: "policy-strong__threshold-8__pic-default__guard-default",
			AxisValues: map[string]string{
				"policy":     "strong",
				"threshold":  "8",
				"pic_mode":   "default",
				"guard_mode": "default",
			},
			Flags: []string{"-fstack-protector-strong", "--param=ssp-buffer-size=8"},
		},
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.LLMCFlagsApplied)
	assert.Equal(t, []string{"-O2", "-fshort-enums"}, result.AppliedLLMCFlags)
	assert.Empty(t, result.DroppedLLMCFlags)
	assert.Equal(t, []string{"-Wall", "-fstack-protector-strong", "--param=ssp-buffer-size=8", "-O2", "-fshort-enums", filepath.Join(workDir, "seed_18.c"), "-o", filepath.Join(workDir, "seed_18")}, capturedArgs)
}

func TestGCCCompiler_Compile_FiltersConflictingFortifyLLMFlags(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "build")
	require.NoError(t, os.MkdirAll(workDir, 0755))

	cfg := GCCCompilerConfig{
		GCCPath: "gcc",
		WorkDir: workDir,
		CFlags:  []string{"-Wall"},
	}
	compiler := NewGCCCompiler(cfg)

	var capturedArgs []string
	compiler.executor = &MockExecutor{
		RunFunc: func(command string, args ...string) (*exec.ExecutionResult, error) {
			capturedArgs = append([]string(nil), args...)
			return &exec.ExecutionResult{ExitCode: 0}, nil
		},
	}

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 19},
		Content: "int main() { return 0; }",
		CFlags:  []string{"-O3", "-D_FORTIFY_SOURCE=3", "-fhardened", "-fstack-protector-strong", "-Wall", "-Wextra"},
		FlagProfile: &seed.FlagProfile{
			Name: "optimization-O2__fortify_mode-fortify2__stack_protector_mode-no-stack-protector",
			AxisValues: map[string]string{
				"optimization":         "O2",
				"fortify_mode":         "fortify2",
				"stack_protector_mode": "no-stack-protector",
			},
			Flags: []string{"-O2", "-D_FORTIFY_SOURCE=2", "-fno-stack-protector"},
		},
	}

	result, err := compiler.Compile(testSeed)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.LLMCFlagsApplied)
	assert.Equal(t, []string{"-Wall", "-Wextra"}, result.AppliedLLMCFlags)
	assert.Equal(t, []string{"-O3", "-D_FORTIFY_SOURCE=3", "-fhardened", "-fstack-protector-strong"}, result.DroppedLLMCFlags)
	assert.Equal(t, []string{"-Wall", "-O2", "-D_FORTIFY_SOURCE=2", "-fno-stack-protector", "-Wall", "-Wextra", filepath.Join(workDir, "seed_19.c"), "-o", filepath.Join(workDir, "seed_19")}, capturedArgs)
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
