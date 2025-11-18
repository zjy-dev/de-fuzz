package coverage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCppAbstractor_Simple tests basic C code abstraction.
func TestCppAbstractor_Simple(t *testing.T) {
	// Create a temporary C file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.c")

	sourceCode := `int calculate(int x, int y) {
    int result = 0;
    
    if (x > 0) {
        if (y > 0) {
            result = x + y;
        } else {
            result = x - y;
        }
    } else {
        result = 0;
    }
    
    return result;
}`

	err := os.WriteFile(testFile, []byte(sourceCode), 0644)
	require.NoError(t, err)

	// Create input with uncovered lines (lines 6 and 8 inside nested if/else)
	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath: testFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "calculate",
						UncoveredLines: []int{6, 8}, // Lines with "result = x + y" and "result = x - y"
						TotalLines:     10,
						CoveredLines:   8,
					},
				},
			},
		},
	}

	// Run abstractor
	abstractor := NewCppAbstractor()
	output, err := abstractor.Abstract(input)
	require.NoError(t, err)
	require.Len(t, output.Functions, 1)

	fn := output.Functions[0]
	assert.Equal(t, testFile, fn.FilePath)
	assert.Equal(t, "calculate", fn.FunctionName)
	assert.NoError(t, fn.Error)
	assert.NotEmpty(t, fn.AbstractedCode)

	// The abstracted code should contain:
	// - Function signature
	// - Outer if condition (critical path)
	// - Inner if/else with uncovered statements
	// - Should NOT contain "result = 0" on line 12 (covered)

	t.Logf("Abstracted code:\n%s", fn.AbstractedCode)

	// Verify key components are present
	assert.Contains(t, fn.AbstractedCode, "int calculate(int x, int y)")
	assert.Contains(t, fn.AbstractedCode, "if (x > 0)")
	assert.Contains(t, fn.AbstractedCode, "result = x + y")
	assert.Contains(t, fn.AbstractedCode, "result = x - y")
}

// TestCppAbstractor_SwitchCase tests switch statement abstraction.
func TestCppAbstractor_SwitchCase(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.c")

	sourceCode := `int process(int mode) {
    int result = 0;
    
    switch (mode) {
        case 1:
            result = 10;
            break;
        case 2:
            result = 20;
            break;
        case 3:
            result = 30;
            break;
        default:
            result = -1;
            break;
    }
    
    return result;
}`

	err := os.WriteFile(testFile, []byte(sourceCode), 0644)
	require.NoError(t, err)

	// Case 3 and default are uncovered (lines 11-12, 14-15)
	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath: testFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "process",
						UncoveredLines: []int{11, 12, 14, 15},
						TotalLines:     15,
						CoveredLines:   11,
					},
				},
			},
		},
	}

	abstractor := NewCppAbstractor()
	output, err := abstractor.Abstract(input)
	require.NoError(t, err)
	require.Len(t, output.Functions, 1)

	fn := output.Functions[0]
	assert.NoError(t, fn.Error)
	assert.NotEmpty(t, fn.AbstractedCode)

	t.Logf("Abstracted code:\n%s", fn.AbstractedCode)

	// Verify structure
	assert.Contains(t, fn.AbstractedCode, "switch (mode)")
	assert.Contains(t, fn.AbstractedCode, "case 1:")
	assert.Contains(t, fn.AbstractedCode, "case 2:")
	assert.Contains(t, fn.AbstractedCode, "case 3:")
	assert.Contains(t, fn.AbstractedCode, "result = 30")
	assert.Contains(t, fn.AbstractedCode, "default:")
	assert.Contains(t, fn.AbstractedCode, "result = -1")
}

// TestAbstractorRegistry tests the registry system.
func TestAbstractorRegistry(t *testing.T) {
	registry := NewAbstractorRegistry()

	// Register C++ abstractor
	cppAbstractor := NewCppAbstractor()
	registry.Register(cppAbstractor)

	// Verify registration
	assert.NotNil(t, registry.GetAbstractor(".c"))
	assert.NotNil(t, registry.GetAbstractor(".cpp"))
	assert.NotNil(t, registry.GetAbstractor(".cc"))
	assert.Nil(t, registry.GetAbstractor(".go"))
}

// TestAbstractorRegistry_AbstractAll tests processing multiple files.
func TestAbstractorRegistry_AbstractAll(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test C file
	cFile := filepath.Join(tmpDir, "test.c")
	cCode := `int add(int a, int b) {
    if (a > 0) {
        return a + b;
    }
    return 0;
}`
	err := os.WriteFile(cFile, []byte(cCode), 0644)
	require.NoError(t, err)

	// Create test C++ file
	cppFile := filepath.Join(tmpDir, "test.cpp")
	cppCode := `int multiply(int x, int y) {
    if (x > 0) {
        return x * y;
    }
    return 0;
}`
	err = os.WriteFile(cppFile, []byte(cppCode), 0644)
	require.NoError(t, err)

	// Create input with both files
	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath: cFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "add",
						UncoveredLines: []int{3}, // Line with "return a + b"
						TotalLines:     4,
						CoveredLines:   3,
					},
				},
			},
			{
				FilePath: cppFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "multiply",
						UncoveredLines: []int{3}, // Line with "return x * y"
						TotalLines:     4,
						CoveredLines:   3,
					},
				},
			},
		},
	}

	// Create registry and process
	registry := NewAbstractorRegistry()
	registry.Register(NewCppAbstractor())

	output, err := registry.AbstractAll(input)
	require.NoError(t, err)
	require.Len(t, output.Functions, 2)

	// Verify both functions were processed
	functionNames := []string{output.Functions[0].FunctionName, output.Functions[1].FunctionName}
	assert.Contains(t, functionNames, "add")
	assert.Contains(t, functionNames, "multiply")

	for _, fn := range output.Functions {
		assert.NoError(t, fn.Error)
		assert.NotEmpty(t, fn.AbstractedCode)
		t.Logf("Function %s abstracted code:\n%s\n", fn.FunctionName, fn.AbstractedCode)
	}
}

// TestConvertGcovrUncoveredReport tests the conversion from gcovr format.
func TestConvertGcovrUncoveredReport(t *testing.T) {
	// This test verifies the data structure conversion
	// The actual gcovr report would come from the gcovr-json-util package

	// We'll test with nil input
	input := ConvertGcovrUncoveredReport(nil, "/base/path")
	assert.NotNil(t, input)
	assert.Len(t, input.Files, 0)
}

// TestGetFileExtension tests file extension extraction.
func TestGetFileExtension(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/path/to/file.c", ".c"},
		{"/path/to/file.cpp", ".cpp"},
		{"/path/to/file.cc", ".cc"},
		{"/path/to/file", ""},
		{"/path/to/.hidden", ".hidden"},
		{"file.c", ".c"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := getFileExtension(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCppAbstractor_FunctionNotFound tests error handling when function is not found.
func TestCppAbstractor_FunctionNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.c")

	sourceCode := `int foo() {
    return 42;
}`

	err := os.WriteFile(testFile, []byte(sourceCode), 0644)
	require.NoError(t, err)

	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath: testFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "nonexistent",
						UncoveredLines: []int{2},
					},
				},
			},
		},
	}

	abstractor := NewCppAbstractor()
	output, err := abstractor.Abstract(input)
	require.NoError(t, err)
	require.Len(t, output.Functions, 1)

	fn := output.Functions[0]
	assert.Error(t, fn.Error)
	assert.Contains(t, fn.Error.Error(), "not found")
	assert.Empty(t, fn.AbstractedCode)
}

// TestCppAbstractor_FileReadError tests error handling for unreadable files.
func TestCppAbstractor_FileReadError(t *testing.T) {
	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath: "/nonexistent/file.c",
				Functions: []UncoveredFunction{
					{
						FunctionName:   "test",
						UncoveredLines: []int{1},
					},
				},
			},
		},
	}

	abstractor := NewCppAbstractor()
	output, err := abstractor.Abstract(input)
	require.NoError(t, err)
	require.Len(t, output.Functions, 1)

	fn := output.Functions[0]
	assert.Error(t, fn.Error)
	assert.Contains(t, fn.Error.Error(), "failed to read file")
}

// TestCppAbstractor_ComplexNestedControl tests complex nested control flow with multiple uncovered branches.
func TestCppAbstractor_ComplexNestedControl(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.cpp")

	sourceCode := `#include <bits/stdc++.h>
using namespace std;

int log_level = 2;

void f(bool flag, int a, int b) {

  if (log_level == 1) {
    cout << "log level is" << log_level << endl;

    if (flag)
      cout << "flag is true" << endl;
  } else if (log_level == 2) {
    cout << "log level is" << log_level << endl;

    cout << log_level << endl;

    if (flag)
      cout << "flag is true" << endl;
    else
      cout << "flag is false" << endl;
  } else
    cout << "log is off";

  if (flag) {
    // ...
    if (a > 0)
      cout << "a > 0" << endl;
    else if (a == 0) {
      cout << "a == 0" << endl;
    }
    //  ...
  } else if (!flag) {
    switch (b) {
    case 0:
      cout << "b == 0" << endl;
      break;
    case 1:
    case 2:
      cout << "Uncovered" << " ";
      cout << "Lines1" << " ";
    default:
      cout << "default" << endl;
      break;
    }
  } else {
    cout << "Uncovered" << " ";
    cout << "Lines2" << " ";
  }
}`

	err := os.WriteFile(testFile, []byte(sourceCode), 0644)
	require.NoError(t, err)

	// Lines 40-41 are "cout << "Uncovered" << " ";" and "cout << "Lines1" << " ";"
	// Lines 47-48 are "cout << "Uncovered" << " ";" and "cout << "Lines2" << " ";"
	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath: testFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "f",
						UncoveredLines: []int{40, 41, 47, 48},
						TotalLines:     45,
						CoveredLines:   41,
					},
				},
			},
		},
	}

	abstractor := NewCppAbstractor()
	output, err := abstractor.Abstract(input)
	require.NoError(t, err)
	require.Len(t, output.Functions, 1)

	fn := output.Functions[0]
	assert.Equal(t, testFile, fn.FilePath)
	assert.Equal(t, "f", fn.FunctionName)
	assert.NoError(t, fn.Error)
	assert.NotEmpty(t, fn.AbstractedCode)

	t.Logf("=== Abstracted Code ===\n%s\n======================", fn.AbstractedCode)

	// Verify function signature is present
	assert.Contains(t, fn.AbstractedCode, "void f(bool flag, int a, int b)")

	// The first if-else-if-else block should be abstracted (all covered)
	// We expect "// ..." for covered branches
	assert.Contains(t, fn.AbstractedCode, "if (log_level == 1)")
	assert.Contains(t, fn.AbstractedCode, "else if (log_level == 2)")

	// The second if-else-if-else block contains uncovered code
	assert.Contains(t, fn.AbstractedCode, "if (flag)")
	assert.Contains(t, fn.AbstractedCode, "else if (!flag)")

	// Switch statement should be present with uncovered case 1/2
	assert.Contains(t, fn.AbstractedCode, "switch (b)")
	assert.Contains(t, fn.AbstractedCode, "case 0:")
	assert.Contains(t, fn.AbstractedCode, "case 1:")
	assert.Contains(t, fn.AbstractedCode, "case 2:")

	// Uncovered lines should be fully preserved
	assert.Contains(t, fn.AbstractedCode, `"Uncovered"`)
	assert.Contains(t, fn.AbstractedCode, `"Lines1"`)
	assert.Contains(t, fn.AbstractedCode, `"Lines2"`)

	// The else branch with uncovered code should be present
	assert.Contains(t, fn.AbstractedCode, "else {")

	// Expected structure verification
	// 1. First control block (log_level checks) should be summarized since all branches are covered
	// 2. Second control block should preserve the path to uncovered code:
	//    - if (flag) should be summarized with // ...
	//    - else if (!flag) should contain full switch with uncovered case 1/2 statements
	//    - else should contain full uncovered statements
}
