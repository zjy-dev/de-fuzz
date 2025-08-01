package llm

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"defuzz/internal/config"
	"defuzz/internal/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("should return DeepSeek client when provider is deepseek", func(t *testing.T) {
		cfg := &config.Config{
			LLM: config.LLMConfig{
				Provider: "deepseek",
			},
		}
		llm, err := New(cfg)
		require.NoError(t, err)
		assert.IsType(t, &DeepSeekClient{}, llm)
	})

	t.Run("should return error for unsupported provider", func(t *testing.T) {
		cfg := &config.Config{
			LLM: config.LLMConfig{
				Provider: "unsupported",
			},
		}
		_, err := New(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported llm provider")
	})
}

func TestDeepSeekClient_GetCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "  mocked response  "}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL)

	completion, err := client.GetCompletion("test prompt")
	require.NoError(t, err)
	assert.Equal(t, "mocked response", completion)
}

func TestDeepSeekClient_GetCompletion_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL)

	_, err := client.GetCompletion("test prompt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api request failed with status 500")
}

func TestDeepSeekClient_Understand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "understanding response"}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL)

	result, err := client.Understand("test prompt")
	require.NoError(t, err)
	assert.Equal(t, "understanding response", result)
}

func TestDeepSeekClient_Generate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "generated code content"}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL)

	result, err := client.Generate("test prompt", seed.SeedTypeC)
	require.NoError(t, err)
	assert.Equal(t, seed.SeedTypeC, result.Type)
	assert.Equal(t, "generated code content", result.Content)
	assert.Contains(t, result.Makefile, "gcc")
}

func TestDeepSeekClient_Analyze(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "analysis result"}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL)
	testSeed := &seed.Seed{
		ID:      "test-seed",
		Type:    "c",
		Content: "int main() { return 0; }",
	}

	result, err := client.Analyze("analyze this", testSeed, "execution feedback")
	require.NoError(t, err)
	assert.Equal(t, "analysis result", result)
}

func TestDeepSeekClient_Mutate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "mutated code content"}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL)
	originalSeed := &seed.Seed{
		ID:       "original-seed",
		Type:     "c",
		Content:  "original content",
		Makefile: "original makefile",
	}

	result, err := client.Mutate("mutate this", originalSeed)
	require.NoError(t, err)
	assert.Equal(t, originalSeed.ID, result.ID)
	assert.Equal(t, originalSeed.Type, result.Type)
	assert.Equal(t, "mutated code content", result.Content)
	assert.Equal(t, originalSeed.Makefile, result.Makefile)
}
