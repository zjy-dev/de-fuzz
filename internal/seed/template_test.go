package seed

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeTemplate(t *testing.T) {
	t.Run("should merge function into template", func(t *testing.T) {
		template := `#include <stdio.h>
#include <string.h>

// FUNCTION_PLACEHOLDER: vulnerable_function

int main(int argc, char *argv[]) {
    if (argc > 1) {
        vulnerable_function(argv[1]);
    }
    return 0;
}`

		functionCode := `void vulnerable_function(char *input) {
    char buffer[10];
    strcpy(buffer, input);
    printf("%s\n", buffer);
}`

		result, err := MergeTemplate(template, functionCode)
		require.NoError(t, err)

		assert.Contains(t, result, "#include <stdio.h>")
		assert.Contains(t, result, "void vulnerable_function(char *input)")
		assert.Contains(t, result, "strcpy(buffer, input)")
		assert.Contains(t, result, "int main(int argc, char *argv[])")
		assert.NotContains(t, result, "FUNCTION_PLACEHOLDER")
	})

	t.Run("should replace block comment containing placeholder", func(t *testing.T) {
		template := `#include <stdio.h>

/**
 * FUNCTION_PLACEHOLDER: seed
 * 
 * LLM Instructions:
 * - Implement a function with a buffer
 * - The function should be vulnerable
 */

int main() {
    seed(100);
    return 0;
}`

		functionCode := `void seed(int fill_size) {
    char buffer[64];
    memset(buffer, 'A', fill_size);
}`

		result, err := MergeTemplate(template, functionCode)
		require.NoError(t, err)

		assert.Contains(t, result, "#include <stdio.h>")
		assert.Contains(t, result, "void seed(int fill_size)")
		assert.Contains(t, result, "memset(buffer, 'A', fill_size)")
		assert.Contains(t, result, "int main()")
		assert.NotContains(t, result, "FUNCTION_PLACEHOLDER")
		assert.NotContains(t, result, "LLM Instructions")
		assert.NotContains(t, result, "*/")
	})

	t.Run("should preserve indentation", func(t *testing.T) {
		template := `#include <stdio.h>

    // FUNCTION_PLACEHOLDER: my_function

int main() {
    my_function();
    return 0;
}`

		functionCode := `void my_function() {
    printf("Hello\n");
}`

		result, err := MergeTemplate(template, functionCode)
		require.NoError(t, err)

		// The function should be indented with 4 spaces (matching placeholder indentation)
		assert.Contains(t, result, "    void my_function() {")
		assert.Contains(t, result, "        printf(\"Hello\\n\");")
	})

	t.Run("should return error if template is empty", func(t *testing.T) {
		_, err := MergeTemplate("", "void foo() {}")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "template cannot be empty")
	})

	t.Run("should return error if functionCode is empty", func(t *testing.T) {
		template := "// FUNCTION_PLACEHOLDER: foo"
		_, err := MergeTemplate(template, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "functionCode cannot be empty")
	})

	t.Run("should return error if no placeholder found", func(t *testing.T) {
		template := `#include <stdio.h>
int main() {
    return 0;
}`
		functionCode := "void foo() {}"
		_, err := MergeTemplate(template, functionCode)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain")
	})
}

func TestExtractFunctionName(t *testing.T) {
	t.Run("should extract function name from template", func(t *testing.T) {
		template := `#include <stdio.h>

// FUNCTION_PLACEHOLDER: vulnerable_function

int main() {
    return 0;
}`
		functionName, err := ExtractFunctionName(template)
		require.NoError(t, err)
		assert.Equal(t, "vulnerable_function", functionName)
	})

	t.Run("should handle template with indented placeholder", func(t *testing.T) {
		template := `#include <stdio.h>

    // FUNCTION_PLACEHOLDER: my_func

int main() {
    return 0;
}`
		functionName, err := ExtractFunctionName(template)
		require.NoError(t, err)
		assert.Equal(t, "my_func", functionName)
	})

	t.Run("should return error if no placeholder found", func(t *testing.T) {
		template := `#include <stdio.h>
int main() {
    return 0;
}`
		_, err := ExtractFunctionName(template)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no function name found")
	})

	t.Run("should return error if placeholder has no function name", func(t *testing.T) {
		template := `#include <stdio.h>
// FUNCTION_PLACEHOLDER:
int main() {
    return 0;
}`
		_, err := ExtractFunctionName(template)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no function name found")
	})
}

func TestGetIndentation(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected string
	}{
		{
			name:     "no indentation",
			line:     "hello",
			expected: "",
		},
		{
			name:     "spaces",
			line:     "    hello",
			expected: "    ",
		},
		{
			name:     "tabs",
			line:     "\t\thello",
			expected: "\t\t",
		},
		{
			name:     "mixed",
			line:     "  \t  hello",
			expected: "  \t  ",
		},
		{
			name:     "only whitespace",
			line:     "    ",
			expected: "    ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getIndentation(tt.line)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIndentCode(t *testing.T) {
	t.Run("should indent each non-empty line", func(t *testing.T) {
		code := `void foo() {
    printf("hello");
}`
		indented := indentCode(code, "    ")
		expected := `    void foo() {
        printf("hello");
    }`
		assert.Equal(t, expected, indented)
	})

	t.Run("should not indent empty lines", func(t *testing.T) {
		code := `void foo() {

    printf("hello");
}`
		indented := indentCode(code, "  ")
		expected := `  void foo() {

      printf("hello");
  }`
		assert.Equal(t, expected, indented)
	})

	t.Run("should handle no indentation", func(t *testing.T) {
		code := `void foo() {
    printf("hello");
}`
		indented := indentCode(code, "")
		assert.Equal(t, code, indented)
	})
}
