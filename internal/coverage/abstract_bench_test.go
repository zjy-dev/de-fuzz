package coverage

import (
	"path/filepath"
	"testing"
)

// BenchmarkCppAbstractor_ShortFile benchmarks abstraction on a short file (50 lines).
func BenchmarkCppAbstractor_ShortFile(b *testing.B) {
	shortFile := filepath.Join("artifacts", "short.cc")

	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath: shortFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "f",
						UncoveredLines: []int{40, 41, 47, 48},
						TotalLines:     45,
						CoveredLines:   41,
					},
				},
			},
		},
	}

	abstractor := NewCppAbstractor()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := abstractor.Abstract(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCppAbstractor_LongFile_1Function4Lines benchmarks abstraction on a long file (2596 lines)
// with 1 function containing 4 uncovered lines.
func BenchmarkCppAbstractor_LongFile_1Function4Lines(b *testing.B) {
	longFile := filepath.Join("artifacts", "long.cc")

	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath: longFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "analyze_functions",
						UncoveredLines: []int{950, 951, 1000, 1001}, // Example lines
						TotalLines:     200,
						CoveredLines:   196,
					},
				},
			},
		},
	}

	abstractor := NewCppAbstractor()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := abstractor.Abstract(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCppAbstractor_LongFile_10Functions100Lines benchmarks abstraction on a long file
// with 10 functions containing 100 total uncovered lines.
func BenchmarkCppAbstractor_LongFile_10Functions100Lines(b *testing.B) {
	longFile := filepath.Join("artifacts", "long.cc")

	// Create 10 functions with 10 uncovered lines each
	functions := make([]UncoveredFunction, 10)
	functionNames := []string{
		"analyze_functions",
		"handle_alias_pairs",
		"mark_functions_to_output",
		"expand_all_functions",
		"output_in_order",
		"ipa_passes",
		"enqueue_node",
		"process_common_attributes",
		"check_global_declaration",
		"maybe_diag_incompatible_alias",
	}

	for i := 0; i < 10; i++ {
		// Each function has 10 uncovered lines spread across different areas
		uncoveredLines := make([]int, 10)
		baseLineStart := 500 + i*100 // Spread functions across the file
		for j := 0; j < 10; j++ {
			uncoveredLines[j] = baseLineStart + j*5
		}

		functions[i] = UncoveredFunction{
			FunctionName:   functionNames[i],
			UncoveredLines: uncoveredLines,
			TotalLines:     100,
			CoveredLines:   90,
		}
	}

	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath:  longFile,
				Functions: functions,
			},
		},
	}

	abstractor := NewCppAbstractor()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := abstractor.Abstract(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCppAbstractor_LongFile_1Function50Lines benchmarks a middle ground:
// 1 function with 50 uncovered lines (heavily uncovered function).
func BenchmarkCppAbstractor_LongFile_1Function50Lines(b *testing.B) {
	longFile := filepath.Join("artifacts", "long.cc")

	// Create 50 uncovered lines in a single function
	uncoveredLines := make([]int, 50)
	for i := 0; i < 50; i++ {
		uncoveredLines[i] = 900 + i*2 // Every other line uncovered
	}

	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath: longFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "analyze_functions",
						UncoveredLines: uncoveredLines,
						TotalLines:     150,
						CoveredLines:   100,
					},
				},
			},
		},
	}

	abstractor := NewCppAbstractor()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := abstractor.Abstract(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCppAbstractor_LongFile_5Functions20LinesEach benchmarks 5 functions
// with 20 uncovered lines each (100 total lines, fewer functions).
func BenchmarkCppAbstractor_LongFile_5Functions20LinesEach(b *testing.B) {
	longFile := filepath.Join("artifacts", "long.cc")

	functions := make([]UncoveredFunction, 5)
	functionNames := []string{
		"analyze_functions",
		"expand_all_functions",
		"output_in_order",
		"ipa_passes",
		"mark_functions_to_output",
	}

	for i := 0; i < 5; i++ {
		// Each function has 20 uncovered lines
		uncoveredLines := make([]int, 20)
		baseLineStart := 600 + i*200
		for j := 0; j < 20; j++ {
			uncoveredLines[j] = baseLineStart + j*3
		}

		functions[i] = UncoveredFunction{
			FunctionName:   functionNames[i],
			UncoveredLines: uncoveredLines,
			TotalLines:     80,
			CoveredLines:   60,
		}
	}

	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath:  longFile,
				Functions: functions,
			},
		},
	}

	abstractor := NewCppAbstractor()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := abstractor.Abstract(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCppAbstractor_MultiFile benchmarks processing multiple files at once.
func BenchmarkCppAbstractor_MultiFile(b *testing.B) {
	shortFile := filepath.Join("artifacts", "short.cc")
	longFile := filepath.Join("artifacts", "long.cc")

	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath: shortFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "f",
						UncoveredLines: []int{40, 41, 47, 48},
						TotalLines:     45,
						CoveredLines:   41,
					},
				},
			},
			{
				FilePath: longFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "analyze_functions",
						UncoveredLines: []int{950, 951, 1000, 1001},
						TotalLines:     200,
						CoveredLines:   196,
					},
					{
						FunctionName:   "expand_all_functions",
						UncoveredLines: []int{1500, 1501, 1550, 1551},
						TotalLines:     150,
						CoveredLines:   146,
					},
				},
			},
		},
	}

	abstractor := NewCppAbstractor()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := abstractor.Abstract(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAbstractorRegistry_AbstractAll benchmarks the registry system
// processing files through the abstractor registry.
func BenchmarkAbstractorRegistry_AbstractAll(b *testing.B) {
	shortFile := filepath.Join("artifacts", "short.cc")
	longFile := filepath.Join("artifacts", "long.cc")

	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath: shortFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "f",
						UncoveredLines: []int{40, 41, 47, 48},
						TotalLines:     45,
						CoveredLines:   41,
					},
				},
			},
			{
				FilePath: longFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "analyze_functions",
						UncoveredLines: []int{950, 951, 1000, 1001},
						TotalLines:     200,
						CoveredLines:   196,
					},
				},
			},
		},
	}

	registry := NewAbstractorRegistry()
	registry.Register(NewCppAbstractor())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := registry.AbstractAll(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCppAbstractor_ParsingOnly benchmarks just the parsing overhead
// without abstraction logic (baseline measurement).
func BenchmarkCppAbstractor_ParsingOnly(b *testing.B) {
	longFile := filepath.Join("artifacts", "long.cc")

	input := &UncoveredInput{
		Files: []UncoveredFile{
			{
				FilePath: longFile,
				Functions: []UncoveredFunction{
					{
						FunctionName:   "analyze_functions",
						UncoveredLines: []int{}, // No uncovered lines - minimal processing
						TotalLines:     200,
						CoveredLines:   200,
					},
				},
			},
		},
	}

	abstractor := NewCppAbstractor()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := abstractor.Abstract(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}
