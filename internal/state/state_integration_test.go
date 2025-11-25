//go:build integration

package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFileManager_Integration_SaveAndLoad tests state persistence.
func TestFileManager_Integration_SaveAndLoad(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state_integration_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create and configure state
	manager := NewFileManager(tempDir)
	err = manager.Load()
	require.NoError(t, err)

	// Allocate some IDs
	for i := 0; i < 10; i++ {
		id := manager.NextID()
		assert.Equal(t, uint64(i+1), id)
	}

	manager.UpdateCurrentID(5)
	manager.UpdateCoverage(8500) // 85.00%
	manager.UpdatePoolSize(100)
	for i := 0; i < 50; i++ {
		manager.IncrementProcessed()
	}

	// Save state
	err = manager.Save()
	require.NoError(t, err)

	// Verify file exists
	statePath := filepath.Join(tempDir, StateFileName)
	assert.FileExists(t, statePath)

	// Create new manager and load
	newManager := NewFileManager(tempDir)
	err = newManager.Load()
	require.NoError(t, err)

	// Verify loaded state
	state := newManager.GetState()
	assert.Equal(t, uint64(10), state.LastAllocatedID)
	assert.Equal(t, uint64(5), state.CurrentFuzzingID)
	assert.Equal(t, uint64(8500), state.TotalCoverage)
	assert.Equal(t, 100, state.Stats.PoolSize)
	assert.Equal(t, 50, state.Stats.ProcessedCount)
}

// TestFileManager_Integration_LoadNonExistent tests loading when no state file exists.
func TestFileManager_Integration_LoadNonExistent(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state_nonexistent_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	manager := NewFileManager(tempDir)
	err = manager.Load()
	require.NoError(t, err)

	// Should have default values
	state := manager.GetState()
	assert.Equal(t, uint64(0), state.LastAllocatedID)
	assert.Equal(t, uint64(0), state.CurrentFuzzingID)
	assert.Equal(t, uint64(0), state.TotalCoverage)
	assert.Equal(t, 0, state.Stats.PoolSize)
	assert.Equal(t, 0, state.Stats.ProcessedCount)
}

// TestFileManager_Integration_NextID tests ID allocation.
func TestFileManager_Integration_NextID(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state_nextid_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	manager := NewFileManager(tempDir)
	err = manager.Load()
	require.NoError(t, err)

	// IDs should be sequential starting from 1
	ids := make([]uint64, 100)
	for i := 0; i < 100; i++ {
		ids[i] = manager.NextID()
	}

	for i := 0; i < 100; i++ {
		assert.Equal(t, uint64(i+1), ids[i])
	}

	state := manager.GetState()
	assert.Equal(t, uint64(100), state.LastAllocatedID)
}

// TestFileManager_Integration_Concurrency tests thread-safety.
func TestFileManager_Integration_Concurrency(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state_concurrent_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	manager := NewFileManager(tempDir)
	err = manager.Load()
	require.NoError(t, err)

	const numGoroutines = 10
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				manager.NextID()
				manager.IncrementProcessed()
				manager.UpdatePoolSize(j)
				manager.UpdateCoverage(uint64(j * 100))
			}
		}()
	}

	wg.Wait()

	state := manager.GetState()
	// All IDs should be allocated
	assert.Equal(t, uint64(numGoroutines*opsPerGoroutine), state.LastAllocatedID)
	// All processed increments should be counted
	assert.Equal(t, numGoroutines*opsPerGoroutine, state.Stats.ProcessedCount)
}

// TestFileManager_Integration_CreateDirectory tests directory creation.
func TestFileManager_Integration_CreateDirectory(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state_mkdir_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	nestedPath := filepath.Join(tempDir, "nested", "directory", "path")
	manager := NewFileManager(nestedPath)
	err = manager.Load()
	require.NoError(t, err)

	manager.NextID()
	err = manager.Save()
	require.NoError(t, err)

	assert.DirExists(t, nestedPath)
	assert.FileExists(t, filepath.Join(nestedPath, StateFileName))
}

// TestFileManager_Integration_JSONFormat tests the JSON file format.
func TestFileManager_Integration_JSONFormat(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state_json_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	manager := NewFileManager(tempDir)
	err = manager.Load()
	require.NoError(t, err)

	manager.NextID()
	manager.NextID()
	manager.UpdateCurrentID(1)
	manager.UpdateCoverage(9999)
	manager.UpdatePoolSize(50)
	manager.IncrementProcessed()

	err = manager.Save()
	require.NoError(t, err)

	// Read and parse JSON directly
	data, err := os.ReadFile(filepath.Join(tempDir, StateFileName))
	require.NoError(t, err)

	var rawState map[string]interface{}
	err = json.Unmarshal(data, &rawState)
	require.NoError(t, err)

	assert.Equal(t, float64(2), rawState["last_allocated_id"])
	assert.Equal(t, float64(1), rawState["current_fuzzing_id"])
	assert.Equal(t, float64(9999), rawState["total_coverage"])

	stats := rawState["queue_stats"].(map[string]interface{})
	assert.Equal(t, float64(50), stats["pool_size"])
	assert.Equal(t, float64(1), stats["processed_count"])
}

// TestFileManager_Integration_Recover tests recovery from saved state.
func TestFileManager_Integration_Recover(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state_recover_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Simulate previous session
	{
		manager := NewFileManager(tempDir)
		manager.Load()

		// Allocate 100 IDs
		for i := 0; i < 100; i++ {
			manager.NextID()
		}
		manager.UpdateCurrentID(50) // Was processing ID 50
		manager.UpdateCoverage(7500)
		manager.UpdatePoolSize(75)
		for i := 0; i < 49; i++ { // Processed 49 seeds
			manager.IncrementProcessed()
		}
		manager.Save()
	}

	// Recover in new session
	{
		manager := NewFileManager(tempDir)
		err := manager.Load()
		require.NoError(t, err)

		state := manager.GetState()
		assert.Equal(t, uint64(100), state.LastAllocatedID)
		assert.Equal(t, uint64(50), state.CurrentFuzzingID)
		assert.Equal(t, uint64(7500), state.TotalCoverage)
		assert.Equal(t, 75, state.Stats.PoolSize)
		assert.Equal(t, 49, state.Stats.ProcessedCount)

		// Continue from where we left off
		nextID := manager.NextID()
		assert.Equal(t, uint64(101), nextID)
	}
}

// TestFileManager_Integration_CorruptedFile tests handling of corrupted state file.
func TestFileManager_Integration_CorruptedFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state_corrupt_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create corrupted state file
	corruptedData := []byte("not valid json {")
	err = os.WriteFile(filepath.Join(tempDir, StateFileName), corruptedData, 0644)
	require.NoError(t, err)

	manager := NewFileManager(tempDir)
	err = manager.Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse state file")
}

// TestFileManager_Integration_GetFilePath tests GetFilePath method.
func TestFileManager_Integration_GetFilePath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state_filepath_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	manager := NewFileManager(tempDir)
	expectedPath := filepath.Join(tempDir, StateFileName)
	assert.Equal(t, expectedPath, manager.GetFilePath())
}

// TestFileManager_Integration_UpdateCoverage tests coverage updates.
func TestFileManager_Integration_UpdateCoverage(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state_coverage_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	manager := NewFileManager(tempDir)
	manager.Load()

	// Update coverage multiple times
	coverageValues := []uint64{0, 1000, 5000, 7500, 9000, 9500, 9900, 10000}
	for _, cov := range coverageValues {
		manager.UpdateCoverage(cov)
		state := manager.GetState()
		assert.Equal(t, cov, state.TotalCoverage)
	}
}

// TestFileManager_Integration_ManagerInterface tests Manager interface implementation.
func TestFileManager_Integration_ManagerInterface(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state_interface_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	var manager Manager = NewFileManager(tempDir)

	err = manager.Load()
	require.NoError(t, err)

	id := manager.NextID()
	assert.Equal(t, uint64(1), id)

	manager.UpdateCurrentID(1)
	manager.UpdateCoverage(5000)
	manager.IncrementProcessed()
	manager.UpdatePoolSize(10)

	state := manager.GetState()
	assert.Equal(t, uint64(1), state.LastAllocatedID)
	assert.Equal(t, uint64(1), state.CurrentFuzzingID)
	assert.Equal(t, uint64(5000), state.TotalCoverage)
	assert.Equal(t, 1, state.Stats.ProcessedCount)
	assert.Equal(t, 10, state.Stats.PoolSize)

	err = manager.Save()
	require.NoError(t, err)
}

// TestFileManager_Integration_MultipleLoadSave tests multiple load/save cycles.
func TestFileManager_Integration_MultipleLoadSave(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state_cycles_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	manager := NewFileManager(tempDir)

	for cycle := 0; cycle < 10; cycle++ {
		err = manager.Load()
		require.NoError(t, err)

		// Do some work
		manager.NextID()
		manager.IncrementProcessed()
		manager.UpdateCoverage(uint64(cycle * 1000))

		err = manager.Save()
		require.NoError(t, err)
	}

	// Final verification
	state := manager.GetState()
	assert.Equal(t, uint64(10), state.LastAllocatedID)
	assert.Equal(t, 10, state.Stats.ProcessedCount)
	assert.Equal(t, uint64(9000), state.TotalCoverage)
}

// TestQueueStats_Integration tests QueueStats struct.
func TestQueueStats_Integration(t *testing.T) {
	stats := QueueStats{
		PoolSize:       100,
		ProcessedCount: 50,
	}

	// Marshal to JSON
	data, err := json.Marshal(stats)
	require.NoError(t, err)

	// Unmarshal back
	var loaded QueueStats
	err = json.Unmarshal(data, &loaded)
	require.NoError(t, err)

	assert.Equal(t, stats.PoolSize, loaded.PoolSize)
	assert.Equal(t, stats.ProcessedCount, loaded.ProcessedCount)
}

// TestGlobalState_Integration tests GlobalState struct.
func TestGlobalState_Integration(t *testing.T) {
	state := GlobalState{
		LastAllocatedID:  100,
		CurrentFuzzingID: 50,
		TotalCoverage:    7500,
		Stats: QueueStats{
			PoolSize:       75,
			ProcessedCount: 49,
		},
	}

	// Marshal to JSON
	data, err := json.Marshal(state)
	require.NoError(t, err)

	// Unmarshal back
	var loaded GlobalState
	err = json.Unmarshal(data, &loaded)
	require.NoError(t, err)

	assert.Equal(t, state.LastAllocatedID, loaded.LastAllocatedID)
	assert.Equal(t, state.CurrentFuzzingID, loaded.CurrentFuzzingID)
	assert.Equal(t, state.TotalCoverage, loaded.TotalCoverage)
	assert.Equal(t, state.Stats.PoolSize, loaded.Stats.PoolSize)
	assert.Equal(t, state.Stats.ProcessedCount, loaded.Stats.ProcessedCount)
}
