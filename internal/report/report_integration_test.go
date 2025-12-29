//go:build integration

package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// TestMarkdownReporter_Integration_Save tests saving a bug report.
func TestMarkdownReporter_Integration_Save(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "report_integration_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	reporter := NewMarkdownReporter(tempDir)

	bug := &oracle.Bug{
		Seed: &seed.Seed{
			Meta: seed.Metadata{ID: 1},
			Content: `
#include <stdio.h>
#include <string.h>

void vulnerable() {
    char buffer[16];
    gets(buffer);
    printf("%s\n", buffer);
}

int main() {
    vulnerable();
    return 0;
}
`,
			TestCases: []seed.TestCase{
				{RunningCommand: "echo AAAA | ./a.out", ExpectedResult: "AAAA"},
				{RunningCommand: "python -c 'print(\"A\"*100)' | ./a.out", ExpectedResult: "Crash"},
			},
		},
		Results: []oracle.Result{
			{Stdout: "AAAA", Stderr: "", ExitCode: 0},
			{Stdout: "", Stderr: "*** stack smashing detected ***: terminated", ExitCode: 134},
		},
		Description: "Stack Buffer Overflow leading to Stack Smashing Detection",
	}

	err = reporter.Save(bug)
	require.NoError(t, err)

	// Find the generated report
	files, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	require.Equal(t, 1, len(files))

	reportPath := filepath.Join(tempDir, files[0].Name())
	assert.True(t, strings.HasPrefix(files[0].Name(), "bug_1_"))
	assert.True(t, strings.HasSuffix(files[0].Name(), ".md"))

	// Verify report content
	content, err := os.ReadFile(reportPath)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "# Bug Report:")
	assert.Contains(t, contentStr, "Stack Buffer Overflow")
	assert.Contains(t, contentStr, "Seed ID: 1")
	assert.Contains(t, contentStr, "vulnerable()")
	assert.Contains(t, contentStr, "gets(buffer)")
	assert.Contains(t, contentStr, "Exit Code:** 134")
	assert.Contains(t, contentStr, "stack smashing detected")
	assert.Contains(t, contentStr, "echo AAAA | ./a.out")
}

// TestMarkdownReporter_Integration_SaveMultipleBugs tests saving multiple reports.
func TestMarkdownReporter_Integration_SaveMultipleBugs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "report_multi_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	reporter := NewMarkdownReporter(tempDir)

	bugs := []*oracle.Bug{
		{
			Seed: &seed.Seed{
				Meta:      seed.Metadata{ID: 1},
				Content:   `int main() { int *p = 0; *p = 1; return 0; }`,
				TestCases: []seed.TestCase{
					{RunningCommand: "./a.out", ExpectedResult: "Crash"},
				},
			},
			Results: []oracle.Result{
				{Stdout: "", Stderr: "Segmentation fault", ExitCode: 139},
			},
			Description: "Null Pointer Dereference",
		},
		{
			Seed: &seed.Seed{
				Meta:      seed.Metadata{ID: 2},
				Content:   `int main() { char b[8]; strcpy(b, "AAAAAAAAAAAAAAAA"); return 0; }`,
				TestCases: []seed.TestCase{
					{RunningCommand: "./a.out", ExpectedResult: "Crash"},
				},
			},
			Results: []oracle.Result{
				{Stdout: "", Stderr: "stack smashing detected", ExitCode: 134},
			},
			Description: "Stack Buffer Overflow",
		},
		{
			Seed: &seed.Seed{
				Meta:      seed.Metadata{ID: 3},
				Content:   `int main() { int x = 0; return 10/x; }`,
				TestCases: []seed.TestCase{
					{RunningCommand: "./a.out", ExpectedResult: "Crash"},
				},
			},
			Results: []oracle.Result{
				{Stdout: "", Stderr: "Floating point exception", ExitCode: 136},
			},
			Description: "Division by Zero",
		},
	}

	for _, bug := range bugs {
		err = reporter.Save(bug)
		require.NoError(t, err)
	}

	files, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	assert.Equal(t, 3, len(files))

	// Verify each report exists
	for _, f := range files {
		assert.True(t, strings.HasSuffix(f.Name(), ".md"))
	}
}

// TestMarkdownReporter_Integration_CreateDirectory tests directory creation.
func TestMarkdownReporter_Integration_CreateDirectory(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "report_mkdir_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	nestedPath := filepath.Join(tempDir, "nested", "reports", "bugs")
	reporter := NewMarkdownReporter(nestedPath)

	bug := &oracle.Bug{
		Seed: &seed.Seed{
			Meta:      seed.Metadata{ID: 1},
			Content:   `int main() { return 0; }`,
			TestCases: []seed.TestCase{},
		},
		Results: []oracle.Result{
			{ExitCode: 1},
		},
		Description: "Test nested directory creation",
	}

	err = reporter.Save(bug)
	require.NoError(t, err)

	assert.DirExists(t, nestedPath)
}

// TestMarkdownReporter_Integration_EmptyResults tests with empty results.
func TestMarkdownReporter_Integration_EmptyResults(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "report_empty_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	reporter := NewMarkdownReporter(tempDir)

	bug := &oracle.Bug{
		Seed: &seed.Seed{
			Meta:      seed.Metadata{ID: 1},
			Content:   `int main() { return 0; }`,
			TestCases: []seed.TestCase{},
		},
		Results:     []oracle.Result{},
		Description: "Bug with no execution results",
	}

	err = reporter.Save(bug)
	require.NoError(t, err)

	files, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	assert.Equal(t, 1, len(files))

	content, err := os.ReadFile(filepath.Join(tempDir, files[0].Name()))
	require.NoError(t, err)
	// Check that report was generated and contains description
	assert.Contains(t, string(content), "Bug with no execution results")
}

// TestMarkdownReporter_Integration_LargeReport tests with large content.
func TestMarkdownReporter_Integration_LargeReport(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large report test in short mode")
	}

	tempDir, err := os.MkdirTemp("", "report_large_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	reporter := NewMarkdownReporter(tempDir)

	// Generate large source code
	largeCode := "#include <stdio.h>\n"
	for i := 0; i < 100; i++ {
		largeCode += `
void func_%d(int x) {
    int arr[100];
    for (int i = 0; i < 100; i++) {
        arr[i] = x + i;
    }
    printf("Function %d completed\n");
}
`
	}
	largeCode += "int main() {\n"
	for i := 0; i < 100; i++ {
		largeCode += "    func_%d(1);\n"
	}
	largeCode += "    return 0;\n}\n"

	// Generate many test cases
	testCases := make([]seed.TestCase, 50)
	for i := 0; i < 50; i++ {
		testCases[i] = seed.TestCase{
			RunningCommand: "./a.out",
			ExpectedResult: "Completed",
		}
	}

	// Generate many results
	results := make([]oracle.Result, 50)
	for i := 0; i < 50; i++ {
		results[i] = oracle.Result{
			Stdout:   strings.Repeat("Function output line\n", 10),
			Stderr:   "",
			ExitCode: 0,
		}
	}

	bug := &oracle.Bug{
		Seed: &seed.Seed{
			Meta:      seed.Metadata{ID: 1},
			Content:   largeCode,
			TestCases: testCases,
		},
		Results:     results,
		Description: "Large bug report with extensive content",
	}

	err = reporter.Save(bug)
	require.NoError(t, err)

	files, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	require.Equal(t, 1, len(files))

	// Verify file was created and has substantial content
	info, err := files[0].Info()
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(1000))
}

// TestMarkdownReporter_Integration_SpecialCharacters tests special character handling.
func TestMarkdownReporter_Integration_SpecialCharacters(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "report_special_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	reporter := NewMarkdownReporter(tempDir)

	bug := &oracle.Bug{
		Seed: &seed.Seed{
			Meta: seed.Metadata{ID: 1},
			Content: `
#include <stdio.h>
int main() {
    // Special: "quotes" 'single' \backslash
    char *s = "Hello \"World\"!";
    printf("%s\n", s);
    return 0;
}
`,
			TestCases: []seed.TestCase{
				{RunningCommand: "echo \"test\" | ./a.out", ExpectedResult: "Hello \"World\"!"},
			},
		},
		Results: []oracle.Result{
			{Stdout: "Hello \"World\"!", Stderr: "", ExitCode: 0},
		},
		Description: "Test with special characters: <, >, &, \"",
	}

	err = reporter.Save(bug)
	require.NoError(t, err)

	files, err := os.ReadDir(tempDir)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(tempDir, files[0].Name()))
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "Hello \"World\"!")
	assert.Contains(t, contentStr, "echo \"test\"")
}

// TestMarkdownReporter_Integration_ReportFormat tests the exact format of reports.
func TestMarkdownReporter_Integration_ReportFormat(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "report_format_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	reporter := NewMarkdownReporter(tempDir)

	bug := &oracle.Bug{
		Seed: &seed.Seed{
			Meta:      seed.Metadata{ID: 1},
			Content:   `int main() { return 42; }`,
			TestCases: []seed.TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "0"},
			},
		},
		Results: []oracle.Result{
			{Stdout: "", Stderr: "", ExitCode: 42},
		},
		Description: "Non-zero exit code",
	}

	err = reporter.Save(bug)
	require.NoError(t, err)

	files, err := os.ReadDir(tempDir)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(tempDir, files[0].Name()))
	require.NoError(t, err)

	contentStr := string(content)

	// Verify markdown structure
	assert.Contains(t, contentStr, "# Bug Report:")
	assert.Contains(t, contentStr, "## Seed ID:")
	assert.Contains(t, contentStr, "## Execution Results")
	assert.Contains(t, contentStr, "### Test Case 1")
	assert.Contains(t, contentStr, "**Exit Code:** 42")
	assert.Contains(t, contentStr, "**Stdout:**")
	assert.Contains(t, contentStr, "**Stderr:**")
	assert.Contains(t, contentStr, "## Seed")
	assert.Contains(t, contentStr, "### Source Code")
	assert.Contains(t, contentStr, "```c")
	assert.Contains(t, contentStr, "### Test Cases")
	assert.Contains(t, contentStr, "- Command: `./a.out`")
	assert.Contains(t, contentStr, "- Expected: 0")
}

// TestMarkdownReporter_Integration_Concurrency tests concurrent report saving.
func TestMarkdownReporter_Integration_Concurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrency test in short mode")
	}

	tempDir, err := os.MkdirTemp("", "report_concurrent_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	reporter := NewMarkdownReporter(tempDir)

	const numBugs = 10
	done := make(chan error, numBugs)

	for i := 0; i < numBugs; i++ {
		go func(idx int) {
			bug := &oracle.Bug{
				Seed: &seed.Seed{
					Meta:      seed.Metadata{ID: uint64(idx + 1)},
					Content:   `int main() { return 0; }`,
					TestCases: []seed.TestCase{},
				},
				Results: []oracle.Result{
					{ExitCode: idx},
				},
				Description: "Concurrent bug",
			}
			done <- reporter.Save(bug)
		}(i)
	}

	for i := 0; i < numBugs; i++ {
		err := <-done
		assert.NoError(t, err)
	}

	files, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	assert.Equal(t, numBugs, len(files))
}

// TestReporterInterface_Integration tests Reporter interface implementation.
func TestReporterInterface_Integration(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "reporter_interface_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	var reporter Reporter = NewMarkdownReporter(tempDir)

	bug := &oracle.Bug{
		Seed: &seed.Seed{
			Meta:      seed.Metadata{ID: 1},
			Content:   `int main() { return 0; }`,
			TestCases: []seed.TestCase{},
		},
		Results:     []oracle.Result{},
		Description: "Testing interface",
	}

	err = reporter.Save(bug)
	assert.NoError(t, err)
}
