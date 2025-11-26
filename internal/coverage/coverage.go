package coverage

import "github.com/zjy-dev/de-fuzz/internal/seed"

// Report represents a parsed coverage report (e.g., from gcovr or llvm-cov).
// It serves as a common data structure for different coverage tools.
type Report interface {
	// To a []byte for easy storage or transmission
	ToBytes() ([]byte, error)
}

// CoverageStats holds coverage statistics for display and decision making.
type CoverageStats struct {
	// Overall coverage percentage (0-100)
	CoveragePercentage float64

	// Line coverage
	TotalLines        int
	TotalCoveredLines int

	// Function coverage (optional, may be 0 if not available)
	TotalFunctions        int
	TotalCoveredFunctions int
}

// CoverageIncrease holds information about what coverage was newly increased.
// This is used to provide context for LLM mutation.
type CoverageIncrease struct {
	// Summary of what was newly covered (human-readable)
	Summary string

	// Detailed increase information formatted for LLM
	FormattedReport string

	// Raw increase data for programmatic access
	NewlyCoveredLines     int
	NewlyCoveredFunctions int

	// UncoveredAbstract is the abstracted code showing uncovered paths
	// This helps LLM understand what code paths are not yet covered
	UncoveredAbstract string
}

// Coverage defines the interface for coverage measurement and analysis.
// It is designed to be modular and support different toolchains (GCC, LLVM, etc.).
type Coverage interface {
	// Clean removes any existing coverage artifacts to ensure a fresh measurement.
	Clean() error

	// Measure compiles a seed and generates a coverage report.
	Measure(s *seed.Seed) (Report, error)

	// HasIncreased compares a new report against the total accumulated coverage
	// and returns true if there is a significant increase.
	HasIncreased(newReport Report) (bool, error)

	// GetIncrease returns detailed information about the coverage increase.
	// Should be called after HasIncreased returns true to get the details.
	GetIncrease(newReport Report) (*CoverageIncrease, error)

	// Merge incorporates the new coverage report into the total accumulated coverage.
	Merge(newReport Report) error

	// GetTotalReport returns the current total accumulated coverage report.
	GetTotalReport() (Report, error)

	// GetStats returns the current total coverage statistics.
	GetStats() (*CoverageStats, error)
}
