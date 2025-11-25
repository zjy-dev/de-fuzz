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
	// separator defines the boundary between C source code and JSON test cases
	separator = "\n// ||||| JSON_TESTCASES_START |||||\n"
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

// SaveSeed saves a single seed to a single file.
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

	// Save to file with .c extension
	filename := s.ID
	if !strings.HasSuffix(filename, ".c") {
		filename += ".c"
	}
	filePath := filepath.Join(basePath, filename)

	if err := os.WriteFile(filePath, []byte(fullContent), 0644); err != nil {
		return fmt.Errorf("failed to write seed file %s: %w", filePath, err)
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
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		// Only process .c files
		if !strings.HasSuffix(filename, ".c") {
			continue
		}

		id := strings.TrimSuffix(filename, ".c")
		filePath := filepath.Join(basePath, filename)

		contentBytes, err := os.ReadFile(filePath)
		if err != nil {
			// Could log this error instead of failing completely
			continue
		}
		content := string(contentBytes)

		// Split content by separator
		parts := strings.Split(content, separator)
		if len(parts) != 2 {
			// Invalid format, skip
			// fmt.Printf("Warning: invalid seed file format: %s\n", filename)
			continue
		}

		sourceCode := parts[0]
		jsonContent := parts[1]

		var testCases []TestCase
		if err := json.Unmarshal([]byte(jsonContent), &testCases); err != nil {
			// fmt.Printf("Warning: failed to unmarshal test cases for %s: %v\n", filename, err)
			continue
		}

		pool.Add(&Seed{
			ID:        id,
			Content:   sourceCode,
			TestCases: testCases,
		})
	}

	return pool, nil
}
