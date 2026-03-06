package llm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemixerClient_ImplementsInterface(t *testing.T) {
	// Verify RemixerClient implements the LLM interface
	var _ LLM = &RemixerClient{}
}

func TestNew_InvalidConfigPath(t *testing.T) {
	_, err := New("nonexistent.yaml", 0.1)
	assert.Error(t, err)
}

func TestNewRemixerClient_DefaultTemperatureFallback(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remixer_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	configContent := `models:
  - name: "mock"
    weight: 1
    providers:
      - type: "openai"
        endpoint: "https://api.example.com/v1"
        model: "mock"
        api_key: "test-key"
`
	configPath := filepath.Join(tmpDir, "remixer.yaml")
	err = os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	client, err := NewRemixerClient(configPath, 0)
	require.NoError(t, err)
	assert.Equal(t, 0.1, client.temperature)
}

func TestRemixerClient_Analyze_NilSeed(t *testing.T) {
	client := &RemixerClient{}
	_, err := client.Analyze("sys", "prompt", nil, "feedback")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "seed cannot be nil")
}

func TestRemixerClient_Mutate_NilSeed(t *testing.T) {
	client := &RemixerClient{}
	_, err := client.Mutate("sys", "prompt", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "seed cannot be nil")
}
