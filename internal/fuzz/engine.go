package fuzz

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zjy-dev/de-fuzz/internal/compiler"
	"github.com/zjy-dev/de-fuzz/internal/corpus"
	"github.com/zjy-dev/de-fuzz/internal/coverage"
	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/logger"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
	"github.com/zjy-dev/de-fuzz/internal/state"
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

	// Metrics tracking
	Metrics state.MetricsManager

	// Prompt builder for LLM interactions
	PromptBuilder *prompt.Builder

	// Understanding context for LLM
	Understanding string

	// Divergence analyzer for execution path analysis (optional)
	// If set, enables divergence-guided mutation refinement
	DivergenceAnalyzer coverage.DivergenceAnalyzer

	// CFG-guided analyzer for target function coverage tracking (optional)
	// If set, enables UI to show target function coverage instead of global coverage
	CFGAnalyzer *coverage.CFGGuidedAnalyzer

	// CompilerPath is the path to the target compiler (used for divergence analysis)
	CompilerPath string

	// Fuzzing parameters
	MaxIterations   int           // Maximum number of fuzzing iterations (0 = unlimited)
	SaveInterval    time.Duration // How often to save state
	CoverageTimeout int           // Timeout for coverage measurement in seconds
	PrintInterval   int           // How often to print progress (in iterations, 0 = never)

	// Divergence analysis parameters
	MaxDivergenceRetries int // Max retries with divergence feedback (default: 2)

	// UI settings
	EnableUI    bool // Enable terminal UI (default: false for backward compatibility)
	UIRefreshMs int  // UI refresh rate in milliseconds (default: 200)
}

// Engine orchestrates the main fuzzing loop.
type Engine struct {
	cfg            EngineConfig
	iterationCount int
	bugsFound      []*oracle.Bug
	startTime      time.Time
	uiEnabled      bool
}

// NewEngine creates a new fuzzing engine.
func NewEngine(cfg EngineConfig) *Engine {
	e := &Engine{
		cfg:       cfg,
		bugsFound: make([]*oracle.Bug, 0),
		uiEnabled: cfg.EnableUI,
	}

	// Enable terminal UI if configured
	if cfg.EnableUI && cfg.Metrics != nil {
		cfg.Metrics.SetUIEnabled(true)
	}

	// Set target function total lines if CFGAnalyzer is configured
	if cfg.CFGAnalyzer != nil && cfg.Metrics != nil {
		totalTargetLines := cfg.CFGAnalyzer.GetTotalTargetLines()
		cfg.Metrics.SetTargetFunctionLines(totalTargetLines)
		logger.Info("Target function total lines: %d", totalTargetLines)
	}

	return e
}

// Run starts the main fuzzing loop.
func (e *Engine) Run() error {
	e.startTime = time.Now()
	logger.Info("Starting fuzzing loop...")

	// Note: Corpus initialization and recovery should be done by the caller
	// to allow for initial seed loading. If corpus is empty, the caller
	// should load initial seeds before calling Run().

	logger.Info("Corpus has %d seeds in queue", e.cfg.Corpus.Len())

	// Main fuzzing loop
	for {
		// Check iteration limit
		if e.cfg.MaxIterations > 0 && e.iterationCount >= e.cfg.MaxIterations {
			logger.Info("Reached max iterations (%d), stopping", e.cfg.MaxIterations)
			break
		}

		// Get next seed from corpus
		currentSeed, ok := e.cfg.Corpus.Next()
		if !ok {
			logger.Info("No more seeds in queue, stopping")
			break
		}

		e.iterationCount++
		logger.Info("Iteration %d: Processing seed ID=%d", e.iterationCount, currentSeed.Meta.ID)

		// Process the seed
		result, err := e.processSeed(currentSeed)
		if err != nil {
			logger.Error("Error processing seed %d: %v", currentSeed.Meta.ID, err)
			// Report as error state
			e.cfg.Corpus.ReportResult(currentSeed.Meta.ID, corpus.FuzzResult{
				State: seed.SeedStateTimeout,
			})
			continue
		}

		// Report result to corpus
		if err := e.cfg.Corpus.ReportResult(currentSeed.Meta.ID, *result); err != nil {
			logger.Error("Error reporting result for seed %d: %v", currentSeed.Meta.ID, err)
		}

		// Print progress periodically
		if e.cfg.PrintInterval > 0 && e.iterationCount%e.cfg.PrintInterval == 0 {
			e.printProgress()
		}

		// Save state periodically
		if e.iterationCount%10 == 0 {
			if err := e.cfg.Corpus.Save(); err != nil {
				logger.Warn("Failed to save state: %v", err)
			}
			// Save metrics
			if e.cfg.Metrics != nil {
				if err := e.cfg.Metrics.Save(); err != nil {
					logger.Warn("Failed to save metrics: %v", err)
				}
			}
		}
	}

	// Final save
	if err := e.cfg.Corpus.Save(); err != nil {
		logger.Warn("Failed to save final state: %v", err)
	}
	// Final metrics save
	if e.cfg.Metrics != nil {
		if err := e.cfg.Metrics.Save(); err != nil {
			logger.Warn("Failed to save final metrics: %v", err)
		}
	}

	e.printSummary()
	return nil
}

// processSeed handles a single seed: compile, execute, measure coverage, analyze.
func (e *Engine) processSeed(s *seed.Seed) (*corpus.FuzzResult, error) {
	startTime := time.Now()

	// Record seed processed
	if e.cfg.Metrics != nil {
		e.cfg.Metrics.RecordSeedProcessed()
	}

	// Step 1: Compile
	logger.Debug("Compiling seed %d...", s.Meta.ID)
	compileResult, err := e.cfg.Compiler.Compile(s)
	if err != nil {
		return nil, fmt.Errorf("compilation failed: %w", err)
	}

	if !compileResult.Success {
		logger.Warn("Seed %d failed to compile: %s", s.Meta.ID, compileResult.Stderr)
		// Record compile failure
		if e.cfg.Metrics != nil {
			e.cfg.Metrics.RecordCompileFailure()
		}
		return &corpus.FuzzResult{
			State:      seed.SeedStateProcessed,
			ExecTimeUs: time.Since(startTime).Microseconds(),
		}, nil
	}

	// Step 2: Execute
	logger.Debug("Executing seed %d...", s.Meta.ID)
	execResults, err := e.cfg.Executor.Execute(s, compileResult.BinaryPath)
	if err != nil {
		return nil, fmt.Errorf("execution failed: %w", err)
	}

	// Step 3: Measure coverage
	logger.Debug("Measuring coverage for seed %d...", s.Meta.ID)
	report, err := e.cfg.Coverage.Measure(s)
	if err != nil {
		logger.Warn("Coverage measurement failed: %v", err)
	}

	// Step 4: Check if coverage increased
	var coverageIncreased bool
	var newCoverage uint64
	var coverageIncrease *coverage.CoverageIncrease
	if report != nil {
		coverageIncreased, err = e.cfg.Coverage.HasIncreased(report)
		if err != nil {
			logger.Warn("Failed to check coverage increase: %v", err)
		}

		if coverageIncreased {
			logger.Info("Seed %d increased coverage!", s.Meta.ID)

			// Record coverage increase
			if e.cfg.Metrics != nil {
				e.cfg.Metrics.RecordCoverageIncrease()
			}

			// Get the coverage increase details BEFORE merging
			coverageIncrease, err = e.cfg.Coverage.GetIncrease(report)
			if err != nil {
				logger.Warn("Failed to get coverage increase details: %v", err)
			} else {
				logger.Info("Coverage increase: %s", coverageIncrease.Summary)
			}

			// Merge the coverage
			if err := e.cfg.Coverage.Merge(report); err != nil {
				logger.Warn("Failed to merge coverage: %v", err)
			}

			// Print current total coverage stats and update metrics
			if stats, err := e.cfg.Coverage.GetStats(); err == nil {
				logger.Info("Total coverage: %.1f%% (%d/%d lines)",
					stats.CoveragePercentage, stats.TotalCoveredLines, stats.TotalLines)
				// Update metrics with coverage stats
				if e.cfg.Metrics != nil {
					e.cfg.Metrics.UpdateCoverageStats(stats.CoveragePercentage, stats.TotalCoveredLines, stats.TotalLines)
				}
			}

			// Update target function coverage if CFGAnalyzer is configured
			if e.cfg.CFGAnalyzer != nil && e.cfg.Metrics != nil {
				coveredTargetLines := e.cfg.CFGAnalyzer.GetTotalCoveredTargetLines()
				e.cfg.Metrics.UpdateTargetCoverage(coveredTargetLines)
				logger.Debug("Target function coverage: %d lines", coveredTargetLines)
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

		// Record oracle check
		if e.cfg.Metrics != nil {
			e.cfg.Metrics.RecordOracleCheck()
		}

		bug, err := e.cfg.Oracle.Analyze(s, ctx, oracleResults)
		if err != nil {
			logger.Error("Analysis failed: %v", err)
			// Record oracle error
			if e.cfg.Metrics != nil {
				e.cfg.Metrics.RecordOracleError()
			}
		} else if bug != nil {
			logger.Error("BUG FOUND in seed %d: %s", s.Meta.ID, bug.Description)
			e.bugsFound = append(e.bugsFound, bug)
			// Record oracle failure (bug found)
			if e.cfg.Metrics != nil {
				e.cfg.Metrics.RecordOracleFailure()
			}
		}
	}

	// Determine final state
	finalState := seed.SeedStateProcessed
	for _, r := range execResults {
		if r.ExitCode != 0 {
			// Check if it's a crash
			if oracle.IsCrashExit(r.ExitCode) {
				finalState = seed.SeedStateCrash
				// Record crash
				if e.cfg.Metrics != nil {
					e.cfg.Metrics.RecordCrash()
				}
				break
			}
		}
	}

	return &corpus.FuzzResult{
		State:       finalState,
		ExecTimeUs:  time.Since(startTime).Microseconds(),
		NewCoverage: newCoverage,
	}, nil
}

// generateNewSeeds uses LLM to create a new seed from an interesting seed.
// It uses coverage information to guide the mutation.
// Generates exactly one seed per coverage increase (constraint solving model).
func (e *Engine) generateNewSeeds(parentSeed *seed.Seed, report coverage.Report, coverageIncrease *coverage.CoverageIncrease) {
	if e.cfg.LLM == nil || e.cfg.PromptBuilder == nil {
		return
	}

	logger.Info("Generating new seed from parent %d...", parentSeed.Meta.ID)

	// Build mutation context from coverage information
	var mutationCtx *prompt.MutationContext
	if coverageIncrease != nil {
		// Get current total stats
		stats, err := e.cfg.Coverage.GetStats()
		if err != nil {
			logger.Warn("Failed to get coverage stats: %v", err)
		}

		mutationCtx = &prompt.MutationContext{
			CoverageIncreaseSummary: coverageIncrease.Summary,
			CoverageIncreaseDetails: coverageIncrease.FormattedReport,
		}

		if stats != nil {
			mutationCtx.TotalCoveragePercentage = stats.CoveragePercentage
			mutationCtx.TotalCoveredLines = stats.TotalCoveredLines
			mutationCtx.TotalLines = stats.TotalLines
		}
	}

	// Generate exactly one seed
	newSeed := e.generateSingleSeed(parentSeed, mutationCtx)
	if newSeed != nil {
		logger.Info("Generated new seed %d from parent %d", newSeed.Meta.ID, parentSeed.Meta.ID)
	}
}

// generateSingleSeed generates a single new seed using LLM mutation.
// Returns the new seed if successful, nil otherwise.
func (e *Engine) generateSingleSeed(parentSeed *seed.Seed, mutationCtx *prompt.MutationContext) *seed.Seed {
	// Build mutation prompt with coverage context
	mutatePrompt, err := e.cfg.PromptBuilder.BuildMutatePrompt(parentSeed, mutationCtx)
	if err != nil {
		logger.Error("Failed to build mutate prompt: %v", err)
		return nil
	}

	// Call LLM with understanding as system prompt
	logger.Debug("Calling LLM for seed mutation...")

	// Record LLM call
	if e.cfg.Metrics != nil {
		e.cfg.Metrics.RecordLLMCall()
	}

	completion, err := e.cfg.LLM.GetCompletionWithSystem(e.cfg.Understanding, mutatePrompt)
	if err != nil {
		logger.Error("Failed to get LLM completion: %v", err)
		if e.cfg.Metrics != nil {
			e.cfg.Metrics.RecordLLMError()
		}
		return nil
	}

	// Parse LLM response using PromptBuilder (handles function template mode)
	newSeed, err := e.cfg.PromptBuilder.ParseLLMResponse(completion)
	if err != nil {
		logger.Error("Failed to parse LLM response: %v", err)
		return nil
	}

	// Set lineage information
	newSeed.Meta.ParentID = parentSeed.Meta.ID
	newSeed.Meta.Depth = parentSeed.Meta.Depth + 1

	// Add to corpus
	if err := e.cfg.Corpus.Add(newSeed); err != nil {
		logger.Error("Failed to add new seed to corpus: %v", err)
		return nil
	}

	// Record seed generated
	if e.cfg.Metrics != nil {
		e.cfg.Metrics.RecordSeedGenerated()
	}

	return newSeed
}

// TryDivergenceRefinedMutation attempts to refine a mutation using divergence analysis.
// This is called when a mutated seed doesn't achieve the expected coverage.
//
// Parameters:
//   - baseSeed: The seed that achieved the target coverage
//   - mutatedSeed: The seed that failed to achieve target coverage
//
// Returns the refined seed if successful, nil otherwise.
func (e *Engine) TryDivergenceRefinedMutation(baseSeed, mutatedSeed *seed.Seed) *seed.Seed {
	if e.cfg.DivergenceAnalyzer == nil {
		logger.Debug("Divergence analysis not available")
		return nil
	}

	if e.cfg.CompilerPath == "" {
		logger.Warn("CompilerPath not set, skipping divergence analysis")
		return nil
	}

	maxRetries := e.cfg.MaxDivergenceRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}

	// Save seeds to temporary files for analysis
	tmpDir, err := os.MkdirTemp("", "defuzz-div-")
	if err != nil {
		logger.Error("Failed to create temp dir for divergence analysis: %v", err)
		return nil
	}
	defer os.RemoveAll(tmpDir)

	basePath := filepath.Join(tmpDir, "base.c")
	if err := os.WriteFile(basePath, []byte(baseSeed.Content), 0644); err != nil {
		logger.Error("Failed to write base seed: %v", err)
		return nil
	}

	currentMutated := mutatedSeed
	for retry := 0; retry < maxRetries; retry++ {
		mutatedPath := filepath.Join(tmpDir, fmt.Sprintf("mutated_%d.c", retry))
		if err := os.WriteFile(mutatedPath, []byte(currentMutated.Content), 0644); err != nil {
			logger.Error("Failed to write mutated seed: %v", err)
			return nil
		}

		// Analyze divergence
		logger.Info("Analyzing divergence (retry %d/%d)...", retry+1, maxRetries)
		divPoint, err := e.cfg.DivergenceAnalyzer.Analyze(basePath, mutatedPath, e.cfg.CompilerPath)
		if err != nil {
			logger.Warn("Divergence analysis failed: %v", err)
			return nil
		}

		if divPoint == nil {
			logger.Info("No divergence found - execution paths are identical")
			return nil
		}

		logger.Info("Divergence found at index %d: %s vs %s",
			divPoint.Index, divPoint.Function1, divPoint.Function2)

		// Build divergence context for LLM
		divCtx := &prompt.DivergenceContext{
			BaseFunction:    divPoint.Function1,
			MutatedFunction: divPoint.Function2,
			DivergenceIndex: divPoint.Index,
			CommonPrefix:    divPoint.CommonPrefix,
			BasePath:        divPoint.Path1,
			MutatedPath:     divPoint.Path2,
			FormattedReport: divPoint.ForLLM(),
		}

		// Build refined prompt
		refinedPrompt, err := e.cfg.PromptBuilder.BuildDivergenceRefinedPrompt(
			baseSeed, currentMutated, divCtx)
		if err != nil {
			logger.Error("Failed to build divergence refined prompt: %v", err)
			return nil
		}

		// Call LLM with refined prompt
		if e.cfg.Metrics != nil {
			e.cfg.Metrics.RecordLLMCall()
		}

		completion, err := e.cfg.LLM.GetCompletionWithSystem(e.cfg.Understanding, refinedPrompt)
		if err != nil {
			logger.Error("Failed to get LLM completion for refined mutation: %v", err)
			if e.cfg.Metrics != nil {
				e.cfg.Metrics.RecordLLMError()
			}
			return nil
		}

		// Parse response
		refinedSeed, err := e.cfg.PromptBuilder.ParseLLMResponse(completion)
		if err != nil {
			logger.Error("Failed to parse refined LLM response: %v", err)
			continue
		}

		// Set lineage
		refinedSeed.Meta.ParentID = baseSeed.Meta.ID
		refinedSeed.Meta.Depth = baseSeed.Meta.Depth + 1

		// Add to corpus
		if err := e.cfg.Corpus.Add(refinedSeed); err != nil {
			logger.Error("Failed to add refined seed to corpus: %v", err)
			return nil
		}

		if e.cfg.Metrics != nil {
			e.cfg.Metrics.RecordSeedGenerated()
		}

		logger.Info("Generated refined seed %d using divergence feedback", refinedSeed.Meta.ID)

		// Update for next iteration
		currentMutated = refinedSeed
	}

	return currentMutated
}

// printProgress prints a one-line progress summary or renders UI.
func (e *Engine) printProgress() {
	if e.uiEnabled && e.cfg.Metrics != nil {
		// Render the terminal UI
		e.cfg.Metrics.RenderUI()
	} else if e.cfg.Metrics != nil {
		logger.Info("Progress: %s", e.cfg.Metrics.FormatOneLine())
	} else {
		elapsed := time.Since(e.startTime)
		logger.Info("Progress: [%v] iterations:%d bugs:%d",
			elapsed.Round(time.Second), e.iterationCount, len(e.bugsFound))
	}
}

// printSummary prints a summary of the fuzzing session.
func (e *Engine) printSummary() {
	// Clear UI if enabled
	if e.uiEnabled && e.cfg.Metrics != nil {
		if ui := e.cfg.Metrics.GetUI(); ui != nil {
			ui.Clear()
		}
	}

	// Print metrics summary if available
	if e.cfg.Metrics != nil {
		logger.Info("%s", e.cfg.Metrics.FormatSummary())
	}

	// Also print basic summary
	elapsed := time.Since(e.startTime)
	logger.Info("=========================================")
	logger.Info("           FUZZING SUMMARY")
	logger.Info("=========================================")
	logger.Info("Duration:     %v", elapsed)
	logger.Info("Iterations:   %d", e.iterationCount)
	logger.Info("Bugs found:   %d", len(e.bugsFound))
	logger.Info("Seeds/sec:    %.2f", float64(e.iterationCount)/elapsed.Seconds())
	logger.Info("=========================================")

	if len(e.bugsFound) > 0 {
		logger.Info("Bugs:")
		for i, bug := range e.bugsFound {
			logger.Info("  [%d] Seed %d: %s", i+1, bug.Seed.Meta.ID, bug.Description)
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
