//go:build integration

package llm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRemixerConfigurationIntegration tests configuration loading and client creation via remixer
func TestRemixerConfigurationIntegration(t *testing.T) {
	t.Run("should create remixer client from config file", func(t *testing.T) {
		// Create a temporary remixer config
		tmpDir, err := os.MkdirTemp("", "llm_integration_*")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		configContent := `models:
  - name: "test-model"
    weight: 1
    providers:
      - type: "openai"
        endpoint: "https://api.example.com/v1"
        model: "test"
        api_key: "test-key"
`
		configPath := filepath.Join(tmpDir, "remixer.yaml")
		err = os.WriteFile(configPath, []byte(configContent), 0644)
		require.NoError(t, err)

		client, err := New(configPath, 0.1)
		require.NoError(t, err)
		assert.NotNil(t, client)
		assert.IsType(t, &RemixerClient{}, client)
	})
}

// TestRemixerRealAPIIntegration tests actual integration with LLM APIs via remixer
// Requires valid API keys and internet connection
func TestRemixerRealAPIIntegration(t *testing.T) {
	t.Skip("Skipping LLM real API integration tests")

	if testing.Short() {
		t.Skip("Skipping real API integration test in short mode")
	}

	// Use project remixer config
	configPath := "../../configs/remixer.yaml"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configPath = "configs/remixer.yaml"
	}

	client, err := New(configPath, 0.1)
	if err != nil {
		t.Skipf("Skipping real API test: failed to create client: %v", err)
	}

	t.Run("GetCompletion_RealAPI", func(t *testing.T) {
		response, err := client.GetCompletion("Write a simple 'Hello, World!' program in C. Keep it very short.")
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotEmpty(t, response)
		t.Logf("GetCompletion response: %s", response)
	})

	t.Run("Understand_RealAPI", func(t *testing.T) {
		understanding, err := client.Understand("I want to fuzz a simple C program. Explain the approach in one sentence.")
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotEmpty(t, understanding)
		t.Logf("Understanding: %s", understanding)
	})

	t.Run("Generate_RealAPI", func(t *testing.T) {
		newSeed, err := client.Generate("system understanding", "Generate a minimal C program with potential integer overflow. One line of code only.")
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotNil(t, newSeed)
		assert.NotEmpty(t, newSeed.Content)
		t.Logf("Generated seed: %s", newSeed.Content)
	})

	t.Run("Analyze_RealAPI", func(t *testing.T) {
		testSeed := &seed.Seed{
			Meta:    seed.Metadata{ID: 1},
			Content: "int main() { int x = 2000000000; return x + x; }",
			TestCases: []seed.TestCase{
				{RunningCommand: "./test", ExpectedResult: "success"},
			},
		}
		analysis, err := client.Analyze("system understanding", "Briefly analyze this overflow", testSeed, "Program returned: -294967296")
		if err != nil {
			t.Logf("API call failed (this might be expected): %v", err)
			return
		}
		assert.NotEmpty(t, analysis)
		t.Logf("Analysis: %s", analysis)
	})

	t.Run("Mutate_RealAPI", func(t *testing.T) {
		originalSeed := &seed.Seed{
			Meta:    seed.Metadata{ID: 2},
			Content: "int main() { return 42; }",
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
