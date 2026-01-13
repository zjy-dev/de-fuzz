package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/zjy-dev/de-fuzz/internal/logger"
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
	OldCoverage uint64 // BB coverage before (basis points)
	NewCoverage uint64 // BB coverage after (basis points)

	// Oracle Results
	OracleVerdict  seed.OracleVerdict // Verdict from oracle analysis
	BugType        string             // Type of bug if detected
	BugDescription string             // Description of bug
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

	// Get retrieves a seed by ID from the processed seeds.
	// Returns nil if the seed is not found.
	Get(id uint64) (*seed.Seed, error)

	// Next retrieves the next seed to process from the queue.
	Next() (*seed.Seed, bool)

	// ReportResult updates a seed's metadata after fuzzing.
	ReportResult(id uint64, result FuzzResult) error

	// Len returns the number of seeds in the queue.
	Len() int

	// Save persists the current state to disk.
	Save() error

	// Finalize updates the global state when fuzzing completes.
	Finalize() error

	// UpdateTotalCoverage updates the total coverage in global state.
	UpdateTotalCoverage(coverageBasisPoints uint64)
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

	// Log recovery status for checkpoint/resume visibility
	totalSeeds := len(seeds)
	pendingCount := len(m.queue)
	processedCount := len(m.processed)

	if totalSeeds == 0 {
		logger.Info("[FRESH START] No seeds found in corpus, starting fresh")
	} else if processedCount == 0 && pendingCount > 0 {
		logger.Info("[FRESH START] Found %d initial seeds, no previous run detected", pendingCount)
	} else if pendingCount > 0 {
		logger.Info("[RESUME] Resuming from checkpoint: %d seeds in corpus (%d processed, %d pending)",
			totalSeeds, processedCount, pendingCount)
	} else {
		logger.Info("[RESUME] All %d seeds already processed, ready for constraint solving", totalSeeds)
	}

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

	// Set depth from parent if not already set
	if s.Meta.Depth == 0 && s.Meta.ParentID > 0 {
		if parent, ok := m.processed[s.Meta.ParentID]; ok {
			s.Meta.Depth = parent.Meta.Depth + 1
		} else {
			s.Meta.Depth = 1 // Default to 1 if parent not found
		}
	}

	// Save to disk
	_, err := seed.SaveSeedWithMetadata(m.corpusDir, s, m.namer)
	if err != nil {
		return fmt.Errorf("failed to save seed: %w", err)
	}

	// Save metadata JSON
	if err := seed.SaveMetadataJSON(m.metadataDir, &s.Meta); err != nil {
		// Log warning but don't fail
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

// Get retrieves a seed by ID from the processed seeds or queue.
// Returns nil if the seed is not found.
func (m *FileManager) Get(id uint64) (*seed.Seed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// First check processed map
	if s, ok := m.processed[id]; ok {
		return s, nil
	}

	// Also check queue (for seeds added but not yet processed)
	for _, s := range m.queue {
		if s.Meta.ID == id {
			return s, nil
		}
	}

	return nil, fmt.Errorf("seed %d not found in corpus", id)
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

	// Store old CovIncrease for rename check
	oldCovIncrease := s.Meta.CovIncrease
	oldPath := s.Meta.ContentPath

	// Update metadata with BB coverage in basis points
	s.Meta.State = result.State
	s.Meta.OldCoverage = result.OldCoverage
	s.Meta.NewCoverage = result.NewCoverage

	// Calculate coverage increase
	if result.NewCoverage > result.OldCoverage {
		s.Meta.CovIncrease = result.NewCoverage - result.OldCoverage
	} else {
		s.Meta.CovIncrease = 0
	}

	// Update oracle results
	s.Meta.OracleVerdict = result.OracleVerdict
	s.Meta.BugType = result.BugType
	s.Meta.BugDescription = result.BugDescription

	// Debug: Log the oracle verdict being saved
	logger.Debug("ReportResult: seed %d oracle_verdict=%q", id, s.Meta.OracleVerdict)

	// Rename seed directory if CovIncrease changed (fixes cov-00000 naming issue)
	// oldPath is the path to source.c, we need to rename the parent directory
	if s.Meta.CovIncrease != oldCovIncrease && oldPath != "" && s.Content != "" {
		oldDir := filepath.Dir(oldPath) // Get the seed directory (parent of source.c)
		newFilename := m.namer.GenerateFilename(&s.Meta, s.Content)
		// Remove .seed extension to get directory name
		newDirName := strings.TrimSuffix(newFilename, filepath.Ext(newFilename))
		newDir := filepath.Join(m.corpusDir, newDirName)
		if oldDir != newDir {
			if err := os.Rename(oldDir, newDir); err != nil {
				logger.Warn("Failed to rename seed directory from %s to %s: %v", oldDir, newDir, err)
			} else {
				// Update ContentPath to point to source.c in the new directory
				s.Meta.ContentPath = filepath.Join(newDir, "source.c")
				s.Meta.FilePath = newDirName
				logger.Debug("Renamed seed %d directory: %s -> %s", id, filepath.Base(oldDir), newDirName)
			}
		}
	}

	// Save metadata as JSON file (not .seed file)
	// This follows fuzzer-plan.md: metadata/ stores JSON files like id-000001.json
	if err := seed.SaveMetadataJSON(m.metadataDir, &s.Meta); err != nil {
		// Log warning but don't fail - metadata is optional
		// The seed is already saved in corpus directory
		logger.Warn("Failed to save metadata for seed %d: %v", id, err)
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

// Finalize updates the global state when fuzzing completes.
// It sets pool_size to 0 and current_fuzzing_id to 0.
func (m *FileManager) Finalize() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stateManager.UpdatePoolSize(0)
	m.stateManager.UpdateCurrentID(0)
	return m.stateManager.Save()
}

// UpdateTotalCoverage updates the total coverage in global state.
func (m *FileManager) UpdateTotalCoverage(coverageBasisPoints uint64) {
	m.stateManager.UpdateCoverage(coverageBasisPoints)
}

// GetStateManager returns the underlying state manager.
func (m *FileManager) GetStateManager() *state.FileManager {
	return m.stateManager
}

// GetCorpusDir returns the corpus directory path.
func (m *FileManager) GetCorpusDir() string {
	return m.corpusDir
}
