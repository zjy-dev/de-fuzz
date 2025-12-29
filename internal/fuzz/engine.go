// Package fuzz provides the fuzzing engine for constraint solving based fuzzing.
package fuzz

import (
	"fmt"
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
)

// Config holds configuration for the fuzzing engine.
type Config struct {
	// Core components
	Corpus   corpus.Manager
	Compiler compiler.Compiler
	Executor executor.Executor
	Coverage coverage.Coverage
	Oracle   oracle.Oracle
	LLM      llm.LLM

	// Analyzer for CFG-guided targeting
	Analyzer *coverage.Analyzer

	// Divergence analyzer for execution path comparison (optional)
	// If set, enables divergence-guided mutation refinement
	DivergenceAnalyzer coverage.DivergenceAnalyzer

	// Compiler path for divergence analysis
	CompilerPath string

	// Prompt builder
	PromptBuilder *prompt.Builder

	// Understanding context
	Understanding string

	// Fuzzing parameters
	MaxIterations   int           // Maximum iterations (0 = unlimited)
	MaxRetries      int           // Max retries per target BB with divergence analysis
	SaveInterval    time.Duration // State save interval
	CoverageTimeout int           // Coverage measurement timeout in seconds
	MappingPath     string        // Path to save/load coverage mapping
}

// Engine implements constraint solving based fuzzing.
type Engine struct {
	cfg            Config
	iterationCount int
	targetHits     int // Number of times we successfully hit a target
	bugsFound      []*oracle.Bug
	startTime      time.Time

	// Paths for divergence analysis
	currentBaseSeedPath    string
	currentMutatedSeedPath string
}

// NewEngine creates a new fuzzing engine.
func NewEngine(cfg Config) *Engine {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	return &Engine{
		cfg:       cfg,
		bugsFound: make([]*oracle.Bug, 0),
	}
}

// Run starts the fuzzing loop.
func (e *Engine) Run() error {
	e.startTime = time.Now()
	logger.Info("Starting fuzzing loop...")

	// Process initial seeds to build coverage mapping
	if err := e.processInitialSeeds(); err != nil {
		return fmt.Errorf("failed to process initial seeds: %w", err)
	}

	// Main fuzzing loop
	for {
		// Check iteration limit
		if e.cfg.MaxIterations > 0 && e.iterationCount >= e.cfg.MaxIterations {
			logger.Info("Reached max iterations (%d), stopping", e.cfg.MaxIterations)
			break
		}

		e.iterationCount++

		// Step 1: Select target BB (one with most successors among uncovered)
		target := e.cfg.Analyzer.SelectTarget()
		if target == nil {
			logger.Info("All target basic blocks covered! Fuzzing complete.")
			break
		}

		logger.Info("Iteration %d: Targeting %s:BB%d (succs=%d, lines=%v)",
			e.iterationCount, target.Function, target.BBID, target.SuccessorCount, target.Lines)

		// Step 2: Try to cover the target with constraint solving
		hit, actualRetries, err := e.solveConstraint(target)
		if err != nil {
			logger.Error("Error solving constraint for %s:BB%d: %v", target.Function, target.BBID, err)
		}

		if hit {
			e.targetHits++
			logger.Info("Successfully covered target %s:BB%d!", target.Function, target.BBID)
		} else {
			logger.Warn("Failed to cover target %s:BB%d after %d retries",
				target.Function, target.BBID, actualRetries)
		}

		// Save state periodically
		if e.iterationCount%10 == 0 {
			e.saveState()
		}
	}

	// Final save
	e.saveState()
	e.printSummary()
	return nil
}

// processInitialSeeds runs all initial seeds to build the coverage mapping.
func (e *Engine) processInitialSeeds() error {
	logger.Info("Processing initial seeds to build coverage mapping...")

	for {
		s, ok := e.cfg.Corpus.Next()
		if !ok {
			break
		}

		logger.Debug("Processing initial seed %d...", s.Meta.ID)

		// Compile and measure coverage
		report, err := e.measureSeed(s)
		if err != nil {
			logger.Warn("Failed to measure initial seed %d: %v", s.Meta.ID, err)
			continue
		}

		// Record coverage in mapping
		if report != nil {
			coveredLines := e.extractCoveredLines(report)
			e.cfg.Analyzer.RecordCoverage(int64(s.Meta.ID), coveredLines)
		}

		// Mark as processed
		e.cfg.Corpus.ReportResult(s.Meta.ID, corpus.FuzzResult{
			State: seed.SeedStateProcessed,
		})
	}

	// Print initial coverage stats
	funcCov := e.cfg.Analyzer.GetFunctionCoverage()
	for name, stats := range funcCov {
		logger.Info("Initial coverage for %s: %d/%d BBs", name, stats.Covered, stats.Total)
	}

	// Save state immediately after processing initial seeds
	// This ensures the mapping is persisted even if the fuzzer crashes before the first checkpoint
	e.saveState()
	logger.Info("Initial coverage mapping saved to disk")

	return nil
}

// solveConstraint tries to generate a seed that covers the target BB.
// Returns (hit bool, actualRetries int, err error)
func (e *Engine) solveConstraint(target *coverage.TargetInfo) (bool, int, error) {
	// Load base seed from corpus if available
	var baseSeed *seed.Seed
	var baseSeedCode string
	if target.BaseSeed != "" {
		baseSeedID := 0
		fmt.Sscanf(target.BaseSeed, "%d", &baseSeedID)
		if baseSeedID > 0 {
			if loadedSeed, err := e.cfg.Corpus.Get(uint64(baseSeedID)); err == nil && loadedSeed != nil {
				baseSeed = loadedSeed
				baseSeedCode = loadedSeed.Content
				logger.Debug("Loaded base seed %d for target", baseSeedID)
			} else {
				logger.Warn("Failed to load base seed %d: %v", baseSeedID, err)
			}
		}
	}

	// Build target context for prompt
	ctx, err := prompt.BuildTargetContextFromCFG(target, baseSeed, e.cfg.Analyzer)
	if err != nil {
		return false, 0, fmt.Errorf("failed to build target context: %w", err)
	}
	// Ensure base seed code is set in context
	if baseSeedCode != "" && ctx.BaseSeedCode == "" {
		ctx.BaseSeedCode = baseSeedCode
	}

	// First attempt: direct constraint solving
	mutatedSeed, err := e.generateMutatedSeed(ctx)
	if err != nil {
		logger.Warn("Failed to generate mutated seed: %v", err)
		return false, 0, nil
	}

	// Try the mutated seed
	hit, coveredTarget, err := e.tryMutatedSeed(mutatedSeed, target)
	if err != nil {
		return false, 0, err
	}

	if hit {
		return true, 0, nil // Hit on first try, 0 retries needed
	}

	// If first attempt failed, try with divergence analysis
	for retry := 0; retry < e.cfg.MaxRetries; retry++ {
		logger.Debug("Retry %d/%d with divergence analysis...", retry+1, e.cfg.MaxRetries)

		// Use divergence analysis if available
		var divInfo *prompt.DivergenceInfo
		divergentFunc := target.Function // Default to target function

		if e.cfg.DivergenceAnalyzer != nil && e.cfg.CompilerPath != "" {
			// Run uftrace divergence analysis
			divPoint, err := e.cfg.DivergenceAnalyzer.Analyze(
				e.currentBaseSeedPath, e.currentMutatedSeedPath, e.cfg.CompilerPath)
			if err != nil {
				logger.Warn("Divergence analysis failed: %v", err)
			} else if divPoint != nil {
				logger.Info("Divergence found at index %d: %s vs %s",
					divPoint.Index, divPoint.Function1, divPoint.Function2)
				divergentFunc = divPoint.Function2
			}
		}

		// Get divergent function source code from analyzer
		divergentFuncCode := ""
		if fn, ok := e.cfg.Analyzer.GetFunction(divergentFunc); ok && fn != nil {
			// Try to read the source code for this function
			if target.File != "" {
				// Find the line range for this function from its BBs
				minLine, maxLine := 0, 0
				for _, bb := range fn.Blocks {
					for _, lineNum := range bb.Lines {
						if minLine == 0 || lineNum < minLine {
							minLine = lineNum
						}
						if lineNum > maxLine {
							maxLine = lineNum
						}
					}
				}
				if minLine > 0 && maxLine > 0 {
					code, err := coverage.ReadSourceLines(target.File, minLine, maxLine)
					if err == nil {
						divergentFuncCode = code
					}
				}
			}
		}

		divInfo = &prompt.DivergenceInfo{
			DivergentFunction:     divergentFunc,
			DivergentFunctionCode: divergentFuncCode,
			MutatedSeedCode:       mutatedSeed.Content,
			BaseSeedCode:          baseSeedCode,
		}

		// Generate refined prompt
		refinedPrompt, err := e.cfg.PromptBuilder.BuildRefinedPrompt(ctx, divInfo)
		if err != nil {
			logger.Warn("Failed to build refined prompt: %v", err)
			continue
		}

		// Debug: Log the refined prompt for divergence analysis
		logger.Debug("=== Divergence Refinement Prompt (Retry %d/%d) ===", retry+1, e.cfg.MaxRetries)
		logger.Debug("Divergent Function: %s", divInfo.DivergentFunction)
		logger.Debug("Refined Prompt:\n%s", refinedPrompt)
		logger.Debug("=== End of Divergence Refinement Prompt ===")

		// Call LLM with refined prompt
		completion, err := e.cfg.LLM.GetCompletionWithSystem(e.cfg.Understanding, refinedPrompt)
		if err != nil {
			logger.Warn("LLM call failed: %v", err)
			continue
		}

		// Parse response
		newSeed, err := e.cfg.PromptBuilder.ParseLLMResponse(completion)
		if err != nil {
			logger.Warn("Failed to parse LLM response: %v", err)
			continue
		}

		// Allocate ID for the new seed before trying it
		newSeed.Meta.ID = e.cfg.Corpus.AllocateID()
		newSeed.Meta.CreatedAt = time.Now()
		if ctx.BaseSeedID > 0 {
			newSeed.Meta.ParentID = uint64(ctx.BaseSeedID)
		}

		// Try the new seed
		hit, coveredTarget, err = e.tryMutatedSeed(newSeed, target)
		if err != nil {
			return false, retry + 1, err
		}

		if hit {
			return true, retry + 1, nil
		}

		// Update mutated seed for next iteration
		mutatedSeed = newSeed

		// If we covered something new (even if not the target), that's progress
		if coveredTarget {
			logger.Info("Covered new lines, continuing to next target")
			return false, retry + 1, nil
		}
	}

	return false, e.cfg.MaxRetries, nil
}

// generateMutatedSeed generates a new seed using LLM with constraint solving prompt.
func (e *Engine) generateMutatedSeed(ctx *prompt.TargetContext) (*seed.Seed, error) {
	// Build constraint solving prompt
	constraintPrompt, err := e.cfg.PromptBuilder.BuildConstraintSolvingPrompt(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	// Call LLM
	completion, err := e.cfg.LLM.GetCompletionWithSystem(e.cfg.Understanding, constraintPrompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	// Parse response
	newSeed, err := e.cfg.PromptBuilder.ParseLLMResponse(completion)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Pre-allocate ID for the new seed before compilation
	// This ensures the seed has a valid ID when being compiled
	newSeed.Meta.ID = e.cfg.Corpus.AllocateID()
	newSeed.Meta.CreatedAt = time.Now()

	// Set lineage information from context
	if ctx.BaseSeedID > 0 {
		newSeed.Meta.ParentID = uint64(ctx.BaseSeedID)
		// Depth will be properly set in tryMutatedSeed when we have parent info
	}

	return newSeed, nil
}

// tryMutatedSeed compiles and runs a mutated seed, checking if it covers the target.
// Returns (hitTarget, coveredAnything, error)
func (e *Engine) tryMutatedSeed(s *seed.Seed, target *coverage.TargetInfo) (bool, bool, error) {
	// Save seed path for divergence analysis
	// Use the MappingPath directory as the state directory
	stateDir := ""
	if e.cfg.MappingPath != "" {
		stateDir = filepath.Dir(e.cfg.MappingPath)
	}

	if s.Meta.ContentPath != "" {
		e.currentMutatedSeedPath = s.Meta.ContentPath
	} else if stateDir != "" {
		// Fallback: construct path from state directory
		e.currentMutatedSeedPath = filepath.Join(stateDir, fmt.Sprintf("seed_%d.c", s.Meta.ID))
	}

	// If base seed was used, save its path too
	if target.BaseSeed != "" && e.currentBaseSeedPath == "" && stateDir != "" {
		e.currentBaseSeedPath = filepath.Join(stateDir, fmt.Sprintf("seed_%s.c", target.BaseSeed))
	}

	// Measure coverage
	report, err := e.measureSeed(s)
	if err != nil {
		return false, false, fmt.Errorf("failed to measure seed: %w", err)
	}

	if report == nil {
		return false, false, nil
	}

	// Extract covered lines
	coveredLines := e.extractCoveredLines(report)

	// Check if target was hit
	hitTarget := false
	for _, line := range coveredLines {
		for _, targetLine := range target.Lines {
			if line == fmt.Sprintf("%s:%d", target.File, targetLine) {
				hitTarget = true
				break
			}
		}
		if hitTarget {
			break
		}
	}

	// Record new coverage and track metrics
	oldCount := len(e.cfg.Analyzer.GetCoveredLines())
	e.cfg.Analyzer.RecordCoverage(int64(s.Meta.ID), coveredLines)
	newTotalCount := len(e.cfg.Analyzer.GetCoveredLines())
	newCount := newTotalCount - oldCount
	coveredNew := newCount > 0

	// Update seed metadata with coverage information
	s.Meta.OldCoverage = uint64(oldCount)
	s.Meta.NewCoverage = uint64(newTotalCount)
	s.Meta.CovIncrease = uint64(newCount)

	// If covered new lines or hit target, add to corpus
	if coveredNew || hitTarget {
		// Set lineage
		s.Meta.Depth = 1 // Generated seeds are depth 1

		if err := e.cfg.Corpus.Add(s); err != nil {
			logger.Warn("Failed to add seed to corpus: %v", err)
		} else {
			logger.Info("Added seed %d to corpus (new lines: %d)", s.Meta.ID, newCount)
		}

		// Also merge into total coverage
		if e.cfg.Coverage != nil {
			if increased, _ := e.cfg.Coverage.HasIncreased(report); increased {
				e.cfg.Coverage.Merge(report)
			}
		}
	}

	// Run oracle if available
	if e.cfg.Oracle != nil && hitTarget {
		e.runOracle(s)
	}

	return hitTarget, coveredNew, nil
}

// measureSeed compiles and measures coverage for a seed.
func (e *Engine) measureSeed(s *seed.Seed) (coverage.Report, error) {
	// Compile
	compileResult, err := e.cfg.Compiler.Compile(s)
	if err != nil {
		return nil, fmt.Errorf("compilation failed: %w", err)
	}

	if !compileResult.Success {
		logger.Debug("Seed failed to compile: %s", compileResult.Stderr)
		return nil, nil
	}

	// Execute (needed to generate coverage data)
	if e.cfg.Executor != nil {
		execStart := time.Now()
		_, err = e.cfg.Executor.Execute(s, compileResult.BinaryPath)
		execDuration := time.Since(execStart)
		s.Meta.ExecTimeUs = execDuration.Microseconds()
		if err != nil {
			logger.Debug("Execution failed: %v", err)
			// Continue anyway - we might still get partial coverage
		}
	}

	// Measure coverage
	if e.cfg.Coverage == nil {
		return nil, nil
	}

	report, err := e.cfg.Coverage.Measure(s)
	if err != nil {
		return nil, fmt.Errorf("coverage measurement failed: %w", err)
	}

	return report, nil
}

// extractCoveredLines extracts covered line identifiers from a coverage report.
// Returns a list of "file:line" strings.
// This method uses the filtered extraction when GCCCoverage is available,
// ensuring only lines from target functions are counted.
func (e *Engine) extractCoveredLines(report coverage.Report) []string {
	if report == nil {
		return make([]string, 0)
	}

	// Try to use filtered extraction if GCCCoverage is available
	if gccCov, ok := e.cfg.Coverage.(*coverage.GCCCoverage); ok {
		lines, err := gccCov.ExtractCoveredLinesFiltered(report)
		if err != nil {
			logger.Debug("Failed to extract filtered covered lines: %v", err)
			return make([]string, 0)
		}
		return lines
	}

	// Fallback to unfiltered extraction for other coverage implementations
	lines, err := coverage.ExtractCoveredLines(report)
	if err != nil {
		logger.Debug("Failed to extract covered lines: %v", err)
		return make([]string, 0)
	}

	return lines
}

// runOracle runs bug detection oracle on a seed.
func (e *Engine) runOracle(s *seed.Seed) {
	compileResult, err := e.cfg.Compiler.Compile(s)
	if err != nil || !compileResult.Success {
		return
	}

	execResults, err := e.cfg.Executor.Execute(s, compileResult.BinaryPath)
	if err != nil {
		return
	}

	oracleResults := make([]oracle.Result, len(execResults))
	for i, r := range execResults {
		oracleResults[i] = oracle.Result{
			Stdout:   r.Stdout,
			Stderr:   r.Stderr,
			ExitCode: r.ExitCode,
		}
	}

	ctx := &oracle.AnalyzeContext{
		BinaryPath: compileResult.BinaryPath,
		Executor:   executor.NewOracleExecutorAdapter(e.cfg.CoverageTimeout),
	}

	bug, err := e.cfg.Oracle.Analyze(s, ctx, oracleResults)
	if err != nil {
		logger.Error("Oracle analysis failed: %v", err)
	} else if bug != nil {
		logger.Error("BUG FOUND in seed %d: %s", s.Meta.ID, bug.Description)
		e.bugsFound = append(e.bugsFound, bug)
	}
}

// saveState saves the current state.
func (e *Engine) saveState() {
	// Save coverage mapping
	if e.cfg.MappingPath != "" {
		if err := e.cfg.Analyzer.SaveMapping(e.cfg.MappingPath); err != nil {
			logger.Warn("Failed to save mapping: %v", err)
		}
	}

	// Save corpus
	if err := e.cfg.Corpus.Save(); err != nil {
		logger.Warn("Failed to save corpus: %v", err)
	}
}

// printSummary prints a summary of the fuzzing session.
func (e *Engine) printSummary() {
	elapsed := time.Since(e.startTime)

	// Get final coverage stats
	funcCov := e.cfg.Analyzer.GetFunctionCoverage()

	logger.Info("=========================================")
	logger.Info("      FUZZING SUMMARY")
	logger.Info("=========================================")
	logger.Info("Duration:       %v", elapsed)
	logger.Info("Iterations:     %d", e.iterationCount)
	logger.Info("Targets hit:    %d", e.targetHits)
	logger.Info("Bugs found:     %d", len(e.bugsFound))
	logger.Info("-----------------------------------------")
	logger.Info("Final BB Coverage:")
	for name, stats := range funcCov {
		pct := float64(0)
		if stats.Total > 0 {
			pct = float64(stats.Covered) / float64(stats.Total) * 100
		}
		logger.Info("  %s: %d/%d BBs (%.1f%%)", name, stats.Covered, stats.Total, pct)
	}
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

// GetTargetHits returns the number of times we successfully hit a target.
func (e *Engine) GetTargetHits() int {
	return e.targetHits
}
