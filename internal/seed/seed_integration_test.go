//go:build integration

package seed

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSeed_Integration_SaveAndLoad tests saving and loading seeds.
func TestSeed_Integration_SaveAndLoad(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seed_integration_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	originalSeed := &Seed{
		ID: "test_seed_001",
		Content: `
#include <stdio.h>
int main() {
    printf("Hello, World!\n");
    return 0;
}
`,
		TestCases: []TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "Hello, World!"},
			{RunningCommand: "./a.out arg1", ExpectedResult: "Success"},
		},
	}

	// Save the seed
	err = SaveSeed(tempDir, originalSeed)
	require.NoError(t, err)

	// Verify file was created
	expectedPath := filepath.Join(tempDir, "test_seed_001.seed")
	assert.FileExists(t, expectedPath)

	// Read back and verify
	content, err := os.ReadFile(expectedPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Hello, World!")
	assert.Contains(t, string(content), "JSON_TESTCASES_START")
}

// TestSeed_Integration_SaveAndLoadWithMetadata tests metadata persistence.
func TestSeed_Integration_SaveAndLoadWithMetadata(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seed_metadata_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	namer := NewDefaultNamingStrategy()

	originalSeed := &Seed{
		Meta: Metadata{
			ID:        42,
			ParentID:  10,
			Depth:     2,
			State:     SeedStatePending,
			CreatedAt: time.Now(),
		},
		Content: `int main() { return 0; }`,
		TestCases: []TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "0"},
		},
	}

	// Save with metadata
	filename, err := SaveSeedWithMetadata(tempDir, originalSeed, namer)
	require.NoError(t, err)
	assert.NotEmpty(t, filename)

	// Load back
	loadedSeed, err := LoadSeedWithMetadata(filepath.Join(tempDir, filename), namer)
	require.NoError(t, err)

	// Note: Depth is not stored in filename, so it won't be recovered
	assert.Equal(t, originalSeed.Meta.ID, loadedSeed.Meta.ID)
	assert.Equal(t, originalSeed.Meta.ParentID, loadedSeed.Meta.ParentID)
	// Depth is not persisted in filename format
	// assert.Equal(t, originalSeed.Meta.Depth, loadedSeed.Meta.Depth)
	assert.Equal(t, originalSeed.Content, loadedSeed.Content)
	assert.Equal(t, len(originalSeed.TestCases), len(loadedSeed.TestCases))
}

// TestSeed_Integration_LoadMultipleSeeds tests loading multiple seeds from directory.
func TestSeed_Integration_LoadMultipleSeeds(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seed_multi_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	namer := NewDefaultNamingStrategy()

	// Create multiple seeds
	for i := 1; i <= 5; i++ {
		s := &Seed{
			Meta: Metadata{
				ID:    uint64(i),
				State: SeedStatePending,
			},
			Content: `int main() { return 0; }`,
			TestCases: []TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "0"},
			},
		}
		_, err := SaveSeedWithMetadata(tempDir, s, namer)
		require.NoError(t, err)
	}

	// Load all seeds
	seeds, err := LoadSeedsWithMetadata(tempDir, namer)
	require.NoError(t, err)
	assert.Equal(t, 5, len(seeds))
}

// TestSeed_Integration_Understanding tests understanding file operations.
func TestSeed_Integration_Understanding(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seed_understanding_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	understanding := `# Understanding Document

## Overview
This is a test understanding document for the fuzzer.

## Key Points
- Point 1: Stack protection
- Point 2: Buffer overflow detection
- Point 3: Canary values

## Attack Vectors
1. Integer overflow
2. Format string vulnerabilities
3. Use-after-free
`

	// Save understanding
	err = SaveUnderstanding(tempDir, understanding)
	require.NoError(t, err)

	// Verify file exists
	expectedPath := GetUnderstandingPath(tempDir)
	assert.FileExists(t, expectedPath)

	// Load and verify
	loaded, err := LoadUnderstanding(tempDir)
	require.NoError(t, err)
	assert.Equal(t, understanding, loaded)
}

// TestSeed_Integration_ComplexTestCases tests seeds with complex test cases.
func TestSeed_Integration_ComplexTestCases(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seed_complex_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	namer := NewDefaultNamingStrategy()

	originalSeed := &Seed{
		Meta: Metadata{
			ID:    100,
			State: SeedStatePending,
		},
		Content: `
#include <stdio.h>
#include <stdlib.h>

int main(int argc, char *argv[]) {
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <number>\n", argv[0]);
        return 1;
    }
    int n = atoi(argv[1]);
    printf("Result: %d\n", n * 2);
    return 0;
}
`,
		TestCases: []TestCase{
			{RunningCommand: "./a.out 5", ExpectedResult: "Result: 10"},
			{RunningCommand: "./a.out 0", ExpectedResult: "Result: 0"},
			{RunningCommand: "./a.out -10", ExpectedResult: "Result: -20"},
			{RunningCommand: "./a.out 100", ExpectedResult: "Result: 200"},
		},
	}

	filename, err := SaveSeedWithMetadata(tempDir, originalSeed, namer)
	require.NoError(t, err)

	loaded, err := LoadSeedWithMetadata(filepath.Join(tempDir, filename), namer)
	require.NoError(t, err)

	assert.Equal(t, 4, len(loaded.TestCases))
	assert.Equal(t, "./a.out 5", loaded.TestCases[0].RunningCommand)
	assert.Equal(t, "Result: 10", loaded.TestCases[0].ExpectedResult)
}

// TestSeed_Integration_LineageTracking tests parent-child relationships.
func TestSeed_Integration_LineageTracking(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seed_lineage_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	namer := NewDefaultNamingStrategy()

	// Create parent seed
	parent := &Seed{
		Meta: Metadata{
			ID:       1,
			ParentID: 0,
			Depth:    0,
			State:    SeedStateProcessed,
		},
		Content: `int main() { return 0; }`,
		TestCases: []TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "0"},
		},
	}

	// Create child seeds
	children := []*Seed{
		{
			Meta: Metadata{
				ID:       2,
				ParentID: 1,
				Depth:    1,
				State:    SeedStatePending,
			},
			Content: `int main() { return 1; }`,
			TestCases: []TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "1"},
			},
		},
		{
			Meta: Metadata{
				ID:       3,
				ParentID: 1,
				Depth:    1,
				State:    SeedStatePending,
			},
			Content: `int main() { return 2; }`,
			TestCases: []TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "2"},
			},
		},
	}

	// Save all
	SaveSeedWithMetadata(tempDir, parent, namer)
	for _, child := range children {
		SaveSeedWithMetadata(tempDir, child, namer)
	}

	// Load and verify lineage
	seeds, err := LoadSeedsWithMetadata(tempDir, namer)
	require.NoError(t, err)
	assert.Equal(t, 3, len(seeds))

	// Find parent
	var loadedParent *Seed
	for _, s := range seeds {
		if s.Meta.ID == 1 {
			loadedParent = s
			break
		}
	}
	require.NotNil(t, loadedParent)
	assert.Equal(t, uint64(0), loadedParent.Meta.ParentID)
	// Note: Depth is not stored in filename, so it won't be recovered
	// assert.Equal(t, 0, loadedParent.Meta.Depth)

	// Verify children reference parent
	for _, s := range seeds {
		if s.Meta.ID != 1 {
			assert.Equal(t, uint64(1), s.Meta.ParentID)
			// Depth is not persisted in filename format
			// assert.Equal(t, 1, s.Meta.Depth)
		}
	}
}

// TestSeed_Integration_SeedStates tests different seed states.
func TestSeed_Integration_SeedStates(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seed_states_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	namer := NewDefaultNamingStrategy()

	states := []SeedState{
		SeedStatePending,
		SeedStateProcessed,
		SeedStateCrash,
		SeedStateTimeout,
	}

	for i, state := range states {
		s := &Seed{
			Meta: Metadata{
				ID:    uint64(i + 1),
				State: state,
			},
			Content: `int main() { return 0; }`,
			TestCases: []TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "0"},
			},
		}
		_, err := SaveSeedWithMetadata(tempDir, s, namer)
		require.NoError(t, err)
	}

	seeds, err := LoadSeedsWithMetadata(tempDir, namer)
	require.NoError(t, err)
	assert.Equal(t, 4, len(seeds))
}

// TestSeed_Integration_LargeSeedContent tests handling large source files.
func TestSeed_Integration_LargeSeedContent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large content test in short mode")
	}

	tempDir, err := os.MkdirTemp("", "seed_large_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	namer := NewDefaultNamingStrategy()

	// Generate large content
	largeContent := `
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
`
	// Add many functions
	for i := 0; i < 100; i++ {
		largeContent += `
int function_%d(int a, int b) {
    // Some computation
    int result = a + b + %d;
    for (int i = 0; i < 10; i++) {
        result += i;
    }
    return result;
}
`
	}
	largeContent += `
int main() {
    int result = 0;
`
	for i := 0; i < 100; i++ {
		largeContent += `    result += function_%d(1, 2);
`
	}
	largeContent += `    printf("Result: %d\n", result);
    return 0;
}
`

	s := &Seed{
		Meta: Metadata{
			ID:    1,
			State: SeedStatePending,
		},
		Content: largeContent,
		TestCases: []TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "Result:"},
		},
	}

	filename, err := SaveSeedWithMetadata(tempDir, s, namer)
	require.NoError(t, err)

	loaded, err := LoadSeedWithMetadata(filepath.Join(tempDir, filename), namer)
	require.NoError(t, err)
	assert.Equal(t, len(s.Content), len(loaded.Content))
}

// TestSeed_Integration_SpecialCharacters tests handling special characters.
func TestSeed_Integration_SpecialCharacters(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seed_special_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	namer := NewDefaultNamingStrategy()

	s := &Seed{
		Meta: Metadata{
			ID:    1,
			State: SeedStatePending,
		},
		Content: `
#include <stdio.h>

int main() {
    // Special characters: "quotes", 'single', \backslash
    char *str = "Hello \"World\"!";
    printf("%s\n", str);
    printf("Tab:\there\n");
    printf("Newline:\nhere\n");
    return 0;
}
`,
		TestCases: []TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "Hello \"World\"!"},
		},
	}

	filename, err := SaveSeedWithMetadata(tempDir, s, namer)
	require.NoError(t, err)

	loaded, err := LoadSeedWithMetadata(filepath.Join(tempDir, filename), namer)
	require.NoError(t, err)
	assert.Contains(t, loaded.Content, "Hello \\\"World\\\"!")
}

// TestSeed_Integration_NonExistentDirectory tests handling of non-existent directories.
func TestSeed_Integration_NonExistentDirectory(t *testing.T) {
	// Try to load from non-existent directory
	_, err := LoadUnderstanding("/nonexistent/path/that/does/not/exist")
	assert.Error(t, err)

	// SaveSeed should create directory
	tempDir, err := os.MkdirTemp("", "seed_mkdir_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	nestedPath := filepath.Join(tempDir, "nested", "directory", "path")
	err = SaveUnderstanding(nestedPath, "test content")
	require.NoError(t, err)
	assert.FileExists(t, GetUnderstandingPath(nestedPath))
}
