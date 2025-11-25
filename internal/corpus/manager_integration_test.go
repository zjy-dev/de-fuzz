//go:build integration

package corpus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// TestFileManager_Integration_FullWorkflow tests the complete corpus workflow.
func TestFileManager_Integration_FullWorkflow(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "corpus_integration_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create manager
	manager := NewFileManager(tempDir)

	// Initialize
	err = manager.Initialize()
	require.NoError(t, err)

	// Verify directories were created
	assert.DirExists(t, filepath.Join(tempDir, CorpusDir))
	assert.DirExists(t, filepath.Join(tempDir, MetadataDir))
	assert.DirExists(t, filepath.Join(tempDir, StateDir))

	// Add seeds
	seeds := []*seed.Seed{
		{
			Content: `int main() { return 0; }`,
			TestCases: []seed.TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "0"},
			},
		},
		{
			Content: `int main() { return 1; }`,
			TestCases: []seed.TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "1"},
			},
		},
		{
			Content: `int main() { return 42; }`,
			TestCases: []seed.TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "42"},
			},
		},
	}

	for _, s := range seeds {
		err := manager.Add(s)
		require.NoError(t, err)
	}

	// Verify queue length
	assert.Equal(t, 3, manager.Len())

	// Process seeds
	for i := 0; i < 3; i++ {
		s, ok := manager.Next()
		require.True(t, ok)
		require.NotNil(t, s)

		// Report result
		err := manager.ReportResult(s.Meta.ID, FuzzResult{
			State:       seed.SeedStateProcessed,
			ExecTimeUs:  1000,
			NewCoverage: uint64(i * 10),
		})
		require.NoError(t, err)
	}

	// Queue should be empty now
	_, ok := manager.Next()
	assert.False(t, ok)
	assert.Equal(t, 0, manager.Len())

	// Save state
	err = manager.Save()
	require.NoError(t, err)
}

// TestFileManager_Integration_Persistence tests that state persists across restarts.
func TestFileManager_Integration_Persistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "corpus_persist_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create first manager and add seeds
	manager1 := NewFileManager(tempDir)
	err = manager1.Initialize()
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		s := &seed.Seed{
			Content: `int main() { return 0; }`,
			TestCases: []seed.TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "0"},
			},
		}
		err := manager1.Add(s)
		require.NoError(t, err)
	}

	// Process 2 seeds (take them from queue)
	for i := 0; i < 2; i++ {
		s, ok := manager1.Next()
		require.True(t, ok)
		manager1.ReportResult(s.Meta.ID, FuzzResult{
			State: seed.SeedStateProcessed,
		})
	}

	// After Next(), queue should have 3 seeds remaining in memory
	assert.Equal(t, 3, manager1.Len())

	err = manager1.Save()
	require.NoError(t, err)

	// Create new manager (simulating restart)
	manager2 := NewFileManager(tempDir)
	err = manager2.Initialize()
	require.NoError(t, err)

	err = manager2.Recover()
	require.NoError(t, err)

	// Note: ReportResult does not persist state changes to disk,
	// so all 5 seeds are still in PENDING state on disk
	// This tests that Recover loads all seeds from disk
	assert.Equal(t, 5, manager2.Len())
}

// TestFileManager_Integration_Recovery tests recovering from disk.
func TestFileManager_Integration_Recovery(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "corpus_recovery_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Setup initial state
	manager := NewFileManager(tempDir)
	err = manager.Initialize()
	require.NoError(t, err)

	// Add seeds with different content
	contents := []string{
		`int main() { return 10; }`,
		`int main() { return 20; }`,
		`int main() { return 30; }`,
	}

	for _, content := range contents {
		s := &seed.Seed{
			Content: content,
			TestCases: []seed.TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "0"},
			},
		}
		err := manager.Add(s)
		require.NoError(t, err)
	}

	err = manager.Save()
	require.NoError(t, err)

	// Create new manager and recover
	manager2 := NewFileManager(tempDir)
	err = manager2.Initialize()
	require.NoError(t, err)

	err = manager2.Recover()
	require.NoError(t, err)

	// Verify all seeds were recovered
	assert.Equal(t, 3, manager2.Len())

	// Verify content is correct
	for i := 0; i < 3; i++ {
		s, ok := manager2.Next()
		require.True(t, ok)
		assert.Contains(t, s.Content, "return")
	}
}

// TestFileManager_Integration_ConcurrentAccess tests thread safety.
func TestFileManager_Integration_ConcurrentAccess(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "corpus_concurrent_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	manager := NewFileManager(tempDir)
	err = manager.Initialize()
	require.NoError(t, err)

	// Add seeds concurrently
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			s := &seed.Seed{
				Content: `int main() { return 0; }`,
				TestCases: []seed.TestCase{
					{RunningCommand: "./a.out", ExpectedResult: "0"},
				},
			}
			manager.Add(s)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// All seeds should be added
	assert.Equal(t, 10, manager.Len())

	// Process concurrently
	processed := make(chan uint64, 10)
	for i := 0; i < 10; i++ {
		go func() {
			s, ok := manager.Next()
			if ok {
				manager.ReportResult(s.Meta.ID, FuzzResult{
					State: seed.SeedStateProcessed,
				})
				processed <- s.Meta.ID
			} else {
				processed <- 0
			}
		}()
	}

	// Collect processed IDs
	ids := make(map[uint64]bool)
	for i := 0; i < 10; i++ {
		id := <-processed
		if id != 0 {
			ids[id] = true
		}
	}

	// All 10 seeds should be processed exactly once
	assert.Equal(t, 10, len(ids))
}

// TestFileManager_Integration_LargeCorpus tests handling large number of seeds.
func TestFileManager_Integration_LargeCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large corpus test in short mode")
	}

	tempDir, err := os.MkdirTemp("", "corpus_large_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	manager := NewFileManager(tempDir)
	err = manager.Initialize()
	require.NoError(t, err)

	numSeeds := 100

	// Add many seeds
	for i := 0; i < numSeeds; i++ {
		s := &seed.Seed{
			Content: `int main() { return 0; }`,
			TestCases: []seed.TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "0"},
			},
		}
		err := manager.Add(s)
		require.NoError(t, err)
	}

	assert.Equal(t, numSeeds, manager.Len())

	// Save and recover
	err = manager.Save()
	require.NoError(t, err)

	manager2 := NewFileManager(tempDir)
	err = manager2.Initialize()
	require.NoError(t, err)

	err = manager2.Recover()
	require.NoError(t, err)

	assert.Equal(t, numSeeds, manager2.Len())
}

// TestFileManager_Integration_SeedStates tests different seed states.
func TestFileManager_Integration_SeedStates(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "corpus_states_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	manager := NewFileManager(tempDir)
	err = manager.Initialize()
	require.NoError(t, err)

	// Add a seed
	s := &seed.Seed{
		Content: `int main() { return 0; }`,
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "0"},
		},
	}
	err = manager.Add(s)
	require.NoError(t, err)

	// Get and test different result states
	retrieved, ok := manager.Next()
	require.True(t, ok)

	testStates := []seed.SeedState{
		seed.SeedStateProcessed,
		seed.SeedStateCrash,
		seed.SeedStateTimeout,
	}

	for _, state := range testStates {
		err := manager.ReportResult(retrieved.Meta.ID, FuzzResult{
			State:       state,
			ExecTimeUs:  1000,
			NewCoverage: 100,
		})
		require.NoError(t, err)
	}
}

// TestFileManager_Integration_CoverageTracking tests coverage updates.
func TestFileManager_Integration_CoverageTracking(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "corpus_coverage_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	manager := NewFileManager(tempDir)
	err = manager.Initialize()
	require.NoError(t, err)

	// Add seeds
	for i := 0; i < 5; i++ {
		s := &seed.Seed{
			Content: `int main() { return 0; }`,
			TestCases: []seed.TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "0"},
			},
		}
		manager.Add(s)
	}

	// Process with increasing coverage
	coverages := []uint64{10, 25, 50, 75, 100}
	for i, cov := range coverages {
		s, ok := manager.Next()
		require.True(t, ok)

		err := manager.ReportResult(s.Meta.ID, FuzzResult{
			State:       seed.SeedStateProcessed,
			NewCoverage: cov,
		})
		require.NoError(t, err)

		// Check state manager has updated coverage
		stateManager := manager.GetStateManager()
		state := stateManager.GetState()
		assert.GreaterOrEqual(t, state.TotalCoverage, coverages[i])
	}
}

// TestFileManager_Integration_EmptyCorpus tests handling empty corpus.
func TestFileManager_Integration_EmptyCorpus(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "corpus_empty_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	manager := NewFileManager(tempDir)
	err = manager.Initialize()
	require.NoError(t, err)

	// Should handle empty corpus gracefully
	assert.Equal(t, 0, manager.Len())

	s, ok := manager.Next()
	assert.False(t, ok)
	assert.Nil(t, s)

	// Recovery on empty should work
	err = manager.Recover()
	require.NoError(t, err)
	assert.Equal(t, 0, manager.Len())
}
