package coverage

import "github.com/zjy-dev/de-fuzz/internal/seed"

// Report represents a parsed coverage report (e.g., from gcovr or llvm-cov).
// It serves as a common data structure for different coverage tools.
type Report interface {
	// To a []byte for easy storage or transmission
	ToBytes() ([]byte, error)
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

	// Merge incorporates the new coverage report into the total accumulated coverage.
	Merge(newReport Report) error

	// GetTotalReport returns the current total accumulated coverage report.
	GetTotalReport() (Report, error)
}
