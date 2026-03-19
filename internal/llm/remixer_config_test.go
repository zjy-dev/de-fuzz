package llm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRemixerConfigDefaultProtocol(t *testing.T) {
	t.Setenv("TEST_ENDPOINT", "https://api.example.com/v1")
	t.Setenv("TEST_API_KEY", "sk-test")

	configPath := writeTempRemixerConfig(t, `
models:
  - name: "test-model"
    weight: 1
    providers:
      - type: "openai"
        endpoint: "${TEST_ENDPOINT}"
        model: "gpt-4"
        api_key: "${TEST_API_KEY}"
`)

	cfg, err := loadRemixerConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := cfg.Models[0].Providers[0].Protocol; got != openAIProtocolAuto {
		t.Fatalf("expected default protocol %q, got %q", openAIProtocolAuto, got)
	}
}

func TestLoadRemixerConfigInvalidProtocol(t *testing.T) {
	configPath := writeTempRemixerConfig(t, `
models:
  - name: "test-model"
    weight: 1
    providers:
      - type: "openai"
        endpoint: "https://api.example.com"
        model: "gpt-4"
        api_key: "test-key"
        protocol: "invalid"
`)

	if _, err := loadRemixerConfig(configPath); err == nil {
		t.Fatal("expected error for invalid protocol")
	}
}

func writeTempRemixerConfig(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "remixer.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	return path
}
