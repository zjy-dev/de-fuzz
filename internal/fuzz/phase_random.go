// Package fuzz provides the fuzzing engine for constraint solving based fuzzing.
package fuzz

import (
	"math/rand"
	"time"

	"github.com/zjy-dev/de-fuzz/internal/corpus"
	"github.com/zjy-dev/de-fuzz/internal/logger"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// RandomMutationPhase manages the random mutation phase after coverage saturation.
// In this phase, seeds are randomly selected from the corpus and mutated.
// Only seeds that trigger oracle bugs are persisted.
type RandomMutationPhase struct {
	engine           *Engine
	maxIterations    int // Maximum iterations in random phase (0 = unlimited)
	iterationCount   int
	bugsFoundInPhase int
	rng              *rand.Rand
}

// NewRandomMutationPhase creates a new random mutation phase.
func NewRandomMutationPhase(engine *Engine, maxIterations int) *RandomMutationPhase {
	return &RandomMutationPhase{
		engine:        engine,
		maxIterations: maxIterations,
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Run executes the random mutation phase.
// It randomly selects seeds from corpus and mutates them,
// persisting only those that trigger oracle bugs.
func (p *RandomMutationPhase) Run() error {
	logger.Info("Starting random mutation phase...")

	// Get all processed seeds from corpus
	processedSeeds := p.getProcessedSeeds()
	if len(processedSeeds) == 0 {
		logger.Warn("No processed seeds available for random mutation")
		return nil
	}

	for {
		// Check iteration limit
		if p.maxIterations > 0 && p.iterationCount >= p.maxIterations {
			logger.Info("Random phase: reached max iterations (%d)", p.maxIterations)
			break
		}

		p.iterationCount++

		// Select a random seed from processed seeds
		baseSeed := processedSeeds[p.rng.Intn(len(processedSeeds))]

		logger.Debug("Random phase iteration %d: mutating seed %d", p.iterationCount, baseSeed.Meta.ID)

		// Mutate and check with oracle
		bug, err := p.mutateAndCheck(baseSeed)
		if err != nil {
			logger.Warn("Random mutation failed: %v", err)
			continue
		}

		if bug != nil {
			p.bugsFoundInPhase++
			logger.Info("Random phase: BUG FOUND (total: %d)", p.bugsFoundInPhase)
		}
	}

	logger.Info("Random mutation phase complete: %d iterations, %d bugs found",
		p.iterationCount, p.bugsFoundInPhase)

	return nil
}

// getProcessedSeeds retrieves all processed seeds that are suitable for mutation.
func (p *RandomMutationPhase) getProcessedSeeds() []*seed.Seed {
	var seeds []*seed.Seed

	// Get seeds from corpus - try to get recent seeds
	fm, ok := p.engine.cfg.Corpus.(*corpus.FileManager)
	if !ok {
		logger.Warn("Cannot access corpus for random phase")
		return seeds
	}

	// Get the state manager to find seed IDs
	stateManager := fm.GetStateManager()
	if stateManager == nil {
		return seeds
	}

	state := stateManager.GetState()

	// Try to load seeds by ID (from 1 to ProcessedCount)
	processedCount := state.Stats.ProcessedCount
	for i := 1; i <= processedCount; i++ {
		s, err := fm.Get(uint64(i))
		if err == nil && s != nil {
			seeds = append(seeds, s)
		}
	}

	return seeds
}

// mutateAndCheck mutates a seed and checks if it triggers a bug.
// Returns the bug if found, nil otherwise.
func (p *RandomMutationPhase) mutateAndCheck(baseSeed *seed.Seed) (*oracle.Bug, error) {
	// Build mutation prompt using the standard mutation prompt
	mutationCtx := &prompt.MutationContext{
		TotalCoveragePercentage: float64(p.engine.cfg.Analyzer.GetBBCoverageBasisPoints()) / 100.0,
	}

	systemPrompt, userPrompt, err := p.engine.cfg.PromptService.GetMutatePrompt("", mutationCtx)
	if err != nil {
		return nil, err
	}

	// Debug: Log prompts (disabled for performance profiling)
	// logger.Debug("=== LLM Call: RandomMutationPhase ===")
	// logger.Debug("[System Prompt]:\n%s", systemPrompt)
	// logger.Debug("[User Prompt]:\n%s", userPrompt)
	// logger.Debug("=== End Prompts ===")

	// Call LLM
	completion, err := p.engine.cfg.LLM.GetCompletionWithSystem(systemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}

	// Parse response
	mutatedSeed, err := p.engine.cfg.PromptService.ParseLLMResponse(completion)
	if err != nil {
		return nil, err
	}

	// Allocate ID
	mutatedSeed.Meta.ID = p.engine.cfg.Corpus.AllocateID()
	mutatedSeed.Meta.ParentID = baseSeed.Meta.ID
	mutatedSeed.Meta.Depth = baseSeed.Meta.Depth + 1
	mutatedSeed.Meta.CreatedAt = time.Now()

	// Compile the seed
	compileResult, err := p.engine.cfg.Compiler.Compile(mutatedSeed)
	if err != nil || !compileResult.Success {
		logger.Debug("Random phase: seed %d failed to compile", mutatedSeed.Meta.ID)
		return nil, nil
	}

	// Run oracle
	if p.engine.cfg.Oracle == nil {
		return nil, nil
	}

	bug := p.engine.runOracle(mutatedSeed, compileResult.BinaryPath)
	if bug != nil {
		// Persist the seed that found a bug
		mutatedSeed.Meta.OracleVerdict = seed.OracleVerdictBug
		mutatedSeed.Meta.BugDescription = bug.Description
		if err := p.engine.cfg.Corpus.Add(mutatedSeed); err != nil {
			logger.Warn("Failed to persist bug-triggering seed: %v", err)
		}
	}

	return bug, nil
}
