package seed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	understandingFile = "understanding.md"
	sourceCFile       = "source.c"
	inputsFile        = "inputs.json"
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

// SaveSeed saves a single seed to a new subdirectory.
func SaveSeed(basePath string, s *Seed) error {
	seedDir := filepath.Join(basePath, s.ID)
	if err := os.MkdirAll(seedDir, 0755); err != nil {
		return fmt.Errorf("failed to create seed directory %s: %w", seedDir, err)
	}

	// Save C source file
	sourcePath := filepath.Join(seedDir, sourceCFile)
	if err := os.WriteFile(sourcePath, []byte(s.Content), 0644); err != nil {
		return fmt.Errorf("failed to write source file for seed %s: %w", s.ID, err)
	}

	// Save test cases as inputs.json
	inputsPath := filepath.Join(seedDir, inputsFile)
	inputsData, err := json.MarshalIndent(s.TestCases, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal test cases for seed %s: %w", s.ID, err)
	}
	if err := os.WriteFile(inputsPath, inputsData, 0644); err != nil {
		return fmt.Errorf("failed to write inputs file for seed %s: %w", s.ID, err)
	}

	return nil
}

// LoadSeeds scans a directory, loads all found seeds, and returns them in a Pool.
func LoadSeeds(basePath string) (Pool, error) {
	pool := NewInMemoryPool()
	entries, err := os.ReadDir(basePath)
	if err != nil {
		// It's not an error if the directory doesn't exist yet.
		if os.IsNotExist(err) {
			return pool, nil
		}
		return nil, fmt.Errorf("failed to read base path %s: %w", basePath, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		id := entry.Name()
		seedDir := filepath.Join(basePath, id)

		// Load C source file
		sourcePath := filepath.Join(seedDir, sourceCFile)
		content, err := os.ReadFile(sourcePath)
		if err != nil {
			// Could log this error instead of failing completely
			continue
		}

		// Load test cases from inputs.json
		inputsPath := filepath.Join(seedDir, inputsFile)
		inputsData, err := os.ReadFile(inputsPath)
		if err != nil {
			continue
		}

		var testCases []TestCase
		if err := json.Unmarshal(inputsData, &testCases); err != nil {
			continue
		}

		pool.Add(&Seed{
			ID:        id,
			Content:   string(content),
			TestCases: testCases,
		})
	}

	return pool, nil
}
