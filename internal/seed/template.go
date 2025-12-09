package seed

import (
	"fmt"
	"strings"
)

const (
	// FunctionPlaceholder is the marker used in templates to indicate where
	// the LLM-generated function should be inserted
	FunctionPlaceholder = "// FUNCTION_PLACEHOLDER:"
)

// MergeTemplate merges a function implementation into a C code template.
// The template should contain a comment block starting with "// FUNCTION_PLACEHOLDER: function_name"
// or a block comment containing "FUNCTION_PLACEHOLDER: function_name".
// The entire comment block (including multi-line comments) will be replaced with the function code.
//
// Example template:
//
//	#include <stdio.h>
//	/**
//	 * FUNCTION_PLACEHOLDER: seed
//	 * LLM Instructions: ...
//	 */
//	int main() { ... }
//
// Example functionCode:
//
//	void seed(int fill_size) {
//	    // implementation
//	}
//
// Returns the complete C code with the function merged into the template.
func MergeTemplate(template, functionCode string) (string, error) {
	if template == "" {
		return "", fmt.Errorf("template cannot be empty")
	}
	if functionCode == "" {
		return "", fmt.Errorf("functionCode cannot be empty")
	}

	// Find the placeholder and determine if it's in a block comment
	lines := strings.Split(template, "\n")
	placeholderIndex := -1
	blockCommentStart := -1
	blockCommentEnd := -1
	inBlockComment := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track block comment state
		if strings.Contains(trimmed, "/*") && !strings.Contains(trimmed, "*/") {
			inBlockComment = true
			blockCommentStart = i
		}

		// Check for placeholder
		if strings.Contains(line, "FUNCTION_PLACEHOLDER:") {
			placeholderIndex = i
			if inBlockComment {
				// Find the end of this block comment
				for j := i; j < len(lines); j++ {
					if strings.Contains(lines[j], "*/") {
						blockCommentEnd = j
						break
					}
				}
			}
			break
		}

		if strings.Contains(trimmed, "*/") {
			inBlockComment = false
			blockCommentStart = -1
		}
	}

	if placeholderIndex == -1 {
		return "", fmt.Errorf("template does not contain FUNCTION_PLACEHOLDER: marker")
	}

	// Determine the range to replace
	startReplace := placeholderIndex
	endReplace := placeholderIndex

	if blockCommentStart != -1 && blockCommentEnd != -1 {
		// Replace the entire block comment
		startReplace = blockCommentStart
		endReplace = blockCommentEnd
	}

	// Get indentation from the start of the block being replaced
	indent := getIndentation(lines[startReplace])

	// Indent each line of the function code
	indentedFunction := indentCode(functionCode, indent)

	// Build the result
	result := make([]string, 0, len(lines)+strings.Count(functionCode, "\n"))
	result = append(result, lines[:startReplace]...)
	result = append(result, indentedFunction)
	result = append(result, lines[endReplace+1:]...)

	return strings.Join(result, "\n"), nil
}

// getIndentation returns the leading whitespace of a string
func getIndentation(line string) string {
	for i, ch := range line {
		if ch != ' ' && ch != '\t' {
			return line[:i]
		}
	}
	return line // entire line is whitespace
}

// indentCode adds the specified indentation to each line of the code
func indentCode(code, indent string) string {
	if indent == "" {
		return code
	}

	lines := strings.Split(code, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "" { // Don't indent empty lines
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}

// ExtractFunctionName extracts the function name from a placeholder line
// Example: "// FUNCTION_PLACEHOLDER: vulnerable_function" -> "vulnerable_function"
func ExtractFunctionName(template string) (string, error) {
	lines := strings.Split(template, "\n")
	for _, line := range lines {
		if strings.Contains(line, FunctionPlaceholder) {
			// Extract the part after the placeholder marker
			parts := strings.SplitN(line, FunctionPlaceholder, 2)
			if len(parts) == 2 {
				functionName := strings.TrimSpace(parts[1])
				if functionName != "" {
					return functionName, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no function name found in template")
}
