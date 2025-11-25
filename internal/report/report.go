package report

import "github.com/zjy-dev/de-fuzz/internal/oracle"

// Reporter defines the interface for saving bug reports.
type Reporter interface {
	// Save saves the details of a discovered bug to disk.
	Save(bug *oracle.Bug) error
}
