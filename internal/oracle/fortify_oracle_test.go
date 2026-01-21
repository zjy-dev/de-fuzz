package oracle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

func TestNewFortifyOracle(t *testing.T) {
	tests := []struct {
		name        string
		options     map[string]interface{}
		wantMaxFill int
		wantBufSize int
	}{
		{
			name:        "default values",
			options:     nil,
			wantMaxFill: DefaultMaxFillSize,
			wantBufSize: 64,
		},
		{
			name: "custom max_fill_size as int",
			options: map[string]interface{}{
				"max_fill_size": 8192,
			},
			wantMaxFill: 8192,
			wantBufSize: 64,
		},
		{
			name: "custom max_fill_size as float64",
			options: map[string]interface{}{
				"max_fill_size": 8192.0,
			},
			wantMaxFill: 8192,
			wantBufSize: 64,
		},
		{
			name: "custom default_buf_size as int",
			options: map[string]interface{}{
				"default_buf_size": 128,
			},
			wantMaxFill: DefaultMaxFillSize,
			wantBufSize: 128,
		},
		{
			name: "custom default_buf_size as float64",
			options: map[string]interface{}{
				"default_buf_size": 128.0,
			},
			wantMaxFill: DefaultMaxFillSize,
			wantBufSize: 128,
		},
		{
			name: "all custom values",
			options: map[string]interface{}{
				"max_fill_size":    2048,
				"default_buf_size": 32,
			},
			wantMaxFill: 2048,
			wantBufSize: 32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oracle, err := NewFortifyOracle(tt.options, nil, nil, "")
			require.NoError(t, err)
			require.NotNil(t, oracle)

			fo := oracle.(*FortifyOracle)
			assert.Equal(t, tt.wantMaxFill, fo.MaxFillSize)
			assert.Equal(t, tt.wantBufSize, fo.DefaultBufSize)
		})
	}
}

func TestFortifyOracle_Analyze_NilContext(t *testing.T) {
	oracle := &FortifyOracle{
		MaxFillSize:    DefaultMaxFillSize,
		DefaultBufSize: 64,
	}

	_, err := oracle.Analyze(&seed.Seed{}, nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires AnalyzeContext")
}

func TestFortifyOracle_Analyze_MissingExecutor(t *testing.T) {
	oracle := &FortifyOracle{
		MaxFillSize:    DefaultMaxFillSize,
		DefaultBufSize: 64,
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/some/path",
		Executor:   nil,
	}

	_, err := oracle.Analyze(&seed.Seed{}, ctx, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires AnalyzeContext")
}

func TestFortifyOracle_Analyze_MissingBinaryPath(t *testing.T) {
	oracle := &FortifyOracle{
		MaxFillSize:    DefaultMaxFillSize,
		DefaultBufSize: 64,
	}

	ctx := &AnalyzeContext{
		BinaryPath: "",
		Executor:   &mockExecutorFortify{},
	}

	_, err := oracle.Analyze(&seed.Seed{}, ctx, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires AnalyzeContext")
}

// mockExecutorFortify is a mock executor for Fortify oracle testing
type mockExecutorFortify struct {
	responses map[string]struct {
		exitCode int
		stdout   string
		stderr   string
		err      error
	}
}

func (m *mockExecutorFortify) ExecuteWithInput(binaryPath string, stdin string) (int, string, string, error) {
	return 0, "", "", nil
}

func (m *mockExecutorFortify) ExecuteWithArgs(binaryPath string, args ...string) (int, string, string, error) {
	if len(args) >= 2 {
		key := args[1] // fill_size
		if resp, ok := m.responses[key]; ok {
			return resp.exitCode, resp.stdout, resp.stderr, resp.err
		}
	}
	return 0, "", "", nil
}

func TestFortifyOracle_Analyze_NoCrash(t *testing.T) {
	oracle := &FortifyOracle{
		MaxFillSize:    100,
		DefaultBufSize: 64,
	}

	// All sizes return exit code 0
	executor := &mockExecutorFortify{
		responses: make(map[string]struct {
			exitCode int
			stdout   string
			stderr   string
			err      error
		}),
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/test/binary",
		Executor:   executor,
	}

	bug, err := oracle.Analyze(&seed.Seed{}, ctx, nil)
	assert.NoError(t, err)
	assert.Nil(t, bug, "No bug should be reported when no crash occurs")
}

func TestFortifyOracle_Analyze_FortifyProtected(t *testing.T) {
	oracle := &FortifyOracle{
		MaxFillSize:    200,
		DefaultBufSize: 64,
	}

	// Simulate Fortify catching the overflow (SIGABRT)
	executor := &mockExecutorFortify{
		responses: map[string]struct {
			exitCode int
			stdout   string
			stderr   string
			err      error
		}{
			"65":  {exitCode: ExitCodeSIGABRT, stdout: "", stderr: "buffer overflow detected"},
			"100": {exitCode: ExitCodeSIGABRT, stdout: "", stderr: "buffer overflow detected"},
			"200": {exitCode: ExitCodeSIGABRT, stdout: "", stderr: "buffer overflow detected"},
		},
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/test/binary",
		Executor:   executor,
	}

	bug, err := oracle.Analyze(&seed.Seed{}, ctx, nil)
	assert.NoError(t, err)
	assert.Nil(t, bug, "SIGABRT means Fortify is working - no bug")
}

func TestFortifyOracle_Analyze_FortifyBypass_WithSentinel(t *testing.T) {
	oracle := &FortifyOracle{
		MaxFillSize:    200,
		DefaultBufSize: 64,
	}

	// Simulate Fortify bypass: SIGSEGV with sentinel = true bypass
	executor := &mockExecutorFortify{
		responses: map[string]struct {
			exitCode int
			stdout   string
			stderr   string
			err      error
		}{
			"100": {exitCode: ExitCodeSIGSEGV, stdout: "SEED_RETURNED\n", stderr: ""},
			"200": {exitCode: ExitCodeSIGSEGV, stdout: "SEED_RETURNED\n", stderr: ""},
		},
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/test/binary",
		Executor:   executor,
	}

	bug, err := oracle.Analyze(&seed.Seed{}, ctx, nil)
	assert.NoError(t, err)
	assert.NotNil(t, bug, "SIGSEGV with sentinel should be reported as bug")
	assert.Contains(t, bug.Description, "_FORTIFY_SOURCE bypass detected")
}

func TestFortifyOracle_Analyze_FortifyBypass_WithoutSentinel(t *testing.T) {
	oracle := &FortifyOracle{
		MaxFillSize:    200,
		DefaultBufSize: 64,
	}

	// Simulate false positive: SIGSEGV without sentinel = indirect crash
	executor := &mockExecutorFortify{
		responses: map[string]struct {
			exitCode int
			stdout   string
			stderr   string
			err      error
		}{
			"100": {exitCode: ExitCodeSIGSEGV, stdout: "", stderr: ""},
			"200": {exitCode: ExitCodeSIGSEGV, stdout: "", stderr: ""},
		},
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/test/binary",
		Executor:   executor,
	}

	bug, err := oracle.Analyze(&seed.Seed{}, ctx, nil)
	assert.NoError(t, err)
	assert.Nil(t, bug, "SIGSEGV without sentinel should be treated as false positive")
}

func TestFortifyOracle_Analyze_SIGBUS_WithSentinel(t *testing.T) {
	oracle := &FortifyOracle{
		MaxFillSize:    200,
		DefaultBufSize: 64,
	}

	// SIGBUS with sentinel = true bypass (unaligned return address)
	executor := &mockExecutorFortify{
		responses: map[string]struct {
			exitCode int
			stdout   string
			stderr   string
			err      error
		}{
			"100": {exitCode: ExitCodeSIGBUS, stdout: "SEED_RETURNED\n", stderr: ""},
			"200": {exitCode: ExitCodeSIGBUS, stdout: "SEED_RETURNED\n", stderr: ""},
		},
	}

	ctx := &AnalyzeContext{
		BinaryPath: "/test/binary",
		Executor:   executor,
	}

	bug, err := oracle.Analyze(&seed.Seed{}, ctx, nil)
	assert.NoError(t, err)
	assert.NotNil(t, bug, "SIGBUS with sentinel should be reported as bug")
	assert.Contains(t, bug.Description, "_FORTIFY_SOURCE bypass detected")
	assert.Contains(t, bug.Description, "unaligned")
}

func TestFortifyOracleRegistration(t *testing.T) {
	// Test that FortifyOracle is properly registered
	oracle, err := New("fortify", nil, nil, nil, "")
	require.NoError(t, err)
	require.NotNil(t, oracle)

	_, ok := oracle.(*FortifyOracle)
	assert.True(t, ok, "Oracle should be of type *FortifyOracle")
}
