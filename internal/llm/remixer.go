package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// RemixerClient implements the LLM interface using the internal remixer
// for weighted-random multi-model LLM selection.
type RemixerClient struct {
	remixer     *remixerEngine
	temperature float64
}

// NewRemixerClient creates a new RemixerClient from a config file path and default temperature.
func NewRemixerClient(configPath string, temperature float64) (*RemixerClient, error) {
	r, err := newRemixerEngine(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create remixer: %w", err)
	}
	if temperature <= 0 {
		temperature = 0.1
	}
	return &RemixerClient{
		remixer:     r,
		temperature: temperature,
	}, nil
}

// GetCompletion sends a raw prompt to the LLM and gets a direct response.
func (c *RemixerClient) GetCompletion(prompt string) (string, error) {
	return c.GetCompletionWithSystem("", prompt)
}

// GetCompletionWithSystem sends a prompt with system context to the LLM.
func (c *RemixerClient) GetCompletionWithSystem(systemPrompt, userPrompt string) (string, error) {
	var messages []remixerMessage

	if systemPrompt != "" {
		messages = append(messages, remixerMessage{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	messages = append(messages, remixerMessage{
		Role:    "user",
		Content: userPrompt,
	})

	temp := c.temperature
	result, err := c.remixer.Chat(context.Background(), remixerChatRequest{
		Messages:    messages,
		Temperature: &temp,
	})
	if err != nil {
		return "", fmt.Errorf("remixer chat failed: %w", err)
	}

	return strings.TrimSpace(result.Content), nil
}

// Understand processes the initial prompt and returns the LLM's summary.
func (c *RemixerClient) Understand(prompt string) (string, error) {
	return c.GetCompletion(prompt)
}

// Generate creates a new seed based on the provided context.
func (c *RemixerClient) Generate(understanding, prompt string) (*seed.Seed, error) {
	completion, err := c.GetCompletionWithSystem(understanding, prompt)
	if err != nil {
		return nil, err
	}

	sourceCode, testCases, err := seed.ParseSeedFromLLMResponse(completion)
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	return &seed.Seed{
		Content:   sourceCode,
		TestCases: testCases,
	}, nil
}

// Analyze interprets the feedback from a seed execution.
func (c *RemixerClient) Analyze(understanding, prompt string, s *seed.Seed, feedback string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("seed cannot be nil")
	}

	analysisPrompt := fmt.Sprintf("%s\n\nSeed Content:\n%s\n\nExecution Feedback:\n%s",
		prompt, s.Content, feedback)

	return c.GetCompletionWithSystem(understanding, analysisPrompt)
}

// Mutate modifies an existing seed to create a new variant.
func (c *RemixerClient) Mutate(understanding, prompt string, s *seed.Seed) (*seed.Seed, error) {
	if s == nil {
		return nil, fmt.Errorf("seed cannot be nil")
	}

	completion, err := c.GetCompletionWithSystem(understanding, prompt)
	if err != nil {
		return nil, err
	}

	sourceCode, testCases, err := seed.ParseSeedFromLLMResponse(completion)
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	return &seed.Seed{
		Meta:      s.Meta,
		Content:   sourceCode,
		TestCases: testCases,
	}, nil
}
