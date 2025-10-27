package oracle
package oracle

import (
	"testing"
)

func TestContainsCrashIndicators(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected bool
	}{
		{
			name:     "segfault",
			output:   "Segmentation fault (core dumped)",
			expected: true,
		},
		{
			name:     "stack smashing",
			output:   "*** stack smashing detected ***: terminated",
			expected: true,
		},
		{
			name:     "buffer overflow",
			output:   "buffer overflow detected",
			expected: true,
		},
		{
			name:     "normal output",
			output:   "Program executed successfully",
			expected: false,
		},
		{
			name:     "case insensitive",
			output:   "SEGMENTATION FAULT",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsCrashIndicators(tt.output)
			if result != tt.expected {
				t.Errorf("containsCrashIndicators(%q) = %v, expected %v", tt.output, result, tt.expected)
			}
		})
	}
}

func TestIsExpectedError(t *testing.T) {
	tests := []struct {
		name     string
		stderr   string
		expected bool
	}{
		{
			name:     "warning message",
			stderr:   "warning: unused variable 'x'",
			expected: true,
		},
		{
			name:     "note message",
			stderr:   "note: candidate function",
			expected: true,
		},
		{
			name:     "error message",
			stderr:   "error: undefined reference",
			expected: false,
		},
		{
			name:     "empty",
			stderr:   "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isExpectedError(tt.stderr)
			if result != tt.expected {
				t.Errorf("isExpectedError(%q) = %v, expected %v", tt.stderr, result, tt.expected)
			}
		})
	}
}
