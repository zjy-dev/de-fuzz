package seed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const compilationRecordFile = "compile_command.json"

// CompilationRecord captures the actual compiler invocation for a seed.
type CompilationRecord struct {
	SeedID            uint64            `json:"seed_id"`
	RecordedAt        time.Time         `json:"recorded_at"`
	SourcePath        string            `json:"source_path"`
	BinaryPath        string            `json:"binary_path"`
	Success           bool              `json:"success"`
	CompilerPath      string            `json:"compiler_path"`
	Command           string            `json:"command"`
	Args              []string          `json:"args"`
	PrefixFlags       []string          `json:"prefix_flags"`
	ConfigCFlags      []string          `json:"config_cflags"`
	ProfileName       string            `json:"profile_name,omitempty"`
	ProfileFlags      []string          `json:"profile_flags,omitempty"`
	ProfileAxes       map[string]string `json:"profile_axes,omitempty"`
	IsNegativeControl bool              `json:"is_negative_control,omitempty"`
	SeedCFlags        []string          `json:"seed_cflags,omitempty"`
	LLMCFlags         []string          `json:"llm_cflags,omitempty"`
	AppliedLLMCFlags  []string          `json:"applied_llm_cflags,omitempty"`
	DroppedLLMCFlags  []string          `json:"dropped_llm_cflags,omitempty"`
	LLMCFlagsApplied  bool              `json:"llm_cflags_applied"`
	EffectiveFlags    []string          `json:"effective_flags"`
	Stdout            string            `json:"stdout,omitempty"`
	Stderr            string            `json:"stderr,omitempty"`
}

// GetCompilationRecordPath returns the path to compile_command.json for a seed directory.
func GetCompilationRecordPath(seedDir string) string {
	return filepath.Join(seedDir, compilationRecordFile)
}

// SaveCompilationRecord saves a compilation record alongside a persisted seed.
func SaveCompilationRecord(seedDir string, record *CompilationRecord) error {
	if record == nil {
		return fmt.Errorf("compilation record is nil")
	}

	if err := os.MkdirAll(seedDir, 0755); err != nil {
		return fmt.Errorf("failed to create seed directory %s: %w", seedDir, err)
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal compilation record: %w", err)
	}

	recordPath := GetCompilationRecordPath(seedDir)
	if err := os.WriteFile(recordPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write compilation record %s: %w", recordPath, err)
	}

	return nil
}

// LoadCompilationRecord loads compile_command.json from a seed directory.
func LoadCompilationRecord(seedDir string) (*CompilationRecord, error) {
	recordPath := GetCompilationRecordPath(seedDir)
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read compilation record %s: %w", recordPath, err)
	}

	var record CompilationRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("failed to unmarshal compilation record %s: %w", recordPath, err)
	}

	return &record, nil
}
