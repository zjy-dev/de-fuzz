package seed

import "time"

// SeedState represents the processing status of a seed.
type SeedState string

const (
	// SeedStatePending indicates the seed is in the queue, waiting to be fuzzed.
	SeedStatePending SeedState = "PENDING"
	// SeedStateProcessed indicates the seed has been fuzzed.
	SeedStateProcessed SeedState = "PROCESSED"
	// SeedStateCrash indicates the seed caused a crash.
	SeedStateCrash SeedState = "CRASH"
	// SeedStateTimeout indicates the seed caused a timeout.
	SeedStateTimeout SeedState = "TIMEOUT"
)

// Metadata contains all meta-information about a seed.
// This is used for lineage tracking, resume functionality, and coverage analysis.
type Metadata struct {
	// Basic Info
	ID        uint64    `json:"id"`         // Global unique ID, starts from 1
	FilePath  string    `json:"file_path"`  // Relative path in corpus directory
	FileSize  int64     `json:"file_size"`  // File size in bytes
	CreatedAt time.Time `json:"created_at"` // Creation timestamp

	// Lineage
	ParentID uint64 `json:"parent_id"` // Parent seed ID (0 for initial seeds)
	Depth    int    `json:"depth"`     // Mutation depth (0 for initial seeds)

	// State
	State SeedState `json:"state"` // Current processing state

	// Metrics (coverage in basis points, e.g., 12.34% = 1234)
	OldCoverage uint64 `json:"old_cov"`  // Coverage before this seed was compiled
	NewCoverage uint64 `json:"new_cov"`  // Coverage after this seed was compiled
	CovIncrease uint64 `json:"cov_incr"` // Coverage increase (new - old)
	ExecTimeUs  int64  `json:"exec_us"`  // Execution time in microseconds

	// ContentHash is an optional short hash (e.g., CRC32 or SHA1 prefix) for deduplication.
	ContentHash string `json:"content_hash,omitempty"`
}

// NewMetadata creates a new Metadata with the given ID and parent information.
func NewMetadata(id, parentID uint64, depth int) *Metadata {
	return &Metadata{
		ID:        id,
		ParentID:  parentID,
		Depth:     depth,
		State:     SeedStatePending,
		CreatedAt: time.Now(),
	}
}
