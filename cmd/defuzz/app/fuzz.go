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
		outputRootDir string
		maxIterations int
		maxNewSeeds   int
		timeout       int
		useQEMU       bool
		qemuPath      string
		qemuSysroot   string
		enableUI      bool
	)

	cmd := &cobra.Command{
		Use:   "fuzz",
		Short: "Start the main fuzzing loop.",
		Long: `Start the main fuzzing loop for the configured target.

This command:
  1. Loads initial seeds from the corpus
  2. Compiles and executes each seed
  3. Measures code coverage
  4. Generates new seeds using LLM when coverage increases
  5. Reports any discovered bugs

The fuzzer will automatically resume from the last saved state if interrupted.

Output directory structure:
  {output_root_dir}/{isa}/{strategy}/
    ├── corpus/      # Seed corpus
    ├── build/       # Compiled binaries
    ├── coverage/    # Coverage reports
    └── state/       # Fuzzing state (for resume)

Configuration:
  Default values are loaded from config.yaml under 'fuzz' section.
  Command line flags override the config file values.

Examples:
  # Start fuzzing with default settings from config
  defuzz fuzz

  # Override output root directory
  defuzz fuzz --output-root my_fuzz_out

  # Fuzz with a maximum of 100 iterations
  defuzz fuzz --max-iterations 100

  # Use QEMU for cross-architecture fuzzing
  defuzz fuzz --use-qemu --qemu-path qemu-aarch64 --qemu-sysroot /usr/aarch64-linux-gnu`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load config first to get defaults
			cfg, err := config.LoadConfig()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Use config values as defaults, command line flags override
			if !cmd.Flags().Changed("output-root") {
				outputRootDir = cfg.Compiler.Fuzz.OutputRootDir
			}
			if !cmd.Flags().Changed("max-iterations") {
				maxIterations = cfg.Compiler.Fuzz.MaxIterations
			}
			if !cmd.Flags().Changed("max-new-seeds") {
				maxNewSeeds = cfg.Compiler.Fuzz.MaxNewSeeds
			}
			if !cmd.Flags().Changed("timeout") {
				timeout = cfg.Compiler.Fuzz.Timeout
			}
			if !cmd.Flags().Changed("use-qemu") {
				useQEMU = cfg.Compiler.Fuzz.UseQEMU
			}
			if !cmd.Flags().Changed("qemu-path") {
				qemuPath = cfg.Compiler.Fuzz.QEMUPath
			}
			if !cmd.Flags().Changed("qemu-sysroot") {
				qemuSysroot = cfg.Compiler.Fuzz.QEMUSysroot
			}

			// Build the actual output directory: {output_root_dir}/{isa}/{strategy}
			outputDir := filepath.Join(outputRootDir, cfg.ISA, cfg.Strategy)

			return runFuzz(cfg, outputDir, maxIterations, maxNewSeeds, timeout, useQEMU, qemuPath, qemuSysroot, enableUI)
		},
	}

	// Flags (these are placeholder defaults, actual defaults come from config)
	cmd.Flags().StringVar(&outputRootDir, "output-root", "fuzz_out", "Root output directory (actual output at {root}/{isa}/{strategy})")
	cmd.Flags().IntVar(&maxIterations, "max-iterations", 0, "Maximum number of fuzzing iterations (0 = unlimited)")
	cmd.Flags().IntVar(&maxNewSeeds, "max-new-seeds", 3, "Maximum new seeds to generate per interesting seed")
	cmd.Flags().IntVar(&timeout, "timeout", 30, "Execution timeout in seconds")
	cmd.Flags().BoolVar(&useQEMU, "use-qemu", false, "Use QEMU for execution (for cross-architecture)")
	cmd.Flags().StringVar(&qemuPath, "qemu-path", "qemu-aarch64", "Path to QEMU user-mode executable")
	cmd.Flags().StringVar(&qemuSysroot, "qemu-sysroot", "", "Sysroot path for QEMU (-L argument)")
	cmd.Flags().BoolVar(&enableUI, "ui", true, "Enable real-time terminal UI (default: true)")

	return cmd
}

func runFuzz(cfg *config.Config, outputDir string, maxIterations, maxNewSeeds, timeout int, useQEMU bool, qemuPath, qemuSysroot string, enableUI bool) error {
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
			QEMUPath: qemuPath,
			Sysroot:  qemuSysroot,
		}, timeout)
		fmt.Printf("[Fuzz] Using QEMU executor: %s\n", qemuPath)
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

	// 11. Create CFG-guided analyzer if configured
	var cfgAnalyzer *coverage.CFGGuidedAnalyzer
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

		logger.Info("Creating CFG-guided analyzer with %d target functions", len(targetFunctions))
		logger.Debug("CFG file: %s", cfg.Compiler.Fuzz.CFGFilePath)
		logger.Debug("Target functions: %v", targetFunctions)

		cfgAnalyzer, err = coverage.NewCFGGuidedAnalyzer(
			cfg.Compiler.Fuzz.CFGFilePath,
			targetFunctions,
			cfg.Compiler.SourceParentPath,
			mappingPath,
		)
		if err != nil {
			logger.Warn("Failed to create CFG analyzer: %v (continuing without target function tracking)", err)
			cfgAnalyzer = nil
		} else {
			logger.Info("CFG analyzer initialized, total target lines: %d", cfgAnalyzer.GetTotalTargetLines())
		}
	}

	// 12. Create and run fuzzing engine
	// Use CFGGuidedEngine if CFGAnalyzer is available (HLPFuzz-style constraint solving)
	// Otherwise use regular Engine (coverage-guided fuzzing)
	fmt.Println("[Fuzz] Starting fuzzing engine...")

	if cfgAnalyzer != nil {
		// CFG-guided mode: progressive constraint solving targeting uncovered BBs
		logger.Info("Using CFG-guided engine (HLPFuzz-style)")
		cfgEngine := fuzz.NewCFGGuidedEngine(fuzz.CFGGuidedConfig{
			Corpus:        corpusManager,
			Compiler:      gccCompiler,
			Executor:      seedExecutor,
			Coverage:      coverageTracker,
			Oracle:        oracleInstance,
			LLM:           llmClient,
			CFGAnalyzer:   cfgAnalyzer,
			PromptBuilder: promptBuilder,
			Understanding: understanding,
			MaxIterations: maxIterations,
			MaxRetries:    3, // Max retries with divergence analysis per target BB
			MappingPath:   filepath.Join(stateDir, "coverage_mapping.json"),
		})
		return cfgEngine.Run()
	}

	// Regular coverage-guided mode
	logger.Info("Using regular coverage-guided engine")
	engine := fuzz.NewEngine(fuzz.EngineConfig{
		Corpus:        corpusManager,
		Compiler:      gccCompiler,
		Executor:      seedExecutor,
		Coverage:      coverageTracker,
		Oracle:        oracleInstance,
		LLM:           llmClient,
		Metrics:       metricsManager,
		CFGAnalyzer:   cfgAnalyzer,
		PromptBuilder: promptBuilder,
		Understanding: understanding,
		MaxIterations: maxIterations,
		MaxNewSeeds:   maxNewSeeds,
		PrintInterval: 1, // Update UI every iteration
		EnableUI:      enableUI,
	})
	return engine.Run()
}
