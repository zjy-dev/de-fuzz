package config

import (
	"os"
	"path/filepath"
	"testing"

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
