package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

const (
	// DefaultMiniMaxEndpoint is the default API endpoint for MiniMax M2.1
	// Using standard MiniMax API format (OpenAI-compatible)
	DefaultMiniMaxEndpoint = "https://api.minimaxi.com/v1/text/chatcompletion_v2"
)

// MiniMaxClient implements the LLM interface for MiniMax M2.1
type MiniMaxClient struct {
	apiKey      string
	model       string
	endpoint    string
	temperature float64
	client      *http.Client
}

// NewMiniMaxClient creates a new client for the MiniMax API.
func NewMiniMaxClient(apiKey, model, endpoint string, temperature float64) *MiniMaxClient {
	if endpoint == "" {
		endpoint = DefaultMiniMaxEndpoint
	}
	if temperature <= 0 {
		temperature = 0.7 // Default temperature
	}
	return &MiniMaxClient{
		apiKey:      apiKey,
		model:       model,
		endpoint:    endpoint,
		temperature: temperature,
		client:      &http.Client{},
	}
}

// GetCompletion sends a prompt to the MiniMax API and returns the response.
func (c *MiniMaxClient) GetCompletion(prompt string) (string, error) {
	return c.GetCompletionWithSystem("", prompt)
}

// GetCompletionWithSystem sends a prompt with system context to the MiniMax API.
// Uses standard MiniMax API format (OpenAI-compatible).
func (c *MiniMaxClient) GetCompletionWithSystem(systemPrompt, userPrompt string) (string, error) {
	messages := []map[string]string{}

	// Add system message if provided
	if systemPrompt != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": systemPrompt,
		})
	}

	// Add user message
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": userPrompt,
	})

	// Build request body (OpenAI-compatible format)
	reqBody := map[string]interface{}{
		"model":       c.model,
		"messages":    messages,
		"temperature": c.temperature,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", c.endpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		return "", fmt.Errorf("api request failed with status %d: %s", resp.StatusCode, buf.String())
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response body: %w", err)
	}

	// Parse response (OpenAI format)
	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if message, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := message["content"].(string); ok {
					return strings.TrimSpace(content), nil
				}
			}
		}
	}

	return "", fmt.Errorf("unexpected response format from API")
}

// Understand processes the initial prompt and returns the LLM's summary.
func (c *MiniMaxClient) Understand(prompt string) (string, error) {
	return c.GetCompletion(prompt)
}

// Generate creates a new seed based on the provided context.
func (c *MiniMaxClient) Generate(understanding, prompt string) (*seed.Seed, error) {
	completion, err := c.GetCompletionWithSystem(understanding, prompt)
	if err != nil {
		return nil, err
	}

	// Parse the completion to extract source code and test cases
	sourceCode, testCases, err := seed.ParseSeedFromLLMResponse(completion)
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	return &seed.Seed{
		Content:   sourceCode,
		TestCases: testCases,
	}, nil
}

// Mutate modifies an existing seed to create a new variant.
func (c *MiniMaxClient) Mutate(understanding, prompt string, s *seed.Seed) (*seed.Seed, error) {
	completion, err := c.GetCompletionWithSystem(understanding, prompt)
	if err != nil {
		return nil, err
	}

	// Parse the completion to extract mutated source code and test cases
	sourceCode, testCases, err := seed.ParseSeedFromLLMResponse(completion)
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	return &seed.Seed{
		Meta:      s.Meta, // Preserve parent's metadata for lineage tracking
		Content:   sourceCode,
		TestCases: testCases,
	}, nil
}

// Analyze interprets the feedback from a seed execution.
func (c *MiniMaxClient) Analyze(understanding, prompt string, s *seed.Seed, feedback string) (string, error) {
	// Construct a specific prompt for analysis that includes the seed content and feedback
	analysisPrompt := fmt.Sprintf("%s\n\nSeed Content:\n%s\n\nExecution Feedback:\n%s",
		prompt, s.Content, feedback)

	return c.GetCompletionWithSystem(understanding, analysisPrompt)
}

// GetAPIKey returns the API key (for testing purposes)
func (c *MiniMaxClient) GetAPIKey() string {
	return c.apiKey
}

// GetModel returns the model name (for testing purposes)
func (c *MiniMaxClient) GetModel() string {
	return c.model
}

// GetEndpoint returns the endpoint URL (for testing purposes)
func (c *MiniMaxClient) GetEndpoint() string {
	return c.endpoint
}

// GetTemperature returns the temperature setting (for testing purposes)
func (c *MiniMaxClient) GetTemperature() float64 {
	return c.temperature
}
