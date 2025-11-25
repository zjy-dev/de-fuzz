package seed

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strconv"
)

// NamingStrategy defines how seed filenames are generated and parsed.
// The filename format is: id-[ID]-src-[ParentID]-cov-[CovIncrement]-[Hash].seed
// Example: id-000123-src-000042-cov-00132-a1b2c3d4.seed
type NamingStrategy interface {
	// GenerateFilename creates the filename string based on metadata and content.
	GenerateFilename(meta *Metadata, content string) string

	// ParseFilename extracts metadata from a filename.
	// Returns partial metadata (ID, ParentID, CovIncrease, ContentHash).
	ParseFilename(filename string) (*Metadata, error)
}

// DefaultNamingStrategy implements the naming format defined in fuzzer-plan.md.
// Format: id-[ID]-src-[ParentID]-cov-[CovIncrement]-[Hash].seed
type DefaultNamingStrategy struct{}

// NewDefaultNamingStrategy creates a new DefaultNamingStrategy.
func NewDefaultNamingStrategy() *DefaultNamingStrategy {
	return &DefaultNamingStrategy{}
}

// filenameRegex matches: id-000123-src-000042-cov-00132-a1b2c3d4.seed
var filenameRegex = regexp.MustCompile(`^id-(\d{6})-src-(\d{6})-cov-(\d{5})-([a-f0-9]{8})\.seed$`)

// GenerateFilename creates the filename string based on metadata and content.
func (s *DefaultNamingStrategy) GenerateFilename(meta *Metadata, content string) string {
	hash := generateContentHash(content)
	return fmt.Sprintf("id-%06d-src-%06d-cov-%05d-%s.seed",
		meta.ID,
		meta.ParentID,
		meta.CovIncrease,
		hash,
	)
}

// ParseFilename extracts metadata from a filename.
func (s *DefaultNamingStrategy) ParseFilename(filename string) (*Metadata, error) {
	matches := filenameRegex.FindStringSubmatch(filename)
	if matches == nil {
		return nil, fmt.Errorf("filename does not match expected format: %s", filename)
	}

	id, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ID: %w", err)
	}

	parentID, err := strconv.ParseUint(matches[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ParentID: %w", err)
	}

	covIncrease, err := strconv.ParseUint(matches[3], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CovIncrease: %w", err)
	}

	return &Metadata{
		ID:          id,
		ParentID:    parentID,
		CovIncrease: covIncrease,
		ContentHash: matches[4],
	}, nil
}

// generateContentHash creates an 8-character hex hash from content.
func generateContentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%08x", h[:4]) // First 4 bytes = 8 hex chars
}

// GenerateContentHash is a public helper to compute content hash.
func GenerateContentHash(content string) string {
	return generateContentHash(content)
}
