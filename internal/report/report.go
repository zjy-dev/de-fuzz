package report

import "defuzz/internal/analysis"

// Reporter defines the interface for saving bug reports.
type Reporter interface {
	// Save saves the details of a discovered bug to disk.
	Save(bug *analysis.Bug) error
}