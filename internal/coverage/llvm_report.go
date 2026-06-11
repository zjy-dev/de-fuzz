package coverage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// LLVMReport represents an llvm-cov export JSON coverage report.
// Like GcovrReport, it stores only the path to the report file.
type LLVMReport struct {
	path string // Path to the llvm-cov export JSON file
}

// ToBytes reads and returns the JSON report data from the file.
func (r *LLVMReport) ToBytes() ([]byte, error) {
	if r.path == "" {
		return nil, fmt.Errorf("report path is empty")
	}

	data, err := os.ReadFile(r.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read report file %s: %w", r.path, err)
	}

	return data, nil
}

// llvmCovExport mirrors the subset of `llvm-cov export -format=text` JSON we need.
type llvmCovExport struct {
	Version string         `json:"version"`
	Type    string         `json:"type"`
	Data    []llvmCovDatum `json:"data"`
}

type llvmCovDatum struct {
	Files     []llvmCovFile     `json:"files"`
	Functions []llvmCovFunction `json:"functions"`
}

type llvmCovFile struct {
	Filename string              `json:"filename"`
	Segments [][]json.RawMessage `json:"segments"`
}

type llvmCovFunction struct {
	Name      string              `json:"name"`
	Count     int64               `json:"count"`
	Filenames []string            `json:"filenames"`
	Regions   [][]json.RawMessage `json:"regions"`
}

// llvmCovExportType is the required value of the JSON "type" field, used to guard
// against accidentally parsing a gcovr JSON report.
const llvmCovExportType = "llvm.coverage.json.export"

// parseLLVMCovExport parses an llvm-cov export JSON file and validates its type.
func parseLLVMCovExport(path string) (*llvmCovExport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read llvm-cov report %s: %w", path, err)
	}

	var export llvmCovExport
	if err := json.Unmarshal(data, &export); err != nil {
		return nil, fmt.Errorf("failed to parse llvm-cov report %s: %w", path, err)
	}

	if export.Type != llvmCovExportType {
		return nil, fmt.Errorf("unexpected coverage report type %q (want %q): %s",
			export.Type, llvmCovExportType, path)
	}

	return &export, nil
}

// jsonNumberToInt64 decodes a JSON number (which may be float-encoded) to int64.
func jsonNumberToInt64(raw json.RawMessage) (int64, bool) {
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return int64(f), true
}

// jsonToBool decodes a segment flag that may be encoded as bool or 0/1.
func jsonToBool(raw json.RawMessage) bool {
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b
	}
	if n, ok := jsonNumberToInt64(raw); ok {
		return n != 0
	}
	return false
}

// coveredLinesFromExport extracts the set of covered "file:line" identifiers from
// a parsed llvm-cov export. A segment tuple is
// [Line, Col, Count, HasCount, IsRegionEntry, IsGapRegion]; a line is covered when
// HasCount && IsRegionEntry && Count > 0.
func coveredLinesFromExport(export *llvmCovExport) map[string][]int {
	result := make(map[string][]int)
	seen := make(map[string]map[int]bool)

	add := func(file string, line int) {
		if seen[file] == nil {
			seen[file] = make(map[int]bool)
		}
		if seen[file][line] {
			return
		}
		seen[file][line] = true
		result[file] = append(result[file], line)
	}

	for _, datum := range export.Data {
		for _, file := range datum.Files {
			for _, seg := range file.Segments {
				if len(seg) < 5 {
					continue
				}
				line, ok := jsonNumberToInt64(seg[0])
				if !ok {
					continue
				}
				count, ok := jsonNumberToInt64(seg[2])
				if !ok {
					continue
				}
				hasCount := jsonToBool(seg[3])
				isRegionEntry := jsonToBool(seg[4])
				if hasCount && isRegionEntry && count > 0 {
					add(file.Filename, int(line))
				}
			}
		}
	}

	for file := range result {
		sort.Ints(result[file])
	}
	return result
}

// llvmTotalReport is the canonical on-disk representation of accumulated LLVM
// coverage. Unlike gcovr, llvm-cov has no "merge two reports" command, so the
// total is stored as the union of covered lines and merged in Go.
type llvmTotalReport struct {
	Type         string           `json:"type"`
	CoveredLines map[string][]int `json:"covered_lines"`
}

const llvmTotalReportType = "defuzz.llvm.coverage.total"

// loadLLVMTotal loads the canonical total report from disk.
func loadLLVMTotal(path string) (*llvmTotalReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var total llvmTotalReport
	if err := json.Unmarshal(data, &total); err != nil {
		return nil, fmt.Errorf("total report is not valid JSON: %w", err)
	}
	if total.CoveredLines == nil {
		total.CoveredLines = make(map[string][]int)
	}
	return &total, nil
}

// coveredLineSet converts the map representation to a set keyed by "file:line".
func coveredLineSet(lines map[string][]int) map[string]bool {
	set := make(map[string]bool)
	for file, nums := range lines {
		for _, n := range nums {
			set[fmt.Sprintf("%s:%d", file, n)] = true
		}
	}
	return set
}

// writeLLVMTotal writes the canonical total report to disk.
func writeLLVMTotal(path string, total *llvmTotalReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create directory for total report: %w", err)
	}
	total.Type = llvmTotalReportType
	data, err := json.MarshalIndent(total, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal total report: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write total report: %w", err)
	}
	return nil
}
