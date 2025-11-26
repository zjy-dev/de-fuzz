package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/config"
	"github.com/zjy-dev/de-fuzz/internal/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("should return DeepSeek client when provider is deepseek", func(t *testing.T) {
		cfg := &config.Config{
			LLM: config.LLMConfig{
				Provider:    "deepseek",
				Temperature: 0.8,
			},
		}
		llm, err := New(cfg)
		require.NoError(t, err)
		assert.IsType(t, &DeepSeekClient{}, llm)

		// Verify temperature is correctly set
		deepseekClient := llm.(*DeepSeekClient)
		assert.Equal(t, 0.8, deepseekClient.GetTemperature())
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

func TestDeepSeekClient_GetCompletionWithSystem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "  system response  "}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL, 0.7)

	completion, err := client.GetCompletionWithSystem("system context", "test prompt")
	require.NoError(t, err)
	assert.Equal(t, "system response", completion)
}

func TestDeepSeekClient_GetCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "  mocked response  "}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL, 0.7)

	completion, err := client.GetCompletion("test prompt")
	require.NoError(t, err)
	assert.Equal(t, "mocked response", completion)
}

func TestDeepSeekClient_GetCompletion_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL, 0.7)

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

	client := NewDeepSeekClient("test_key", "test_model", server.URL, 0.7)

	result, err := client.Understand("test prompt")
	require.NoError(t, err)
	assert.Equal(t, "understanding response", result)
}

func TestDeepSeekClient_Generate(t *testing.T) {
	mockResponse := `int main() { return 0; }
// ||||| JSON_TESTCASES_START |||||
[
  {
    "running command": "./prog",
    "expected result": "success"
  }
]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		responseJSON, _ := json.Marshal(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": mockResponse}},
			},
		})
		w.Write(responseJSON)
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL, 0.7)

	result, err := client.Generate("system understanding", "test prompt")
	require.NoError(t, err)
	assert.Equal(t, "int main() { return 0; }", result.Content)
	assert.Len(t, result.TestCases, 1)
	assert.Equal(t, "./prog", result.TestCases[0].RunningCommand)
}

func TestDeepSeekClient_Analyze(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "analysis result"}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL, 0.7)
	testCases := []seed.TestCase{
		{RunningCommand: "./test", ExpectedResult: "success"},
	}
	testSeed := &seed.Seed{
		ID:        "test-seed",
		Content:   "int main() { return 0; }",
		TestCases: testCases,
	}

	result, err := client.Analyze("system understanding", "analyze this", testSeed, "execution feedback")
	require.NoError(t, err)
	assert.Equal(t, "analysis result", result)
}

func TestDeepSeekClient_Mutate(t *testing.T) {
	mockResponse := `int main() { return 1; }
// ||||| JSON_TESTCASES_START |||||
[
  {
    "running command": "./prog arg1",
    "expected result": "mutated output"
  }
]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		responseJSON, _ := json.Marshal(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": mockResponse}},
			},
		})
		w.Write(responseJSON)
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL, 0.7)
	originalTestCases := []seed.TestCase{
		{RunningCommand: "./test", ExpectedResult: "output"},
	}
	originalSeed := &seed.Seed{
		ID:        "original-seed",
		Meta:      seed.Metadata{ID: 123, ParentID: 0, Depth: 1},
		Content:   "original content",
		TestCases: originalTestCases,
	}

	result, err := client.Mutate("system understanding", "mutate this", originalSeed)
	require.NoError(t, err)
	// Meta should be preserved from original seed
	assert.Equal(t, originalSeed.Meta.ID, result.Meta.ID)
	assert.Equal(t, originalSeed.Meta.Depth, result.Meta.Depth)
	// Content should be mutated
	assert.Equal(t, "int main() { return 1; }", result.Content)
	// Test cases should be from the new response
	assert.Len(t, result.TestCases, 1)
	assert.Equal(t, "./prog arg1", result.TestCases[0].RunningCommand)
}

func TestNewDeepSeekClient_Temperature(t *testing.T) {
	t.Run("should use provided temperature", func(t *testing.T) {
		client := NewDeepSeekClient("test_key", "test_model", "http://test.com", 0.9)
		assert.Equal(t, 0.9, client.GetTemperature())
	})

	t.Run("should use default temperature when zero provided", func(t *testing.T) {
		client := NewDeepSeekClient("test_key", "test_model", "http://test.com", 0)
		assert.Equal(t, 0.7, client.GetTemperature())
	})

	t.Run("should use default temperature when negative provided", func(t *testing.T) {
		client := NewDeepSeekClient("test_key", "test_model", "http://test.com", -0.5)
		assert.Equal(t, 0.7, client.GetTemperature())
	})
}
