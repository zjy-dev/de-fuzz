package report

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"defuzz/internal/analysis"
)

// MarkdownReporter implements the Reporter interface by saving reports as markdown files.
type MarkdownReporter struct {
	outputDir string
}

// NewMarkdownReporter creates a new MarkdownReporter.
func NewMarkdownReporter(outputDir string) *MarkdownReporter {
	return &MarkdownReporter{
		outputDir: outputDir,
	}
}

// Save saves the details of a discovered bug to a markdown file.
func (r *MarkdownReporter) Save(bug *analysis.Bug) error {
	if err := os.MkdirAll(r.outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create report directory: %w", err)
	}

	reportName := fmt.Sprintf("bug_%s_%d.md", bug.Seed.ID, time.Now().UnixNano())
	reportPath := filepath.Join(r.outputDir, reportName)

	var content string
	content += fmt.Sprintf("# Bug Report: %s\n\n", bug.Description)
	content += fmt.Sprintf("## Seed ID: %s\n\n", bug.Seed.ID)
	content += fmt.Sprintf("## Execution Result\n\n")
	content += fmt.Sprintf("### Exit Code: %d\n\n", bug.Result.ExitCode)
	content += fmt.Sprintf("### Stdout\n\n```\n%s\n```\n\n", bug.Result.Stdout)
	content += fmt.Sprintf("### Stderr\n\n```\n%s\n```\n\n", bug.Result.Stderr)
	content += fmt.Sprintf("## Seed\n\n")
	content += fmt.Sprintf("### Source Code\n\n```c\n%s\n```\n\n", bug.Seed.Content)
	content += fmt.Sprintf("### Makefile\n\n```makefile\n%s\n```\n\n", bug.Seed.Makefile)
	content += fmt.Sprintf("### Run Script\n\n```sh\n%s\n```\n\n", bug.Seed.RunScript)

	return os.WriteFile(reportPath, []byte(content), 0644)
}
