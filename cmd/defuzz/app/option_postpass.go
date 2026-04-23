package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/zjy-dev/de-fuzz/internal/config"
	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/logger"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/postpass"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	seedexecutor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
	"github.com/zjy-dev/de-fuzz/internal/vm"
)

// NewOptionPostPassCommand creates the "option-postpass" subcommand.
func NewOptionPostPassCommand() *cobra.Command {
	var (
		output     string
		runDir     string
		isa        string
		strategy   string
		logDir     string
		timeout    int
		useQEMU    bool
		workers    int
		matrixPath string
		runName    string
	)

	cmd := &cobra.Command{
		Use:   "option-postpass",
		Short: "Replay corpus seeds across deterministic defense-option combinations.",
		Long: `Replay persisted corpus seeds across deterministic strategy-managed compiler option combinations.

This command:
  1. Scans the persisted corpus under {output}/{isa}/{strategy}/corpus
  2. Loads each seed's compile_command.json as the baseline compile record
  3. Removes strategy-owned flag families from that baseline
  4. Traverses the configured option matrix for the active strategy/ISA
  5. Recompiles, reruns, and re-analyzes each combination with the configured oracle

Results are written under:
  {output}/{isa}/{strategy}/postpass/{run-name}/

The main corpus is not mutated by this command.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfigWithOverrides(isa, strategy)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if !cmd.Flags().Changed("output") {
				output = cfg.Compiler.Fuzz.OutputRootDir
			}
			if !cmd.Flags().Changed("log-dir") {
				logDir = cfg.LogDir
			}
			if !cmd.Flags().Changed("timeout") {
				timeout = cfg.Compiler.Fuzz.Timeout
			}
			if !cmd.Flags().Changed("use-qemu") {
				useQEMU = cfg.Compiler.Fuzz.UseQEMU
			}
			if runName == "" {
				runName = time.Now().Format("20060102_150405")
			}

			outputDir := filepath.Join(output, cfg.ISA, cfg.Strategy)
			if runDir != "" {
				outputDir = runDir
			}
			return runOptionPostPass(cfg, outputDir, logDir, timeout, useQEMU, workers, matrixPath, runName)
		},
	}

	cmd.Flags().StringVar(&output, "output", "fuzz_out", "Output directory root (actual output at {output}/{isa}/{strategy})")
	cmd.Flags().StringVar(&runDir, "run-dir", "", "Direct path to a specific fuzz run directory (overrides --output/--isa/--strategy path composition)")
	cmd.Flags().StringVar(&isa, "isa", "", "Override ISA from config.yaml when selecting compiler config and default run directory")
	cmd.Flags().StringVar(&strategy, "strategy", "", "Override defense strategy from config.yaml when selecting compiler config and default run directory")
	cmd.Flags().StringVar(&logDir, "log-dir", "", "Log file directory (timestamped log files, empty = console only)")
	cmd.Flags().IntVar(&timeout, "timeout", 30, "Execution timeout in seconds")
	cmd.Flags().BoolVar(&useQEMU, "use-qemu", false, "Use QEMU for cross-architecture execution")
	cmd.Flags().IntVar(&workers, "workers", 0, "Number of concurrent replay workers (0 = use strategy default from postpass.yaml)")
	cmd.Flags().StringVar(&matrixPath, "matrix-config", "configs/postpass.yaml", "Path to the strategy option post-pass matrix config")
	cmd.Flags().StringVar(&runName, "run-name", "", "Optional run name for the post-pass output directory")

	return cmd
}

func runOptionPostPass(
	cfg *config.Config,
	outputDir string,
	logDir string,
	timeout int,
	useQEMU bool,
	workers int,
	matrixPath string,
	runName string,
) error {
	logLevel := cfg.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}

	if logDir != "" {
		if err := logger.InitWithFile(logLevel, logDir); err != nil {
			return fmt.Errorf("failed to initialize logger with file: %w", err)
		}
		defer logger.Close()
	} else {
		logger.Init(logLevel)
	}

	logger.Info("[PostPass] Target: %s / %s", cfg.ISA, cfg.Strategy)
	logger.Info("[PostPass] Output directory: %s", outputDir)
	logger.Info("[PostPass] Matrix config: %s", matrixPath)

	matrixCfg, err := postpass.LoadConfig(matrixPath)
	if err != nil {
		return err
	}
	matrix, err := matrixCfg.Strategy(cfg.Strategy)
	if err != nil {
		return err
	}
	if workers <= 0 {
		workers = matrix.Workers
		if workers <= 0 {
			workers = 1
		}
	}
	logger.Info("[PostPass] Workers: %d", workers)

	var llmClient llm.LLM
	promptBuilder := prompt.NewBuilder(cfg.Compiler.Fuzz.MaxTestCases, cfg.Compiler.Fuzz.FunctionTemplate)
	understanding := ""
	basePath := filepath.Join("initial_seeds", cfg.ISA, cfg.Strategy)
	if content, err := seed.LoadUnderstanding(basePath); err == nil {
		understanding = content
	}

	if cfg.Compiler.Oracle.Type == "llm" {
		llmClient, err = llm.New(cfg.RemixerConfigPath, cfg.DefaultTemperature)
		if err != nil {
			return fmt.Errorf("failed to create LLM client: %w", err)
		}
	}

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

	var oracleExecutor oracle.Executor
	var seedExec seedexecutor.Executor
	if useQEMU {
		oracleExecutor = seedexecutor.NewQEMUOracleExecutorAdapter(
			cfg.Compiler.Fuzz.QEMUPath,
			cfg.Compiler.Fuzz.QEMUSysroot,
			timeout,
		)
		seedExec = seedexecutor.NewQEMUExecutor(vm.QEMUConfig{
			QEMUPath: cfg.Compiler.Fuzz.QEMUPath,
			Sysroot:  cfg.Compiler.Fuzz.QEMUSysroot,
		}, timeout)
	} else {
		oracleExecutor = seedexecutor.NewOracleExecutorAdapter(timeout)
		seedExec = seedexecutor.NewLocalExecutor(timeout)
	}

	runRoot, corpusDir, err := resolveRunRootAndCorpusDir(outputDir)
	if err != nil {
		return err
	}
	logger.Info("[PostPass] Resolved run root: %s", runRoot)
	logger.Info("[PostPass] Resolved corpus dir: %s", corpusDir)

	runner := postpass.NewRunner(postpass.RunnerConfig{
		Strategy:       cfg.Strategy,
		ISA:            cfg.ISA,
		RunName:        runName,
		Workers:        workers,
		CorpusDir:      corpusDir,
		OutputDir:      runRoot,
		CompilerPath:   cfg.Compiler.Path,
		PrefixPath:     filepath.Dir(cfg.Compiler.Path),
		Matrix:         matrix,
		Oracle:         oracleInstance,
		OracleExecutor: oracleExecutor,
		SeedExecutor:   seedExec,
	})

	summary, err := runner.Run()
	if err != nil {
		return err
	}

	logger.Info("[PostPass] Completed run %s: seeds=%d combos=%d attempts=%d bugs=%d compile_failures=%d oracle_errors=%d skipped_seeds=%d skipped_combos=%d",
		summary.RunName,
		summary.SeedCount,
		summary.CombinationCount,
		summary.AttemptCount,
		summary.BugsFound,
		summary.CompileFailures,
		summary.OracleErrors,
		summary.SkippedSeeds,
		summary.SkippedCombos,
	)

	return nil
}

func resolveRunRootAndCorpusDir(path string) (string, string, error) {
	cleaned := filepath.Clean(path)
	info, err := os.Stat(cleaned)
	if err != nil {
		return "", "", fmt.Errorf("postpass input path not found %s: %w", cleaned, err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("postpass input path is not a directory: %s", cleaned)
	}

	corpusCandidate := filepath.Join(cleaned, "corpus")
	if corpusInfo, err := os.Stat(corpusCandidate); err == nil && corpusInfo.IsDir() {
		return cleaned, corpusCandidate, nil
	}

	if filepath.Base(cleaned) == "corpus" {
		return filepath.Dir(cleaned), cleaned, nil
	}

	return "", "", fmt.Errorf("corpus directory not found under %s and path itself is not a corpus directory", cleaned)
}
