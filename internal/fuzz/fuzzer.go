package fuzz

import (
	"fmt"
	"path/filepath"
	"time"

	"defuzz/internal/analysis"
	"defuzz/internal/compiler"
	"defuzz/internal/config"
	"defuzz/internal/coverage"
	"defuzz/internal/llm"
	"defuzz/internal/oracle"
	"defuzz/internal/prompt"
	"defuzz/internal/report"
	"defuzz/internal/seed"
	executor "defuzz/internal/seed_executor"
	"defuzz/internal/vm"
)

// Fuzzer orchestrates the main fuzzing logic.
type Fuzzer struct {
	// Dependencies (provided via constructor)
	cfg      *config.Config
	prompt   *prompt.Builder
	llm      llm.LLM
	seedPool seed.Pool
	compiler compiler.Compiler
	executor executor.Executor
	analyzer analysis.Analyzer
	reporter report.Reporter
	vm       vm.VM
	coverage coverage.Coverage // NEW: Coverage tracker
	oracle   oracle.Oracle     // NEW: Bug oracle

	// Internal state
	llmContext string // The "understanding" from the LLM
	bugCount   int
	basePath   string
}

// NewFuzzer creates and initializes a new Fuzzer instance.
func NewFuzzer(
	cfg *config.Config,
	prompt *prompt.Builder,
	llm llm.LLM,
	seedPool seed.Pool,
	compiler compiler.Compiler,
	exec executor.Executor,
	analyzer analysis.Analyzer,
	reporter report.Reporter,
	vm vm.VM,
	coverage coverage.Coverage, // NEW: Coverage parameter
	oracle oracle.Oracle, // NEW: Oracle parameter
) *Fuzzer {
	return &Fuzzer{
		cfg:      cfg,
		prompt:   prompt,
		llm:      llm,
		seedPool: seedPool,
		compiler: compiler,
		executor: exec,
		analyzer: analyzer,
		reporter: reporter,
		vm:       vm,
		coverage: coverage,
		oracle:   oracle,
		basePath: fmt.Sprintf("initial_seeds/%s/%s", cfg.Fuzzer.ISA, cfg.Fuzzer.Strategy),
	}
}

// Generate creates the initial understanding and seed pool.
func (f *Fuzzer) Generate() error {
	fmt.Println("Generating initial understanding...")
	p, err := f.prompt.BuildUnderstandPrompt(f.cfg.Fuzzer.ISA, f.cfg.Fuzzer.Strategy, f.basePath)
	if err != nil {
		return fmt.Errorf("failed to build understand prompt: %w", err)
	}

	understanding, err := f.llm.Understand(p)
	if err != nil {
		return fmt.Errorf("llm failed to understand prompt: %w", err)
	}
	f.llmContext = understanding

	// if err := seed.SaveUnderstanding(f.basePath, f.llmContext); err != nil {
	// 	return fmt.Errorf("failed to save understanding: %w", err)
	// }
	// fmt.Printf("Understanding saved to %s\n", seed.GetUnderstandingPath(f.basePath))

	fmt.Printf("Generating %d initial seeds...\n", f.cfg.Fuzzer.InitialSeeds)
	for i := 0; i < f.cfg.Fuzzer.InitialSeeds; i++ {
		genPrompt, err := f.prompt.BuildGeneratePrompt(f.basePath)
		if err != nil {
			return fmt.Errorf("failed to build generate prompt: %w", err)
		}

		newSeed, err := f.llm.Generate(f.llmContext, genPrompt)
		if err != nil {
			fmt.Printf("Warning: LLM failed to generate seed %d: %v\n", i+1, err)
			continue
		}
		newSeed.ID = fmt.Sprintf("%03d", i+1)

		// if err := seed.SaveSeed(f.basePath, newSeed); err != nil {
		// 	fmt.Printf("Warning: failed to save seed %s: %v\n", newSeed.ID, err)
		// 	continue
		// }
		fmt.Printf("  - Saved seed %s\n", newSeed.ID)
	}

	fmt.Println("Generation complete.")
	return nil
}

// Fuzz runs the main fuzzing loop with coverage-guided mutation and oracle-based bug detection.
func (f *Fuzzer) Fuzz() error {
	fmt.Println("Starting fuzzing loop...")

	// 1. Setup
	var err error
	// f.llmContext, err = seed.LoadUnderstanding(f.basePath)
	if err != nil {
		return fmt.Errorf("failed to load understanding: %w", err)
	}
	fmt.Println("Loaded understanding context.")

	// f.seedPool, err = seed.LoadSeeds(f.basePath)
	if err != nil {
		return fmt.Errorf("failed to load seeds: %w", err)
	}
	if f.seedPool.Len() == 0 {
		return fmt.Errorf("seed pool is empty, run 'generate' mode first")
	}
	fmt.Printf("Loaded %d seeds into the pool.\n", f.seedPool.Len())

	if err := f.vm.Create(); err != nil {
		return fmt.Errorf("failed to create vm: %w", err)
	}
	defer f.vm.Stop()
	fmt.Println("VM created successfully.")

	// 2. Fuzzing Loop
	for {
		if f.bugCount >= f.cfg.Fuzzer.BugQuota {
			fmt.Printf("Bug quota of %d reached. Exiting.\n", f.cfg.Fuzzer.BugQuota)
			break
		}

		currentSeed := f.seedPool.Next()
		if currentSeed == nil {
			fmt.Println("Seed pool is empty. Exiting.")
			break
		}
		fmt.Printf("Fuzzing with seed %s...\n", currentSeed.ID)

		// a. Save seed to a directory for compilation
		seedPath := filepath.Join(f.basePath, currentSeed.ID)

		// b. Measure coverage
		fmt.Println("  - Measuring coverage...")
		newCovInfo, err := f.coverage.Measure(seedPath)
		if err != nil {
			fmt.Printf("  - Coverage measurement failed: %v\n", err)
			continue
		}

		// c. Check if coverage increased
		coverageIncreased, err := f.coverage.HasIncreased(newCovInfo)
		if err != nil {
			fmt.Printf("  - Coverage comparison failed: %v\n", err)
			continue
		}

		if !coverageIncreased {
			fmt.Println("  - No coverage increase, skipping seed.")
			continue
		}

		fmt.Println("  - Coverage increased! Merging and continuing...")
		if err := f.coverage.Merge(newCovInfo); err != nil {
			fmt.Printf("  - Warning: failed to merge coverage: %v\n", err)
		}

		// d. Execute each test case
		var runRes []oracle.Result
		for _, testCase := range currentSeed.TestCases {
			// Execute the compiled binary via VM
			result, err := f.vm.Run("./prog", testCase.RunningCommand)
			if err != nil {
				fmt.Printf("  - Execution failed for test case '%s': %v\n", testCase.RunningCommand, err)
				continue
			}

			runRes = append(runRes, oracle.Result{
				Stdout:   result.Stdout,
				Stderr:   result.Stderr,
				ExitCode: result.ExitCode,
			})
		}

		if len(runRes) == 0 {
			fmt.Printf("  - All test cases failed to execute\n")
			continue
		}

		// e. Use Oracle to check for bugs
		fmt.Println("  - Analyzing execution with oracle...")
		bugFound, description, err := f.oracle.Analyze(currentSeed, runRes)
		if err != nil {
			fmt.Printf("  - Oracle analysis failed: %v\n", err)
			continue
		}

		if bugFound {
			f.bugCount++
			fmt.Printf("  - BUG FOUND! (%d/%d)\n", f.bugCount, f.cfg.Fuzzer.BugQuota)
			fmt.Printf("  - Description: %s\n", description)

			// Save bug report
			bug := &analysis.Bug{
				Seed:        currentSeed,
				Results:     convertOracleResultsToExecutorResults(runRes),
				Description: description,
			}
			if err := f.reporter.Save(bug); err != nil {
				fmt.Printf("  - Warning: failed to save bug report: %v\n", err)
			}
		} else {
			fmt.Println("  - No bug found.")
		}

		// f. Mutate seed since coverage increased
		fmt.Println("  - Mutating seed...")
		mutatePrompt, err := f.prompt.BuildMutatePrompt(currentSeed)
		if err != nil {
			fmt.Printf("  - Warning: failed to build mutate prompt: %v\n", err)
			continue
		}
		mutatedSeed, err := f.llm.Mutate(f.llmContext, mutatePrompt, currentSeed)
		if err != nil {
			fmt.Printf("  - Warning: LLM failed to mutate seed: %v\n", err)
			continue
		}
		mutatedSeed.ID = fmt.Sprintf("%s-m-%d", currentSeed.ID, time.Now().UnixNano())
		f.seedPool.Add(mutatedSeed)
		fmt.Printf("  - Added mutated seed %s to the pool.\n", mutatedSeed.ID)
	}

	fmt.Println("Fuzzing complete.")
	return nil
}

// convertOracleResultsToExecutorResults converts oracle.Result to executor.ExecutionResult
func convertOracleResultsToExecutorResults(oracleResults []oracle.Result) []executor.ExecutionResult {
	results := make([]executor.ExecutionResult, len(oracleResults))
	for i, or := range oracleResults {
		results[i] = executor.ExecutionResult{
			Stdout:   or.Stdout,
			Stderr:   or.Stderr,
			ExitCode: or.ExitCode,
		}
	}
	return results
}
