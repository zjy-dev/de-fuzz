package coverage

// Coverage handles the coverage information for a given compiler.
type Coverage interface {
	// Measure compiles the seed and returns the new coverage info.
	Measure(seedPath string) (newCoverageInfo []byte, err error)

	// HasIncreased checks if the new coverage information has increased
	// compared to the total accumulated coverage.
	HasIncreased(newCoverageInfo []byte) (bool, error)

	// Merge merges the new coverage information into the total coverage.
	Merge(newCoverageInfo []byte) error
}
