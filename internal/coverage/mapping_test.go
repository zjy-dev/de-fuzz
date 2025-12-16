package coverage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCoverageMapping_NewAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	mappingPath := filepath.Join(tmpDir, "mapping.json")

	// Create a new mapping
	cm, err := NewCoverageMapping(mappingPath)
	if err != nil {
		t.Fatalf("NewCoverageMapping() failed: %v", err)
	}

	// Record some lines
	cm.RecordLine(LineID{File: "/test.c", Line: 10}, 1)
	cm.RecordLine(LineID{File: "/test.c", Line: 20}, 2)
	cm.RecordLine(LineID{File: "/other.c", Line: 5}, 3)

	// Save
	if err := cm.Save(""); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(mappingPath); err != nil {
		t.Fatalf("Mapping file not created: %v", err)
	}

	// Load into new mapping
	cm2, err := NewCoverageMapping(mappingPath)
	if err != nil {
		t.Fatalf("Loading existing mapping failed: %v", err)
	}

	// Verify data
	seedID, found := cm2.GetSeedForLine(LineID{File: "/test.c", Line: 10})
	if !found || seedID != 1 {
		t.Errorf("Expected seedID 1 for /test.c:10, got %d (found=%v)", seedID, found)
	}

	seedID, found = cm2.GetSeedForLine(LineID{File: "/test.c", Line: 20})
	if !found || seedID != 2 {
		t.Errorf("Expected seedID 2 for /test.c:20, got %d (found=%v)", seedID, found)
	}
}

func TestCoverageMapping_RecordLine(t *testing.T) {
	cm, err := NewCoverageMapping("")
	if err != nil {
		t.Fatalf("NewCoverageMapping() failed: %v", err)
	}

	// First recording should return true
	isNew := cm.RecordLine(LineID{File: "/test.c", Line: 10}, 1)
	if !isNew {
		t.Error("First RecordLine should return true")
	}

	// Second recording of same line should return false
	isNew = cm.RecordLine(LineID{File: "/test.c", Line: 10}, 2)
	if isNew {
		t.Error("Second RecordLine should return false")
	}

	// Original seedID should be preserved
	seedID, found := cm.GetSeedForLine(LineID{File: "/test.c", Line: 10})
	if !found || seedID != 1 {
		t.Errorf("Expected original seedID 1, got %d", seedID)
	}
}

func TestCoverageMapping_RecordLines(t *testing.T) {
	cm, err := NewCoverageMapping("")
	if err != nil {
		t.Fatalf("NewCoverageMapping() failed: %v", err)
	}

	lines := []LineID{
		{File: "/test.c", Line: 10},
		{File: "/test.c", Line: 20},
		{File: "/test.c", Line: 30},
	}

	newCount := cm.RecordLines(lines, 1)
	if newCount != 3 {
		t.Errorf("Expected 3 new lines, got %d", newCount)
	}

	// Record same lines again (should add 0)
	newCount = cm.RecordLines(lines, 2)
	if newCount != 0 {
		t.Errorf("Expected 0 new lines on repeat, got %d", newCount)
	}

	// Record mix of new and existing
	mixedLines := []LineID{
		{File: "/test.c", Line: 10}, // existing
		{File: "/test.c", Line: 40}, // new
	}
	newCount = cm.RecordLines(mixedLines, 3)
	if newCount != 1 {
		t.Errorf("Expected 1 new line in mixed, got %d", newCount)
	}
}

func TestCoverageMapping_GetCoveredLines(t *testing.T) {
	cm, err := NewCoverageMapping("")
	if err != nil {
		t.Fatalf("NewCoverageMapping() failed: %v", err)
	}

	lines := []LineID{
		{File: "/test.c", Line: 10},
		{File: "/test.c", Line: 20},
		{File: "/other.c", Line: 5},
	}
	cm.RecordLines(lines, 1)

	covered := cm.GetCoveredLines()
	if len(covered) != 3 {
		t.Errorf("Expected 3 covered lines, got %d", len(covered))
	}

	// Check specific lines
	if !covered[LineID{File: "/test.c", Line: 10}] {
		t.Error("/test.c:10 should be covered")
	}
	if !covered[LineID{File: "/other.c", Line: 5}] {
		t.Error("/other.c:5 should be covered")
	}
}

func TestCoverageMapping_FindClosestCoveredLine(t *testing.T) {
	cm, err := NewCoverageMapping("")
	if err != nil {
		t.Fatalf("NewCoverageMapping() failed: %v", err)
	}

	cm.RecordLine(LineID{File: "/test.c", Line: 10}, 1)
	cm.RecordLine(LineID{File: "/test.c", Line: 20}, 2)
	cm.RecordLine(LineID{File: "/test.c", Line: 30}, 3)

	// Find closest to line 25 (should be line 20)
	lid, seedID, found := cm.FindClosestCoveredLine("/test.c", 25)
	if !found {
		t.Fatal("Should find a closest line")
	}
	if lid.Line != 20 {
		t.Errorf("Expected closest line 20, got %d", lid.Line)
	}
	if seedID != 2 {
		t.Errorf("Expected seedID 2, got %d", seedID)
	}

	// Find closest to line 5 (no line before 5)
	_, _, found = cm.FindClosestCoveredLine("/test.c", 5)
	if found {
		t.Error("Should not find a line before 5")
	}

	// Find closest to line 35 (should be line 30)
	lid, seedID, found = cm.FindClosestCoveredLine("/test.c", 35)
	if !found {
		t.Fatal("Should find a closest line")
	}
	if lid.Line != 30 {
		t.Errorf("Expected closest line 30, got %d", lid.Line)
	}
	if seedID != 3 {
		t.Errorf("Expected seedID 3, got %d", seedID)
	}

	// Different file (should not find)
	_, _, found = cm.FindClosestCoveredLine("/other.c", 15)
	if found {
		t.Error("Should not find line in different file")
	}
}

func TestCoverageMapping_GetCoveredLinesForFile(t *testing.T) {
	cm, err := NewCoverageMapping("")
	if err != nil {
		t.Fatalf("NewCoverageMapping() failed: %v", err)
	}

	cm.RecordLine(LineID{File: "/test.c", Line: 10}, 1)
	cm.RecordLine(LineID{File: "/test.c", Line: 20}, 2)
	cm.RecordLine(LineID{File: "/other.c", Line: 5}, 3)

	lines := cm.GetCoveredLinesForFile("/test.c")
	if len(lines) != 2 {
		t.Errorf("Expected 2 lines for /test.c, got %d", len(lines))
	}

	// Check values (order may vary)
	found10, found20 := false, false
	for _, l := range lines {
		if l == 10 {
			found10 = true
		}
		if l == 20 {
			found20 = true
		}
	}
	if !found10 || !found20 {
		t.Errorf("Expected lines 10 and 20, got %v", lines)
	}

	lines = cm.GetCoveredLinesForFile("/other.c")
	if len(lines) != 1 || lines[0] != 5 {
		t.Errorf("Expected [5] for /other.c, got %v", lines)
	}
}

func TestCoverageMapping_IsCovered(t *testing.T) {
	cm, err := NewCoverageMapping("")
	if err != nil {
		t.Fatalf("NewCoverageMapping() failed: %v", err)
	}

	cm.RecordLine(LineID{File: "/test.c", Line: 10}, 1)

	if !cm.IsCovered(LineID{File: "/test.c", Line: 10}) {
		t.Error("/test.c:10 should be covered")
	}

	if cm.IsCovered(LineID{File: "/test.c", Line: 20}) {
		t.Error("/test.c:20 should not be covered")
	}
}

func TestCoverageMapping_TotalCoveredLines(t *testing.T) {
	cm, err := NewCoverageMapping("")
	if err != nil {
		t.Fatalf("NewCoverageMapping() failed: %v", err)
	}

	if cm.TotalCoveredLines() != 0 {
		t.Error("New mapping should have 0 covered lines")
	}

	cm.RecordLine(LineID{File: "/test.c", Line: 10}, 1)
	cm.RecordLine(LineID{File: "/test.c", Line: 20}, 2)

	if cm.TotalCoveredLines() != 2 {
		t.Errorf("Expected 2 total covered lines, got %d", cm.TotalCoveredLines())
	}
}

func TestLineID_String(t *testing.T) {
	lid := LineID{File: "/path/to/test.c", Line: 42}
	expected := "/path/to/test.c:42"
	if lid.String() != expected {
		t.Errorf("Expected %q, got %q", expected, lid.String())
	}
}
