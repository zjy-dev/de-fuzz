package fuzz

import (
	"fmt"
	"log"
	"time"

	"github.com/zjy-dev/de-fuzz/internal/compiler"
	"github.com/zjy-dev/de-fuzz/internal/corpus"
	"github.com/zjy-dev/de-fuzz/internal/coverage"
	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
)

// EngineConfig holds the configuration for the fuzzing engine.
type EngineConfig struct {
	// Core components
	Corpus   corpus.Manager
	Compiler compiler.Compiler
	Executor executor.Executor
	Coverage coverage.Coverage
	Oracle   oracle.Oracle
	LLM      llm.LLM

	// Prompt builder for LLM interactions
	PromptBuilder *prompt.Builder

	// Understanding context for LLM
	Understanding string

	// Fuzzing parameters
	MaxIterations   int           // Maximum number of fuzzing iterations (0 = unlimited)
	MaxNewSeeds     int           // Max new seeds to generate per interesting seed
	SaveInterval    time.Duration // How often to save state
	CoverageTimeout int           // Timeout for coverage measurement in seconds
}

// Engine orchestrates the main fuzzing loop.
type Engine struct {
	cfg            EngineConfig
	iterationCount int
	bugsFound      []*oracle.Bug
	startTime      time.Time
}

// NewEngine creates a new fuzzing engine.
func NewEngine(cfg EngineConfig) *Engine {
	return &Engine{
		cfg:       cfg,
		bugsFound: make([]*oracle.Bug, 0),
	}
}

// Run starts the main fuzzing loop.
func (e *Engine) Run() error {
	e.startTime = time.Now()
	log.Println("[Engine] Starting fuzzing loop...")

	// Note: Corpus initialization and recovery should be done by the caller
	// to allow for initial seed loading. If corpus is empty, the caller
	// should load initial seeds before calling Run().

	log.Printf("[Engine] Corpus has %d seeds in queue", e.cfg.Corpus.Len())

	// Main fuzzing loop
	for {
		// Check iteration limit
		if e.cfg.MaxIterations > 0 && e.iterationCount >= e.cfg.MaxIterations {
			log.Printf("[Engine] Reached max iterations (%d), stopping", e.cfg.MaxIterations)
			break
		}

		// Get next seed from corpus
		currentSeed, ok := e.cfg.Corpus.Next()
		if !ok {
			log.Println("[Engine] No more seeds in queue, stopping")
			break
		}

		e.iterationCount++
		log.Printf("[Engine] Iteration %d: Processing seed ID=%d", e.iterationCount, currentSeed.Meta.ID)

		// Process the seed
		result, err := e.processSeed(currentSeed)
		if err != nil {
			log.Printf("[Engine] Error processing seed %d: %v", currentSeed.Meta.ID, err)
			// Report as error state
			e.cfg.Corpus.ReportResult(currentSeed.Meta.ID, corpus.FuzzResult{
				State: seed.SeedStateTimeout,
			})
			continue
		}

		// Report result to corpus
		if err := e.cfg.Corpus.ReportResult(currentSeed.Meta.ID, *result); err != nil {
			log.Printf("[Engine] Error reporting result for seed %d: %v", currentSeed.Meta.ID, err)
		}

		// Save state periodically
		if e.iterationCount%10 == 0 {
			if err := e.cfg.Corpus.Save(); err != nil {
				log.Printf("[Engine] Warning: failed to save state: %v", err)
			}
		}
	}

	// Final save
	if err := e.cfg.Corpus.Save(); err != nil {
		log.Printf("[Engine] Warning: failed to save final state: %v", err)
	}

	e.printSummary()
	return nil
}

// processSeed handles a single seed: compile, execute, measure coverage, analyze.
func (e *Engine) processSeed(s *seed.Seed) (*corpus.FuzzResult, error) {
	startTime := time.Now()

	// Step 1: Compile
	log.Printf("[Engine] Compiling seed %d...", s.Meta.ID)
	compileResult, err := e.cfg.Compiler.Compile(s)
	if err != nil {
		return nil, fmt.Errorf("compilation failed: %w", err)
	}

	if !compileResult.Success {
		log.Printf("[Engine] Seed %d failed to compile: %s", s.Meta.ID, compileResult.Stderr)
		return &corpus.FuzzResult{
			State:      seed.SeedStateProcessed,
			ExecTimeUs: time.Since(startTime).Microseconds(),
		}, nil
	}

	// Step 2: Execute
	log.Printf("[Engine] Executing seed %d...", s.Meta.ID)
	execResults, err := e.cfg.Executor.Execute(s, compileResult.BinaryPath)
	if err != nil {
		return nil, fmt.Errorf("execution failed: %w", err)
	}

	// Step 3: Measure coverage
	log.Printf("[Engine] Measuring coverage for seed %d...", s.Meta.ID)
	report, err := e.cfg.Coverage.Measure(s)
	if err != nil {
		log.Printf("[Engine] Coverage measurement failed: %v", err)
	}

	// Step 4: Check if coverage increased
	var coverageIncreased bool
	var newCoverage uint64
	var coverageIncrease *coverage.CoverageIncrease
	if report != nil {
		coverageIncreased, err = e.cfg.Coverage.HasIncreased(report)
		if err != nil {
			log.Printf("[Engine] Failed to check coverage increase: %v", err)
		}

		if coverageIncreased {
			log.Printf("[Engine] Seed %d increased coverage!", s.Meta.ID)

			// Get the coverage increase details BEFORE merging
			coverageIncrease, err = e.cfg.Coverage.GetIncrease(report)
			if err != nil {
				log.Printf("[Engine] Failed to get coverage increase details: %v", err)
			} else {
				log.Printf("[Engine] Coverage increase: %s", coverageIncrease.Summary)
			}

			// Merge the coverage
			if err := e.cfg.Coverage.Merge(report); err != nil {
				log.Printf("[Engine] Failed to merge coverage: %v", err)
			}

			// Print current total coverage stats
			if stats, err := e.cfg.Coverage.GetStats(); err == nil {
				log.Printf("[Engine] Total coverage: %.1f%% (%d/%d lines)",
					stats.CoveragePercentage, stats.TotalCoveredLines, stats.TotalLines)
			}

			// Generate new seeds from this interesting seed with coverage context
			e.generateNewSeeds(s, report, coverageIncrease)
		}
	}

	// Step 5: Analyze for bugs using Oracle
	if e.cfg.Oracle != nil {
		// Convert executor results to oracle results
		oracleResults := make([]oracle.Result, len(execResults))
		for i, r := range execResults {
			oracleResults[i] = oracle.Result{
				Stdout:   r.Stdout,
				Stderr:   r.Stderr,
				ExitCode: r.ExitCode,
			}
		}

		// Create analyze context for active oracles
		ctx := &oracle.AnalyzeContext{
			BinaryPath: compileResult.BinaryPath,
			Executor:   executor.NewOracleExecutorAdapter(e.cfg.CoverageTimeout),
		}

		bug, err := e.cfg.Oracle.Analyze(s, ctx, oracleResults)
		if err != nil {
			log.Printf("[Engine] Analysis failed: %v", err)
		} else if bug != nil {
			log.Printf("[Engine] BUG FOUND in seed %d: %s", s.Meta.ID, bug.Description)
			e.bugsFound = append(e.bugsFound, bug)
		}
	}

	// Determine final state
	state := seed.SeedStateProcessed
	for _, r := range execResults {
		if r.ExitCode != 0 {
			// Check if it's a crash
			if oracle.IsCrashExit(r.ExitCode) {
				state = seed.SeedStateCrash
				break
			}
		}
	}

	return &corpus.FuzzResult{
		State:       state,
		ExecTimeUs:  time.Since(startTime).Microseconds(),
		NewCoverage: newCoverage,
	}, nil
}

// generateNewSeeds uses LLM to create new seeds from an interesting seed.
// It uses coverage information to guide the mutation.
func (e *Engine) generateNewSeeds(parentSeed *seed.Seed, report coverage.Report, coverageIncrease *coverage.CoverageIncrease) {
	if e.cfg.LLM == nil || e.cfg.PromptBuilder == nil {
		return
	}

	maxNew := e.cfg.MaxNewSeeds
	if maxNew <= 0 {
		maxNew = 3 // Default
	}

	log.Printf("[Engine] Generating %d new seeds from parent %d...", maxNew, parentSeed.Meta.ID)

	// Build mutation context from coverage information
	var mutationCtx *prompt.MutationContext
	if coverageIncrease != nil {
		// Get current total stats
		stats, err := e.cfg.Coverage.GetStats()
		if err != nil {
			log.Printf("[Engine] Failed to get coverage stats: %v", err)
		}

		mutationCtx = &prompt.MutationContext{
			CoverageIncreaseSummary: coverageIncrease.Summary,
			CoverageIncreaseDetails: coverageIncrease.FormattedReport,
			UncoveredAbstract:       coverageIncrease.UncoveredAbstract,
		}

		if stats != nil {
			mutationCtx.TotalCoveragePercentage = stats.CoveragePercentage
			mutationCtx.TotalCoveredLines = stats.TotalCoveredLines
			mutationCtx.TotalLines = stats.TotalLines
		}
	}

	for i := 0; i < maxNew; i++ {
		// Build mutation prompt with coverage context
		mutatePrompt, err := e.cfg.PromptBuilder.BuildMutatePrompt(parentSeed, mutationCtx)
		if err != nil {
			log.Printf("[Engine] Failed to build mutate prompt: %v", err)
			continue
		}

		// Call LLM with understanding as system prompt
		completion, err := e.cfg.LLM.GetCompletionWithSystem(e.cfg.Understanding, mutatePrompt)
		if err != nil {
			log.Printf("[Engine] Failed to get LLM completion: %v", err)
			continue
		}

		// Parse LLM response using PromptBuilder (handles function template mode)
		newSeed, err := e.cfg.PromptBuilder.ParseLLMResponse(completion)
		if err != nil {
			log.Printf("[Engine] Failed to parse LLM response: %v", err)
			continue
		}

		// Set lineage information
		newSeed.Meta.ParentID = parentSeed.Meta.ID
		newSeed.Meta.Depth = parentSeed.Meta.Depth + 1

		// Add to corpus
		if err := e.cfg.Corpus.Add(newSeed); err != nil {
			log.Printf("[Engine] Failed to add new seed to corpus: %v", err)
			continue
		}

		log.Printf("[Engine] Generated new seed %d from parent %d", newSeed.Meta.ID, parentSeed.Meta.ID)
	}
}

// printSummary prints a summary of the fuzzing session.
func (e *Engine) printSummary() {
	elapsed := time.Since(e.startTime)
	log.Println("========================================")
	log.Println("           FUZZING SUMMARY")
	log.Println("========================================")
	log.Printf("Duration:     %v", elapsed)
	log.Printf("Iterations:   %d", e.iterationCount)
	log.Printf("Bugs found:   %d", len(e.bugsFound))
	log.Printf("Seeds/sec:    %.2f", float64(e.iterationCount)/elapsed.Seconds())
	log.Println("========================================")

	if len(e.bugsFound) > 0 {
		log.Println("Bugs:")
		for i, bug := range e.bugsFound {
			log.Printf("  [%d] Seed %d: %s", i+1, bug.Seed.Meta.ID, bug.Description)
		}
	}
}

// GetBugs returns all bugs found during fuzzing.
func (e *Engine) GetBugs() []*oracle.Bug {
	return e.bugsFound
}

// GetIterationCount returns the number of iterations completed.
func (e *Engine) GetIterationCount() int {
	return e.iterationCount
}
