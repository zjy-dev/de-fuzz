package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	// StateFileName is the name of the global state file.
	StateFileName = "global_state.json"
)

// QueueStats holds simple statistics about the fuzzing queue.
type QueueStats struct {
	PoolSize       int `json:"pool_size"`
	ProcessedCount int `json:"processed_count"`
}

// GlobalState represents the persistent state of the fuzzing session.
// It is used for resume functionality and tracking overall progress.
type GlobalState struct {
	LastAllocatedID  uint64     `json:"last_allocated_id"`  // Next seed ID will be this + 1
	CurrentFuzzingID uint64     `json:"current_fuzzing_id"` // ID of the seed currently being fuzzed
	TotalCoverage    uint64     `json:"total_coverage"`     // Global coverage in basis points
	Stats            QueueStats `json:"queue_stats"`
}

// Manager handles the persistence and modification of the global state.
type Manager interface {
	// Load reads the state from disk.
	Load() error

	// Save writes the state to disk.
	Save() error

	// NextID increments and returns the next unique seed ID.
	NextID() uint64

	// UpdateCurrentID sets the ID currently being fuzzed.
	UpdateCurrentID(id uint64)

	// UpdateCoverage updates the global coverage metric.
	UpdateCoverage(newCov uint64)

	// IncrementProcessed increments the processed count.
	IncrementProcessed()

	// UpdatePoolSize sets the current pool size.
	UpdatePoolSize(size int)

	// GetState returns a copy of the current state.
	GetState() GlobalState
}

// FileManager is a file-backed implementation of the Manager interface.
type FileManager struct {
	mu       sync.Mutex
	filePath string
	state    GlobalState
}

// NewFileManager creates a new FileManager for the given directory.
// The state file will be stored at dir/global_state.json.
func NewFileManager(dir string) *FileManager {
	return &FileManager{
		filePath: filepath.Join(dir, StateFileName),
		state:    GlobalState{},
	}
}

// Load reads the state from disk.
// If the file doesn't exist, it initializes with default values.
func (m *FileManager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Initialize with default state
			m.state = GlobalState{
				LastAllocatedID:  0,
				CurrentFuzzingID: 0,
				TotalCoverage:    0,
				Stats: QueueStats{
					PoolSize:       0,
					ProcessedCount: 0,
				},
			}
			return nil
		}
		return fmt.Errorf("failed to read state file %s: %w", m.filePath, err)
	}

	if err := json.Unmarshal(data, &m.state); err != nil {
		return fmt.Errorf("failed to parse state file %s: %w", m.filePath, err)
	}

	return nil
}

// Save writes the state to disk.
func (m *FileManager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Ensure directory exists
	dir := filepath.Dir(m.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(m.filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file %s: %w", m.filePath, err)
	}

	return nil
}

// NextID increments and returns the next unique seed ID.
// IDs start from 1.
func (m *FileManager) NextID() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.LastAllocatedID++
	return m.state.LastAllocatedID
}

// UpdateCurrentID sets the ID currently being fuzzed.
func (m *FileManager) UpdateCurrentID(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.CurrentFuzzingID = id
}

// UpdateCoverage updates the global coverage metric.
func (m *FileManager) UpdateCoverage(newCov uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.TotalCoverage = newCov
}

// IncrementProcessed increments the processed count.
func (m *FileManager) IncrementProcessed() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.Stats.ProcessedCount++
}

// UpdatePoolSize sets the current pool size.
func (m *FileManager) UpdatePoolSize(size int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.Stats.PoolSize = size
}

// GetState returns a copy of the current state.
func (m *FileManager) GetState() GlobalState {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.state
}

// GetFilePath returns the path to the state file.
func (m *FileManager) GetFilePath() string {
	return m.filePath
}
