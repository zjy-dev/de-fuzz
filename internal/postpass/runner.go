package postpass

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/zjy-dev/de-fuzz/internal/compiler"
	"github.com/zjy-dev/de-fuzz/internal/logger"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	seedexecutor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
)

// RunnerConfig captures the dependencies needed for a post-pass run.
type RunnerConfig struct {
	Strategy       string
	ISA            string
	RunName        string
	Workers        int
	CorpusDir      string
	OutputDir      string
	CompilerPath   string
	PrefixPath     string
	Matrix         *StrategyMatrix
	Oracle         oracle.Oracle
	OracleExecutor oracle.Executor
	SeedExecutor   seedexecutor.Executor
}

// RunSummary aggregates high-level results for a post-pass run.
type RunSummary struct {
	RunName          string    `json:"run_name"`
	Strategy         string    `json:"strategy"`
	ISA              string    `json:"isa"`
	StartedAt        time.Time `json:"started_at"`
	CompletedAt      time.Time `json:"completed_at"`
	SeedCount        int       `json:"seed_count"`
	CombinationCount int       `json:"combination_count"`
	AttemptCount     int       `json:"attempt_count"`
	CompileFailures  int       `json:"compile_failures"`
	OracleErrors     int       `json:"oracle_errors"`
	BugsFound        int       `json:"bugs_found"`
	SkippedSeeds     int       `json:"skipped_seeds"`
	SkippedNoRecord  int       `json:"skipped_missing_compile_record"`
}

// AttemptRecord captures a single post-pass replay attempt.
type AttemptRecord struct {
	SeedID              uint64                  `json:"seed_id"`
	SeedDir             string                  `json:"seed_dir"`
	SeedSourcePath      string                  `json:"seed_source_path"`
	OriginalProfileName string                  `json:"original_profile_name,omitempty"`
	Strategy            string                  `json:"strategy"`
	ISA                 string                  `json:"isa"`
	RunName             string                  `json:"run_name"`
	AttemptedAt         time.Time               `json:"attempted_at"`
	CombinationName     string                  `json:"combination_name"`
	CombinationValues   map[string]string       `json:"combination_values,omitempty"`
	BaselineFlags       []string                `json:"baseline_flags,omitempty"`
	RemovedOwnedFlags   []string                `json:"removed_owned_flags,omitempty"`
	StrategyFlags       []string                `json:"strategy_flags,omitempty"`
	CompileSuccess      bool                    `json:"compile_success"`
	CompileError        string                  `json:"compile_error,omitempty"`
	ExecutionResults    []ExecutionResultRecord `json:"execution_results,omitempty"`
	OracleVerdict       string                  `json:"oracle_verdict"`
	OracleError         string                  `json:"oracle_error,omitempty"`
	BugDescription      string                  `json:"bug_description,omitempty"`
}

// ExecutionResultRecord is the JSON form of a reused test-case execution result.
type ExecutionResultRecord struct {
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
}

// Runner executes the option post-pass.
type Runner struct {
	cfg RunnerConfig
}

type attemptTask struct {
	original          *seed.Seed
	record            *seed.CompilationRecord
	combo             MaterializedCombo
	baselineFlags     []string
	removedOwnedFlags []string
	attemptDir        string
}

type attemptResult struct {
	attempt    *AttemptRecord
	attemptDir string
	err        error
}

// NewRunner creates a new option post-pass runner.
func NewRunner(cfg RunnerConfig) *Runner {
	return &Runner{cfg: cfg}
}

// Run scans the corpus and executes the deterministic option traversal.
func (r *Runner) Run() (*RunSummary, error) {
	if r.cfg.Matrix == nil {
		return nil, fmt.Errorf("postpass matrix is nil")
	}
	if r.cfg.CorpusDir == "" {
		return nil, fmt.Errorf("corpus dir is required")
	}
	if r.cfg.OutputDir == "" {
		return nil, fmt.Errorf("output dir is required")
	}
	if r.cfg.CompilerPath == "" {
		return nil, fmt.Errorf("compiler path is required")
	}
	if r.cfg.Oracle == nil {
		return nil, fmt.Errorf("oracle is required")
	}
	if r.cfg.OracleExecutor == nil {
		return nil, fmt.Errorf("oracle executor is required")
	}
	if r.cfg.SeedExecutor == nil {
		return nil, fmt.Errorf("seed executor is required")
	}
	if r.cfg.Workers <= 0 {
		r.cfg.Workers = 1
	}

	combos, err := r.cfg.Matrix.Materialize(r.cfg.ISA)
	if err != nil {
		return nil, err
	}
	if len(combos) == 0 {
		return nil, fmt.Errorf("no option combinations materialized for %s/%s", r.cfg.Strategy, r.cfg.ISA)
	}

	seeds, err := seed.LoadSeedsWithMetadata(r.cfg.CorpusDir, seed.NewDefaultNamingStrategy())
	if err != nil {
		return nil, fmt.Errorf("load corpus seeds: %w", err)
	}
	sort.SliceStable(seeds, func(i, j int) bool {
		return seeds[i].Meta.ID < seeds[j].Meta.ID
	})

	runDir := filepath.Join(r.cfg.OutputDir, "postpass", r.cfg.RunName)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return nil, fmt.Errorf("create postpass run dir %s: %w", runDir, err)
	}

	summary := &RunSummary{
		RunName:          r.cfg.RunName,
		Strategy:         r.cfg.Strategy,
		ISA:              r.cfg.ISA,
		StartedAt:        time.Now(),
		SeedCount:        len(seeds),
		CombinationCount: len(combos),
	}

	tasks := make([]attemptTask, 0, len(seeds)*len(combos))

	for _, corpusSeed := range seeds {
		seedDir := filepath.Dir(corpusSeed.Meta.ContentPath)
		record, err := seed.LoadCompilationRecord(seedDir)
		if err != nil {
			logger.Warn("[PostPass] Skipping seed %d (%s): missing compile_command.json: %v", corpusSeed.Meta.ID, corpusSeed.Meta.FilePath, err)
			summary.SkippedSeeds++
			summary.SkippedNoRecord++
			continue
		}

		logger.Info("[PostPass] Replaying seed %d (%s) across %d combinations", corpusSeed.Meta.ID, corpusSeed.Meta.FilePath, len(combos))
		baselineFlags, removedOwnedFlags := ReconstructBaseline(record, r.cfg.Matrix.StripRules)

		for _, combo := range combos {
			attemptDir := filepath.Join(runDir, corpusSeed.Meta.FilePath, combo.SafeName())
			tasks = append(tasks, attemptTask{
				original:          corpusSeed,
				record:            record,
				combo:             combo,
				baselineFlags:     append([]string(nil), baselineFlags...),
				removedOwnedFlags: append([]string(nil), removedOwnedFlags...),
				attemptDir:        attemptDir,
			})
		}
	}

	summary.AttemptCount = len(tasks)
	logger.Info("[PostPass] Starting %d attempts with %d worker(s)", len(tasks), r.cfg.Workers)

	if err := r.runTasks(tasks, summary); err != nil {
		return nil, err
	}

	summary.CompletedAt = time.Now()
	if err := saveJSON(filepath.Join(runDir, "summary.json"), summary); err != nil {
		return nil, fmt.Errorf("save postpass summary: %w", err)
	}

	return summary, nil
}

func (r *Runner) runTasks(tasks []attemptTask, summary *RunSummary) error {
	if len(tasks) == 0 {
		return nil
	}

	workerCount := r.cfg.Workers
	if workerCount > len(tasks) {
		workerCount = len(tasks)
	}

	taskCh := make(chan attemptTask)
	resultCh := make(chan attemptResult, workerCount)

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskCh {
				attempt, err := r.runAttempt(
					task.original,
					task.record,
					task.combo,
					task.baselineFlags,
					task.removedOwnedFlags,
					task.attemptDir,
				)
				resultCh <- attemptResult{
					attempt:    attempt,
					attemptDir: task.attemptDir,
					err:        err,
				}
			}
		}()
	}

	go func() {
		for _, task := range tasks {
			taskCh <- task
		}
		close(taskCh)
		wg.Wait()
		close(resultCh)
	}()

	var firstErr error
	for result := range resultCh {
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		if result.attempt == nil {
			continue
		}

		attempt := result.attempt
		if !attempt.CompileSuccess {
			summary.CompileFailures++
			logger.Warn("[PostPass] Compile failed: seed=%d combo=%s dir=%s error=%s",
				attempt.SeedID, attempt.CombinationName, result.attemptDir, attempt.CompileError)
		}
		if attempt.OracleError != "" {
			summary.OracleErrors++
			logger.Warn("[PostPass] Oracle error: seed=%d combo=%s dir=%s error=%s",
				attempt.SeedID, attempt.CombinationName, result.attemptDir, attempt.OracleError)
		}
		if attempt.OracleVerdict == "BUG" {
			summary.BugsFound++
			logger.Error("[PostPass] BUG FOUND: seed=%d combo=%s dir=%s description=%s",
				attempt.SeedID, attempt.CombinationName, result.attemptDir, attempt.BugDescription)
		}
	}

	return firstErr
}

func (r *Runner) runAttempt(
	original *seed.Seed,
	record *seed.CompilationRecord,
	combo MaterializedCombo,
	baselineFlags []string,
	removedOwnedFlags []string,
	attemptDir string,
) (*AttemptRecord, error) {
	if err := os.MkdirAll(attemptDir, 0755); err != nil {
		return nil, fmt.Errorf("create attempt dir %s: %w", attemptDir, err)
	}

	replaySeed := cloneSeed(original)
	replaySeed.FlagProfile = &seed.FlagProfile{
		Name:       combo.Name,
		AxisValues: cloneStringMap(combo.GroupValues),
		Flags:      append([]string(nil), combo.StrategyFlags...),
	}

	if err := persistReusedArtifacts(attemptDir, replaySeed); err != nil {
		return nil, fmt.Errorf("persist attempt seed artifacts: %w", err)
	}

	compilerInstance := compiler.NewGCCCompiler(compiler.GCCCompilerConfig{
		GCCPath:          r.cfg.CompilerPath,
		WorkDir:          attemptDir,
		PrefixPath:       r.cfg.PrefixPath,
		CFlags:           append([]string(nil), baselineFlags...),
		DisableLLMCFlags: true,
	})

	compileResult, compileErr := compilerInstance.Compile(replaySeed)
	attempt := &AttemptRecord{
		SeedID:              original.Meta.ID,
		SeedDir:             original.Meta.FilePath,
		SeedSourcePath:      original.Meta.ContentPath,
		OriginalProfileName: record.ProfileName,
		Strategy:            r.cfg.Strategy,
		ISA:                 r.cfg.ISA,
		RunName:             r.cfg.RunName,
		AttemptedAt:         time.Now(),
		CombinationName:     combo.Name,
		CombinationValues:   cloneStringMap(combo.GroupValues),
		BaselineFlags:       append([]string(nil), baselineFlags...),
		RemovedOwnedFlags:   append([]string(nil), removedOwnedFlags...),
		StrategyFlags:       append([]string(nil), combo.StrategyFlags...),
		OracleVerdict:       "SKIPPED",
	}

	if compileErr != nil {
		attempt.CompileError = compileErr.Error()
		if err := saveJSON(filepath.Join(attemptDir, "attempt.json"), attempt); err != nil {
			return nil, fmt.Errorf("save failed attempt record: %w", err)
		}
		return attempt, nil
	}

	if compileResult != nil {
		compileRecord := compileResult.ToCompilationRecord(
			original.Meta.ID,
			filepath.Join(attemptDir, "source.c"),
		)
		if compileRecord != nil {
			if err := seed.SaveCompilationRecord(attemptDir, compileRecord); err != nil {
				return nil, fmt.Errorf("save replay compile record: %w", err)
			}
		}
	}

	attempt.CompileSuccess = compileResult != nil && compileResult.Success
	if !attempt.CompileSuccess {
		if compileResult != nil {
			attempt.CompileError = compileResult.Stderr
		}
		if err := saveJSON(filepath.Join(attemptDir, "attempt.json"), attempt); err != nil {
			return nil, fmt.Errorf("save compile-failed attempt record: %w", err)
		}
		return attempt, nil
	}

	executionResults, oracleResults, execErr := r.executeSeed(replaySeed, compileResult.BinaryPath)
	attempt.ExecutionResults = executionResults
	if execErr != nil {
		attempt.OracleError = execErr.Error()
		attempt.OracleVerdict = "ERROR"
		if err := saveJSON(filepath.Join(attemptDir, "attempt.json"), attempt); err != nil {
			return nil, fmt.Errorf("save execution-error attempt record: %w", err)
		}
		return attempt, nil
	}

	bug, oracleErr := r.cfg.Oracle.Analyze(replaySeed, &oracle.AnalyzeContext{
		BinaryPath: compileResult.BinaryPath,
		Executor:   r.cfg.OracleExecutor,
	}, oracleResults)
	if oracleErr != nil {
		attempt.OracleError = oracleErr.Error()
		attempt.OracleVerdict = "ERROR"
	} else if bug != nil {
		attempt.OracleVerdict = "BUG"
		attempt.BugDescription = bug.Description
	} else {
		attempt.OracleVerdict = "NORMAL"
	}

	if err := saveJSON(filepath.Join(attemptDir, "attempt.json"), attempt); err != nil {
		return nil, fmt.Errorf("save attempt record: %w", err)
	}
	return attempt, nil
}

func (r *Runner) executeSeed(s *seed.Seed, binaryPath string) ([]ExecutionResultRecord, []oracle.Result, error) {
	results, err := r.cfg.SeedExecutor.Execute(s, binaryPath)
	if err != nil {
		return nil, nil, err
	}

	executionRecords := make([]ExecutionResultRecord, 0, len(results))
	oracleResults := make([]oracle.Result, 0, len(results))
	for _, result := range results {
		executionRecords = append(executionRecords, ExecutionResultRecord{
			Stdout:   result.Stdout,
			Stderr:   result.Stderr,
			ExitCode: result.ExitCode,
		})
		oracleResults = append(oracleResults, oracle.Result{
			Stdout:   result.Stdout,
			Stderr:   result.Stderr,
			ExitCode: result.ExitCode,
		})
	}

	return executionRecords, oracleResults, nil
}

func cloneSeed(s *seed.Seed) *seed.Seed {
	if s == nil {
		return &seed.Seed{}
	}

	testCases := make([]seed.TestCase, 0, len(s.TestCases))
	for _, tc := range s.TestCases {
		testCases = append(testCases, tc)
	}

	cloned := &seed.Seed{
		Meta:      s.Meta,
		Content:   s.Content,
		TestCases: testCases,
	}
	return cloned
}

func persistReusedArtifacts(dir string, s *seed.Seed) error {
	if err := os.WriteFile(filepath.Join(dir, "source.c"), []byte(s.Content), 0644); err != nil {
		return fmt.Errorf("write source.c: %w", err)
	}

	if len(s.TestCases) > 0 {
		data, err := json.MarshalIndent(s.TestCases, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal testcases: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "testcases.json"), data, 0644); err != nil {
			return fmt.Errorf("write testcases.json: %w", err)
		}
	}

	if s.FlagProfile != nil {
		data, err := json.MarshalIndent(s.FlagProfile, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal flag profile: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "flag_profile.json"), data, 0644); err != nil {
			return fmt.Errorf("write flag_profile.json: %w", err)
		}
	}

	return nil
}

func saveJSON(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
