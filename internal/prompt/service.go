package prompt

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// Phase represents a fuzzing phase
type Phase string

const (
	PhaseGenerate     Phase = "generate"
	PhaseConstraint   Phase = "constraint"
	PhaseCompileError Phase = "compile_error"
	PhaseMutate       Phase = "mutate"
)

// PromptService manages prompt assembly and provides unified API for getting prompts
type PromptService struct {
	baseDir       string // Directory containing base prompts (e.g., "prompts/base")
	understanding string // Content of understanding.md (background context)
	builder       *Builder
}

// NewPromptService creates a new PromptService
// baseDir: directory containing base/*.md prompts
// understandingPath: path to understanding.md (can be empty)
// builder: Builder instance for generating user prompts
func NewPromptService(baseDir, understandingPath string, builder *Builder) (*PromptService, error) {
	if builder == nil {
		return nil, fmt.Errorf("builder must not be nil")
	}

	// Set default base dir if not provided
	if baseDir == "" {
		baseDir = "prompts/base"
	}

	s := &PromptService{
		baseDir: baseDir,
		builder: builder,
	}

	// Load understanding if path provided
	if understandingPath != "" {
		content, err := os.ReadFile(understandingPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to read understanding: %w", err)
			}
			// File doesn't exist - that's okay
		} else {
			s.understanding = string(content)
		}
	}

	return s, nil
}

// GetSystemPrompt returns the assembled system prompt for a given phase
// Assembly order: basePrompt + "\n\n" + understanding
func (s *PromptService) GetSystemPrompt(phase Phase) (string, error) {
	// Load base prompt for this phase
	basePath := filepath.Join(s.baseDir, string(phase)+".md")
	baseContent, err := os.ReadFile(basePath)
	if err != nil {
		return "", fmt.Errorf("failed to read base prompt %s: %w", basePath, err)
	}

	// Assemble: base + understanding
	result := string(baseContent)

	if s.understanding != "" {
		result += "\n\n" + s.understanding
	}

	return result, nil
}

// GetConstraintPrompt returns (system, user) prompts for constraint solving
func (s *PromptService) GetConstraintPrompt(ctx *TargetContext) (string, string, error) {
	systemPrompt, err := s.GetSystemPrompt(PhaseConstraint)
	if err != nil {
		return "", "", err
	}

	userPrompt, err := s.builder.BuildConstraintSolvingPrompt(ctx)
	if err != nil {
		return "", "", err
	}

	return systemPrompt, userPrompt, nil
}

// GetRefinedPrompt returns (system, user) prompts for divergence-based retry
func (s *PromptService) GetRefinedPrompt(ctx *TargetContext, div *DivergenceInfo) (string, string, error) {
	systemPrompt, err := s.GetSystemPrompt(PhaseConstraint)
	if err != nil {
		return "", "", err
	}

	userPrompt, err := s.builder.BuildRefinedPrompt(ctx, div)
	if err != nil {
		return "", "", err
	}

	return systemPrompt, userPrompt, nil
}

// GetCompileErrorPrompt returns (system, user) prompts for compile error retry
func (s *PromptService) GetCompileErrorPrompt(ctx *TargetContext, errInfo *CompileErrorInfo) (string, string, error) {
	systemPrompt, err := s.GetSystemPrompt(PhaseCompileError)
	if err != nil {
		return "", "", err
	}

	userPrompt, err := s.builder.BuildCompileErrorRetryPrompt(ctx, errInfo)
	if err != nil {
		return "", "", err
	}

	return systemPrompt, userPrompt, nil
}

// GetMutatePrompt returns (system, user) prompts for mutation
func (s *PromptService) GetMutatePrompt(basePath string, mutationCtx *MutationContext) (string, string, error) {
	systemPrompt, err := s.GetSystemPrompt(PhaseMutate)
	if err != nil {
		return "", "", err
	}

	userPrompt, err := s.builder.BuildMutatePrompt(nil, mutationCtx)
	if err != nil {
		return "", "", err
	}

	return systemPrompt, userPrompt, nil
}

// GetGeneratePrompt returns (system, user) prompts for seed generation
func (s *PromptService) GetGeneratePrompt(basePath string) (string, string, error) {
	systemPrompt, err := s.GetSystemPrompt(PhaseGenerate)
	if err != nil {
		return "", "", err
	}

	userPrompt, err := s.builder.BuildGeneratePrompt(basePath)
	if err != nil {
		return "", "", err
	}

	return systemPrompt, userPrompt, nil
}

// ParseLLMResponse parses LLM response into a seed
// This is a convenience wrapper around builder.ParseLLMResponse
func (s *PromptService) ParseLLMResponse(response string) (*seed.Seed, error) {
	return s.builder.ParseLLMResponse(response)
}
