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

	// Prompt service for unified prompt management
	PromptService *prompt.PromptService

	// Fuzzing parameters
	MaxIterations   int           // Maximum iterations (0 = unlimited)
	MaxRetries      int           // Max retries per target BB with divergence analysis
	SaveInterval    time.Duration // State save interval
	CoverageTimeout int           // Coverage measurement timeout in seconds
	MappingPath     string        // Path to save/load coverage mapping

	// Oracle executor for cross-architecture execution (e.g., QEMU)
	// If nil, uses OracleExecutorAdapter with local execution
	OracleExecutor oracle.Executor

	// Random Mutation Phase (activated when coverage is saturated)
	EnableRandomPhase   bool // Enable random mutation phase after coverage saturation
	MaxRandomIterations int  // Maximum iterations in random phase (0 = unlimited)
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

// seedTryResult holds the result of trying a mutated seed.
// It captures compile errors to enable feedback-based retry.
type seedTryResult struct {
	HitTarget     bool   // Whether the target BB was covered
	CoveredNew    bool   // Whether any new coverage was achieved
	CompileFailed bool   // Whether compilation failed
	CompileError  string // Compiler error output (if compile failed)
	SeedCode      string // The seed code that was tried

	// Oracle results
	OracleVerdict  seed.OracleVerdict // Verdict from oracle analysis
	BugType        string             // Type of bug if detected
	BugDescription string             // Description of bug
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

	// Special case: limit=0 means only run initial seeds, skip constraint solving
	if e.cfg.MaxIterations == 0 {
		logger.Info("Limit=0: skipping constraint solving loop")
		e.finalizeState()
		e.printSummary()
		return nil
	}

	// Main fuzzing loop
	for {
		// Check iteration limit (-1 = unlimited)
		if e.cfg.MaxIterations > 0 && e.iterationCount >= e.cfg.MaxIterations {
			logger.Info("Reached max iterations (%d), stopping", e.cfg.MaxIterations)
			break
		}

		e.iterationCount++

		// Step 1: Select target BB (one with most successors among uncovered)
		target := e.cfg.Analyzer.SelectTarget()
		if target == nil {
			logger.Info("All target basic blocks covered! Fuzzing complete.")

			// Enter random mutation phase if enabled
			if e.cfg.EnableRandomPhase {
				logger.Info("Entering random mutation phase...")
				randomPhase := NewRandomMutationPhase(e, e.cfg.MaxRandomIterations)
				if err := randomPhase.Run(); err != nil {
					logger.Error("Random phase error: %v", err)
				}
			}
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

	// Final save with correct global state
	e.finalizeState()
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

		// Get coverage before processing this seed
		oldBasisPoints := e.cfg.Analyzer.GetBBCoverageBasisPoints()

		// Compile and measure coverage
		report, binaryPath, err := e.measureSeed(s)
		if err != nil {
			logger.Warn("Failed to measure initial seed %d: %v", s.Meta.ID, err)
			continue
		}

		// Record coverage in mapping
		if report != nil {
			coveredLines := e.extractCoveredLines(report)
			e.cfg.Analyzer.RecordCoverage(int64(s.Meta.ID), coveredLines)
		}

		// Get coverage after processing
		newBasisPoints := e.cfg.Analyzer.GetBBCoverageBasisPoints()

		// Run oracle on initial seed if configured
		oracleVerdict := seed.OracleVerdictSkipped
		if e.cfg.Oracle != nil && binaryPath != "" {
			bug := e.runOracle(s, binaryPath)
			if bug != nil {
				oracleVerdict = seed.OracleVerdictBug
				logger.Info("Initial seed %d triggered oracle bug: %s", s.Meta.ID, bug.Description)
			} else {
				oracleVerdict = seed.OracleVerdictNormal
			}
		}

		// Mark as processed with coverage and oracle info
		e.cfg.Corpus.ReportResult(s.Meta.ID, corpus.FuzzResult{
			State:         seed.SeedStateProcessed,
			OldCoverage:   oldBasisPoints,
			NewCoverage:   newBasisPoints,
			OracleVerdict: oracleVerdict,
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
	result, err := e.tryMutatedSeed(mutatedSeed, target)
	if err != nil {
		return false, 0, err
	}

	if result.HitTarget {
		return true, 0, nil // Hit on first try, 0 retries needed
	}

	// If first attempt failed, try with divergence analysis
	// Track last seed result for compile error feedback
	var lastResult *seedTryResult

	// Try multiple retries with divergence analysis
	var refinedPrompt string
	var systemPrompt string // Declare systemPrompt at broader scope
	for retry := 0; retry < e.cfg.MaxRetries; retry++ {
		logger.Debug("Retry %d/%d with divergence analysis...", retry+1, e.cfg.MaxRetries)

		// Check if previous attempt had compile error
		if lastResult != nil && lastResult.CompileFailed {
			// Use compile error prompt for feedback
			compileErrInfo := &prompt.CompileErrorInfo{
				FailedSeedCode: lastResult.SeedCode,
				CompilerOutput: lastResult.CompileError,
				ExitCode:       1, // Generic failure
				RetryAttempt:   retry + 1,
				MaxRetries:     e.cfg.MaxRetries,
			}
			var userPrompt string
			systemPrompt, userPrompt, err = e.cfg.PromptService.GetCompileErrorPrompt(ctx, compileErrInfo)
			if err != nil {
				logger.Warn("Failed to build compile error prompt: %v", err)
				continue
			}
			refinedPrompt = userPrompt
			logger.Debug("Using compile error feedback prompt for retry %d", retry+1)
			logger.Debug("[System Prompt]:\n%s", systemPrompt)
		} else {
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
			var userPrompt string
			systemPrompt, userPrompt, err = e.cfg.PromptService.GetRefinedPrompt(ctx, divInfo)
			refinedPrompt = userPrompt
			if err != nil {
				logger.Warn("Failed to build refined prompt: %v", err)
				continue
			}

			// Debug: Log the refined prompt for divergence analysis
			logger.Debug("=== Divergence Refinement Prompt (Retry %d/%d) ===", retry+1, e.cfg.MaxRetries)
			logger.Debug("Divergent Function: %s", divInfo.DivergentFunction)
			logger.Debug("Refined Prompt:\n%s", refinedPrompt)
			logger.Debug("=== End of Divergence Refinement Prompt ===")
		}

		// Debug: Log prompts for retry
		logger.Debug("=== LLM Call: solveConstraint Retry %d/%d ===", retry+1, e.cfg.MaxRetries)
		logger.Debug("[System Prompt]:\n%s", systemPrompt)
		logger.Debug("[Refined Prompt]:\n%s", refinedPrompt)

		// Call LLM with refined prompt
		completion, err := e.cfg.LLM.GetCompletionWithSystem(systemPrompt, refinedPrompt)
		if err != nil {
			logger.Warn("LLM call failed: %v", err)
			continue
		}

		// Parse response
		newSeed, err := e.cfg.PromptService.ParseLLMResponse(completion)
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

		// Try the new seed with V2 to capture compile errors
		lastResult, err = e.tryMutatedSeed(newSeed, target)
		if err != nil {
			return false, retry + 1, err
		}

		if lastResult.HitTarget {
			return true, retry + 1, nil
		}

		// Update mutated seed for next iteration
		mutatedSeed = newSeed

		// If we covered something new (even if not the target), that's progress
		if lastResult.CoveredNew {
			logger.Info("Covered new lines, continuing to next target")
			return false, retry + 1, nil
		}
	}

	// Failed to cover target after all retries - decay its weight
	e.cfg.Analyzer.DecayBBWeight(target.Function, target.BBID)

	return false, e.cfg.MaxRetries, nil
}

// generateMutatedSeed generates a new seed using LLM with constraint solving prompt.
func (e *Engine) generateMutatedSeed(ctx *prompt.TargetContext) (*seed.Seed, error) {
	// Build constraint solving prompt
	systemPrompt, userPrompt, err := e.cfg.PromptService.GetConstraintPrompt(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to build constraint prompt: %w", err)
	}

	// Debug: Log prompts
	logger.Debug("=== LLM Call: generateMutatedSeed ===")
	logger.Debug("[System Prompt]:\n%s", systemPrompt)
	logger.Debug("[Constraint Prompt]:\n%s", userPrompt)
	logger.Debug("=== End Prompts ===")

	// Call LLM
	completion, err := e.cfg.LLM.GetCompletionWithSystem(systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	// Parse response
	newSeed, err := e.cfg.PromptService.ParseLLMResponse(completion)
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
// Returns detailed result including compile errors for LLM feedback.
func (e *Engine) tryMutatedSeed(s *seed.Seed, target *coverage.TargetInfo) (*seedTryResult, error) {
	result := &seedTryResult{
		SeedCode: s.Content,
	}

	// Save seed path for divergence analysis
	stateDir := ""
	if e.cfg.MappingPath != "" {
		stateDir = filepath.Dir(e.cfg.MappingPath)
	}

	if s.Meta.ContentPath != "" {
		e.currentMutatedSeedPath = s.Meta.ContentPath
	} else if stateDir != "" {
		e.currentMutatedSeedPath = filepath.Join(stateDir, fmt.Sprintf("seed_%d.c", s.Meta.ID))
	}

	if target.BaseSeed != "" && e.currentBaseSeedPath == "" && stateDir != "" {
		e.currentBaseSeedPath = filepath.Join(stateDir, fmt.Sprintf("seed_%s.c", target.BaseSeed))
	}

	// Compile first to detect compile errors
	compileResult, err := e.cfg.Compiler.Compile(s)
	if err != nil {
		result.CompileFailed = true
		result.CompileError = fmt.Sprintf("compilation error: %v", err)
		return result, nil
	}

	if !compileResult.Success {
		result.CompileFailed = true
		result.CompileError = compileResult.Stderr
		logger.Debug("Seed failed to compile: %s", compileResult.Stderr)
		return result, nil
	}

	// Measure coverage (generated by instrumented compiler during compilation)
	if e.cfg.Coverage == nil {
		return result, nil
	}

	report, err := e.cfg.Coverage.Measure(s)
	if err != nil {
		return result, fmt.Errorf("coverage measurement failed: %w", err)
	}

	if report == nil {
		return result, nil
	}

	// Extract covered lines
	coveredLines := e.extractCoveredLines(report)

	// Check if target was hit
	for _, line := range coveredLines {
		for _, targetLine := range target.Lines {
			if line == fmt.Sprintf("%s:%d", target.File, targetLine) {
				result.HitTarget = true
				break
			}
		}
		if result.HitTarget {
			break
		}
	}

	// Record new coverage and track metrics
	oldBasisPoints := e.cfg.Analyzer.GetBBCoverageBasisPoints()
	e.cfg.Analyzer.RecordCoverage(int64(s.Meta.ID), coveredLines)
	newBasisPoints := e.cfg.Analyzer.GetBBCoverageBasisPoints()
	result.CoveredNew = newBasisPoints > oldBasisPoints

	// Update seed metadata
	s.Meta.OldCoverage = oldBasisPoints
	s.Meta.NewCoverage = newBasisPoints
	if newBasisPoints > oldBasisPoints {
		s.Meta.CovIncrease = newBasisPoints - oldBasisPoints
	}

	// Run oracle for ALL mutated seeds
	foundBug := false
	if e.cfg.Oracle != nil {
		bug := e.runOracle(s, compileResult.BinaryPath)
		if bug != nil {
			result.OracleVerdict = seed.OracleVerdictBug
			result.BugDescription = bug.Description
			foundBug = true
			logger.Info("Seed %d triggered bug: %s", s.Meta.ID, bug.Description)
		} else {
			result.OracleVerdict = seed.OracleVerdictNormal
		}
	} else {
		result.OracleVerdict = seed.OracleVerdictSkipped
	}

	// Persist oracle verdict to seed metadata
	s.Meta.OracleVerdict = result.OracleVerdict

	// Add to corpus if: covered new lines, hit target, OR found bug
	if result.CoveredNew || result.HitTarget || foundBug {
		s.Meta.Depth = 1
		if err := e.cfg.Corpus.Add(s); err != nil {
			logger.Warn("Failed to add seed to corpus: %v", err)
		} else {
			reason := "coverage"
			if foundBug {
				reason = "bug"
			} else if result.HitTarget {
				reason = "target"
			}
			logger.Info("Added seed %d to corpus (reason: %s, cov: %d -> %d bp)", s.Meta.ID, reason, oldBasisPoints, newBasisPoints)
		}

		if e.cfg.Coverage != nil {
			if increased, _ := e.cfg.Coverage.HasIncreased(report); increased {
				e.cfg.Coverage.Merge(report)
			}
		}
	}

	return result, nil
}

// measureSeed compiles and measures coverage for a seed.
// Returns the coverage report, the compiled binary path, and any error.
func (e *Engine) measureSeed(s *seed.Seed) (coverage.Report, string, error) {
	// Compile
	compileResult, err := e.cfg.Compiler.Compile(s)
	if err != nil {
		return nil, "", fmt.Errorf("compilation failed: %w", err)
	}

	if !compileResult.Success {
		logger.Debug("Seed failed to compile: %s", compileResult.Stderr)
		return nil, "", nil
	}

	// Measure coverage (generated by instrumented compiler during compilation)
	if e.cfg.Coverage == nil {
		return nil, compileResult.BinaryPath, nil
	}

	report, err := e.cfg.Coverage.Measure(s)
	if err != nil {
		return nil, "", fmt.Errorf("coverage measurement failed: %w", err)
	}

	return report, compileResult.BinaryPath, nil
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
// binaryPath is the path to the already-compiled binary.
// Returns the detected bug (if any) for persistence.
func (e *Engine) runOracle(s *seed.Seed, binaryPath string) *oracle.Bug {
	if binaryPath == "" {
		return nil
	}

	ctx := &oracle.AnalyzeContext{
		BinaryPath: binaryPath,
		Executor:   e.cfg.OracleExecutor,
	}

	// Fall back to local executor if OracleExecutor not configured
	if ctx.Executor == nil {
		ctx.Executor = executor.NewOracleExecutorAdapter(e.cfg.CoverageTimeout)
	}

	// Oracle handles all execution internally (e.g., CanaryOracle does binary search)
	bug, err := e.cfg.Oracle.Analyze(s, ctx, nil)
	if err != nil {
		logger.Error("Oracle analysis failed: %v", err)
		return nil
	}

	if bug != nil {
		logger.Error("BUG FOUND in seed %d: %s", s.Meta.ID, bug.Description)
		e.bugsFound = append(e.bugsFound, bug)
	}

	return bug
}

// saveState saves the current state.
func (e *Engine) saveState() {
	// Update total coverage in global state
	coverageBP := e.cfg.Analyzer.GetBBCoverageBasisPoints()
	e.cfg.Corpus.UpdateTotalCoverage(coverageBP)

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

// finalizeState saves state and finalizes global state when fuzzing completes.
func (e *Engine) finalizeState() {
	// Update total coverage
	coverageBP := e.cfg.Analyzer.GetBBCoverageBasisPoints()
	e.cfg.Corpus.UpdateTotalCoverage(coverageBP)

	// Save coverage mapping
	if e.cfg.MappingPath != "" {
		if err := e.cfg.Analyzer.SaveMapping(e.cfg.MappingPath); err != nil {
			logger.Warn("Failed to save mapping: %v", err)
		}
	}

	// Finalize corpus state (sets pool_size=0, current_fuzzing_id=0)
	if err := e.cfg.Corpus.Finalize(); err != nil {
		logger.Warn("Failed to finalize corpus: %v", err)
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
