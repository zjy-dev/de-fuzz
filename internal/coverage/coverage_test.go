package coverage

import (
	"testing"
)

func TestParseCoverageData(t *testing.T) {
	data := []byte(`func1:10/20
func2:5/10
func3:0/5
`)

	result, err := parseCoverageData(data)
	if err != nil {
		t.Fatalf("parseCoverageData failed: %v", err)
	}

	if len(result) != 3 {
		t.Errorf("expected 3 functions, got %d", len(result))
	}

	// Test func1
	if fc, ok := result["func1"]; ok {
		if fc.LinesCov != 10 || fc.LinesTotal != 20 {
			t.Errorf("func1: expected 10/20, got %d/%d", fc.LinesCov, fc.LinesTotal)
		}
	} else {
		t.Error("func1 not found")
	}

	// Test func2
	if fc, ok := result["func2"]; ok {
		if fc.LinesCov != 5 || fc.LinesTotal != 10 {
			t.Errorf("func2: expected 5/10, got %d/%d", fc.LinesCov, fc.LinesTotal)
		}
	} else {
		t.Error("func2 not found")
	}
}

func TestContains(t *testing.T) {
	slice := []string{"func1", "func2", "func3"}

	if !contains(slice, "func1") {
		t.Error("expected to find func1")
	}

	if !contains(slice, "func2") {
		t.Error("expected to find func2")
	}

	if contains(slice, "func4") {
		t.Error("should not find func4")
	}
}
