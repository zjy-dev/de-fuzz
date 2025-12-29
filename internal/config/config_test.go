package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

// setupTestConfigs creates a temporary directory structure for testing.
// It returns the temporary root directory and a cleanup function.
func setupTestConfigs(t *testing.T) (string, func()) {
	configDir, err := os.MkdirTemp("", "config_test_")
	assert.NoError(t, err)

	// Viper requires a "configs" subdirectory to be present.
	actualConfigPath := filepath.Join(configDir, "configs")
	err = os.Mkdir(actualConfigPath, 0755)
	assert.NoError(t, err)

	// Change working directory to the parent of "configs"
	oldWd, err := os.Getwd()
	assert.NoError(t, err)
	err = os.Chdir(configDir)
	assert.NoError(t, err)

	cleanup := func() {
		os.Chdir(oldWd)
		os.RemoveAll(configDir)
	}

	return actualConfigPath, cleanup
}

func TestLoad_Success(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create a test config file with 'config' top-level object
	configContent := `
config:
  isa: "x64"
  strategy: "canary"
  llm:
    provider: "deepseek"
  compiler:
    name: "gcc"
    version: "12.2.0"
`
	configFile := filepath.Join(actualConfigPath, "config.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	assert.NoError(t, err)

	// Test loading the config
	var loadedCfg Config
	err = Load("config", &loadedCfg)
	assert.NoError(t, err)
	assert.Equal(t, "x64", loadedCfg.ISA)
	assert.Equal(t, "canary", loadedCfg.Strategy)
	assert.Equal(t, "deepseek", loadedCfg.LLM.Provider)
}

func TestLoad_FileNotExists(t *testing.T) {
	_, cleanup := setupTestConfigs(t)
	defer cleanup()

	var cfg Config
	err := Load("non_existent_config", &cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read config file")
}

func TestLoad_EmptyFile(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create an empty config file
	emptyConfigFile := filepath.Join(actualConfigPath, "empty.yaml")
	err := os.WriteFile(emptyConfigFile, []byte(""), 0644)
	assert.NoError(t, err)

	var cfg Config
	err = Load("empty", &cfg)
	assert.NoError(t, err) // Viper doesn't error on empty files, just unmarshals nothing
	assert.Empty(t, cfg.ISA)
	assert.Empty(t, cfg.Strategy)
}

func TestLoad_MalformedYAML(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create a malformed config file
	malformedContent := "config: test\n  isa: oops" // Bad indentation
	malformedFile := filepath.Join(actualConfigPath, "malformed.yaml")
	err := os.WriteFile(malformedFile, []byte(malformedContent), 0644)
	assert.NoError(t, err)

	var cfg Config
	err = Load("malformed", &cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read config file")
}

func TestGetCompilerConfigName(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create main config file with 'config' top-level object
	configContent := `
config:
  isa: "x64"
  strategy: "canary"
  llm:
    provider: "deepseek"
  compiler:
    name: "gcc"
    version: "12.2.0"
`
	configFile := filepath.Join(actualConfigPath, "config.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	assert.NoError(t, err)

	// Load config
	var cfg Config
	err = Load("config", &cfg)
	assert.NoError(t, err)

	// Test GetCompilerConfigName
	configName := GetCompilerConfigName(&cfg)
	assert.Equal(t, "gcc-v12.2.0-x64-canary", configName)
}

func TestGetCompilerConfigPath(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create main config file with 'config' top-level object
	configContent := `
config:
  isa: "x64"
  strategy: "canary"
  llm:
    provider: "deepseek"
  compiler:
    name: "gcc"
    version: "12.2.0"
`
	configFile := filepath.Join(actualConfigPath, "config.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	assert.NoError(t, err)

	// Create the expected compiler config file
	compilerConfigContent := `
compiler:
  path: "/path/to/gcc"
  gcovr_exec_path: "/path/to/build"
`
	compilerConfigFile := filepath.Join(actualConfigPath, "gcc-v12.2.0-x64-canary.yaml")
	err = os.WriteFile(compilerConfigFile, []byte(compilerConfigContent), 0644)
	assert.NoError(t, err)

	// Load config
	var cfg Config
	err = Load("config", &cfg)
	assert.NoError(t, err)

	// Test GetCompilerConfigPath
	configPath, err := GetCompilerConfigPath(&cfg)
	assert.NoError(t, err)
	assert.Contains(t, configPath, "gcc-v12.2.0-x64-canary.yaml")

	// Verify the file exists
	_, err = os.Stat(configPath)
	assert.NoError(t, err)
}

func TestGetCompilerConfigPath_NotFound(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create main config file with 'config' top-level object
	configContent := `
config:
  isa: "x64"
  strategy: "stackguard"
  llm:
    provider: "deepseek"
  compiler:
    name: "clang"
    version: "15.0.0"
`
	configFile := filepath.Join(actualConfigPath, "config.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	assert.NoError(t, err)

	// Load config
	var cfg Config
	err = Load("config", &cfg)
	assert.NoError(t, err)

	// Test GetCompilerConfigPath when file doesn't exist
	_, err = GetCompilerConfigPath(&cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "compiler config file not found")
}

func TestLoad_CompilerConfig_WithSourceParentPath(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create a compiler config file with all fields including source_parent_path
	compilerConfigContent := `
compiler:
  path: "/root/fuzz-coverage/gcc-build-selective/gcc/xgcc"
  gcovr_exec_path: "/root/fuzz-coverage/gcc-build-selective"
  source_parent_path: "/root/fuzz-coverage"
  gcovr_command: 'gcovr --exclude ".*\.(h|hpp|hxx)$" --gcov-executable "gcov-14" -r ..'
  total_report_path: "/root/fuzz-coverage/workspace/reports/total.json"
`
	compilerConfigFile := filepath.Join(actualConfigPath, "test-compiler.yaml")
	err := os.WriteFile(compilerConfigFile, []byte(compilerConfigContent), 0644)
	assert.NoError(t, err)

	// Load the compiler config
	var compilerCfg CompilerConfig
	err = Load("test-compiler", &compilerCfg)
	assert.NoError(t, err)

	// Verify all fields are loaded correctly
	assert.Equal(t, "/root/fuzz-coverage/gcc-build-selective/gcc/xgcc", compilerCfg.Path)
	assert.Equal(t, "/root/fuzz-coverage/gcc-build-selective", compilerCfg.GcovrExecPath)
	assert.Equal(t, "/root/fuzz-coverage", compilerCfg.SourceParentPath)
	assert.Equal(t, `gcovr --exclude ".*\.(h|hpp|hxx)$" --gcov-executable "gcov-14" -r ..`, compilerCfg.GcovrCommand)
	assert.Equal(t, "/root/fuzz-coverage/workspace/reports/total.json", compilerCfg.TotalReportPath)
}

func TestLoad_CompilerConfig_WithoutSourceParentPath(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create a compiler config file without source_parent_path (for backward compatibility)
	compilerConfigContent := `
compiler:
  path: "/path/to/gcc"
  gcovr_exec_path: "/path/to/build"
`
	compilerConfigFile := filepath.Join(actualConfigPath, "legacy-compiler.yaml")
	err := os.WriteFile(compilerConfigFile, []byte(compilerConfigContent), 0644)
	assert.NoError(t, err)

	// Load the compiler config
	var compilerCfg CompilerConfig
	err = Load("legacy-compiler", &compilerCfg)
	assert.NoError(t, err)

	// Verify fields are loaded correctly
	assert.Equal(t, "/path/to/gcc", compilerCfg.Path)
	assert.Equal(t, "/path/to/build", compilerCfg.GcovrExecPath)
	// Optional fields should be empty string when not provided
	assert.Equal(t, "", compilerCfg.SourceParentPath)
	assert.Equal(t, "", compilerCfg.GcovrCommand)
	assert.Equal(t, "", compilerCfg.TotalReportPath)
}

func TestLoad_FuzzConfig(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create a config file with fuzz section
	// Note: In the actual config.yaml, 'llm' is a string (provider name),
	// but the Config struct expects LLMConfig. This is handled specially in LoadConfig.
	// For Load() function, we test with the actual file format.
	configContent := `
config:
  isa: "x64"
  strategy: "canary"
  compiler:
    name: "gcc"
    version: "12.2.0"
  fuzz:
    output_root_dir: "my_fuzz_out"
    max_iterations: 100
    max_new_seeds: 5
    max_constraint_retries: 5
    timeout: 60
    use_qemu: true
    qemu_path: "qemu-x86_64"
    qemu_sysroot: "/usr/x86_64-linux-gnu"
`
	configFile := filepath.Join(actualConfigPath, "config.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	assert.NoError(t, err)

	// Load using viper directly since Load() has type mismatch issues with llm field
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(actualConfigPath)
	err = v.ReadInConfig()
	assert.NoError(t, err)

	// Parse fuzz config
	var fuzzCfg FuzzConfig
	err = v.UnmarshalKey("config.fuzz", &fuzzCfg)
	assert.NoError(t, err)

	// Verify fuzz config fields
	assert.Equal(t, "my_fuzz_out", fuzzCfg.OutputRootDir)
	assert.Equal(t, 100, fuzzCfg.MaxIterations)
	assert.Equal(t, 5, fuzzCfg.MaxNewSeeds)
	assert.Equal(t, 5, fuzzCfg.MaxConstraintRetries)
	assert.Equal(t, 60, fuzzCfg.Timeout)
	assert.True(t, fuzzCfg.UseQEMU)
	assert.Equal(t, "qemu-x86_64", fuzzCfg.QEMUPath)
	assert.Equal(t, "/usr/x86_64-linux-gnu", fuzzCfg.QEMUSysroot)
}

func TestLoad_FuzzConfig_Defaults(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create a config file without fuzz section
	configContent := `
config:
  isa: "x64"
  strategy: "canary"
  compiler:
    name: "gcc"
    version: "12.2.0"
`
	configFile := filepath.Join(actualConfigPath, "config.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	assert.NoError(t, err)

	// Load using viper directly
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(actualConfigPath)
	err = v.ReadInConfig()
	assert.NoError(t, err)

	// Parse fuzz config - should be empty when not specified
	var fuzzCfg FuzzConfig
	err = v.UnmarshalKey("config.fuzz", &fuzzCfg)
	assert.NoError(t, err)

	// Fuzz config should have zero values when not specified in file
	assert.Equal(t, "", fuzzCfg.OutputRootDir)
	assert.Equal(t, 0, fuzzCfg.MaxIterations)
	assert.Equal(t, 0, fuzzCfg.MaxNewSeeds)
	assert.Equal(t, 0, fuzzCfg.MaxConstraintRetries)
	assert.Equal(t, 0, fuzzCfg.Timeout)
	assert.False(t, fuzzCfg.UseQEMU)
	assert.Equal(t, "", fuzzCfg.QEMUPath)
	assert.Equal(t, "", fuzzCfg.QEMUSysroot)
}

func TestResolveEnvVars(t *testing.T) {
	// Set up test environment variables
	os.Setenv("TEST_API_KEY", "secret123")
	os.Setenv("TEST_ENDPOINT", "https://api.test.com")
	defer os.Unsetenv("TEST_API_KEY")
	defer os.Unsetenv("TEST_ENDPOINT")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Braced format with existing env var",
			input:    "${TEST_API_KEY}",
			expected: "secret123",
		},
		{
			name:     "Simple format with existing env var",
			input:    "$TEST_API_KEY",
			expected: "secret123",
		},
		{
			name:     "Mixed text with env var",
			input:    "Bearer ${TEST_API_KEY}",
			expected: "Bearer secret123",
		},
		{
			name:     "Multiple env vars",
			input:    "${TEST_API_KEY} at ${TEST_ENDPOINT}",
			expected: "secret123 at https://api.test.com",
		},
		{
			name:     "Non-existent env var stays as-is",
			input:    "${NONEXISTENT_VAR}",
			expected: "${NONEXISTENT_VAR}",
		},
		{
			name:     "Simple format non-existent",
			input:    "$NONEXISTENT_VAR",
			expected: "$NONEXISTENT_VAR",
		},
		{
			name:     "No env vars",
			input:    "plain text",
			expected: "plain text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveEnvVars(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLoadEnvFromDotEnv(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "env_test_")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create .env file
	envContent := `# This is a comment
TEST_API_KEY=secret_key_123
TEST_ENDPOINT=https://api.test.com/v1
EMPTY_VAR=
QUOTED_VAR="value with spaces"
SINGLE_QUOTED_VAR='single quoted'
`
	envFile := filepath.Join(tempDir, ".env")
	err = os.WriteFile(envFile, []byte(envContent), 0644)
	assert.NoError(t, err)

	// Load .env file
	err = LoadEnvFromDotEnv(tempDir)
	assert.NoError(t, err)

	// Verify environment variables are set
	assert.Equal(t, "secret_key_123", os.Getenv("TEST_API_KEY"))
	assert.Equal(t, "https://api.test.com/v1", os.Getenv("TEST_ENDPOINT"))
	assert.Equal(t, "", os.Getenv("EMPTY_VAR"))
	assert.Equal(t, "value with spaces", os.Getenv("QUOTED_VAR"))
	assert.Equal(t, "single quoted", os.Getenv("SINGLE_QUOTED_VAR"))

	// Clean up
	os.Unsetenv("TEST_API_KEY")
	os.Unsetenv("TEST_ENDPOINT")
	os.Unsetenv("EMPTY_VAR")
	os.Unsetenv("QUOTED_VAR")
	os.Unsetenv("SINGLE_QUOTED_VAR")
}

func TestLoadEnvFromDotEnv_NotExists(t *testing.T) {
	// Create a temporary directory without .env file
	tempDir, err := os.MkdirTemp("", "env_test_")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Should not error when .env doesn't exist
	err = LoadEnvFromDotEnv(tempDir)
	assert.NoError(t, err)
}

func TestLoadEnvFromDotEnv_OverrideProtection(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "env_test_")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Set environment variable before loading .env
	os.Setenv("PREEXISTING_VAR", "original_value")
	defer os.Unsetenv("PREEXISTING_VAR")

	// Create .env file with same variable
	envContent := "PREEXISTING_VAR=new_value\n"
	envFile := filepath.Join(tempDir, ".env")
	err = os.WriteFile(envFile, []byte(envContent), 0644)
	assert.NoError(t, err)

	// Load .env file - should NOT override existing variable
	err = LoadEnvFromDotEnv(tempDir)
	assert.NoError(t, err)

	// Original value should be preserved
	assert.Equal(t, "original_value", os.Getenv("PREEXISTING_VAR"))
}

func TestResolveEnvVarsInMap(t *testing.T) {
	// Set up test environment variables
	os.Setenv("TEST_KEY", "resolved_value")
	defer os.Unsetenv("TEST_KEY")

	testMap := map[string]interface{}{
		"api_key":   "${TEST_KEY}",
		"endpoint":  "https://api.example.com",
		"nested": map[string]interface{}{
			"inner_key": "$TEST_KEY",
		},
		"array": []interface{}{
			"$TEST_KEY",
			"static_value",
		},
	}

	resolveInMap(testMap)

	assert.Equal(t, "resolved_value", testMap["api_key"])
	assert.Equal(t, "https://api.example.com", testMap["endpoint"])
	nested := testMap["nested"].(map[string]interface{})
	assert.Equal(t, "resolved_value", nested["inner_key"])
	array := testMap["array"].([]interface{})
	assert.Equal(t, "resolved_value", array[0])
	assert.Equal(t, "static_value", array[1])
}

func TestLoad_FuzzConfig_PartialConfig(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create a config file with partial fuzz section
	configContent := `
config:
  isa: "x64"
  strategy: "canary"
  compiler:
    name: "gcc"
    version: "12.2.0"
  fuzz:
    max_iterations: 50
    max_constraint_retries: 10
    timeout: 45
`
	configFile := filepath.Join(actualConfigPath, "config.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	assert.NoError(t, err)

	// Load using viper directly
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(actualConfigPath)
	err = v.ReadInConfig()
	assert.NoError(t, err)

	// Parse fuzz config
	var fuzzCfg FuzzConfig
	err = v.UnmarshalKey("config.fuzz", &fuzzCfg)
	assert.NoError(t, err)

	// Verify specified fields
	assert.Equal(t, 50, fuzzCfg.MaxIterations)
	assert.Equal(t, 10, fuzzCfg.MaxConstraintRetries)
	assert.Equal(t, 45, fuzzCfg.Timeout)
	// Unspecified fields should be zero values
	assert.Equal(t, "", fuzzCfg.OutputRootDir)
	assert.Equal(t, 0, fuzzCfg.MaxNewSeeds)
	assert.False(t, fuzzCfg.UseQEMU)
}
