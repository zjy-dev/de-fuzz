package coverage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// LineID uniquely identifies a line of code.
type LineID struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// String returns a string representation of LineID for use as map keys.
func (l LineID) String() string {
	return fmt.Sprintf("%s:%d", l.File, l.Line)
}

// CoverageMapping maintains the mapping between source lines and the first seed that covered them.
// It supports persistence to JSON for checkpoint/resume capability.
type CoverageMapping struct {
	mu sync.RWMutex

	// LineToSeed maps each covered line to the ID of the first seed that covered it
	LineToSeed map[string]int64 `json:"line_to_seed"`

	// path is the file path for persistence
	path string
}

// NewCoverageMapping creates a new CoverageMapping instance.
// If path is provided and the file exists, it will be loaded.
func NewCoverageMapping(path string) (*CoverageMapping, error) {
	cm := &CoverageMapping{
		LineToSeed: make(map[string]int64),
		path:       path,
	}

	// Try to load existing mapping if file exists
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if err := cm.Load(path); err != nil {
				return nil, fmt.Errorf("failed to load existing mapping: %w", err)
			}
		}
	}

	return cm, nil
}

// RecordLine records that a specific line was first covered by the given seed.
// Returns true if this is a new line (not previously covered).
func (cm *CoverageMapping) RecordLine(line LineID, seedID int64) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	key := line.String()
	if _, exists := cm.LineToSeed[key]; exists {
		return false // Already covered
	}

	cm.LineToSeed[key] = seedID
	return true
}

// RecordLines records multiple lines as covered by the given seed.
// Returns the count of newly covered lines.
func (cm *CoverageMapping) RecordLines(lines []LineID, seedID int64) int {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	newCount := 0
	for _, line := range lines {
		key := line.String()
		if _, exists := cm.LineToSeed[key]; !exists {
			cm.LineToSeed[key] = seedID
			newCount++
		}
	}
	return newCount
}

// GetSeedForLine returns the ID of the first seed that covered the given line.
// Returns (seedID, true) if found, (0, false) otherwise.
func (cm *CoverageMapping) GetSeedForLine(line LineID) (int64, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	seedID, exists := cm.LineToSeed[line.String()]
	return seedID, exists
}

// IsCovered returns true if the given line has been covered.
func (cm *CoverageMapping) IsCovered(line LineID) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	_, exists := cm.LineToSeed[line.String()]
	return exists
}

// GetCoveredLines returns a set of all covered lines.
func (cm *CoverageMapping) GetCoveredLines() map[LineID]bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make(map[LineID]bool, len(cm.LineToSeed))
	for key := range cm.LineToSeed {
		// Parse key back to LineID
		var file string
		var line int
		if _, err := fmt.Sscanf(key, "%s", &file); err == nil {
			// Find the last colon to separate file from line
			for i := len(key) - 1; i >= 0; i-- {
				if key[i] == ':' {
					file = key[:i]
					fmt.Sscanf(key[i+1:], "%d", &line)
					break
				}
			}
			result[LineID{File: file, Line: line}] = true
		}
	}
	return result
}

// GetCoveredLinesForFile returns all covered lines in the specified file.
func (cm *CoverageMapping) GetCoveredLinesForFile(file string) []int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var lines []int
	prefix := file + ":"
	for key := range cm.LineToSeed {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			var line int
			fmt.Sscanf(key[len(prefix):], "%d", &line)
			lines = append(lines, line)
		}
	}
	return lines
}

// TotalCoveredLines returns the total number of covered lines.
func (cm *CoverageMapping) TotalCoveredLines() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return len(cm.LineToSeed)
}

// Save persists the mapping to disk.
func (cm *CoverageMapping) Save(path string) error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if path == "" {
		path = cm.path
	}
	if path == "" {
		return fmt.Errorf("no path specified for saving")
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, err := json.MarshalIndent(cm, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal mapping: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write mapping file: %w", err)
	}

	return nil
}

// Load loads the mapping from disk.
func (cm *CoverageMapping) Load(path string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read mapping file: %w", err)
	}

	if err := json.Unmarshal(data, cm); err != nil {
		return fmt.Errorf("failed to unmarshal mapping: %w", err)
	}

	cm.path = path
	return nil
}

// FindClosestCoveredLine finds the covered line that is closest to (and before) the target line
// within the same file. Returns (lineID, seedID, true) if found, or zero values and false if not.
func (cm *CoverageMapping) FindClosestCoveredLine(file string, targetLine int) (LineID, int64, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	closestLine := -1
	var closestSeedID int64

	prefix := file + ":"
	for key, seedID := range cm.LineToSeed {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			var line int
			fmt.Sscanf(key[len(prefix):], "%d", &line)

			// Find the closest line that is before or equal to targetLine
			if line <= targetLine && line > closestLine {
				closestLine = line
				closestSeedID = seedID
			}
		}
	}

	if closestLine == -1 {
		return LineID{}, 0, false
	}

	return LineID{File: file, Line: closestLine}, closestSeedID, true
}
