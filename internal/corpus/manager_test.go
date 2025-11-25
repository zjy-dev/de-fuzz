package corpus

import (
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

func TestFileManager(t *testing.T) {
	t.Run("should initialize directory structure", func(t *testing.T) {
		tmpDir := t.TempDir()
		manager := NewFileManager(tmpDir)

		err := manager.Initialize()
		if err != nil {
			t.Fatalf("failed to initialize: %v", err)
		}
	})

	t.Run("should add and retrieve seeds", func(t *testing.T) {
		tmpDir := t.TempDir()
		manager := NewFileManager(tmpDir)
		_ = manager.Initialize()

		// Add a seed
		s := &seed.Seed{
			Meta: seed.Metadata{
				ParentID: 0,
				Depth:    0,
			},
			Content: "int main() { return 0; }",
			TestCases: []seed.TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "0"},
			},
		}

		err := manager.Add(s)
		if err != nil {
			t.Fatalf("failed to add seed: %v", err)
		}

		// Check ID was assigned
		if s.Meta.ID != 1 {
			t.Errorf("expected ID 1, got %d", s.Meta.ID)
		}

		// Check queue length
		if manager.Len() != 1 {
			t.Errorf("expected queue length 1, got %d", manager.Len())
		}

		// Retrieve seed
		retrieved, ok := manager.Next()
		if !ok {
			t.Error("expected to retrieve seed")
		}

		if retrieved.Meta.ID != 1 {
			t.Errorf("expected ID 1, got %d", retrieved.Meta.ID)
		}

		// Queue should be empty now
		if manager.Len() != 0 {
			t.Errorf("expected queue length 0, got %d", manager.Len())
		}
	})

	t.Run("should recover from disk", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create and add seeds
		manager1 := NewFileManager(tmpDir)
		_ = manager1.Initialize()

		s1 := &seed.Seed{
			Meta:    seed.Metadata{ParentID: 0, Depth: 0},
			Content: "int main() { return 0; }",
			TestCases: []seed.TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "0"},
			},
		}
		s2 := &seed.Seed{
			Meta:    seed.Metadata{ParentID: 0, Depth: 0},
			Content: "int main() { return 1; }",
			TestCases: []seed.TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "1"},
			},
		}

		_ = manager1.Add(s1)
		_ = manager1.Add(s2)
		_ = manager1.Save()

		// Create new manager and recover
		manager2 := NewFileManager(tmpDir)
		err := manager2.Recover()
		if err != nil {
			t.Fatalf("failed to recover: %v", err)
		}

		// Should have 2 seeds in queue
		if manager2.Len() != 2 {
			t.Errorf("expected queue length 2, got %d", manager2.Len())
		}
	})

	t.Run("should report results", func(t *testing.T) {
		tmpDir := t.TempDir()
		manager := NewFileManager(tmpDir)
		_ = manager.Initialize()

		s := &seed.Seed{
			Meta:    seed.Metadata{ParentID: 0, Depth: 0},
			Content: "int main() { return 0; }",
			TestCases: []seed.TestCase{
				{RunningCommand: "./a.out", ExpectedResult: "0"},
			},
		}
		_ = manager.Add(s)

		// Get seed
		retrieved, _ := manager.Next()

		// Report result
		err := manager.ReportResult(retrieved.Meta.ID, FuzzResult{
			State:       seed.SeedStateProcessed,
			ExecTimeUs:  1000,
			NewCoverage: 2500,
		})
		if err != nil {
			t.Fatalf("failed to report result: %v", err)
		}

		// Check state was updated
		state := manager.GetStateManager().GetState()
		if state.Stats.ProcessedCount != 1 {
			t.Errorf("expected ProcessedCount 1, got %d", state.Stats.ProcessedCount)
		}
	})
}
