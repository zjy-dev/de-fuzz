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
// It saves the seed content to a separate source.c file and returns the generated filename.
// The metadata's ContentPath field will be updated to point to the source.c file.
func SaveSeedWithMetadata(dir string, s *Seed, namer NamingStrategy) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Generate filename using naming strategy
	filename := namer.GenerateFilename(&s.Meta, s.Content)

	// Create a subdirectory for this seed's files
	seedDir := filepath.Join(dir, strings.TrimSuffix(filename, filepath.Ext(filename)))
	if err := os.MkdirAll(seedDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create seed directory %s: %w", seedDir, err)
	}

	// Save source code to source.c
	sourceFile := filepath.Join(seedDir, "source.c")
	if err := os.WriteFile(sourceFile, []byte(s.Content), 0644); err != nil {
		return "", fmt.Errorf("failed to write source file %s: %w", sourceFile, err)
	}

	// Save test cases to testcases.json if they exist
	if len(s.TestCases) > 0 {
		jsonData, err := json.MarshalIndent(s.TestCases, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal test cases: %w", err)
		}
		testCasesFile := filepath.Join(seedDir, "testcases.json")
		if err := os.WriteFile(testCasesFile, jsonData, 0644); err != nil {
			return "", fmt.Errorf("failed to write test cases file %s: %w", testCasesFile, err)
		}
	}

	// Update metadata
	s.Meta.FilePath = filename
	s.Meta.ContentPath = sourceFile // Store absolute path to source.c
	s.Meta.FileSize = int64(len(s.Content))
	s.Meta.ContentHash = GenerateContentHash(s.Content)

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
		// New format: each seed is a directory
		if entry.IsDir() {
			seedDir := filepath.Join(dir, entry.Name())

			// Check if source.c exists
			sourceFile := filepath.Join(seedDir, "source.c")
			if _, err := os.Stat(sourceFile); err != nil {
				continue // Not a valid seed directory
			}

			// Read source code
			sourceBytes, err := os.ReadFile(sourceFile)
			if err != nil {
				continue
			}

			// Try to parse metadata from directory name
			meta, err := namer.ParseFilename(entry.Name() + ".seed")
			if err != nil {
				continue
			}

			// Read test cases if they exist
			var testCases []TestCase
			testCasesFile := filepath.Join(seedDir, "testcases.json")
			if data, err := os.ReadFile(testCasesFile); err == nil {
				json.Unmarshal(data, &testCases)
			}

			// Update metadata
			meta.FilePath = entry.Name()
			meta.ContentPath = sourceFile
			meta.FileSize = int64(len(sourceBytes))

			if meta.State == "" {
				meta.State = SeedStatePending
			}

			seeds = append(seeds, &Seed{
				Meta:      *meta,
				Content:   string(sourceBytes),
				TestCases: testCases,
			})
			continue
		}

		// Old format: single .seed file (for backward compatibility)
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

// SaveMetadataJSON saves the metadata as a JSON file.
// The filename is id-XXXXXX.json (e.g., id-000001.json).
func SaveMetadataJSON(dir string, meta *Metadata) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Generate filename: id-XXXXXX.json
	filename := fmt.Sprintf("id-%06d.json", meta.ID)
	filePath := filepath.Join(dir, filename)

	// Marshal metadata to JSON
	jsonData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write metadata file %s: %w", filePath, err)
	}

	return nil
}

// LoadMetadataJSON loads a metadata JSON file.
func LoadMetadataJSON(filePath string) (*Metadata, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata file %s: %w", filePath, err)
	}

	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &meta, nil
}

// LoadAllMetadataJSON loads all metadata JSON files from a directory.
func LoadAllMetadataJSON(dir string) ([]*Metadata, error) {
	var metas []*Metadata

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return metas, nil
		}
		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		if !strings.HasSuffix(filename, ".json") {
			continue
		}

		filePath := filepath.Join(dir, filename)
		meta, err := LoadMetadataJSON(filePath)
		if err != nil {
			// Skip invalid files
			continue
		}

		metas = append(metas, meta)
	}

	return metas, nil
}
