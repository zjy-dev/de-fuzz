package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"defuzz/internal/seed"
)

const (
	DefaultDeepSeekEndpoint = "https://api.deepseek.com/v1/chat/completions"
)

// parseSeedFromResponse extracts source code and test cases from LLM response
func parseSeedFromResponse(response string) (string, []seed.TestCase, error) {
	// Extract source code
	sourceRegex := regexp.MustCompile(`Source \(c\):\s*---\s*(.*?)\s*---`)
	sourceMatches := sourceRegex.FindStringSubmatch(response)
	if len(sourceMatches) < 2 {
		return "", nil, fmt.Errorf("could not find source code in response")
	}
	sourceCode := strings.TrimSpace(sourceMatches[1])

	// Extract test cases JSON
	testCasesRegex := regexp.MustCompile(`Test Cases \(json\):\s*---\s*(.*?)\s*---`)
	testCasesMatches := testCasesRegex.FindStringSubmatch(response)
	if len(testCasesMatches) < 2 {
		return "", nil, fmt.Errorf("could not find test cases in response")
	}
	testCasesJSON := strings.TrimSpace(testCasesMatches[1])

	// Parse test cases JSON
	var testCases []seed.TestCase
	if err := json.Unmarshal([]byte(testCasesJSON), &testCases); err != nil {
		return "", nil, fmt.Errorf("failed to parse test cases JSON: %w", err)
	}

	return sourceCode, testCases, nil
}

// DeepSeekClient implements the LLM interface for the DeepSeek model.
type DeepSeekClient struct {
	apiKey      string
	model       string
	endpoint    string
	temperature float64
	client      *http.Client
}

// NewDeepSeekClient creates a new client for the DeepSeek API.
func NewDeepSeekClient(apiKey, model, endpoint string, temperature float64) *DeepSeekClient {
	if endpoint == "" {
		endpoint = DefaultDeepSeekEndpoint
	}
	if temperature <= 0 {
		temperature = 0.7 // Default temperature
	}
	return &DeepSeekClient{
		apiKey:      apiKey,
		model:       model,
		endpoint:    endpoint,
		temperature: temperature,
		client:      &http.Client{},
	}
}

// GetCompletion sends a prompt to the DeepSeek API and returns the response.
func (c *DeepSeekClient) GetCompletion(prompt string) (string, error) {
	return c.GetCompletionWithSystem("", prompt)
}

// GetCompletionWithSystem sends a prompt with system context to the DeepSeek API.
func (c *DeepSeekClient) GetCompletionWithSystem(systemPrompt, userPrompt string) (string, error) {
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
func (c *DeepSeekClient) Generate(understanding, prompt string) (*seed.Seed, error) {
	completion, err := c.GetCompletionWithSystem(understanding, prompt)
	if err != nil {
		return nil, err
	}

	// Parse the completion to extract source code and test cases
	sourceCode, testCases, err := parseSeedFromResponse(completion)
	if err != nil {
		// Fallback to using raw completion as source if parsing fails
		return &seed.Seed{
			Content:   completion,
			TestCases: []seed.TestCase{{RunningCommand: "./prog", ExpectedResult: "success"}}, // Default test case
		}, nil
	}

	return &seed.Seed{
		Content:   sourceCode,
		TestCases: testCases,
	}, nil
}

// Mutate modifies an existing seed to create a new variant.
func (c *DeepSeekClient) Mutate(understanding, prompt string, s *seed.Seed) (*seed.Seed, error) {
	completion, err := c.GetCompletionWithSystem(understanding, prompt)
	if err != nil {
		return nil, err
	}

	// Parse the completion to extract mutated source code and test cases
	sourceCode, testCases, err := parseSeedFromResponse(completion)
	if err != nil {
		// Fallback: only update the content if parsing fails
		return &seed.Seed{
			ID:        s.ID,
			Content:   completion,  // Use raw completion as mutated source
			TestCases: s.TestCases, // Keep original test cases
		}, nil
	}

	return &seed.Seed{
		ID:        s.ID, // Keep the original ID for tracking
		Content:   sourceCode,
		TestCases: testCases,
	}, nil
}

// Analyze interprets the feedback from a seed execution.
func (c *DeepSeekClient) Analyze(understanding, prompt string, s *seed.Seed, feedback string) (string, error) {
	// Construct a specific prompt for analysis that includes the seed content and feedback
	analysisPrompt := fmt.Sprintf("%s\n\nSeed Content:\n%s\n\nExecution Feedback:\n%s",
		prompt, s.Content, feedback)

	return c.GetCompletionWithSystem(understanding, analysisPrompt)
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

// GetTemperature returns the temperature setting (for testing purposes)
func (c *DeepSeekClient) GetTemperature() float64 {
	return c.temperature
}
