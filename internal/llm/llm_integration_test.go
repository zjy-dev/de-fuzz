//go:build integration

package llm

import (
	"os"
	"strings"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/config"
	"github.com/zjy-dev/de-fuzz/internal/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLLMConfigurationIntegration tests configuration loading and client creation
func TestLLMConfigurationIntegration(t *testing.T) {
	t.Run("should load configuration and create LLM client", func(t *testing.T) {
		// Create a temporary config file for testing
		tempConfig := `llm:
  provider: "deepseek"
  model: "deepseek-coder"
  api_key: "test_key"
  endpoint: "https://api.deepseek.com/v1/chat/completions"
fuzzer:
  isa: "x86_64"
  strategy: "feedback-driven"
  initial_seeds: 10
  bug_quota: 5`

		configDir, err := os.MkdirTemp("", "llm_integration_test_")
		require.NoError(t, err)
		defer os.RemoveAll(configDir)

		// Create configs subdirectory
		configsDir := configDir + "/configs"
		err = os.MkdirAll(configsDir, 0755)
		require.NoError(t, err)

		configFile := configsDir + "/test_config.yaml"
		err = os.WriteFile(configFile, []byte(tempConfig), 0644)
		require.NoError(t, err)

		// Temporarily change working directory to the temp directory
		originalWd, err := os.Getwd()
		require.NoError(t, err)
		defer os.Chdir(originalWd)

		err = os.Chdir(configDir)
		require.NoError(t, err)

		// Load configuration
		var cfg config.Config
		err = config.Load("test_config", &cfg)
		require.NoError(t, err)

		// Verify configuration was loaded correctly
		assert.Equal(t, "deepseek", cfg.LLM.Provider)
		assert.Equal(t, "deepseek-coder", cfg.LLM.Model)
		assert.Equal(t, "test_key", cfg.LLM.APIKey)
		assert.Equal(t, "https://api.deepseek.com/v1/chat/completions", cfg.LLM.Endpoint)

		// Create LLM client from configuration
		llmClient, err := New(&cfg)
		require.NoError(t, err)
		assert.IsType(t, &DeepSeekClient{}, llmClient)

		// Verify client properties
		deepSeekClient := llmClient.(*DeepSeekClient)
		assert.Equal(t, "test_key", deepSeekClient.GetAPIKey())
		assert.Equal(t, "deepseek-coder", deepSeekClient.GetModel())
		assert.Equal(t, "https://api.deepseek.com/v1/chat/completions", deepSeekClient.GetEndpoint())
	})
}

// TestDeepSeekRealAPIIntegration tests actual integration with DeepSeek API
// This test requires a valid API key in configs/llm.yaml and internet connection
func TestDeepSeekRealAPIIntegration(t *testing.T) {
	t.Skip("Skipping LLM real API integration tests")

	// Skip if running in CI or short mode
	if testing.Short() {
		t.Skip("Skipping real API integration test in short mode")
	}

	// Load real configuration
	var cfg config.Config
	err := config.Load("llm", &cfg)
	if err != nil {
		t.Skipf("Skipping real API test: failed to load configuration: %v", err)
	}

	// Skip if API key is placeholder or empty
	if cfg.LLM.APIKey == "YOUR_API_KEY_HERE" || cfg.LLM.APIKey == "" {
		t.Skip("Skipping real API test: no valid API key configured")
	}

	// Create LLM client
	client, err := New(&cfg)
	require.NoError(t, err)

	t.Run("GetCompletion_RealAPI", func(t *testing.T) {
		prompt := "Write a simple 'Hello, World!' program in C. Keep it very short."
		response, err := client.GetCompletion(prompt)
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotEmpty(t, response)
		t.Logf("GetCompletion response: %s", response)
	})

	t.Run("Understand_RealAPI", func(t *testing.T) {
		prompt := "I want to fuzz a simple C program that performs basic arithmetic. Explain the approach in one sentence."
		understanding, err := client.Understand(prompt)
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotEmpty(t, understanding)
		t.Logf("Understanding: %s", understanding)
	})

	t.Run("Generate_RealAPI", func(t *testing.T) {
		prompt := "Generate a minimal C program with potential integer overflow. One line of code only."
		newSeed, err := client.Generate("system understanding", prompt)
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotNil(t, newSeed)
		assert.NotEmpty(t, newSeed.Content)
		t.Logf("Generated seed: %s", newSeed.Content)
	})

	t.Run("Analyze_RealAPI", func(t *testing.T) {
		analyzeTestCases := []seed.TestCase{
			{RunningCommand: "./test", ExpectedResult: "success"},
		}
		testSeed := &seed.Seed{
			Meta: seed.Metadata{
				ID: 1,
			},
			Content:   "int main() { int x = 2000000000; return x + x; }",
			TestCases: analyzeTestCases,
		}
		feedback := "Program returned: -294967296"

		analysis, err := client.Analyze("system understanding", "Briefly analyze this overflow", testSeed, feedback)
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotEmpty(t, analysis)
		t.Logf("Analysis: %s", analysis)
	})

	t.Run("Mutate_RealAPI", func(t *testing.T) {
		mutateTestCases := []seed.TestCase{
			{RunningCommand: "./prog", ExpectedResult: "42"},
		}
		originalSeed := &seed.Seed{
			Meta: seed.Metadata{
				ID: 2,
			},
			Content:   "int main() { return 42; }",
			TestCases: mutateTestCases,
		}

		mutatedSeed, err := client.Mutate("system understanding", "Change the return value only", originalSeed)
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotNil(t, mutatedSeed)
		assert.NotEmpty(t, mutatedSeed.Content)
		t.Logf("Original: %s", originalSeed.Content)
		t.Logf("Mutated: %s", mutatedSeed.Content)
	})
}

// TestMiniMaxRealAPIIntegration tests actual integration with MiniMax M2.1 API
// This test requires a valid API key in configs/llm.yaml and internet connection
// Set provider to "minimax" in llm.yaml to run this test
func TestMiniMaxRealAPIIntegration(t *testing.T) {
	t.Skip("Skipping LLM real API integration tests")

	// Skip if running in CI or short mode
	if testing.Short() {
		t.Skip("Skipping real API integration test in short mode")
	}

	// Load real configuration using LoadConfig which properly handles llm.yaml array format
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Skipf("Skipping real API test: failed to load configuration: %v", err)
	}

	// Skip if not using minimax provider
	if cfg.LLM.Provider != "minimax" {
		t.Skip("Skipping MiniMax test: provider is not 'minimax'")
	}

	// Skip if API key is placeholder or empty
	if strings.HasPrefix(cfg.LLM.APIKey, "${") || cfg.LLM.APIKey == "" {
		t.Skip("Skipping real API test: no valid API key configured (use env var or hardcoded key)")
	}

	// Create LLM client
	client, err := New(cfg)
	require.NoError(t, err)

	// Verify it's a MiniMaxClient
	assert.IsType(t, &MiniMaxClient{}, client)

	t.Run("GetCompletion_RealAPI", func(t *testing.T) {
		prompt := "Write a simple 'Hello, World!' program in C. Keep it very short."
		response, err := client.GetCompletion(prompt)
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotEmpty(t, response)
		t.Logf("GetCompletion response: %s", response)
	})

	t.Run("Understand_RealAPI", func(t *testing.T) {
		prompt := "I want to fuzz a simple C program that performs basic arithmetic. Explain the approach in one sentence."
		understanding, err := client.Understand(prompt)
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotEmpty(t, understanding)
		t.Logf("Understanding: %s", understanding)
	})

	t.Run("Generate_RealAPI", func(t *testing.T) {
		prompt := "Generate a minimal C program with potential integer overflow. One line of code only."
		newSeed, err := client.Generate("system understanding", prompt)
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotNil(t, newSeed)
		assert.NotEmpty(t, newSeed.Content)
		t.Logf("Generated seed: %s", newSeed.Content)
	})

	t.Run("Mutate_RealAPI", func(t *testing.T) {
		originalSeed := &seed.Seed{
			Meta: seed.Metadata{
				ID: 1,
			},
			Content: "int main() { return 42; }",
		}

		mutatedSeed, err := client.Mutate("system understanding", "Change the return value to 100", originalSeed)
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotNil(t, mutatedSeed)
		assert.NotEmpty(t, mutatedSeed.Content)
		t.Logf("Original: %s", originalSeed.Content)
		t.Logf("Mutated: %s", mutatedSeed.Content)
	})
}
