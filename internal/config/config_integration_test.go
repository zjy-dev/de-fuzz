//go:build integration

package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Integration(t *testing.T) {
	// This test requires the actual config files to be present
	// Try multiple paths to find the configs directory
	configPaths := []string{
		"configs/config.yaml",
		"../configs/config.yaml",
		"../../configs/config.yaml",
	}

	configFound := false
	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			configFound = true
			break
		}
	}

	if !configFound {
		t.Skip("Skipping integration test: config files not found")
	}

	// Load the full configuration
	cfg, err := LoadConfig()
	require.NoError(t, err, "LoadConfig should succeed with real config files")

	// Verify main config fields
	assert.NotEmpty(t, cfg.ISA, "ISA should be loaded")
	assert.NotEmpty(t, cfg.Strategy, "Strategy should be loaded")

	// Verify LLM config
	assert.NotEmpty(t, cfg.LLM.Provider, "LLM provider should be loaded")

	// Verify compiler config
	assert.NotEmpty(t, cfg.Compiler.Path, "Compiler path should be loaded")
	assert.NotEmpty(t, cfg.Compiler.GcovrExecPath, "Gcovr exec path should be loaded")
	// Note: SourceParentPath is optional, so we just log it if present
	if cfg.Compiler.SourceParentPath != "" {
		t.Logf("Source parent path loaded: %s", cfg.Compiler.SourceParentPath)
	}
}

func TestGetCompilerConfigPath_Integration(t *testing.T) {
	// This test requires the actual config files to be present
	// Try multiple paths to find the configs directory
	configPaths := []string{
		"configs/config.yaml",
		"../configs/config.yaml",
		"../../configs/config.yaml",
	}

	configFound := false
	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			configFound = true
			break
		}
	}

	if !configFound {
		t.Skip("Skipping integration test: config files not found")
	}

	// Load config using LoadConfig() which handles the complex loading logic
	cfg, err := LoadConfig()
	require.NoError(t, err)

	// Get compiler config path
	configPath, err := GetCompilerConfigPath(cfg)
	require.NoError(t, err, "GetCompilerConfigPath should succeed")

	// Verify the path exists
	_, err = os.Stat(configPath)
	assert.NoError(t, err, "Compiler config file should exist at returned path")

	// Verify the path matches the expected pattern
	configName := GetCompilerConfigName(cfg)
	assert.Contains(t, configPath, configName+".yaml", "Path should contain the config name")
}

func TestGetCompilerConfigName_Integration(t *testing.T) {
	// This test requires the actual config files to be present
	// Try multiple paths to find the configs directory
	configPaths := []string{
		"configs/config.yaml",
		"../configs/config.yaml",
		"../../configs/config.yaml",
	}

	configFound := false
	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			configFound = true
			break
		}
	}

	if !configFound {
		t.Skip("Skipping integration test: config files not found")
	}

	// Load config using LoadConfig() which handles the complex loading logic
	cfg, err := LoadConfig()
	require.NoError(t, err)

	// Get compiler config name
	configName := GetCompilerConfigName(cfg)
	assert.NotEmpty(t, configName, "Config name should not be empty")

	// Verify the pattern: {compiler}-v{version}-{isa}-{strategy}
	assert.Contains(t, configName, cfg.ISA, "Config name should contain ISA")
	assert.Contains(t, configName, cfg.Strategy, "Config name should contain strategy")
	assert.Contains(t, configName, "-v", "Config name should contain version prefix")
}
