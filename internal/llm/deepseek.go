package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"defuzz/internal/seed"
)

const (
	DefaultDeepSeekEndpoint = "https://api.deepseek.com/v1/chat/completions"
)

// DeepSeekClient implements the LLM interface for the DeepSeek model.
type DeepSeekClient struct {
	apiKey   string
	model    string
	endpoint string
	client   *http.Client
}

// NewDeepSeekClient creates a new client for the DeepSeek API.
func NewDeepSeekClient(apiKey, model, endpoint string) *DeepSeekClient {
	if endpoint == "" {
		endpoint = DefaultDeepSeekEndpoint
	}
	return &DeepSeekClient{
		apiKey:   apiKey,
		model:    model,
		endpoint: endpoint,
		client:   &http.Client{},
	}
}

// GetCompletion sends a prompt to the DeepSeek API and returns the response.
func (c *DeepSeekClient) GetCompletion(prompt string) (string, error) {
	reqBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", c.endpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("api request failed with status %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response body: %w", err)
	}

	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if message, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := message["content"].(string); ok {
					return strings.TrimSpace(content), nil
				}
			}
		}
	}

	return "", fmt.Errorf("unexpected response format from api")
}

// Understand processes the initial prompt and returns the LLM's summary.
func (c *DeepSeekClient) Understand(prompt string) (string, error) {
	return c.GetCompletion(prompt)
}

// Generate creates a new seed based on the provided context.
func (c *DeepSeekClient) Generate(prompt string, seedType seed.SeedType) (*seed.Seed, error) {
	completion, err := c.GetCompletion(prompt)
	if err != nil {
		return nil, err
	}
	// TODO: A more robust implementation would parse the completion
	// to separate the source from the Makefile.
	return &seed.Seed{
		Type:     seedType,
		Content:  completion,
		Makefile: "all:\n\tgcc source.c -o prog", // Placeholder
	}, nil
}

// Mutate modifies an existing seed to create a new variant.
func (c *DeepSeekClient) Mutate(prompt string, s *seed.Seed) (*seed.Seed, error) {
	completion, err := c.GetCompletion(prompt)
	if err != nil {
		return nil, err
	}
	return &seed.Seed{
		ID:       s.ID, // Keep the original ID for tracking
		Type:     s.Type,
		Content:  completion,
		Makefile: s.Makefile, // Assume Makefile doesn't change for now
	}, nil
}

// Analyze interprets the feedback from a seed execution.
func (c *DeepSeekClient) Analyze(prompt string, s *seed.Seed, feedback string) (string, error) {
	// Construct a specific prompt for analysis that includes the seed content and feedback
	analysisPrompt := fmt.Sprintf("%s\n\nSeed Content:\n%s\n\nExecution Feedback:\n%s",
		prompt, s.Content, feedback)

	return c.GetCompletion(analysisPrompt)
}

// GetAPIKey returns the API key (for testing purposes)
func (c *DeepSeekClient) GetAPIKey() string {
	return c.apiKey
}

// GetModel returns the model name (for testing purposes)
func (c *DeepSeekClient) GetModel() string {
	return c.model
}

// GetEndpoint returns the endpoint URL (for testing purposes)
func (c *DeepSeekClient) GetEndpoint() string {
	return c.endpoint
}
