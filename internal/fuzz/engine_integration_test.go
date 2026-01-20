//go:build integration
// +build integration

package fuzz

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zjy-dev/de-fuzz/internal/compiler"
	"github.com/zjy-dev/de-fuzz/internal/corpus"
	"github.com/zjy-dev/de-fuzz/internal/coverage"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
)

// testSourceFile is the full path as it appears in CFG files
const testSourceFile = "/root/project/de-fuzz/target_compilers/gcc-v12.2.0-x64/gcc-releases-gcc-12.2.0/gcc/cfgexpand.cc"

func TestEngine_Integration_BasicFlow(t *testing.T) {
	// Check if CFG file exists
	cfgPath := "/root/project/de-fuzz/target_compilers/gcc-v12.2.0-x64/gcc-build/gcc/cfgexpand.cc.015t.cfg"
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Skipf("CFG file not found at %s", cfgPath)
	}

	tmpDir, err := os.MkdirTemp("", "cfg-engine-integration-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Setup paths
	mappingPath := filepath.Join(tmpDir, "mapping.json")

	// Create corpus with one initial seed
	corp := corpus.NewFileManager(tmpDir)

	err = corp.Initialize()
	require.NoError(t, err)

	initialSeed := &seed.Seed{
		Meta: seed.Metadata{
			ID:    1,
			State: seed.SeedStatePending,
			Depth: 0,
		},
		Content: `int main() { return 0; }`,
	}
	err = corp.Add(initialSeed)
	require.NoError(t, err)

	// Create CFG-guided analyzer
	targetFunctions := []string{"stack_protect_classify_type"}
	cfgAnalyzer, err := coverage.NewAnalyzer(
		cfgPath,
		targetFunctions,
		"",
		mappingPath,
		0.8,
	)
	require.NoError(t, err)

	// Create mock components
	mockCompiler := &mockCompiler{}
	mockExecutor := &mockExecutor{}
	mockLLM := &mockLLM{response: "int main() { int x = 42; return x; }"}

	// Create prompt builder
	promptBuilder := prompt.NewBuilder(0, "")

	// Create engine
	cfg := Config{
		Corpus:          corp,
		Compiler:        mockCompiler,
		Executor:        mockExecutor,
		Coverage:        nil, // Not needed for this test
		Oracle:          nil,
		LLM:             mockLLM,
		Analyzer:        cfgAnalyzer,
		PromptBuilder:   promptBuilder,
		Understanding:   "Test compiler fuzzing",
		MaxIterations:   5,
		MaxRetries:      2,
		SaveInterval:    time.Minute,
		CoverageTimeout: 10,
		MappingPath:     mappingPath,
	}

	engine := NewEngine(cfg)
	require.NotNil(t, engine)

	// Test 1: Process initial seeds
	t.Run("process_initial_seeds", func(t *testing.T) {
		// The engine should process the initial seed
		// For now, we just verify it doesn't crash
		// In a real test, we'd mock the coverage measurement

		// Save state
		err := corp.Save()
		require.NoError(t, err)
	})

	// Test 2: Basic statistics
	t.Run("engine_statistics", func(t *testing.T) {
		assert.Equal(t, 0, engine.GetIterationCount())
		assert.Equal(t, 0, engine.GetTargetHits())
		assert.Empty(t, engine.GetBugs())
	})
}

// Mock implementations for testing

type mockCompiler struct {
	compileCount int
}

func (m *mockCompiler) Compile(s *seed.Seed) (*compiler.CompileResult, error) {
	m.compileCount++
	return &compiler.CompileResult{
		Success:    true,
		BinaryPath: "/tmp/mock_binary",
		Stdout:     "",
		Stderr:     "",
	}, nil
}

func (m *mockCompiler) GetWorkDir() string {
	return "/tmp"
}

type mockExecutor struct {
	executeCount int
}

func (m *mockExecutor) Execute(s *seed.Seed, binaryPath string) ([]executor.ExecutionResult, error) {
	m.executeCount++
	return []executor.ExecutionResult{
		{
			Stdout:   "mock output",
			Stderr:   "",
			ExitCode: 0,
		},
	}, nil
}

type mockLLM struct {
	response  string
	callCount int
}

func (m *mockLLM) GetCompletion(prompt string) (string, error) {
	m.callCount++
	return m.response, nil
}

func (m *mockLLM) GetCompletionWithSystem(system, prompt string) (string, error) {
	m.callCount++
	return m.response, nil
}

func (m *mockLLM) Analyze(understanding string, query string, s *seed.Seed, diff string) (string, error) {
	return "mock analysis", nil
}

func (m *mockLLM) Understand(prompt string) (string, error) {
	return "mock understanding", nil
}

func (m *mockLLM) Generate(understanding, prompt string) (*seed.Seed, error) {
	return &seed.Seed{Content: m.response}, nil
}

func (m *mockLLM) Mutate(understanding, prompt string, s *seed.Seed) (*seed.Seed, error) {
	return &seed.Seed{Content: m.response}, nil
}

func TestEngine_Integration_TargetSelection(t *testing.T) {
	cfgPath := "/root/project/de-fuzz/target_compilers/gcc-v12.2.0-x64/gcc-build/gcc/cfgexpand.cc.015t.cfg"
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Skipf("CFG file not found")
	}

	tmpDir, err := os.MkdirTemp("", "cfg-target-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mappingPath := filepath.Join(tmpDir, "mapping.json")

	// Create analyzer
	cfgAnalyzer, err := coverage.NewAnalyzer(
		cfgPath,
		[]string{"stack_protect_classify_type", "stack_protect_decl_phase"},
		"",
		mappingPath,
		0.8,
	)
	require.NoError(t, err)

	// Test target selection without any coverage
	t.Run("select_initial_target", func(t *testing.T) {
		target := cfgAnalyzer.SelectTarget()
		require.NotNil(t, target)

		t.Logf("Initial target: %s:BB%d (succs=%d, lines=%v)",
			target.Function, target.BBID, target.SuccessorCount, target.Lines)

		// Should select a BB with maximum successors
		assert.Greater(t, target.SuccessorCount, 0)
		assert.NotEmpty(t, target.Function)
		assert.NotEmpty(t, target.Lines)
	})

	// Test progressive target selection
	t.Run("progressive_target_selection", func(t *testing.T) {
		selectedTargets := make(map[string]bool)

		for i := 0; i < 10; i++ {
			target := cfgAnalyzer.SelectTarget()
			if target == nil {
				t.Logf("All targets covered after %d iterations", i)
				break
			}

			targetKey := fmt.Sprintf("%s:BB%d", target.Function, target.BBID)
			selectedTargets[targetKey] = true

			t.Logf("Iteration %d: %s (succs=%d)", i+1, targetKey, target.SuccessorCount)

			// Simulate covering this target
			if len(target.Lines) > 0 {
				lines := make([]string, len(target.Lines))
				for j, line := range target.Lines {
					lines[j] = fmt.Sprintf("%s:%d", target.File, line)
				}
				cfgAnalyzer.RecordCoverage(int64(i+1), lines)
			}
		}

		t.Logf("Selected %d unique targets", len(selectedTargets))
		assert.Greater(t, len(selectedTargets), 0)
	})
}

func TestEngine_Integration_MappingPersistence(t *testing.T) {
	cfgPath := "/root/project/de-fuzz/target_compilers/gcc-v12.2.0-x64/gcc-build/gcc/cfgexpand.cc.015t.cfg"
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Skipf("CFG file not found")
	}

	tmpDir, err := os.MkdirTemp("", "cfg-persist-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mappingPath := filepath.Join(tmpDir, "mapping.json")

	// Create analyzer and record some coverage
	t.Run("record_coverage", func(t *testing.T) {
		analyzer, err := coverage.NewAnalyzer(
			cfgPath,
			[]string{"stack_protect_classify_type"},
			"",
			mappingPath,
			0.8,
		)
		require.NoError(t, err)

		// Record coverage for multiple iterations using full path matching CFG format
		// Use non-overlapping line numbers to get exactly 10 unique lines
		for i := 0; i < 5; i++ {
			lines := []string{
				fmt.Sprintf("%s:%d", testSourceFile, 1819+i*2),   // 1819, 1821, 1823, 1825, 1827
				fmt.Sprintf("%s:%d", testSourceFile, 1819+i*2+1), // 1820, 1822, 1824, 1826, 1828
			}
			analyzer.RecordCoverage(int64(i+1), lines)
		}

		// Save mapping
		err = analyzer.SaveMapping(mappingPath)
		require.NoError(t, err)

		// Get coverage stats
		funcCov := analyzer.GetFunctionCoverage()
		t.Logf("Coverage before save: %+v", funcCov["stack_protect_classify_type"])
	})

	// Load and verify
	t.Run("load_and_verify", func(t *testing.T) {
		analyzer, err := coverage.NewAnalyzer(
			cfgPath,
			[]string{"stack_protect_classify_type"},
			"",
			mappingPath,
			0.8,
		)
		require.NoError(t, err)

		// Verify coverage was loaded
		funcCov := analyzer.GetFunctionCoverage()
		coverage := funcCov["stack_protect_classify_type"]

		t.Logf("Coverage after load: %d/%d BBs", coverage.Covered, coverage.Total)
		assert.Greater(t, coverage.Covered, 0, "Should have loaded coverage")

		// Verify covered lines
		coveredLines := analyzer.GetCoveredLines()
		t.Logf("Loaded %d covered lines", len(coveredLines))
		assert.Equal(t, 10, len(coveredLines), "Should have 10 lines (5 iterations * 2 lines)")
	})
}

func TestEngine_Integration_CoverageProgression(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping coverage progression test in short mode")
	}

	cfgPath := "/root/project/de-fuzz/target_compilers/gcc-v12.2.0-x64/gcc-build/gcc/cfgexpand.cc.015t.cfg"
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Skipf("CFG file not found")
	}

	tmpDir, err := os.MkdirTemp("", "cfg-progression-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mappingPath := filepath.Join(tmpDir, "mapping.json")

	analyzer, err := coverage.NewAnalyzer(
		cfgPath,
		[]string{
			"stack_protect_classify_type",
			"stack_protect_decl_phase",
			"stack_protect_decl_phase_1",
			"stack_protect_decl_phase_2",
		},
		"",
		mappingPath,
		0.8,
	)
	require.NoError(t, err)

	// Simulate fuzzing campaign
	maxIterations := 50
	coverageHistory := make([]int, 0, maxIterations)

	// Record initial coverage
	funcCov := analyzer.GetFunctionCoverage()
	initialCovered := 0
	for _, cov := range funcCov {
		initialCovered += cov.Covered
	}
	coverageHistory = append(coverageHistory, initialCovered)

	for i := 0; i < maxIterations; i++ {
		target := analyzer.SelectTarget()
		if target == nil {
			t.Logf("All BBs covered after %d iterations!", i)
			break
		}

		// Simulate covering the target
		if len(target.Lines) > 0 {
			lines := make([]string, len(target.Lines))
			for j, targetLine := range target.Lines {
				lines[j] = fmt.Sprintf("%s:%d", target.File, targetLine)
			}
			analyzer.RecordCoverage(int64(i+1), lines)

			if i%10 == 0 {
				t.Logf("Iteration %d: target %s:BB%d",
					i, target.Function, target.BBID)
				// Record coverage stats
				funcCov := analyzer.GetFunctionCoverage()
				totalCovered := 0
				for _, cov := range funcCov {
					totalCovered += cov.Covered
				}
				coverageHistory = append(coverageHistory, totalCovered)
			}
		}
	}

	// Analyze coverage progression after loop completes
	t.Logf("\nCoverage progression over %d checkpoints:", len(coverageHistory))
	if len(coverageHistory) > 1 {
		t.Logf("  Start: %d BBs", coverageHistory[0])
		t.Logf("  End:   %d BBs", coverageHistory[len(coverageHistory)-1])
		t.Logf("  Gain:  %d BBs", coverageHistory[len(coverageHistory)-1]-coverageHistory[0])

		// Coverage should generally increase
		assert.GreaterOrEqual(t, coverageHistory[len(coverageHistory)-1], coverageHistory[0],
			"Coverage should not decrease over time")
	}
}
