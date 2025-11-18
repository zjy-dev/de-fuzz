package coverage

// UncoveredInput represents the input for code abstraction.
// This is a generic structure that can be populated from different sources
// (e.g., gcovr reports, LLVM coverage reports, or custom analysis).
type UncoveredInput struct {
	Files []UncoveredFile
}

// UncoveredFile represents a single source file with uncovered functions.
type UncoveredFile struct {
	// FilePath is the absolute path to the source file.
	FilePath string

	// Functions contains all uncovered functions in this file.
	Functions []UncoveredFunction
}

// UncoveredFunction represents a single function with uncovered lines.
type UncoveredFunction struct {
	// FunctionName is the function identifier (e.g., mangled name in C++).
	FunctionName string

	// DemangledName is the human-readable function name (optional).
	// If empty, FunctionName will be used for matching.
	DemangledName string

	// UncoveredLines contains the line numbers that are not covered.
	// Line numbers are 1-indexed.
	UncoveredLines []int

	// TotalLines is the total number of executable lines in the function.
	TotalLines int

	// CoveredLines is the number of covered lines in the function.
	CoveredLines int
}

// AbstractedFunction represents the result of code abstraction for a single function.
type AbstractedFunction struct {
	// FilePath is the source file path.
	FilePath string

	// FunctionName is the function identifier.
	FunctionName string

	// DemangledName is the human-readable function name.
	DemangledName string

	// AbstractedCode is the abstracted source code with:
	// - Full path to uncovered code preserved
	// - Other branches pruned to top-level summary
	AbstractedCode string

	// UncoveredLines contains the original uncovered line numbers.
	UncoveredLines []int

	// Error contains any error that occurred during abstraction.
	Error error
}

// AbstractedOutput represents the complete output of code abstraction.
type AbstractedOutput struct {
	Functions []AbstractedFunction
}

// CodeAbstractor defines the interface for abstracting source code based on coverage.
// Different implementations can be created for different languages (C/C++, Go, Rust, etc.).
type CodeAbstractor interface {
	// Abstract processes the uncovered input and generates abstracted code for each function.
	// It returns an AbstractedOutput containing all successfully abstracted functions,
	// with individual errors stored in each AbstractedFunction.Error field.
	Abstract(input *UncoveredInput) (*AbstractedOutput, error)

	// SupportedLanguages returns the file extensions this abstractor supports.
	// For example: []string{".c", ".cpp", ".cc", ".cxx", ".h", ".hpp"}
	SupportedLanguages() []string
}

// AbstractorRegistry manages different CodeAbstractor implementations.
type AbstractorRegistry struct {
	abstractors map[string]CodeAbstractor // key: file extension
}

// NewAbstractorRegistry creates a new registry for code abstractors.
func NewAbstractorRegistry() *AbstractorRegistry {
	return &AbstractorRegistry{
		abstractors: make(map[string]CodeAbstractor),
	}
}

// Register adds a CodeAbstractor to the registry.
// It registers the abstractor for all file extensions it supports.
func (r *AbstractorRegistry) Register(abstractor CodeAbstractor) {
	for _, ext := range abstractor.SupportedLanguages() {
		r.abstractors[ext] = abstractor
	}
}

// GetAbstractor returns the appropriate abstractor for a given file extension.
// Returns nil if no abstractor is registered for the extension.
func (r *AbstractorRegistry) GetAbstractor(fileExt string) CodeAbstractor {
	return r.abstractors[fileExt]
}

// AbstractAll processes all files in the input using the appropriate abstractors.
// It automatically selects the right abstractor based on file extension.
func (r *AbstractorRegistry) AbstractAll(input *UncoveredInput) (*AbstractedOutput, error) {
	output := &AbstractedOutput{
		Functions: make([]AbstractedFunction, 0),
	}

	// Group files by extension
	filesByExt := make(map[string]*UncoveredInput)
	for _, file := range input.Files {
		// Determine file extension
		ext := getFileExtension(file.FilePath)
		if ext == "" {
			// Skip files without extension
			continue
		}

		if filesByExt[ext] == nil {
			filesByExt[ext] = &UncoveredInput{
				Files: make([]UncoveredFile, 0),
			}
		}
		filesByExt[ext].Files = append(filesByExt[ext].Files, file)
	}

	// Process each group with its appropriate abstractor
	for ext, extInput := range filesByExt {
		abstractor := r.GetAbstractor(ext)
		if abstractor == nil {
			// No abstractor for this extension, skip
			// Could add warning here if needed
			continue
		}

		extOutput, err := abstractor.Abstract(extInput)
		if err != nil {
			// Continue processing other extensions even if one fails
			continue
		}

		output.Functions = append(output.Functions, extOutput.Functions...)
	}

	return output, nil
}

// getFileExtension extracts the file extension from a file path.
// Returns empty string if no extension is found.
func getFileExtension(filePath string) string {
	for i := len(filePath) - 1; i >= 0; i-- {
		if filePath[i] == '.' {
			return filePath[i:]
		}
		if filePath[i] == '/' || filePath[i] == '\\' {
			// Reached directory separator without finding extension
			return ""
		}
	}
	return ""
}
