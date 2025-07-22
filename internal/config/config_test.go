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

	// Create a test LLM config file
	llmContent := `
provider: test_provider
model: test_model
api_key: test_api_key
endpoint: http://localhost:8080
`
	llmConfigFile := filepath.Join(actualConfigPath, "llm.yaml")
	err := os.WriteFile(llmConfigFile, []byte(llmContent), 0644)
	assert.NoError(t, err)

	// Test loading the LLM config
	var loadedLlmCfg LLMConfig
	err = Load("llm", &loadedLlmCfg)
	assert.NoError(t, err)
	assert.Equal(t, "test_provider", loadedLlmCfg.Provider)
	assert.Equal(t, "test_model", loadedLlmCfg.Model)
	assert.Equal(t, "test_api_key", loadedLlmCfg.APIKey)
	assert.Equal(t, "http://localhost:8080", loadedLlmCfg.Endpoint)
}

func TestLoad_FileNotExists(t *testing.T) {
	_, cleanup := setupTestConfigs(t)
	defer cleanup()

	var cfg LLMConfig
	err := Load("non_existent_config", &cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Config File \"non_existent_config\" Not Found")
}

func TestLoad_EmptyFile(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create an empty config file
	emptyConfigFile := filepath.Join(actualConfigPath, "empty.yaml")
	err := os.WriteFile(emptyConfigFile, []byte(""), 0644)
	assert.NoError(t, err)

	var cfg LLMConfig
	err = Load("empty", &cfg)
	assert.NoError(t, err) // Viper doesn't error on empty files, just unmarshals nothing
	assert.Empty(t, cfg.Provider)
	assert.Empty(t, cfg.Model)
}

func TestLoad_MalformedYAML(t *testing.T) {
	actualConfigPath, cleanup := setupTestConfigs(t)
	defer cleanup()

	// Create a malformed config file
	malformedContent := "provider: test\n  model: oops" // Bad indentation
	malformedFile := filepath.Join(actualConfigPath, "malformed.yaml")
	err := os.WriteFile(malformedFile, []byte(malformedContent), 0644)
	assert.NoError(t, err)

	var cfg LLMConfig
	err = Load("malformed", &cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read config file")
}
