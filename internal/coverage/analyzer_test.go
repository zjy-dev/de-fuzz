package coverage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzer_NewAnalyzer(t *testing.T) {
	// Create a temporary CFG file with proper format
	tmpDir := t.TempDir()
	cfgContent := `;; Function test_func (test_func, funcdef_no=0, decl_uid=2)
;;   with 3 basic blocks.

;; 2 succs {3 4}
<bb 2>:
if (x_3(D) > 10)
  goto <bb 3>
else
  goto <bb 4>
endif

;; 1 succs {2}
<bb 3>:
return x_3(D)

;; 1 succs {2}
<bb 4>:
x_5 = x_3(D) + 1;
goto <bb 2>

test_func (test_func, funcdef_no=0, decl_uid=2) {
}
`

	cfgPath := filepath.Join(tmpDir, "test.cfg")
	err := os.WriteFile(cfgPath, []byte(cfgContent), 0644)
	require.NoError(t, err)

	mappingPath := filepath.Join(tmpDir, "mapping.json")

	analyzer, err := NewAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)
	require.NoError(t, err)
	assert.NotNil(t, analyzer)

	// Test basic functionality
	fn, ok := analyzer.GetFunction("test_func")
	assert.True(t, ok)
	assert.Equal(t, "test_func", fn.Name)
}

func TestAnalyzer_GetFunctionCoverage(t *testing.T) {
	tmpDir := t.TempDir()
	cfgContent := `;; Function test_func (test_func, funcdef_no=0, decl_uid=2)
;;   with 3 basic blocks.

;; 2 succs {3 4}
<bb 2>:
if (x_3(D) > 10)
  goto <bb 3>
else
  goto <bb 4>
endif

;; 1 succs {2}
<bb 3>:
return x_3(D)

;; 1 succs {2}
<bb 4>:
x_5 = x_3(D) + 1;
goto <bb 2>

test_func (test_func, funcdef_no=0, decl_uid=2) {
}
`

	cfgPath := filepath.Join(tmpDir, "test.cfg")
	err := os.WriteFile(cfgPath, []byte(cfgContent), 0644)
	require.NoError(t, err)

	mappingPath := filepath.Join(tmpDir, "mapping.json")
	analyzer, err := NewAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)
	require.NoError(t, err)

	// Initially no coverage
	cov := analyzer.GetFunctionCoverage()
	assert.Contains(t, cov, "test_func")
	// BB 2 should exist (excluding entry/exit)
	_ = cov
}

func TestAnalyzer_RecordCoverage(t *testing.T) {
	tmpDir := t.TempDir()
	cfgContent := `;; Function test_func (test_func, funcdef_no=0, decl_uid=2)
;;   with 3 basic blocks.

;; 2 succs {3 4}
<bb 2>:
if (x_3(D) > 10)
  goto <bb 3>
else
  goto <bb 4>
endif

;; 1 succs {2}
<bb 3>:
return x_3(D)

;; 1 succs {2}
<bb 4>:
x_5 = x_3(D) + 1;
goto <bb 2>

test_func (test_func, funcdef_no=0, decl_uid=2) {
}
`

	cfgPath := filepath.Join(tmpDir, "test.cfg")
	err := os.WriteFile(cfgPath, []byte(cfgContent), 0644)
	require.NoError(t, err)

	mappingPath := filepath.Join(tmpDir, "mapping.json")
	analyzer, err := NewAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)
	require.NoError(t, err)

	// Record coverage
	analyzer.RecordCoverage(1, []string{"test.c:5", "test.c:10"})

	covered := analyzer.GetCoveredLines()
	assert.True(t, covered[LineID{File: "test.c", Line: 5}])
	assert.True(t, covered[LineID{File: "test.c", Line: 10}])
}

func TestAnalyzer_SaveAndLoadMapping(t *testing.T) {
	tmpDir := t.TempDir()
	cfgContent := `;; Function test_func (test_func, funcdef_no=0, decl_uid=2)
;;   with 3 basic blocks.

;; 2 succs {3 4}
<bb 2>:
if (x_3(D) > 10)
  goto <bb 3>
else
  goto <bb 4>
endif

test_func (test_func, funcdef_no=0, decl_uid=2) {
}
`

	cfgPath := filepath.Join(tmpDir, "test.cfg")
	err := os.WriteFile(cfgPath, []byte(cfgContent), 0644)
	require.NoError(t, err)

	mappingPath := filepath.Join(tmpDir, "mapping.json")

	// Create first analyzer and record coverage
	analyzer1, err := NewAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)
	require.NoError(t, err)
	analyzer1.RecordCoverage(1, []string{"test.c:5"})
	analyzer1.SaveMapping(mappingPath)

	// Create second analyzer and load
	analyzer2, err := NewAnalyzer(cfgPath, []string{"test_func"}, "", mappingPath)
	require.NoError(t, err)

	// Should have loaded the coverage
	covered := analyzer2.GetCoveredLines()
	assert.True(t, covered[LineID{File: "test.c", Line: 5}])
}

func TestCoverageMapping_NewAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	mappingPath := filepath.Join(tmpDir, "mapping.json")

	cm, err := NewCoverageMapping(mappingPath)
	require.NoError(t, err)
	assert.NotNil(t, cm)

	// Record some lines
	cm.RecordLine(LineID{File: "test.c", Line: 10}, 1)
	cm.RecordLine(LineID{File: "test.c", Line: 20}, 2)

	// Save
	err = cm.Save(mappingPath)
	require.NoError(t, err)

	// Load into new instance
	cm2, err := NewCoverageMapping(mappingPath)
	require.NoError(t, err)

	// Verify
	seedID, found := cm2.GetSeedForLine(LineID{File: "test.c", Line: 10})
	assert.True(t, found)
	assert.Equal(t, int64(1), seedID)

	seedID, found = cm2.GetSeedForLine(LineID{File: "test.c", Line: 20})
	assert.True(t, found)
	assert.Equal(t, int64(2), seedID)
}

func TestCoverageMapping_FindClosestCoveredLine(t *testing.T) {
	cm, err := NewCoverageMapping("")
	require.NoError(t, err)

	// Record some lines
	cm.RecordLine(LineID{File: "test.c", Line: 10}, 1)
	cm.RecordLine(LineID{File: "test.c", Line: 20}, 2)
	cm.RecordLine(LineID{File: "test.c", Line: 30}, 3)

	// Find closest line before target
	lid, seedID, found := cm.FindClosestCoveredLine("test.c", 25)
	assert.True(t, found)
	assert.Equal(t, 20, lid.Line)
	assert.Equal(t, int64(2), seedID)

	// Target before any covered line
	lid, seedID, found = cm.FindClosestCoveredLine("test.c", 5)
	assert.False(t, found)
}

func TestCoverageMapping_TotalCoveredLines(t *testing.T) {
	cm, err := NewCoverageMapping("")
	require.NoError(t, err)

	assert.Equal(t, 0, cm.TotalCoveredLines())

	cm.RecordLine(LineID{File: "test.c", Line: 10}, 1)
	assert.Equal(t, 1, cm.TotalCoveredLines())

	cm.RecordLine(LineID{File: "test.c", Line: 20}, 2)
	assert.Equal(t, 2, cm.TotalCoveredLines())

	// Duplicate should not increase count
	cm.RecordLine(LineID{File: "test.c", Line: 10}, 3)
	assert.Equal(t, 2, cm.TotalCoveredLines())
}
