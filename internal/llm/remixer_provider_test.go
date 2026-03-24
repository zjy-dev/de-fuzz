package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/sashabaranov/go-openai"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newJSONResponse(t *testing.T, statusCode int, payload any) *http.Response {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	return &http.Response{
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}
}

func testOpenAIProvider(t *testing.T, endpoint, model, apiKey, protocol string, transport roundTripFunc) *openAIProvider {
	t.Helper()

	p, err := newOpenAIProvider(remixerProviderConfig{
		Type:     "openai",
		Endpoint: endpoint,
		Model:    model,
		APIKey:   apiKey,
		Protocol: protocol,
	})
	if err != nil {
		t.Fatalf("creating provider: %v", err)
	}

	httpClient := &http.Client{Transport: transport}
	openAIConfig := openai.DefaultConfig(apiKey)
	openAIConfig.BaseURL = p.baseURL
	openAIConfig.HTTPClient = httpClient

	p.client = openai.NewClientWithConfig(openAIConfig)
	p.httpClient = httpClient
	return p
}

func testAnthropicProvider(t *testing.T, endpoint, model, apiKey string, transport roundTripFunc) *anthropicProvider {
	t.Helper()

	p := newAnthropicProvider(remixerProviderConfig{
		Type:     "anthropic",
		Endpoint: endpoint,
		Model:    model,
		APIKey:   apiKey,
	})
	p.client = anthropic.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(endpoint),
		option.WithHTTPClient(&http.Client{Transport: transport}),
	)
	return p
}

func TestOpenAIProviderChat(t *testing.T) {
	p := testOpenAIProvider(
		t,
		"https://openai.example",
		"test-model",
		"test-key",
		"",
		func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if r.URL.Path != "/v1/chat/completions" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}

			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decoding request body: %v", err)
			}
			if body["model"] != "test-model" {
				t.Errorf("expected model 'test-model', got %v", body["model"])
			}

			return newJSONResponse(t, http.StatusOK, map[string]any{
				"id":    "test-id",
				"model": "test-model",
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": "Hello from mock!",
						},
						"finish_reason": "stop",
					},
				},
			}), nil
		},
	)

	resp, err := p.Chat(context.Background(), remixerChatRequest{
		Messages: []remixerMessage{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
	if resp.Content != "Hello from mock!" {
		t.Errorf("expected 'Hello from mock!', got %q", resp.Content)
	}
	if resp.Model != "test-model" {
		t.Errorf("expected model 'test-model', got %q", resp.Model)
	}
}

func TestOpenAIProviderNormalizesLegacyEndpoint(t *testing.T) {
	p := testOpenAIProvider(
		t,
		"https://openai.example/v1/chat/completions",
		"test-model",
		"test-key",
		"",
		func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/chat/completions" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}

			return newJSONResponse(t, http.StatusOK, map[string]any{
				"model": "test-model",
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"role":    "assistant",
							"content": "ok",
						},
					},
				},
			}), nil
		},
	)

	_, err := p.Chat(context.Background(), remixerChatRequest{
		Messages: []remixerMessage{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
}

func TestOpenAIProviderResponses(t *testing.T) {
	p := testOpenAIProvider(
		t,
		"https://openai.example",
		"gpt-5.4",
		"test-key",
		openAIProtocolResponses,
		func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/responses" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Errorf("unexpected authorization header: %s", got)
			}

			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decoding request body: %v", err)
			}
			if body["instructions"] != "Be helpful" {
				t.Errorf("unexpected instructions: %v", body["instructions"])
			}
			if _, ok := body["temperature"]; ok {
				t.Errorf("did not expect temperature for GPT-5 responses request")
			}
			if _, ok := body["max_output_tokens"]; ok {
				t.Errorf("did not expect max_output_tokens for responses request")
			}

			input, ok := body["input"].([]any)
			if !ok || len(input) != 2 {
				t.Fatalf("expected 2 input messages, got %#v", body["input"])
			}

			return newJSONResponse(t, http.StatusOK, map[string]any{
				"model": "gpt-5.4",
				"output": []map[string]any{
					{
						"type": "message",
						"content": []map[string]any{
							{
								"type": "output_text",
								"text": "Hello from responses!",
							},
						},
					},
				},
			}), nil
		},
	)

	temp := 0.1
	resp, err := p.Chat(context.Background(), remixerChatRequest{
		Messages: []remixerMessage{
			{Role: "system", Content: "Be helpful"},
			{Role: "assistant", Content: "Earlier context"},
			{Role: "user", Content: "Hello"},
		},
		Temperature: &temp,
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
	if resp.Content != "Hello from responses!" {
		t.Errorf("expected 'Hello from responses!', got %q", resp.Content)
	}
}

func TestOpenAIProviderResponsesSSE(t *testing.T) {
	p := testOpenAIProvider(
		t,
		"https://openai.example",
		"gpt-5.4",
		"test-key",
		openAIProtocolResponses,
		func(r *http.Request) (*http.Response, error) {
			payload := "" +
				"event: response.created\n" +
				"data: {\"type\":\"response.created\",\"response\":{\"model\":\"gpt-5.4\",\"output\":[]}}\n\n" +
				"event: response.completed\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-5.4\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}]}}\n\n"

			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{"text/event-stream"},
				},
				Body: io.NopCloser(strings.NewReader(payload)),
			}, nil
		},
	)

	resp, err := p.Chat(context.Background(), remixerChatRequest{
		Messages: []remixerMessage{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
	if resp.Content != "OK" {
		t.Fatalf("expected SSE response content OK, got %q", resp.Content)
	}
}

func TestOpenAIProviderResponsesDefaultInstructions(t *testing.T) {
	p := testOpenAIProvider(
		t,
		"https://openai.example",
		"gpt-5.4",
		"test-key",
		openAIProtocolResponses,
		func(r *http.Request) (*http.Response, error) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decoding request body: %v", err)
			}
			if body["instructions"] != defaultResponsesInstruction {
				t.Fatalf("expected default instructions %q, got %v", defaultResponsesInstruction, body["instructions"])
			}

			return newJSONResponse(t, http.StatusOK, map[string]any{
				"model": "gpt-5.4",
				"output": []map[string]any{
					{
						"type": "message",
						"content": []map[string]any{
							{
								"type": "output_text",
								"text": "OK",
							},
						},
					},
				},
			}), nil
		},
	)

	_, err := p.Chat(context.Background(), remixerChatRequest{
		Messages: []remixerMessage{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
}

func TestAnthropicProviderChat(t *testing.T) {
	p := testAnthropicProvider(
		t,
		"https://anthropic.example",
		"claude-test",
		"test-ant-key",
		func(r *http.Request) (*http.Response, error) {
			if got := r.Header.Get("X-Api-Key"); got != "test-ant-key" {
				t.Errorf("unexpected API key header: %s", got)
			}

			return newJSONResponse(t, http.StatusOK, map[string]any{
				"id":    "msg_test",
				"type":  "message",
				"role":  "assistant",
				"model": "claude-test",
				"content": []map[string]any{
					{
						"type": "text",
						"text": "Hello from Anthropic mock!",
					},
				},
				"stop_reason": "end_turn",
			}), nil
		},
	)

	resp, err := p.Chat(context.Background(), remixerChatRequest{
		Messages: []remixerMessage{
			{Role: "system", Content: "Be helpful"},
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
	if resp.Content != "Hello from Anthropic mock!" {
		t.Errorf("expected anthropic content, got %q", resp.Content)
	}
}
