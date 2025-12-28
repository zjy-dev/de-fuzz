package seed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorage(t *testing.T) {
	basePath, err := os.MkdirTemp("", "seed_storage_test_")
	require.NoError(t, err)
	defer os.RemoveAll(basePath)

	t.Run("should save and load understanding", func(t *testing.T) {
		content := "This is the LLM's understanding."
		err := SaveUnderstanding(basePath, content)
		require.NoError(t, err)

		loadedContent, err := LoadUnderstanding(basePath)
		require.NoError(t, err)
		assert.Equal(t, content, loadedContent)
	})

	t.Run("should save and load a single seed", func(t *testing.T) {
		testCases := []TestCase{
			{RunningCommand: "./prog", ExpectedResult: "success"},
		}
		s := &Seed{
			Meta:      Metadata{ID: 1},
			Content:   "int main() { return 0; }",
			TestCases: testCases,
		}
		namer := NewDefaultNamingStrategy()
		filename, err := SaveSeedWithMetadata(basePath, s, namer)
		require.NoError(t, err)

		// Verify directory exists (filename without .seed extension)
		seedDir := filepath.Join(basePath, strings.TrimSuffix(filename, ".seed"))
		assert.DirExists(t, seedDir)

		// Verify source.c exists
		sourceFile := filepath.Join(seedDir, "source.c")
		assert.FileExists(t, sourceFile)

		// Verify content
		content, err := os.ReadFile(sourceFile)
		require.NoError(t, err)
		assert.Contains(t, string(content), "int main() { return 0; }")
	})

	t.Run("should save and load different seeds", func(t *testing.T) {
		// Clear and recreate the directory
		os.RemoveAll(basePath)
		os.MkdirAll(basePath, 0755)

		// Test multiple seeds with different content
		testCases1 := []TestCase{{RunningCommand: "./prog", ExpectedResult: "success"}}
		testCases2 := []TestCase{{RunningCommand: "./prog", ExpectedResult: "hello"}}
		testCases3 := []TestCase{{RunningCommand: "./prog", ExpectedResult: "segfault"}}

		namer := NewDefaultNamingStrategy()
		seeds := []*Seed{
			{Meta: Metadata{ID: 1}, Content: "int main() { return 0; }", TestCases: testCases1},
			{Meta: Metadata{ID: 2}, Content: "#include <stdio.h>\nint main() { printf(\"hello\"); }", TestCases: testCases2},
			{Meta: Metadata{ID: 3}, Content: "int main() { int x[10]; return x[20]; }", TestCases: testCases3},
		}

		var filenames []string
		for _, s := range seeds {
			filename, err := SaveSeedWithMetadata(basePath, s, namer)
			require.NoError(t, err)
			filenames = append(filenames, strings.TrimSuffix(filename, ".seed"))
		}

		// Verify directories exist with correct naming
		assert.DirExists(t, filepath.Join(basePath, filenames[0]))
		assert.DirExists(t, filepath.Join(basePath, filenames[1]))
		assert.DirExists(t, filepath.Join(basePath, filenames[2]))
	})

	t.Run("should load multiple seeds with metadata", func(t *testing.T) {
		// Clear the directory first
		os.RemoveAll(basePath)
		os.MkdirAll(basePath, 0755)

		testCases1 := []TestCase{{RunningCommand: "./prog1", ExpectedResult: "result1"}}
		testCases2 := []TestCase{{RunningCommand: "./prog2", ExpectedResult: "result2"}}
		testCases3 := []TestCase{{RunningCommand: "./prog3", ExpectedResult: "result3"}}

		namer := NewDefaultNamingStrategy()
		s1 := &Seed{Meta: Metadata{ID: 1}, Content: "c1", TestCases: testCases1}
		s2 := &Seed{Meta: Metadata{ID: 2}, Content: "asm2", TestCases: testCases2}
		s3 := &Seed{Meta: Metadata{ID: 3}, Content: "casm3", TestCases: testCases3}
		_, err := SaveSeedWithMetadata(basePath, s1, namer)
		require.NoError(t, err)
		_, err = SaveSeedWithMetadata(basePath, s2, namer)
		require.NoError(t, err)
		_, err = SaveSeedWithMetadata(basePath, s3, namer)
		require.NoError(t, err)

		seeds, err := LoadSeedsWithMetadata(basePath, namer)
		require.NoError(t, err)
		assert.Equal(t, 3, len(seeds))

		// Build a map for easy lookup
		seedMap := make(map[uint64]*Seed)
		for _, s := range seeds {
			seedMap[s.Meta.ID] = s
		}
		assert.Contains(t, seedMap, uint64(1))
		assert.Contains(t, seedMap, uint64(2))
		assert.Contains(t, seedMap, uint64(3))
		assert.Equal(t, "c1", seedMap[1].Content)
		assert.Equal(t, testCases1, seedMap[1].TestCases)
		assert.Equal(t, "asm2", seedMap[2].Content)
		assert.Equal(t, testCases2, seedMap[2].TestCases)
	})

	t.Run("should return empty slice if base path does not exist", func(t *testing.T) {
		seeds, err := LoadSeedsWithMetadata(filepath.Join(basePath, "non_existent_dir"), NewDefaultNamingStrategy())
		require.NoError(t, err)
		assert.Equal(t, 0, len(seeds))
	})
}
