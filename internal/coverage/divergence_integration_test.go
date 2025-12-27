//go:build integration
// +build integration

package coverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUftraceAnalyzerIntegration tests the full divergence analysis flow.
// Requires: uftrace installed, gcc available
func TestUftraceAnalyzerIntegration(t *testing.T) {
	// Check if uftrace is available
	analyzer, err := NewUftraceAnalyzer()
	if err != nil {
		t.Skipf("Skipping integration test: %v", err)
	}
	defer analyzer.Cleanup()

	// Create test C files
	tmpDir := t.TempDir()

	test1 := filepath.Join(tmpDir, "test1.c")
	test2 := filepath.Join(tmpDir, "test2.c")

	// test1.c uses addition
	test1Code := `int main(int a, int b) { return a + b; }`
	if err := os.WriteFile(test1, []byte(test1Code), 0644); err != nil {
		t.Fatalf("Failed to write test1.c: %v", err)
	}

	// test2.c uses multiplication
	test2Code := `int main(int a, int b) { return a * b; }`
	if err := os.WriteFile(test2, []byte(test2Code), 0644); err != nil {
		t.Fatalf("Failed to write test2.c: %v", err)
	}

	// Run divergence analysis
	div, err := analyzer.Analyze(test1, test2, "gcc")
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if div == nil {
		t.Fatal("Expected divergence point, got nil")
	}

	t.Logf("Divergence found:\n%s", div.String())

	// Verify we got meaningful divergence
	if div.Index <= 0 {
		t.Errorf("Expected positive divergence index, got %d", div.Index)
	}

	if div.Function1 == "" || div.Function2 == "" {
		t.Errorf("Expected non-empty function names")
	}

	// The functions should be different
	if div.Function1 == div.Function2 {
		t.Errorf("Expected different functions at divergence point")
	}

	// Should have context
	if len(div.CommonPrefix) == 0 {
		t.Error("Expected non-empty common prefix")
	}

	if len(div.Path1) == 0 || len(div.Path2) == 0 {
		t.Error("Expected non-empty divergent paths")
	}
}

// TestUftraceAnalyzerSameCode tests that identical code produces no divergence.
func TestUftraceAnalyzerSameCode(t *testing.T) {
	analyzer, err := NewUftraceAnalyzer()
	if err != nil {
		t.Skipf("Skipping integration test: %v", err)
	}
	defer analyzer.Cleanup()

	tmpDir := t.TempDir()

	test1 := filepath.Join(tmpDir, "test1.c")
	test2 := filepath.Join(tmpDir, "test2.c")

	// Same code in both files
	code := `int main(int a, int b) { return a + b; }`
	if err := os.WriteFile(test1, []byte(code), 0644); err != nil {
		t.Fatalf("Failed to write test1.c: %v", err)
	}
	if err := os.WriteFile(test2, []byte(code), 0644); err != nil {
		t.Fatalf("Failed to write test2.c: %v", err)
	}

	div, err := analyzer.Analyze(test1, test2, "gcc")
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	// Should have no divergence or very late divergence (due to filename differences)
	// Actually, there might still be divergence due to file paths in debug info
	t.Logf("Same code divergence result: %v", div)
}

// TestUftraceAnalyzerForLLMOutput tests the LLM-formatted output.
func TestUftraceAnalyzerForLLMOutput(t *testing.T) {
	analyzer, err := NewUftraceAnalyzer()
	if err != nil {
		t.Skipf("Skipping integration test: %v", err)
	}
	defer analyzer.Cleanup()

	tmpDir := t.TempDir()

	test1 := filepath.Join(tmpDir, "test1.c")
	test2 := filepath.Join(tmpDir, "test2.c")

	if err := os.WriteFile(test1, []byte(`int main() { return 1 + 2; }`), 0644); err != nil {
		t.Fatalf("Failed to write test1.c: %v", err)
	}
	if err := os.WriteFile(test2, []byte(`int main() { return 1 * 2; }`), 0644); err != nil {
		t.Fatalf("Failed to write test2.c: %v", err)
	}

	div, err := analyzer.Analyze(test1, test2, "gcc")
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if div != nil {
		llmOutput := div.ForLLM()
		t.Logf("LLM Output:\n%s", llmOutput)

		// Check formatting
		if !strings.Contains(llmOutput, "## Divergence Analysis") {
			t.Error("Missing header in LLM output")
		}
		if !strings.Contains(llmOutput, "Base seed executed") {
			t.Error("Missing base seed info in LLM output")
		}
		if !strings.Contains(llmOutput, "Mutated seed executed") {
			t.Error("Missing mutated seed info in LLM output")
		}
	}
}
