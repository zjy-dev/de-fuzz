package coverage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/exec"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// LLVMTarget specifies a source file and the functions within it to track.
type LLVMTarget struct {
	File      string
	Functions []string
}

// LLVMCoverageConfig holds configuration for LLVMCoverage.
type LLVMCoverageConfig struct {
	Executor         exec.Executor
	CompileFunc      func(*seed.Seed) error // Compiles a seed with the instrumented clang (sets LLVM_PROFILE_FILE)
	CompilerBinary   string                 // Instrumented clang binary (the llvm-cov export target)
	ProfileDir       string                 // Directory where .profraw files are written / cleaned
	ProfdataCommand  string                 // llvm-profdata command (e.g. "llvm-profdata")
	CovCommand       string                 // llvm-cov command (e.g. "llvm-cov")
	DemanglerCommand string                 // optional demangler (e.g. "llvm-cxxfilt")
	TotalReportPath  string                 // canonical total report path
	SeedReportDir    string                 // directory for per-seed JSON reports
	Targets          []LLVMTarget           // target files/functions for filtering
}

// LLVMCoverage implements the Coverage interface using LLVM source-based coverage.
type LLVMCoverage struct {
	executor         exec.Executor
	compileFunc      func(*seed.Seed) error
	compilerBinary   string
	profileDir       string
	profdataCommand  string
	covCommand       string
	demanglerCommand string
	totalReportPath  string
	seedReportDir    string
	targets          []LLVMTarget

	// Cached increase data computed by HasIncreased, reused by GetIncrease.
	lastIncreaseLines []string
	lastIncreaseValid bool
	lastFirstSeed     bool
}

// NewLLVMCoverage creates a new LLVM coverage tracker.
func NewLLVMCoverage(cfg LLVMCoverageConfig) *LLVMCoverage {
	totalReportPath := cfg.TotalReportPath
	if !filepath.IsAbs(totalReportPath) {
		if abs, err := filepath.Abs(totalReportPath); err == nil {
			totalReportPath = abs
		}
	}

	profdataCmd := cfg.ProfdataCommand
	if profdataCmd == "" {
		profdataCmd = "llvm-profdata"
	}

	seedReportDir := cfg.SeedReportDir
	if seedReportDir == "" {
		seedReportDir = filepath.Dir(totalReportPath)
	}

	return &LLVMCoverage{
		executor:         cfg.Executor,
		compileFunc:      cfg.CompileFunc,
		compilerBinary:   cfg.CompilerBinary,
		profileDir:       cfg.ProfileDir,
		profdataCommand:  profdataCmd,
		covCommand:       cfg.CovCommand,
		demanglerCommand: cfg.DemanglerCommand,
		totalReportPath:  totalReportPath,
		seedReportDir:    seedReportDir,
		targets:          cfg.Targets,
	}
}

// Clean removes runtime coverage artifacts (.profraw / .profdata) from the
// profile directory. Build-time structure data (.ll) is preserved.
func (l *LLVMCoverage) Clean() error {
	if l.profileDir == "" {
		return nil
	}
	cleanCmd := fmt.Sprintf("find %s -name '*.profraw' -delete; find %s -name '*.profdata' -delete",
		l.profileDir, l.profileDir)
	if _, err := l.executor.Run("sh", "-c", cleanCmd); err != nil {
		return fmt.Errorf("failed to clean profile artifacts: %w", err)
	}
	return nil
}

// Prepare resets runtime coverage artifacts before a new compilation.
func (l *LLVMCoverage) Prepare() error {
	return l.Clean()
}

// Measure compiles the seed and generates a coverage report.
func (l *LLVMCoverage) Measure(s *seed.Seed) (Report, error) {
	if s.Meta.ID == 0 {
		return nil, fmt.Errorf("seed ID must be assigned before measuring coverage (got ID=0)")
	}

	if err := l.Clean(); err != nil {
		return nil, fmt.Errorf("failed to clean coverage files: %w", err)
	}

	if l.compileFunc != nil {
		if err := l.compileFunc(s); err != nil {
			return nil, fmt.Errorf("failed to compile seed: %w", err)
		}
	}

	return l.MeasureCompiled(s)
}

// MeasureCompiled merges the .profraw files and exports an llvm-cov JSON report
// after the caller has already compiled the seed.
func (l *LLVMCoverage) MeasureCompiled(s *seed.Seed) (Report, error) {
	if s.Meta.ID == 0 {
		return nil, fmt.Errorf("seed ID must be assigned before measuring coverage (got ID=0)")
	}

	if err := os.MkdirAll(l.seedReportDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create seed report directory: %w", err)
	}

	profdataPath := filepath.Join(l.seedReportDir, fmt.Sprintf("%d.profdata", s.Meta.ID))
	seedReportPath := filepath.Join(l.seedReportDir, fmt.Sprintf("%d.json", s.Meta.ID))

	// Step 1: merge .profraw -> .profdata
	mergeCmd := fmt.Sprintf("%s merge -sparse %s/*.profraw -o %s",
		l.profdataCommand, l.profileDir, profdataPath)
	if result, err := l.executor.Run("sh", "-c", mergeCmd); err != nil {
		stderr := ""
		if result != nil {
			stderr = result.Stderr
		}
		return nil, fmt.Errorf("failed to merge profraw: %w (stderr: %s)", err, stderr)
	}

	// Step 2: llvm-cov export -> JSON
	exportCmd := fmt.Sprintf("%s export %s -instr-profile=%s -format=text > %s",
		l.covCommand, l.compilerBinary, profdataPath, seedReportPath)
	result, err := l.executor.Run("sh", "-c", exportCmd)
	if err != nil {
		stderr := ""
		if result != nil {
			stderr = result.Stderr
		}
		return nil, fmt.Errorf("failed to run llvm-cov export: %w (stderr: %s)", err, stderr)
	}

	if _, err := os.Stat(seedReportPath); err != nil {
		return nil, fmt.Errorf("llvm-cov report not created: %w (command: %s)", err, exportCmd)
	}

	return &LLVMReport{path: seedReportPath}, nil
}

// HasIncreased checks if the new report covers lines not in the total report.
func (l *LLVMCoverage) HasIncreased(newReport Report) (bool, error) {
	l.lastIncreaseValid = false
	l.lastIncreaseLines = nil
	l.lastFirstSeed = false

	rep, ok := newReport.(*LLVMReport)
	if !ok {
		return false, fmt.Errorf("expected LLVMReport, got %T", newReport)
	}

	newLines, err := l.filteredLineSet(rep.path)
	if err != nil {
		return false, err
	}

	// If total report doesn't exist, this is the first seed.
	if _, statErr := os.Stat(l.totalReportPath); os.IsNotExist(statErr) {
		l.lastIncreaseLines = sortedSetKeys(newLines)
		l.lastIncreaseValid = true
		l.lastFirstSeed = true
		return len(newLines) > 0, nil
	}

	total, err := loadLLVMTotal(l.totalReportPath)
	if err != nil {
		return false, fmt.Errorf("failed to load total report: %w", err)
	}
	totalSet := coveredLineSet(total.CoveredLines)

	var increase []string
	for key := range newLines {
		if !totalSet[key] {
			increase = append(increase, key)
		}
	}
	sort.Strings(increase)
	l.lastIncreaseLines = increase
	l.lastIncreaseValid = true
	return len(increase) > 0, nil
}

// GetIncrease returns details about the coverage increase computed by HasIncreased.
func (l *LLVMCoverage) GetIncrease(newReport Report) (*CoverageIncrease, error) {
	if !l.lastIncreaseValid {
		if _, err := l.HasIncreased(newReport); err != nil {
			return nil, fmt.Errorf("failed to compute increase: %w", err)
		}
	}

	if l.lastFirstSeed {
		return &CoverageIncrease{
			Summary:           "First seed - initial coverage established",
			FormattedReport:   "This is the first seed, establishing baseline coverage.",
			NewlyCoveredLines: len(l.lastIncreaseLines),
		}, nil
	}

	var sb strings.Builder
	sb.WriteString("## Coverage Increase Summary\n\n")
	for _, line := range l.lastIncreaseLines {
		sb.WriteString(fmt.Sprintf("- %s\n", line))
	}

	return &CoverageIncrease{
		Summary:           fmt.Sprintf("Covered %d new lines", len(l.lastIncreaseLines)),
		FormattedReport:   sb.String(),
		NewlyCoveredLines: len(l.lastIncreaseLines),
	}, nil
}

// Merge merges the new coverage report into the total report (union of lines).
func (l *LLVMCoverage) Merge(newReport Report) error {
	rep, ok := newReport.(*LLVMReport)
	if !ok {
		return fmt.Errorf("expected LLVMReport, got %T", newReport)
	}

	newLines, err := l.filteredLineMap(rep.path)
	if err != nil {
		return err
	}

	// If total doesn't exist, initialize it from the new report.
	if _, statErr := os.Stat(l.totalReportPath); os.IsNotExist(statErr) {
		total := &llvmTotalReport{CoveredLines: newLines}
		return writeLLVMTotal(l.totalReportPath, total)
	}

	total, err := loadLLVMTotal(l.totalReportPath)
	if err != nil {
		return fmt.Errorf("failed to load total report: %w", err)
	}

	// Union new lines into total.
	for file, lines := range newLines {
		existing := make(map[int]bool)
		for _, n := range total.CoveredLines[file] {
			existing[n] = true
		}
		for _, n := range lines {
			if !existing[n] {
				existing[n] = true
				total.CoveredLines[file] = append(total.CoveredLines[file], n)
			}
		}
		sort.Ints(total.CoveredLines[file])
	}

	return writeLLVMTotal(l.totalReportPath, total)
}

// GetTotalReport returns the current total accumulated coverage report.
func (l *LLVMCoverage) GetTotalReport() (Report, error) {
	if _, err := os.Stat(l.totalReportPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("total report does not exist: %s", l.totalReportPath)
	}
	if _, err := loadLLVMTotal(l.totalReportPath); err != nil {
		return nil, err
	}
	return &LLVMReport{path: l.totalReportPath}, nil
}

// GetStats returns the current total coverage statistics.
func (l *LLVMCoverage) GetStats() (*CoverageStats, error) {
	if _, err := os.Stat(l.totalReportPath); os.IsNotExist(err) {
		return &CoverageStats{}, nil
	}
	total, err := loadLLVMTotal(l.totalReportPath)
	if err != nil {
		return nil, err
	}

	covered := 0
	for _, lines := range total.CoveredLines {
		covered += len(lines)
	}

	return &CoverageStats{
		TotalCoveredLines: covered,
		TotalLines:        covered,
	}, nil
}

// ExtractCoveredLinesFiltered extracts the covered "file:line" identifiers from a
// report, filtered to the configured target files/functions.
func (l *LLVMCoverage) ExtractCoveredLinesFiltered(report Report) ([]string, error) {
	rep, ok := report.(*LLVMReport)
	if !ok {
		return nil, fmt.Errorf("expected LLVMReport, got %T", report)
	}
	set, err := l.filteredLineSet(rep.path)
	if err != nil {
		return nil, err
	}
	return sortedSetKeys(set), nil
}

// filteredLineMap parses the report and returns covered lines (file -> []line)
// filtered to the configured targets.
func (l *LLVMCoverage) filteredLineMap(reportPath string) (map[string][]int, error) {
	export, err := parseLLVMCovExport(reportPath)
	if err != nil {
		return nil, err
	}
	covered := coveredLinesFromExport(export)
	return l.applyTargetFilter(export, covered), nil
}

// filteredLineSet parses the report and returns covered lines as a "file:line" set.
func (l *LLVMCoverage) filteredLineSet(reportPath string) (map[string]bool, error) {
	lines, err := l.filteredLineMap(reportPath)
	if err != nil {
		return nil, err
	}
	return coveredLineSet(lines), nil
}

// applyTargetFilter restricts covered lines to the configured target files and
// functions. With no targets configured, the input is returned unchanged.
func (l *LLVMCoverage) applyTargetFilter(export *llvmCovExport, covered map[string][]int) map[string][]int {
	if len(l.targets) == 0 {
		return covered
	}

	// Build file -> matcher of target function names.
	fileMatchers := make(map[string]*targetFunctionMatcher)
	for _, t := range l.targets {
		key := normalizeCoveragePath(t.File)
		m, ok := fileMatchers[key]
		if !ok {
			m = newTargetFunctionMatcher()
			fileMatchers[key] = m
		}
		for _, fn := range t.Functions {
			m.add(fn)
		}
	}

	// Determine the allowed line ranges per file from matching functions' regions.
	type lineRange struct{ start, end int }
	allowedRanges := make(map[string][]lineRange)
	demangled := l.demangleFunctionNames(export)
	for _, datum := range export.Data {
		for _, fn := range datum.Functions {
			name := demangled[fn.Name]
			for _, fname := range fn.Filenames {
				normFile := normalizeCoveragePath(fname)
				matcher := matchFileMatcher(fileMatchers, normFile)
				if matcher == nil {
					continue
				}
				if !matcher.matches(name) && !matcher.matches(fn.Name) {
					continue
				}
				for _, region := range fn.Regions {
					if len(region) < 3 {
						continue
					}
					start, ok1 := jsonNumberToInt64(region[0])
					end, ok2 := jsonNumberToInt64(region[2])
					if !ok1 || !ok2 {
						continue
					}
					allowedRanges[normFile] = append(allowedRanges[normFile], lineRange{int(start), int(end)})
				}
			}
		}
	}

	result := make(map[string][]int)
	for file, lines := range covered {
		normFile := normalizeCoveragePath(file)
		ranges := matchFileRanges(allowedRanges, normFile)
		if ranges == nil {
			continue
		}
		for _, line := range lines {
			for _, r := range ranges {
				if line >= r.start && line <= r.end {
					result[file] = append(result[file], line)
					break
				}
			}
		}
	}
	return result
}

// demangleFunctionNames returns a map of mangled -> demangled name. If no
// demangler is configured (or it fails), names map to themselves.
func (l *LLVMCoverage) demangleFunctionNames(export *llvmCovExport) map[string]string {
	names := make([]string, 0)
	seen := make(map[string]bool)
	for _, datum := range export.Data {
		for _, fn := range datum.Functions {
			if !seen[fn.Name] {
				seen[fn.Name] = true
				names = append(names, fn.Name)
			}
		}
	}

	result := make(map[string]string, len(names))
	for _, n := range names {
		result[n] = n
	}

	if l.demanglerCommand == "" || len(names) == 0 {
		return result
	}

	// Pass mangled names as arguments to the demangler (llvm-cxxfilt / c++filt
	// accept names as positional args and print one demangled name per line).
	out, err := l.executor.Run(l.demanglerCommand, names...)
	if err != nil || out == nil {
		return result
	}
	lines := strings.Split(strings.TrimRight(out.Stdout, "\n"), "\n")
	if len(lines) != len(names) {
		return result
	}
	for i, n := range names {
		if strings.TrimSpace(lines[i]) != "" {
			result[n] = strings.TrimSpace(lines[i])
		}
	}
	return result
}

func matchFileMatcher(matchers map[string]*targetFunctionMatcher, normFile string) *targetFunctionMatcher {
	if m, ok := matchers[normFile]; ok {
		return m
	}
	base := filepath.Base(normFile)
	if m, ok := matchers[base]; ok {
		return m
	}
	for key, m := range matchers {
		if strings.HasSuffix(normFile, key) || filepath.Base(key) == base {
			return m
		}
	}
	return nil
}

func matchFileRanges[T any](ranges map[string][]T, normFile string) []T {
	if r, ok := ranges[normFile]; ok {
		return r
	}
	base := filepath.Base(normFile)
	for key, r := range ranges {
		if strings.HasSuffix(normFile, key) || filepath.Base(key) == base {
			return r
		}
	}
	return nil
}

func sortedSetKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
