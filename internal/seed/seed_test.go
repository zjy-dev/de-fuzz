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
		s1 := &Seed{ID: "1", Type: SeedTypeC}
		s2 := &Seed{ID: "2", Type: SeedTypeAsm}

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
		s := &Seed{
			ID:       "001",
			Type:     SeedTypeC,
			Content:  "int main() { return 0; }",
			Makefile: "all:\n\tgcc source.c -o prog",
		}
		err := SaveSeed(basePath, s)
		require.NoError(t, err)

		// Verify directory and files exist
		seedDir := filepath.Join(basePath, "001_c")
		assert.DirExists(t, seedDir)
		assert.FileExists(t, filepath.Join(seedDir, "source.c"))
		assert.FileExists(t, filepath.Join(seedDir, "Makefile"))
	})

	t.Run("should save and load different seed types", func(t *testing.T) {
		// Test all three seed types
		seeds := []*Seed{
			{ID: "c001", Type: SeedTypeC, Content: "int main() { return 0; }", Makefile: "gcc source.c"},
			{ID: "casm001", Type: SeedTypeCAsm, Content: ".text\n.global main\nmain:", Makefile: "gcc source.s"},
			{ID: "asm001", Type: SeedTypeAsm, Content: ".section .text\nmain:", Makefile: "as source.s"},
		}

		for _, s := range seeds {
			err := SaveSeed(basePath, s)
			require.NoError(t, err)
		}

		// Verify directories exist with correct naming
		assert.DirExists(t, filepath.Join(basePath, "c001_c"))
		assert.DirExists(t, filepath.Join(basePath, "casm001_c-asm"))
		assert.DirExists(t, filepath.Join(basePath, "asm001_asm"))
	})

	t.Run("should load multiple seeds", func(t *testing.T) {
		// Clear the directory first
		os.RemoveAll(basePath)
		os.MkdirAll(basePath, 0755)

		s1 := &Seed{ID: "s1", Type: SeedTypeC, Content: "c1", Makefile: "m1"}
		s2 := &Seed{ID: "s2", Type: SeedTypeAsm, Content: "asm2", Makefile: "m2"}
		s3 := &Seed{ID: "s3", Type: SeedTypeCAsm, Content: "casm3", Makefile: "m3"}
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
		assert.Equal(t, SeedTypeC, seeds["s1"].Type)
		assert.Equal(t, SeedTypeAsm, seeds["s2"].Type)
		assert.Equal(t, SeedTypeCAsm, seeds["s3"].Type)
		assert.Equal(t, "c1", seeds["s1"].Content)
		assert.Equal(t, "m2", seeds["s2"].Makefile)
	})

	t.Run("should return empty pool if base path does not exist", func(t *testing.T) {
		pool, err := LoadSeeds(filepath.Join(basePath, "non_existent_dir"))
		require.NoError(t, err)
		assert.Equal(t, 0, pool.Len())
	})
}
