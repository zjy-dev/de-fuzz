package seed

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemoryPool(t *testing.T) {
	pool := NewInMemoryPool()

	t.Run("should be empty initially", func(t *testing.T) {
		assert.Equal(t, 0, pool.Len())
		assert.Nil(t, pool.Next())
	})

	t.Run("should add and retrieve seeds", func(t *testing.T) {
		testCases1 := []TestCase{
			{RunningCommand: "./prog", ExpectedResult: "success"},
		}
		testCases2 := []TestCase{
			{RunningCommand: "./prog -v", ExpectedResult: "verbose output"},
		}

		s1 := &Seed{ID: "1", Content: "int main(){}", TestCases: testCases1}
		s2 := &Seed{ID: "2", Content: "int foo(){}", TestCases: testCases2}

		pool.Add(s1)
		pool.Add(s2)
		assert.Equal(t, 2, pool.Len())

		nextSeed := pool.Next()
		assert.Equal(t, s1, nextSeed)
		assert.Equal(t, 1, pool.Len())

		nextSeed = pool.Next()
		assert.Equal(t, s2, nextSeed)
		assert.Equal(t, 0, pool.Len())

		assert.Nil(t, pool.Next())
	})
}

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
			ID:        "001",
			Content:   "int main() { return 0; }",
			TestCases: testCases,
		}
		err := SaveSeed(basePath, s)
		require.NoError(t, err)

		// Verify file exists
		seedFile := filepath.Join(basePath, "001.c")
		assert.FileExists(t, seedFile)

		// Verify content
		content, err := os.ReadFile(seedFile)
		require.NoError(t, err)
		assert.Contains(t, string(content), "int main() { return 0; }")
		assert.Contains(t, string(content), "JSON_TESTCASES_START")
		assert.Contains(t, string(content), "running command")
	})

	t.Run("should save and load different seeds", func(t *testing.T) {
		// Test multiple seeds with different content
		testCases1 := []TestCase{{RunningCommand: "./prog", ExpectedResult: "success"}}
		testCases2 := []TestCase{{RunningCommand: "./prog", ExpectedResult: "hello"}}
		testCases3 := []TestCase{{RunningCommand: "./prog", ExpectedResult: "segfault"}}

		seeds := []*Seed{
			{ID: "c001", Content: "int main() { return 0; }", TestCases: testCases1},
			{ID: "c002", Content: "#include <stdio.h>\nint main() { printf(\"hello\"); }", TestCases: testCases2},
			{ID: "c003", Content: "int main() { int x[10]; return x[20]; }", TestCases: testCases3},
		}

		for _, s := range seeds {
			err := SaveSeed(basePath, s)
			require.NoError(t, err)
		}

		// Verify files exist with correct naming
		assert.FileExists(t, filepath.Join(basePath, "c001.c"))
		assert.FileExists(t, filepath.Join(basePath, "c002.c"))
		assert.FileExists(t, filepath.Join(basePath, "c003.c"))
	})

	t.Run("should load multiple seeds", func(t *testing.T) {
		// Clear the directory first
		os.RemoveAll(basePath)
		os.MkdirAll(basePath, 0755)

		testCases1 := []TestCase{{RunningCommand: "./prog1", ExpectedResult: "result1"}}
		testCases2 := []TestCase{{RunningCommand: "./prog2", ExpectedResult: "result2"}}
		testCases3 := []TestCase{{RunningCommand: "./prog3", ExpectedResult: "result3"}}

		s1 := &Seed{ID: "s1", Content: "c1", TestCases: testCases1}
		s2 := &Seed{ID: "s2", Content: "asm2", TestCases: testCases2}
		s3 := &Seed{ID: "s3", Content: "casm3", TestCases: testCases3}
		require.NoError(t, SaveSeed(basePath, s1))
		require.NoError(t, SaveSeed(basePath, s2))
		require.NoError(t, SaveSeed(basePath, s3))

		pool, err := LoadSeeds(basePath)
		require.NoError(t, err)
		assert.Equal(t, 3, pool.Len())

		// Note: LoadSeeds doesn't guarantee order, so we check presence
		seeds := make(map[string]*Seed)
		for {
			s := pool.Next()
			if s == nil {
				break
			}
			seeds[s.ID] = s
		}
		assert.Contains(t, seeds, "s1")
		assert.Contains(t, seeds, "s2")
		assert.Contains(t, seeds, "s3")
		assert.Equal(t, "c1", seeds["s1"].Content)
		assert.Equal(t, testCases1, seeds["s1"].TestCases)
		assert.Equal(t, "asm2", seeds["s2"].Content)
		assert.Equal(t, testCases2, seeds["s2"].TestCases)
	})

	t.Run("should return empty pool if base path does not exist", func(t *testing.T) {
		pool, err := LoadSeeds(filepath.Join(basePath, "non_existent_dir"))
		require.NoError(t, err)
		assert.Equal(t, 0, pool.Len())
	})
}
