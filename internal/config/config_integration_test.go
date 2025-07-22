//go:build integration
// +build integration

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIntegrationLoadLLMConfig(t *testing.T) {
	var cfg LLMConfig
	err := Load("llm_test", &cfg)
	assert.NoError(t, err)

	assert.Equal(t, "test_provider", cfg.Provider)
	assert.Equal(t, "test_model", cfg.Model)
	assert.Equal(t, "test_api_key_123", cfg.APIKey)
	assert.Equal(t, "http://localhost:8080/test", cfg.Endpoint)
}
