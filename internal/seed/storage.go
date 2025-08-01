package seed

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	understandingFile = "understanding.md"
	sourceCFile       = "source.c"
	sourceAsmFile     = "source.s"
	makefile          = "Makefile"
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
	seedDir := filepath.Join(basePath, fmt.Sprintf("%s_%s", s.ID, s.Type))
	if err := os.MkdirAll(seedDir, 0755); err != nil {
		return fmt.Errorf("failed to create seed directory %s: %w", seedDir, err)
	}

	var sourceFileName string
	switch s.Type {
	case SeedTypeC:
		sourceFileName = sourceCFile
	case SeedTypeCAsm:
		sourceFileName = sourceAsmFile
	case SeedTypeAsm:
		sourceFileName = sourceAsmFile
	default:
		return fmt.Errorf("unknown seed type: %s", s.Type)
	}

	sourcePath := filepath.Join(seedDir, sourceFileName)
	if err := os.WriteFile(sourcePath, []byte(s.Content), 0644); err != nil {
		return fmt.Errorf("failed to write source file for seed %s: %w", s.ID, err)
	}

	makefile_path := filepath.Join(seedDir, makefile)
	if err := os.WriteFile(makefile_path, []byte(s.Makefile), 0644); err != nil {
		return fmt.Errorf("failed to write makefile for seed %s: %w", s.ID, err)
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

		dirName := entry.Name()
		// Find the last underscore to separate ID from type
		lastUnderscore := strings.LastIndex(dirName, "_")
		if lastUnderscore == -1 {
			continue // Not a valid seed directory name
		}
		id := dirName[:lastUnderscore]
		seedType := dirName[lastUnderscore+1:]

		seedDir := filepath.Join(basePath, dirName)
		var sourceFileName string
		seedTypeEnum := SeedType(seedType)
		switch seedTypeEnum {
		case SeedTypeC:
			sourceFileName = sourceCFile
		case SeedTypeCAsm:
			sourceFileName = sourceAsmFile
		case SeedTypeAsm:
			sourceFileName = sourceAsmFile
		default:
			continue // Skip unknown types
		}

		sourcePath := filepath.Join(seedDir, sourceFileName)
		content, err := os.ReadFile(sourcePath)
		if err != nil {
			// Could log this error instead of failing completely
			continue
		}

		makefile_path := filepath.Join(seedDir, makefile)
		mf, err := os.ReadFile(makefile_path)
		if err != nil {
			continue
		}

		pool.Add(&Seed{
			ID:       id,
			Type:     seedTypeEnum,
			Content:  string(content),
			Makefile: string(mf),
		})
	}

	return pool, nil
}
