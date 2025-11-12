package report

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zjy-dev/de-fuzz/internal/analysis"
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
	content += "## Execution Results\n\n"

	// Report all test case results
	for i, result := range bug.Results {
		content += fmt.Sprintf("### Test Case %d\n\n", i+1)
		content += fmt.Sprintf("**Exit Code:** %d\n\n", result.ExitCode)
		content += fmt.Sprintf("**Stdout:**\n\n```\n%s\n```\n\n", result.Stdout)
		content += fmt.Sprintf("**Stderr:**\n\n```\n%s\n```\n\n", result.Stderr)
	}

	content += "## Seed\n\n"
	content += fmt.Sprintf("### Source Code\n\n```c\n%s\n```\n\n", bug.Seed.Content)

	// Report test cases
	content += "### Test Cases\n\n"
	for i, tc := range bug.Seed.TestCases {
		content += fmt.Sprintf("**Test Case %d:**\n", i+1)
		content += fmt.Sprintf("- Command: `%s`\n", tc.RunningCommand)
		content += fmt.Sprintf("- Expected: %s\n\n", tc.ExpectedResult)
	}

	return os.WriteFile(reportPath, []byte(content), 0644)
}
