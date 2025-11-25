package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileManager(t *testing.T) {
	t.Run("should initialize with default state", func(t *testing.T) {
		tmpDir := t.TempDir()
		manager := NewFileManager(tmpDir)

		err := manager.Load()
		if err != nil {
			t.Fatalf("failed to load: %v", err)
		}

		state := manager.GetState()
		if state.LastAllocatedID != 0 {
			t.Errorf("expected LastAllocatedID 0, got %d", state.LastAllocatedID)
		}
		if state.CurrentFuzzingID != 0 {
			t.Errorf("expected CurrentFuzzingID 0, got %d", state.CurrentFuzzingID)
		}
	})

	t.Run("should allocate sequential IDs", func(t *testing.T) {
		tmpDir := t.TempDir()
		manager := NewFileManager(tmpDir)
		_ = manager.Load()

		id1 := manager.NextID()
		id2 := manager.NextID()
		id3 := manager.NextID()

		if id1 != 1 {
			t.Errorf("expected first ID 1, got %d", id1)
		}
		if id2 != 2 {
			t.Errorf("expected second ID 2, got %d", id2)
		}
		if id3 != 3 {
			t.Errorf("expected third ID 3, got %d", id3)
		}
	})

	t.Run("should save and load state", func(t *testing.T) {
		tmpDir := t.TempDir()
		manager := NewFileManager(tmpDir)
		_ = manager.Load()

		// Modify state
		manager.NextID()
		manager.NextID()
		manager.UpdateCurrentID(2)
		manager.UpdateCoverage(1500)
		manager.UpdatePoolSize(10)
		manager.IncrementProcessed()
		manager.IncrementProcessed()

		// Save
		err := manager.Save()
		if err != nil {
			t.Fatalf("failed to save: %v", err)
		}

		// Verify file exists
		statePath := filepath.Join(tmpDir, StateFileName)
		if _, err := os.Stat(statePath); os.IsNotExist(err) {
			t.Error("state file should exist")
		}

		// Load in new manager
		manager2 := NewFileManager(tmpDir)
		err = manager2.Load()
		if err != nil {
			t.Fatalf("failed to load: %v", err)
		}

		state := manager2.GetState()
		if state.LastAllocatedID != 2 {
			t.Errorf("expected LastAllocatedID 2, got %d", state.LastAllocatedID)
		}
		if state.CurrentFuzzingID != 2 {
			t.Errorf("expected CurrentFuzzingID 2, got %d", state.CurrentFuzzingID)
		}
		if state.TotalCoverage != 1500 {
			t.Errorf("expected TotalCoverage 1500, got %d", state.TotalCoverage)
		}
		if state.Stats.PoolSize != 10 {
			t.Errorf("expected PoolSize 10, got %d", state.Stats.PoolSize)
		}
		if state.Stats.ProcessedCount != 2 {
			t.Errorf("expected ProcessedCount 2, got %d", state.Stats.ProcessedCount)
		}
	})

	t.Run("should update coverage", func(t *testing.T) {
		tmpDir := t.TempDir()
		manager := NewFileManager(tmpDir)
		_ = manager.Load()

		manager.UpdateCoverage(2500)
		state := manager.GetState()

		if state.TotalCoverage != 2500 {
			t.Errorf("expected TotalCoverage 2500, got %d", state.TotalCoverage)
		}
	})
}
