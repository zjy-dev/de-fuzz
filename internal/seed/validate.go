package seed

import (
	"encoding/json"
	"fmt"
	"regexp"
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

// ParseFunctionFromLLMResponse extracts function code from LLM response (for template mode).
// It strips markdown code blocks and returns the raw function code.
func ParseFunctionFromLLMResponse(response string) (string, error) {
	functionCode := strings.TrimSpace(response)
	functionCode = stripMarkdownCodeBlocks(functionCode)

	if functionCode == "" {
		return "", &ValidationError{
			Field:   "function",
			Message: "function code is empty",
		}
	}

	return functionCode, nil
}

// ParseFunctionWithTestCasesFromLLMResponse extracts function code and test cases from LLM response.
// This is used when function template mode is combined with test case generation.
// Format: function code + separator + JSON test cases
func ParseFunctionWithTestCasesFromLLMResponse(response string) (string, []TestCase, error) {
	// Use the separator to split function code and test cases
	separatorMarker := "// ||||| JSON_TESTCASES_START |||||"

	// Split response by the separator
	parts := strings.SplitN(response, separatorMarker, 2)
	if len(parts) < 2 {
		return "", nil, &ValidationError{
			Field:   "format",
			Message: "could not find separator '// ||||| JSON_TESTCASES_START |||||' in response",
		}
	}

	functionCode := strings.TrimSpace(parts[0])
	functionCode = stripMarkdownCodeBlocks(functionCode)
	testCasesJSON := strings.TrimSpace(parts[1])

	// Validate function code is not empty
	if functionCode == "" {
		return "", nil, &ValidationError{
			Field:   "function",
			Message: "function code is empty",
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

	return functionCode, testCases, nil
}

// ParseCodeOnlyFromLLMResponse extracts source code without test cases from LLM response.
// Used when MaxTestCases is 0.
func ParseCodeOnlyFromLLMResponse(response string) (string, error) {
	sourceCode := strings.TrimSpace(response)
	sourceCode = stripMarkdownCodeBlocks(sourceCode)

	if sourceCode == "" {
		return "", &ValidationError{
			Field:   "source",
			Message: "source code is empty",
		}
	}

	return sourceCode, nil
}

// stripMarkdownCodeBlocks extracts code from markdown code blocks or strips markers.
// If the response contains code blocks (```...```), it extracts only the code inside.
// If no code blocks are found, it returns the original text with any stray ``` markers removed.
func stripMarkdownCodeBlocks(code string) string {
	// First, try to extract code from markdown code blocks
	// Pattern: ```[language]\n...code...\n```
	codeBlockRegex := regexp.MustCompile("(?s)```(?:c|cpp|C|CPP)?\\s*\\n(.+?)\\n?```")
	matches := codeBlockRegex.FindAllStringSubmatch(code, -1)

	if len(matches) > 0 {
		// Extract and concatenate all code blocks
		var codeBlocks []string
		for _, match := range matches {
			if len(match) > 1 {
				codeBlocks = append(codeBlocks, strings.TrimSpace(match[1]))
			}
		}
		return strings.TrimSpace(strings.Join(codeBlocks, "\n\n"))
	}

	// No code blocks found, fall back to removing stray ``` markers
	lines := strings.Split(code, "\n")
	var result []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check if this is a code block marker
		if strings.HasPrefix(trimmed, "```") {
			continue
		}

		result = append(result, line)
	}

	return strings.TrimSpace(strings.Join(result, "\n"))
}

// ValidateSeed validates a seed's content.
// Test cases are optional (for function template mode).
func ValidateSeed(s *Seed) error {
	if s == nil {
		return &ValidationError{Field: "seed", Message: "seed is nil"}
	}

	if s.Content == "" {
		return &ValidationError{Field: "content", Message: "content is empty"}
	}

	// Test cases are optional - only validate if present
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
