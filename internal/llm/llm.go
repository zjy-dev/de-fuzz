package llm

import (
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// LLM defines the interface for interacting with a Large Language Model.
type LLM interface {
	// GetCompletion sends a raw prompt to the LLM and gets a direct response.
	GetCompletion(prompt string) (string, error)

	// GetCompletionWithSystem sends a prompt with system context to the LLM.
	GetCompletionWithSystem(systemPrompt, userPrompt string) (string, error)

	// Understand processes the initial prompt and returns the LLM's summary.
	Understand(prompt string) (string, error)

	// Generate creates a new seed based on the provided context (understanding as system prompt).
	Generate(understanding, prompt string) (*seed.Seed, error)

	// Analyze interprets the feedback from a seed execution (understanding as system prompt).
	Analyze(understanding, prompt string, s *seed.Seed, feedback string) (string, error)

	// Mutate modifies an existing seed to create a new variant (understanding as system prompt).
	Mutate(understanding, prompt string, s *seed.Seed) (*seed.Seed, error)
}

// New creates a new LLM client backed by the internal remixer.
// configPath is the path to the remixer YAML config file.
// temperature is the default sampling temperature for all requests.
func New(configPath string, temperature float64) (LLM, error) {
	return NewRemixerClient(configPath, temperature)
}
