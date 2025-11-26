package seed

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidationError represents an error during seed validation.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error in %s: %s", e.Field, e.Message)
}

// ParseSeedFromLLMResponse extracts source code and test cases from LLM response.
// This is the canonical parsing function used by both generation and mutation.
// Uses the unified storage format with separator: // ||||| JSON_TESTCASES_START |||||
func ParseSeedFromLLMResponse(response string) (string, []TestCase, error) {
	// Use the separator defined in storage.go (without leading newline for split)
	separatorMarker := "// ||||| JSON_TESTCASES_START |||||"

	// Split response by the separator
	parts := strings.SplitN(response, separatorMarker, 2)
	if len(parts) < 2 {
		return "", nil, &ValidationError{
			Field:   "format",
			Message: "could not find separator '// ||||| JSON_TESTCASES_START |||||' in response",
		}
	}

	sourceCode := strings.TrimSpace(parts[0])
	testCasesJSON := strings.TrimSpace(parts[1])

	// Validate source code is not empty
	if sourceCode == "" {
		return "", nil, &ValidationError{
			Field:   "source",
			Message: "source code is empty",
		}
	}

	// Parse test cases JSON
	var testCases []TestCase
	if err := json.Unmarshal([]byte(testCasesJSON), &testCases); err != nil {
		return "", nil, &ValidationError{
			Field:   "test_cases",
			Message: fmt.Sprintf("failed to parse test cases JSON: %v", err),
		}
	}

	// Validate we have at least one test case
	if len(testCases) == 0 {
		return "", nil, &ValidationError{
			Field:   "test_cases",
			Message: "at least one test case is required",
		}
	}

	// Validate each test case
	for i, tc := range testCases {
		if tc.RunningCommand == "" {
			return "", nil, &ValidationError{
				Field:   "test_cases",
				Message: fmt.Sprintf("test case %d: running command is empty", i+1),
			}
		}
	}

	return sourceCode, testCases, nil
}

// ValidateSeed validates a seed's content and test cases.
func ValidateSeed(s *Seed) error {
	if s == nil {
		return &ValidationError{Field: "seed", Message: "seed is nil"}
	}

	if s.Content == "" {
		return &ValidationError{Field: "content", Message: "content is empty"}
	}

	if len(s.TestCases) == 0 {
		return &ValidationError{Field: "test_cases", Message: "at least one test case is required"}
	}

	for i, tc := range s.TestCases {
		if tc.RunningCommand == "" {
			return &ValidationError{
				Field:   "test_cases",
				Message: fmt.Sprintf("test case %d: running command is empty", i+1),
			}
		}
	}

	return nil
}
