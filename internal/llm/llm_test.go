package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/config"
)

func TestMiniMaxClient_Creation(t *testing.T) {
	// Test creating a MiniMax client directly
	client := NewMiniMaxClient("test-api-key", "MiniMax-M2.1", "", 0.7)
	assert.NotNil(t, client)
	assert.IsType(t, &MiniMaxClient{}, client)

	assert.Equal(t, "test-api-key", client.GetAPIKey())
	assert.Equal(t, "MiniMax-M2.1", client.GetModel())
	assert.Equal(t, DefaultMiniMaxEndpoint, client.GetEndpoint())
	assert.Equal(t, 0.7, client.GetTemperature())
}

func TestMiniMaxClient_CustomEndpoint(t *testing.T) {
	client := NewMiniMaxClient("test-key", "test-model", "https://custom.endpoint.com/v1", 0.5)
	assert.Equal(t, "https://custom.endpoint.com/v1", client.GetEndpoint())
	assert.Equal(t, 0.5, client.GetTemperature())
}

func TestMiniMaxClient_NewFromConfig(t *testing.T) {
	// Create a config with minimax provider
	cfg := &config.Config{
		LLM: config.LLMConfig{
			Provider:    "minimax",
			Model:       "MiniMax-M2.1",
			APIKey:      "test-key-from-config",
			Endpoint:    "https://api.minimax.chat/v1/text/chatcompletion_v2",
			Temperature: 0.6,
		},
	}

	client, err := New(cfg)
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.IsType(t, &MiniMaxClient{}, client)

	miniMaxClient := client.(*MiniMaxClient)
	assert.Equal(t, "test-key-from-config", miniMaxClient.GetAPIKey())
	assert.Equal(t, "MiniMax-M2.1", miniMaxClient.GetModel())
	assert.Equal(t, "https://api.minimax.chat/v1/text/chatcompletion_v2", miniMaxClient.GetEndpoint())
}

func TestMiniMaxClient_ImplementsInterface(t *testing.T) {
	// Verify MiniMaxClient implements the LLM interface
	var _ LLM = &MiniMaxClient{}
}
