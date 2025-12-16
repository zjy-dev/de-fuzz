package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuilder_BuildConstraintSolvingPrompt(t *testing.T) {
	builder := NewBuilder(3, "")

	ctx := &TargetContext{
		TargetFunction: "stack_protect_classify_type",
		TargetBBID:     5,
		TargetLines:    []int{1826, 1827},
		SuccessorCount: 2,
		SourceFile:     "/path/to/cfgexpand.cc",
		BaseSeedID:     42,
		BaseSeedCode:   "int main() { char buf[100]; return 0; }",
		BaseSeedLine:   1820,
		CoveredLines:   []int{1819, 1820, 1822},
	}

	prompt, err := builder.BuildConstraintSolvingPrompt(ctx)
	if err != nil {
		t.Fatalf("BuildConstraintSolvingPrompt() failed: %v", err)
	}

	// Check essential components are present
	checks := []string{
		"stack_protect_classify_type", // Function name
		"BB5",                         // Basic block ID
		"2 successors",                // Branching factor
		"1826",                        // Target line
		"Example Seed",                // Base seed section
		"char buf[100]",               // Base seed code
	}

	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("Prompt should contain %q", check)
		}
	}

	t.Logf("Generated prompt length: %d chars", len(prompt))
}

func TestBuilder_BuildConstraintSolvingPrompt_NoBaseSeed(t *testing.T) {
	builder := NewBuilder(0, "") // No test cases

	ctx := &TargetContext{
		TargetFunction: "test_func",
		TargetBBID:     3,
		TargetLines:    []int{100, 101},
		SuccessorCount: 3,
		SourceFile:     "/path/to/test.c",
	}

	prompt, err := builder.BuildConstraintSolvingPrompt(ctx)
	if err != nil {
		t.Fatalf("BuildConstraintSolvingPrompt() failed: %v", err)
	}

	// Should not have example seed section
	if strings.Contains(prompt, "Example Seed") {
		t.Error("Prompt should not have Example Seed section when no base seed")
	}

	// Should have output format without test cases
	if strings.Contains(prompt, "test cases") && !strings.Contains(prompt, "No test cases") {
		t.Error("Prompt should not require test cases")
	}
}

func TestBuilder_BuildRefinedPrompt(t *testing.T) {
	builder := NewBuilder(1, "")

	ctx := &TargetContext{
		TargetFunction: "stack_protect_decl_phase",
		TargetBBID:     7,
		TargetLines:    []int{1876, 1877},
		SuccessorCount: 2,
		SourceFile:     "/path/to/cfgexpand.cc",
		BaseSeedCode:   "int main() { return 0; }",
		BaseSeedLine:   1870,
	}

	div := &DivergenceInfo{
		DivergentFunction: "stack_protect_classify_type",
		DivergentFile:     "/path/to/cfgexpand.cc",
		DivergentLine:     1825,
		MutatedSeedCode:   "int main() { int x = 1; return x; }",
	}

	prompt, err := builder.BuildRefinedPrompt(ctx, div)
	if err != nil {
		t.Fatalf("BuildRefinedPrompt() failed: %v", err)
	}

	// Check essential components
	checks := []string{
		"Divergence Analysis",
		"FAILED",
		"stack_protect_classify_type", // Divergent function
		"1825",                        // Divergent line
		"Failed Mutation",
		"Working Example",
	}

	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("Prompt should contain %q", check)
		}
	}

	t.Logf("Generated refined prompt length: %d chars", len(prompt))
}

func TestBuilder_BuildRefinedPrompt_NilInputs(t *testing.T) {
	builder := NewBuilder(0, "")

	_, err := builder.BuildRefinedPrompt(nil, &DivergenceInfo{})
	if err == nil {
		t.Error("Should return error for nil context")
	}

	_, err = builder.BuildRefinedPrompt(&TargetContext{}, nil)
	if err == nil {
		t.Error("Should return error for nil divergence info")
	}
}

func TestGenerateAnnotatedFunctionCode(t *testing.T) {
	// Create a temporary source file
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "test.c")

	content := `int test_func(int x) {
    if (x > 0) {
        return x + 1;
    } else {
        return x - 1;
    }
    return 0;
}`
	if err := os.WriteFile(srcFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}

	// Generate annotated code
	annotated, err := GenerateAnnotatedFunctionCode(
		srcFile,
		1, 8,
		[]int{1, 2, 3}, // Covered lines
		[]int{5},       // Target line
	)
	if err != nil {
		t.Fatalf("GenerateAnnotatedFunctionCode() failed: %v", err)
	}

	// Check annotations
	if !strings.Contains(annotated, "[✓]") {
		t.Error("Should contain covered marker [✓]")
	}
	if !strings.Contains(annotated, "[→]") {
		t.Error("Should contain target marker [→]")
	}
	if !strings.Contains(annotated, "[✗]") {
		t.Error("Should contain uncovered marker [✗]")
	}

	// Check line numbers
	if !strings.Contains(annotated, "   1:") {
		t.Error("Should contain line number 1")
	}

	t.Logf("Annotated code:\n%s", annotated)
}

func TestGenerateAnnotatedFunctionCode_FileNotFound(t *testing.T) {
	_, err := GenerateAnnotatedFunctionCode(
		"/nonexistent/file.c",
		1, 10,
		nil,
		nil,
	)
	if err == nil {
		t.Error("Should return error for nonexistent file")
	}
}

func TestGenerateAnnotatedFunctionCode_OutOfBounds(t *testing.T) {
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "short.c")
	os.WriteFile(srcFile, []byte("int x = 1;"), 0644)

	_, err := GenerateAnnotatedFunctionCode(
		srcFile,
		1, 100, // End line way out of bounds
		nil,
		nil,
	)
	// Should not error, just include available lines
	if err != nil {
		t.Logf("Got error (may be expected): %v", err)
	}
}

func TestBuilder_GetOutputFormat(t *testing.T) {
	tests := []struct {
		name         string
		maxTestCases int
		template     string
		wantContains string
	}{
		{
			name:         "with test cases",
			maxTestCases: 3,
			template:     "",
			wantContains: "JSON format",
		},
		{
			name:         "no test cases",
			maxTestCases: 0,
			template:     "",
			wantContains: "No test cases",
		},
		{
			name:         "template with test cases",
			maxTestCases: 2,
			template:     "template.c",
			wantContains: "function implementation",
		},
		{
			name:         "template no test cases",
			maxTestCases: 0,
			template:     "template.c",
			wantContains: "function implementation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := NewBuilder(tt.maxTestCases, tt.template)
			format := builder.getOutputFormat()
			if !strings.Contains(format, tt.wantContains) {
				t.Errorf("getOutputFormat() should contain %q, got: %s", tt.wantContains, format)
			}
		})
	}
}
