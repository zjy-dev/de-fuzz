package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/zjy-dev/de-fuzz/internal/seed"
	"github.com/zjy-dev/de-fuzz/internal/state"
)

const (
	// CorpusDir is the subdirectory for seed files.
	CorpusDir = "corpus"
	// MetadataDir is the subdirectory for metadata files (optional).
	MetadataDir = "metadata"
	// StateDir is the subdirectory for global state.
	StateDir = "state"
)

// FuzzResult contains the outcome of a fuzzing iteration.
type FuzzResult struct {
	State       seed.SeedState
	ExecTimeUs  int64
	NewCoverage uint64
}

// Manager manages the lifecycle of seeds on disk and in memory.
type Manager interface {
	// Initialize prepares the directory structure.
	Initialize() error

	// Recover scans the corpus directory to rebuild the in-memory queue
	// and restore the GlobalState if necessary.
	Recover() error

	// Add persists a new seed to disk and adds it to the processing queue.
	// It handles ID allocation via the State Manager.
	Add(s *seed.Seed) error

	// AllocateID allocates and returns the next unique seed ID without persisting.
	// Use this to pre-assign an ID to a seed before compilation.
	AllocateID() uint64

	// Next retrieves the next seed to process from the queue.
	Next() (*seed.Seed, bool)

	// ReportResult updates a seed's metadata after fuzzing.
	ReportResult(id uint64, result FuzzResult) error

	// Len returns the number of seeds in the queue.
	Len() int

	// Save persists the current state to disk.
	Save() error
}

// FileManager is a file-backed implementation of the corpus Manager.
type FileManager struct {
	mu           sync.Mutex
	baseDir      string
	corpusDir    string
	metadataDir  string
	stateDir     string
	stateManager *state.FileManager
	namer        seed.NamingStrategy
	queue        []*seed.Seed          // Seeds waiting to be processed
	processed    map[uint64]*seed.Seed // Seeds that have been processed
}

// NewFileManager creates a new corpus FileManager.
func NewFileManager(baseDir string) *FileManager {
	stateDir := filepath.Join(baseDir, StateDir)
	return &FileManager{
		baseDir:      baseDir,
		corpusDir:    filepath.Join(baseDir, CorpusDir),
		metadataDir:  filepath.Join(baseDir, MetadataDir),
		stateDir:     stateDir,
		stateManager: state.NewFileManager(stateDir),
		namer:        seed.NewDefaultNamingStrategy(),
		queue:        make([]*seed.Seed, 0),
		processed:    make(map[uint64]*seed.Seed),
	}
}

// Initialize prepares the directory structure.
func (m *FileManager) Initialize() error {
	dirs := []string{m.corpusDir, m.metadataDir, m.stateDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Load or initialize state
	if err := m.stateManager.Load(); err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}

	return nil
}

// Recover scans the corpus directory to rebuild the in-memory queue.
func (m *FileManager) Recover() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Load state first
	if err := m.stateManager.Load(); err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}

	// Load all seeds from corpus
	seeds, err := seed.LoadSeedsWithMetadata(m.corpusDir, m.namer)
	if err != nil {
		return fmt.Errorf("failed to load seeds: %w", err)
	}

	// Separate pending and processed seeds
	m.queue = make([]*seed.Seed, 0)
	m.processed = make(map[uint64]*seed.Seed)

	for _, s := range seeds {
		switch s.Meta.State {
		case seed.SeedStatePending:
			m.queue = append(m.queue, s)
		default:
			m.processed[s.Meta.ID] = s
		}
	}

	// Sort queue by ID (FIFO based on creation order)
	sort.Slice(m.queue, func(i, j int) bool {
		return m.queue[i].Meta.ID < m.queue[j].Meta.ID
	})

	// Update pool size in state
	m.stateManager.UpdatePoolSize(len(m.queue))

	return nil
}

// Add persists a new seed to disk and adds it to the processing queue.
func (m *FileManager) Add(s *seed.Seed) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Allocate new ID if not set
	if s.Meta.ID == 0 {
		s.Meta.ID = m.stateManager.NextID()
	}

	// Ensure state is pending
	s.Meta.State = seed.SeedStatePending

	// Save to disk
	_, err := seed.SaveSeedWithMetadata(m.corpusDir, s, m.namer)
	if err != nil {
		return fmt.Errorf("failed to save seed: %w", err)
	}

	// Add to queue
	m.queue = append(m.queue, s)
	m.stateManager.UpdatePoolSize(len(m.queue))

	return nil
}

// AllocateID allocates and returns the next unique seed ID without persisting.
// This allows pre-assigning an ID to a seed before compilation.
func (m *FileManager) AllocateID() uint64 {
	return m.stateManager.NextID()
}

// Next retrieves the next seed to process from the queue.
// Returns false if the queue is empty.
func (m *FileManager) Next() (*seed.Seed, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.queue) == 0 {
		return nil, false
	}

	// Pop from front (FIFO)
	s := m.queue[0]
	m.queue = m.queue[1:]

	// Update state
	m.stateManager.UpdateCurrentID(s.Meta.ID)
	m.stateManager.UpdatePoolSize(len(m.queue))

	// Store in processed map so ReportResult can find it
	m.processed[s.Meta.ID] = s

	return s, true
}

// ReportResult updates a seed's metadata after fuzzing.
func (m *FileManager) ReportResult(id uint64, result FuzzResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the seed (should be in processed map from Next())
	s, ok := m.processed[id]
	if !ok {
		// Seed not found in processed map - this shouldn't happen
		// but create a placeholder for state tracking
		s = &seed.Seed{
			Meta: seed.Metadata{ID: id},
		}
		m.processed[id] = s
	}

	// Update metadata
	s.Meta.State = result.State
	s.Meta.ExecTimeUs = result.ExecTimeUs
	s.Meta.NewCoverage = result.NewCoverage

	// Calculate coverage increase
	if result.NewCoverage > s.Meta.OldCoverage {
		s.Meta.CovIncrease = result.NewCoverage - s.Meta.OldCoverage
	}

	// Save metadata as JSON file (not .seed file)
	// This follows fuzzer-plan.md: metadata/ stores JSON files like id-000001.json
	if err := seed.SaveMetadataJSON(m.metadataDir, &s.Meta); err != nil {
		// Log warning but don't fail - metadata is optional
		// The seed is already saved in corpus directory
	}

	// Update global state
	m.stateManager.IncrementProcessed()
	if result.NewCoverage > m.stateManager.GetState().TotalCoverage {
		m.stateManager.UpdateCoverage(result.NewCoverage)
	}

	return nil
}

// Len returns the number of seeds in the queue.
func (m *FileManager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue)
}

// Save persists the current state to disk.
func (m *FileManager) Save() error {
	return m.stateManager.Save()
}

// GetStateManager returns the underlying state manager.
func (m *FileManager) GetStateManager() *state.FileManager {
	return m.stateManager
}

// GetCorpusDir returns the corpus directory path.
func (m *FileManager) GetCorpusDir() string {
	return m.corpusDir
}
