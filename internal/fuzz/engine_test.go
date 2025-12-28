package fuzz

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/compiler"
	"github.com/zjy-dev/de-fuzz/internal/corpus"
	"github.com/zjy-dev/de-fuzz/internal/coverage"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
)

// --- Mock implementations ---

type MockCorpusManager struct {
	seeds         []*seed.Seed
	currentIndex  int
	initializeFn  func() error
	recoverFn     func() error
	addFn         func(s *seed.Seed) error
	reportResults []corpus.FuzzResult
	nextID        uint64
}

func (m *MockCorpusManager) Initialize() error {
	if m.initializeFn != nil {
		return m.initializeFn()
	}
	return nil
}

func (m *MockCorpusManager) Recover() error {
	if m.recoverFn != nil {
		return m.recoverFn()
	}
	return nil
}

func (m *MockCorpusManager) Add(s *seed.Seed) error {
	if m.addFn != nil {
		return m.addFn(s)
	}
	m.seeds = append(m.seeds, s)
	return nil
}

func (m *MockCorpusManager) AllocateID() uint64 {
	m.nextID++
	return m.nextID
}

func (m *MockCorpusManager) Next() (*seed.Seed, bool) {
	if m.currentIndex >= len(m.seeds) {
		return nil, false
	}
	s := m.seeds[m.currentIndex]
	m.currentIndex++
	return s, true
}

func (m *MockCorpusManager) ReportResult(id uint64, result corpus.FuzzResult) error {
	m.reportResults = append(m.reportResults, result)
	return nil
}

func (m *MockCorpusManager) Len() int {
	return len(m.seeds) - m.currentIndex
}

func (m *MockCorpusManager) Save() error {
	return nil
}

type MockCompiler struct {
	compileFn func(s *seed.Seed) (*compiler.CompileResult, error)
}

func (m *MockCompiler) Compile(s *seed.Seed) (*compiler.CompileResult, error) {
	if m.compileFn != nil {
		return m.compileFn(s)
	}
	return &compiler.CompileResult{
		BinaryPath: "/tmp/test_binary",
		Success:    true,
	}, nil
}

func (m *MockCompiler) CompileWithCoverage(s *seed.Seed) (*compiler.CompileResult, error) {
	return m.Compile(s)
}

func (m *MockCompiler) GetWorkDir() string {
	return "/tmp"
}

type MockExecutor struct {
	executeFn func(s *seed.Seed, binaryPath string) ([]executor.ExecutionResult, error)
}

func (m *MockExecutor) Execute(s *seed.Seed, binaryPath string) ([]executor.ExecutionResult, error) {
	if m.executeFn != nil {
		return m.executeFn(s, binaryPath)
	}
	return []executor.ExecutionResult{
		{Stdout: "output", Stderr: "", ExitCode: 0},
	}, nil
}

type MockCoverage struct {
	measureFn     func(s *seed.Seed) (coverage.Report, error)
	hasIncreaseFn func(report coverage.Report) (bool, error)
	getIncreaseFn func(report coverage.Report) (*coverage.CoverageIncrease, error)
	mergeFn       func(report coverage.Report) error
	getStatsFn    func() (*coverage.CoverageStats, error)
}

func (m *MockCoverage) Clean() error {
	return nil
}

func (m *MockCoverage) Measure(s *seed.Seed) (coverage.Report, error) {
	if m.measureFn != nil {
		return m.measureFn(s)
	}
	return nil, nil
}

func (m *MockCoverage) HasIncreased(report coverage.Report) (bool, error) {
	if m.hasIncreaseFn != nil {
		return m.hasIncreaseFn(report)
	}
	return false, nil
}

func (m *MockCoverage) GetIncrease(report coverage.Report) (*coverage.CoverageIncrease, error) {
	if m.getIncreaseFn != nil {
		return m.getIncreaseFn(report)
	}
	return &coverage.CoverageIncrease{
		Summary:         "Test coverage increase",
		FormattedReport: "Detailed report",
	}, nil
}

func (m *MockCoverage) Merge(report coverage.Report) error {
	if m.mergeFn != nil {
		return m.mergeFn(report)
	}
	return nil
}

func (m *MockCoverage) GetTotalReport() (coverage.Report, error) {
	return nil, nil
}

func (m *MockCoverage) GetStats() (*coverage.CoverageStats, error) {
	if m.getStatsFn != nil {
		return m.getStatsFn()
	}
	return &coverage.CoverageStats{
		CoveragePercentage: 50.0,
		TotalLines:         100,
		TotalCoveredLines:  50,
	}, nil
}

type MockOracle struct {
	analyzeFn func(s *seed.Seed, ctx *oracle.AnalyzeContext, results []oracle.Result) (*oracle.Bug, error)
}

func (m *MockOracle) Analyze(s *seed.Seed, ctx *oracle.AnalyzeContext, results []oracle.Result) (*oracle.Bug, error) {
	if m.analyzeFn != nil {
		return m.analyzeFn(s, ctx, results)
	}
	return nil, nil
}

// --- Tests ---

func TestNewEngine(t *testing.T) {
	cfg := EngineConfig{
		MaxIterations: 100,
	}

	engine := NewEngine(cfg)

	assert.NotNil(t, engine)
	assert.Equal(t, 100, engine.cfg.MaxIterations)
	assert.Empty(t, engine.bugsFound)
}

func TestEngine_Run_EmptyCorpus(t *testing.T) {
	mockCorpus := &MockCorpusManager{
		seeds: []*seed.Seed{},
	}

	engine := NewEngine(EngineConfig{
		Corpus: mockCorpus,
	})

	err := engine.Run()

	require.NoError(t, err)
	assert.Equal(t, 0, engine.GetIterationCount())
}

func TestEngine_Run_SingleSeed(t *testing.T) {
	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 1},
		Content: "int main() { return 0; }",
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: ""},
		},
	}

	mockCorpus := &MockCorpusManager{
		seeds: []*seed.Seed{testSeed},
	}

	mockCompiler := &MockCompiler{}
	mockExecutor := &MockExecutor{}
	mockCoverage := &MockCoverage{}

	engine := NewEngine(EngineConfig{
		Corpus:   mockCorpus,
		Compiler: mockCompiler,
		Executor: mockExecutor,
		Coverage: mockCoverage,
	})

	err := engine.Run()

	require.NoError(t, err)
	assert.Equal(t, 1, engine.GetIterationCount())
	assert.Len(t, mockCorpus.reportResults, 1)
}

func TestEngine_Run_MaxIterations(t *testing.T) {
	seeds := make([]*seed.Seed, 10)
	for i := range seeds {
		seeds[i] = &seed.Seed{
			Meta:      seed.Metadata{ID: uint64(i + 1)},
			Content:   "int main() { return 0; }",
			TestCases: []seed.TestCase{{RunningCommand: "./a.out"}},
		}
	}

	mockCorpus := &MockCorpusManager{seeds: seeds}
	mockCompiler := &MockCompiler{}
	mockExecutor := &MockExecutor{}
	mockCoverage := &MockCoverage{}

	engine := NewEngine(EngineConfig{
		Corpus:        mockCorpus,
		Compiler:      mockCompiler,
		Executor:      mockExecutor,
		Coverage:      mockCoverage,
		MaxIterations: 3,
	})

	err := engine.Run()

	require.NoError(t, err)
	assert.Equal(t, 3, engine.GetIterationCount())
}

func TestEngine_Run_BugDetection(t *testing.T) {
	testSeed := &seed.Seed{
		Meta:      seed.Metadata{ID: 1},
		Content:   "int main() { int *p = 0; *p = 1; return 0; }",
		TestCases: []seed.TestCase{{RunningCommand: "./a.out"}},
	}

	mockCorpus := &MockCorpusManager{seeds: []*seed.Seed{testSeed}}
	mockCompiler := &MockCompiler{}
	mockExecutor := &MockExecutor{
		executeFn: func(s *seed.Seed, binaryPath string) ([]executor.ExecutionResult, error) {
			return []executor.ExecutionResult{
				{Stdout: "", Stderr: "Segmentation fault", ExitCode: 139},
			}, nil
		},
	}
	mockCoverage := &MockCoverage{}
	mockOracle := &MockOracle{
		analyzeFn: func(s *seed.Seed, ctx *oracle.AnalyzeContext, results []oracle.Result) (*oracle.Bug, error) {
			return &oracle.Bug{
				Seed:        s,
				Description: "Null pointer dereference",
			}, nil
		},
	}

	engine := NewEngine(EngineConfig{
		Corpus:   mockCorpus,
		Compiler: mockCompiler,
		Executor: mockExecutor,
		Coverage: mockCoverage,
		Oracle:   mockOracle,
	})

	err := engine.Run()

	require.NoError(t, err)
	bugs := engine.GetBugs()
	assert.Len(t, bugs, 1)
	assert.Equal(t, "Null pointer dereference", bugs[0].Description)
}

func TestEngine_Run_CompileFailure(t *testing.T) {
	testSeed := &seed.Seed{
		Meta:      seed.Metadata{ID: 1},
		Content:   "invalid c code",
		TestCases: []seed.TestCase{{RunningCommand: "./a.out"}},
	}

	mockCorpus := &MockCorpusManager{seeds: []*seed.Seed{testSeed}}
	mockCompiler := &MockCompiler{
		compileFn: func(s *seed.Seed) (*compiler.CompileResult, error) {
			return &compiler.CompileResult{
				Success: false,
				Stderr:  "syntax error",
			}, nil
		},
	}
	mockExecutor := &MockExecutor{}
	mockCoverage := &MockCoverage{}

	engine := NewEngine(EngineConfig{
		Corpus:   mockCorpus,
		Compiler: mockCompiler,
		Executor: mockExecutor,
		Coverage: mockCoverage,
	})

	err := engine.Run()

	require.NoError(t, err)
	assert.Len(t, mockCorpus.reportResults, 1)
}

func TestEngine_GetBugs(t *testing.T) {
	engine := NewEngine(EngineConfig{})

	assert.Empty(t, engine.GetBugs())

	engine.bugsFound = []*oracle.Bug{
		{Description: "Bug 1"},
		{Description: "Bug 2"},
	}

	bugs := engine.GetBugs()
	assert.Len(t, bugs, 2)
}

func TestEngine_GetIterationCount(t *testing.T) {
	engine := NewEngine(EngineConfig{})

	assert.Equal(t, 0, engine.GetIterationCount())

	engine.iterationCount = 42
	assert.Equal(t, 42, engine.GetIterationCount())
}
