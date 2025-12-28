package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/zjy-dev/de-fuzz/internal/compiler"
	"github.com/zjy-dev/de-fuzz/internal/config"
	"github.com/zjy-dev/de-fuzz/internal/corpus"
	"github.com/zjy-dev/de-fuzz/internal/coverage"
	"github.com/zjy-dev/de-fuzz/internal/exec"
	"github.com/zjy-dev/de-fuzz/internal/fuzz"
	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/logger"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
	"github.com/zjy-dev/de-fuzz/internal/state"
	"github.com/zjy-dev/de-fuzz/internal/vm"
)

// NewFuzzCommand creates the "fuzz" subcommand.
func NewFuzzCommand() *cobra.Command {
	var (
		output  string
		limit   int
		timeout int
		useQEMU bool
	)

	cmd := &cobra.Command{
		Use:   "fuzz",
		Short: "Start the main fuzzing loop.",
		Long: `Start the main fuzzing loop for the configured target.

This command:
  1. Selects uncovered basic blocks with most successors (CFG-guided)
  2. Uses LLM to generate seeds that satisfy path constraints
  3. Refines failed mutations with divergence analysis
  4. Reports any discovered bugs

The fuzzer will automatically resume from the last saved state if interrupted.

Output directory structure:
  {output}/{isa}/{strategy}/
    ├── corpus/      # Seed corpus
    ├── build/       # Compiled binaries
    ├── coverage/    # Coverage reports
    └── state/       # Fuzzing state (for resume)

Configuration:
  Default values are loaded from config.yaml.
  Command line flags override the config file values.

Constraints:
  --limit and --timeout work independently:
    --limit: Maximum number of target BBs to attempt (0 = unlimited)
    --timeout: Maximum execution time per seed in seconds

Examples:
  # Start fuzzing with defaults from config
  defuzz fuzz

  # Override output directory
  defuzz fuzz --output my_fuzz_out

  # Limit to 50 target basic blocks for constraint solving
  defuzz fuzz --limit 50

  # Use QEMU for cross-architecture fuzzing
  defuzz fuzz --use-qemu

  # Limit to 30 targets with 60s timeout each
  defuzz fuzz --limit 30 --timeout 60`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load config first to get defaults
			cfg, err := config.LoadConfig()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Use config values as defaults, command line flags override
			if !cmd.Flags().Changed("output") {
				output = cfg.Compiler.Fuzz.OutputRootDir
			}
			if !cmd.Flags().Changed("limit") {
				limit = cfg.Compiler.Fuzz.MaxIterations
			}
			if !cmd.Flags().Changed("timeout") {
				timeout = cfg.Compiler.Fuzz.Timeout
			}
			if !cmd.Flags().Changed("use-qemu") {
				useQEMU = cfg.Compiler.Fuzz.UseQEMU
			}

			// Build the actual output directory: {output}/{isa}/{strategy}
			outputDir := filepath.Join(output, cfg.ISA, cfg.Strategy)

			return runFuzz(cfg, outputDir, limit, timeout, useQEMU)
		},
	}

	// Core flags only - detailed config should be in config files
	cmd.Flags().StringVar(&output, "output", "fuzz_out", "Output directory (actual output at {output}/{isa}/{strategy})")
	cmd.Flags().IntVar(&limit, "limit", 0, "Max number of target BBs for constraint solving (0 = unlimited)")
	cmd.Flags().IntVar(&timeout, "timeout", 30, "Execution timeout in seconds")
	cmd.Flags().BoolVar(&useQEMU, "use-qemu", false, "Use QEMU for cross-architecture execution")

	return cmd
}

func runFuzz(cfg *config.Config, outputDir string, limit, timeout int, useQEMU bool) error {
	// Initialize logger with configured level
	logLevel := cfg.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}
	logger.Init(logLevel)

	logger.Info("Target: %s / %s", cfg.ISA, cfg.Strategy)
	logger.Info("Output directory: %s", outputDir)
	logger.Debug("Log level: %s", logLevel)

	// Create state directory (used for resume capability)
	stateDir := filepath.Join(outputDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// 2. Create corpus manager
	corpusManager := corpus.NewFileManager(outputDir)

	// 3. Create compiler
	// Note: We do NOT add --coverage here. Coverage tracking is for the COMPILER itself,
	// not the compiled binary. The instrumented compiler generates .gcda files when it runs.
	compilerDir := filepath.Dir(cfg.Compiler.Path)

	// Use CFlags from config (allows customization per ISA/strategy)
	// Default to basic flags if not specified in config
	cflags := cfg.Compiler.CFlags
	if len(cflags) == 0 {
		logger.Warn("No cflags specified in config, using defaults")
		cflags = []string{"-fstack-protector-strong", "-O0"}
	}

	gccCompiler := compiler.NewGCCCompiler(compiler.GCCCompilerConfig{
		GCCPath:    cfg.Compiler.Path,
		WorkDir:    filepath.Join(outputDir, "build"),
		PrefixPath: compilerDir,
		CFlags:     cflags,
	})

	// 4. Create executor (local or QEMU)
	var seedExecutor executor.Executor
	if useQEMU {
		seedExecutor = executor.NewQEMUExecutor(vm.QEMUConfig{
			QEMUPath: cfg.Compiler.Fuzz.QEMUPath,
			Sysroot:  cfg.Compiler.Fuzz.QEMUSysroot,
		}, timeout)
		fmt.Printf("[Fuzz] Using QEMU executor: %s\n", cfg.Compiler.Fuzz.QEMUPath)
	} else {
		seedExecutor = executor.NewLocalExecutor(timeout)
		fmt.Println("[Fuzz] Using local executor")
	}

	// 5. Create coverage tracker
	cmdExecutor := exec.NewCommandExecutor()

	// Create a compile function wrapper for coverage
	compileFunc := func(s *seed.Seed) error {
		result, err := gccCompiler.Compile(s)
		if err != nil {
			return err
		}
		if !result.Success {
			return fmt.Errorf("compilation failed: %s", result.Stderr)
		}
		return nil
	}

	filterConfigPath, _ := config.GetCompilerConfigPath(cfg)

	// Determine gcovr command: use config if set, otherwise use default
	gcovrCommand := cfg.Compiler.GcovrCommand
	if gcovrCommand == "" {
		return fmt.Errorf("gcovr command not specified in config")
	}

	// Determine total report path: use config if set, otherwise use state directory
	// This is critical for resume capability - the total.json stores accumulated coverage
	totalReportPath := cfg.Compiler.TotalReportPath
	if totalReportPath == "" {
		totalReportPath = filepath.Join(stateDir, "total.json")
	}
	fmt.Printf("[Fuzz] Coverage report path: %s\n", totalReportPath)

	// Check if we're resuming (total.json exists)
	if _, err := os.Stat(totalReportPath); err == nil {
		fmt.Println("[Fuzz] Found existing coverage data, resuming from checkpoint...")
	} else {
		fmt.Println("[Fuzz] Starting fresh fuzzing session...")
	}

	coverageTracker := coverage.NewGCCCoverage(
		cmdExecutor,
		compileFunc,
		cfg.Compiler.GcovrExecPath,
		gcovrCommand,
		totalReportPath,
		filterConfigPath,
	)

	// 6. Create LLM client
	llmClient, err := llm.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %w", err)
	}

	// 7. Load understanding and initial seeds path
	basePath := filepath.Join("initial_seeds", cfg.ISA, cfg.Strategy)
	understanding, err := seed.LoadUnderstanding(basePath)
	if err != nil {
		return fmt.Errorf("understanding not found at %s, please run 'defuzz generate' first: %w", basePath, err)
	}

	// 8. Create prompt builder and oracle
	promptBuilder := prompt.NewBuilder(cfg.Compiler.Fuzz.MaxTestCases, cfg.Compiler.Fuzz.FunctionTemplate)

	// Create oracle using the registry
	oracleInstance, err := oracle.New(
		cfg.Compiler.Oracle.Type,
		cfg.Compiler.Oracle.Options,
		llmClient,
		promptBuilder,
		understanding,
	)
	if err != nil {
		return fmt.Errorf("failed to create oracle: %w", err)
	}

	// 9. Initialize corpus and load initial seeds if needed
	if err := corpusManager.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize corpus: %w", err)
	}

	if err := corpusManager.Recover(); err != nil {
		return fmt.Errorf("failed to recover corpus: %w", err)
	}

	// If corpus is empty, load initial seeds
	if corpusManager.Len() == 0 {
		logger.Info("Corpus is empty, loading initial seeds from %s...", basePath)
		initialSeeds, err := seed.LoadSeedsWithMetadata(basePath, seed.NewDefaultNamingStrategy())
		if err != nil {
			return fmt.Errorf("failed to load initial seeds: %w", err)
		}
		if len(initialSeeds) == 0 {
			return fmt.Errorf("no initial seeds found in %s, please run 'defuzz generate' first", basePath)
		}
		for _, s := range initialSeeds {
			// Reset ID to 0 so corpus manager assigns a new unique ID
			s.Meta.ID = 0
			if err := corpusManager.Add(s); err != nil {
				return fmt.Errorf("failed to add initial seed to corpus: %w", err)
			}
		}
		logger.Info("Loaded %d initial seeds", len(initialSeeds))
	}

	// 10. Create metrics manager
	metricsManager := state.NewFileMetricsManager(stateDir)
	if err := metricsManager.Load(); err != nil {
		logger.Warn("Failed to load existing metrics: %v", err)
	}

	// 11. Create analyzer if configured
	var analyzer *coverage.Analyzer
	if cfg.Compiler.Fuzz.CFGFilePath != "" && len(cfg.Compiler.Targets) > 0 {
		// Collect all target function names
		var targetFunctions []string
		for _, target := range cfg.Compiler.Targets {
			targetFunctions = append(targetFunctions, target.Functions...)
		}

		// Determine mapping path
		mappingPath := cfg.Compiler.Fuzz.MappingPath
		if mappingPath == "" {
			mappingPath = filepath.Join(stateDir, "coverage_mapping.json")
		}

		logger.Info("Creating analyzer with %d target functions", len(targetFunctions))
		logger.Debug("CFG file: %s", cfg.Compiler.Fuzz.CFGFilePath)
		logger.Debug("Target functions: %v", targetFunctions)

		analyzer, err = coverage.NewAnalyzer(
			cfg.Compiler.Fuzz.CFGFilePath,
			targetFunctions,
			cfg.Compiler.SourceParentPath,
			mappingPath,
		)
		if err != nil {
			logger.Warn("Failed to create analyzer: %v (continuing without target function tracking)", err)
			analyzer = nil
		} else {
			logger.Info("Analyzer initialized, total target lines: %d", analyzer.GetTotalTargetLines())
		}
	}

	// 12. Create and run fuzzing engine
	// Use Engine for constraint solving based fuzzing
	fmt.Println("[Fuzz] Starting fuzzing engine...")
	logger.Info("Using fuzzing engine")

	cfgEngine := fuzz.NewEngine(fuzz.Config{
		Corpus:        corpusManager,
		Compiler:      gccCompiler,
		Executor:      seedExecutor,
		Coverage:      coverageTracker,
		Oracle:        oracleInstance,
		LLM:           llmClient,
		Analyzer:      analyzer,
		PromptBuilder: promptBuilder,
		Understanding: understanding,
		MaxIterations: limit,
		MaxRetries:    cfg.Compiler.Fuzz.MaxConstraintRetries,
		MappingPath:   filepath.Join(stateDir, "coverage_mapping.json"),
	})
	return cfgEngine.Run()
}
