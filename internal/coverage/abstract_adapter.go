package coverage

import (
	"path/filepath"

	"github.com/zjy-dev/gcovr-json-util/v2/pkg/gcovr"
)

// ConvertGcovrUncoveredReport converts gcovr-json-util's UncoveredReport
// to the generic UncoveredInput format.
//
// Parameters:
//   - report: The gcovr UncoveredReport to convert
//   - sourceParentPath: The base path to prepend to relative file paths
//     (e.g., "/root/fuzz-coverage" for GCC source tree)
func ConvertGcovrUncoveredReport(report *gcovr.UncoveredReport, sourceParentPath string) *UncoveredInput {
	if report == nil {
		return &UncoveredInput{Files: []UncoveredFile{}}
	}

	input := &UncoveredInput{
		Files: make([]UncoveredFile, 0, len(report.Files)),
	}

	for _, gcovrFile := range report.Files {
		// Build absolute file path
		filePath := gcovrFile.FilePath
		if sourceParentPath != "" {
			filePath = filepath.Join(sourceParentPath, gcovrFile.FilePath)
		}

		uncoveredFile := UncoveredFile{
			FilePath:  filePath,
			Functions: make([]UncoveredFunction, 0, len(gcovrFile.UncoveredFunctions)),
		}

		for _, gcovrFunc := range gcovrFile.UncoveredFunctions {
			uncoveredFunc := UncoveredFunction{
				FunctionName:   gcovrFunc.FunctionName,
				DemangledName:  gcovrFunc.DemangledName,
				UncoveredLines: gcovrFunc.UncoveredLineNumbers,
				TotalLines:     gcovrFunc.TotalLines,
				CoveredLines:   gcovrFunc.CoveredLines,
			}
			uncoveredFile.Functions = append(uncoveredFile.Functions, uncoveredFunc)
		}

		input.Files = append(input.Files, uncoveredFile)
	}

	return input
}
