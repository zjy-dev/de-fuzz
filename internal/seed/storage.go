package seed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	understandingFile = "understanding.md"
	// Separator defines the boundary between C source code and JSON test cases.
	// Exported for use by other packages.
	Separator = "\n// ||||| JSON_TESTCASES_START |||||\n"
	// separator is kept for backward compatibility within this package.
	separator = Separator
)

// GetUnderstandingPath returns the full path to the understanding.md file.
func GetUnderstandingPath(basePath string) string {
	return filepath.Join(basePath, understandingFile)
}

// SaveUnderstanding saves the LLM's understanding to a file.
func SaveUnderstanding(basePath, content string) error {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return fmt.Errorf("failed to create base path %s: %w", basePath, err)
	}
	filePath := GetUnderstandingPath(basePath)
	return os.WriteFile(filePath, []byte(content), 0644)
}

// LoadUnderstanding loads the LLM's understanding from a file.
func LoadUnderstanding(basePath string) (string, error) {
	filePath := GetUnderstandingPath(basePath)
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read understanding file %s: %w", filePath, err)
	}
	return string(content), nil
}

// SaveSeed saves a single seed to a single file (legacy format without metadata).
// Prefer SaveSeedWithMetadata for new code.
// The file format is:
// <C Source Code>
// // ||||| JSON_TESTCASES_START |||||
// <JSON Test Cases>
func SaveSeed(basePath string, s *Seed) error {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return fmt.Errorf("failed to create base path %s: %w", basePath, err)
	}

	// Marshal test cases to JSON
	jsonData, err := json.MarshalIndent(s.TestCases, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal test cases for seed %s: %w", s.ID, err)
	}

	// Combine content
	fullContent := s.Content + separator + string(jsonData)

	// Save to file with .seed extension
	filename := s.ID
	if !strings.HasSuffix(filename, ".seed") {
		filename += ".seed"
	}
	filePath := filepath.Join(basePath, filename)

	if err := os.WriteFile(filePath, []byte(fullContent), 0644); err != nil {
		return fmt.Errorf("failed to write seed file %s: %w", filePath, err)
	}

	return nil
}

// SaveSeedWithMetadata saves a seed using the specified naming strategy.
// It returns the generated filename.
func SaveSeedWithMetadata(dir string, s *Seed, namer NamingStrategy) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Marshal test cases to JSON
	jsonData, err := json.MarshalIndent(s.TestCases, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal test cases: %w", err)
	}

	// Combine content
	fullContent := s.Content + Separator + string(jsonData)

	// Generate filename using naming strategy
	filename := namer.GenerateFilename(&s.Meta, s.Content)

	// Update metadata
	s.Meta.FilePath = filename
	s.Meta.FileSize = int64(len(fullContent))
	s.Meta.ContentHash = GenerateContentHash(s.Content)

	filePath := filepath.Join(dir, filename)
	if err := os.WriteFile(filePath, []byte(fullContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write seed file %s: %w", filePath, err)
	}

	return filename, nil
}

// LoadSeedWithMetadata loads a single seed file and parses its metadata from the filename.
func LoadSeedWithMetadata(filePath string, namer NamingStrategy) (*Seed, error) {
	filename := filepath.Base(filePath)

	// Parse metadata from filename
	meta, err := namer.ParseFilename(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to parse filename %s: %w", filename, err)
	}

	// Read file content
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	content := string(contentBytes)

	// Split content by separator
	parts := strings.Split(content, Separator)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid seed file format: %s", filename)
	}

	sourceCode := parts[0]
	jsonContent := parts[1]

	var testCases []TestCase
	if err := json.Unmarshal([]byte(jsonContent), &testCases); err != nil {
		return nil, fmt.Errorf("failed to unmarshal test cases: %w", err)
	}

	// Get file info for size
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file %s: %w", filePath, err)
	}

	meta.FilePath = filename
	meta.FileSize = info.Size()

	// Set default state to PENDING if not set (filename doesn't contain state)
	if meta.State == "" {
		meta.State = SeedStatePending
	}

	return &Seed{
		Meta:      *meta,
		Content:   sourceCode,
		TestCases: testCases,
	}, nil
}

// LoadSeedsWithMetadata scans a directory and loads all seeds with their metadata.
func LoadSeedsWithMetadata(dir string, namer NamingStrategy) ([]*Seed, error) {
	var seeds []*Seed

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return seeds, nil
		}
		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		if !strings.HasSuffix(filename, ".seed") {
			continue
		}

		filePath := filepath.Join(dir, filename)
		seed, err := LoadSeedWithMetadata(filePath, namer)
		if err != nil {
			// Skip invalid files, could log warning
			continue
		}

		seeds = append(seeds, seed)
	}

	return seeds, nil
}
