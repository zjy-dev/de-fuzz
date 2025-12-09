package prompt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuilder_BuildUnderstandPrompt(t *testing.T) {
	builder := NewBuilder(3, "")

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "prompt_test_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	t.Run("should build a valid prompt with given isa and strategy", func(t *testing.T) {
		isa := "x86_64"
		strategy := "stackguard"
		prompt, err := builder.BuildUnderstandPrompt(isa, strategy, tempDir)

		require.NoError(t, err)
		assert.Contains(t, prompt, "Target ISA: x86_64")
		assert.Contains(t, prompt, "Defense Strategy: stackguard")
		assert.Contains(t, prompt, "[ISA Stack Layout of the target compiler]")
		assert.Contains(t, prompt, "Not available for now") // Default content when files don't exist
	})

	t.Run("should include file contents when auxiliary files exist", func(t *testing.T) {
		isa := "arm64"
		strategy := "aslr"

		// Create auxiliary files
		stackLayoutContent := "ARM64 stack layout information"

		stackLayoutPath := filepath.Join(tempDir, "stack_layout.md")

		err := os.WriteFile(stackLayoutPath, []byte(stackLayoutContent), 0644)
		require.NoError(t, err)

		prompt, err := builder.BuildUnderstandPrompt(isa, strategy, tempDir)

		require.NoError(t, err)
		assert.Contains(t, prompt, "Target ISA: arm64")
		assert.Contains(t, prompt, "Defense Strategy: aslr")
		assert.Contains(t, prompt, stackLayoutContent)
	})

	t.Run("should return error if isa is empty", func(t *testing.T) {
		_, err := builder.BuildUnderstandPrompt("", "stackguard", tempDir)
		assert.Error(t, err)
	})

	t.Run("should return error if strategy is empty", func(t *testing.T) {
		_, err := builder.BuildUnderstandPrompt("x86_64", "", tempDir)
		assert.Error(t, err)
	})
}

func TestReadFileOrDefault(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "prompt_test_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	t.Run("should return file content when file exists", func(t *testing.T) {
		testContent := "test file content"
		testFile := filepath.Join(tempDir, "test.txt")
		err := os.WriteFile(testFile, []byte(testContent), 0644)
		require.NoError(t, err)

		content, err := readFileOrDefault(testFile)
		require.NoError(t, err)
		assert.Equal(t, testContent, content)
	})

	t.Run("should return default message when file does not exist", func(t *testing.T) {
		nonExistentFile := filepath.Join(tempDir, "does_not_exist.txt")
		content, err := readFileOrDefault(nonExistentFile)
		require.NoError(t, err)
		assert.Equal(t, "Not available for now", content)
	})
}

func TestBuilder_BuildGeneratePrompt(t *testing.T) {
	builder := NewBuilder(3, "")

	t.Run("should build a valid generate prompt", func(t *testing.T) {
		prompt, err := builder.BuildGeneratePrompt("nothing")
		require.NoError(t, err)
		assert.Contains(t, prompt, "Generate a new, complete, and valid seed for fuzzing.")
		assert.Contains(t, prompt, "source code")
		assert.Contains(t, prompt, "// ||||| JSON_TESTCASES_START |||||")
		assert.Contains(t, prompt, "source.c")
	})
}

func TestBuilder_BuildMutatePrompt(t *testing.T) {
	builder := NewBuilder(3, "")
	testCases := []seed.TestCase{
		{RunningCommand: "./prog", ExpectedResult: "success"},
	}
	s := &seed.Seed{
		Content:   "int main() { return 0; }",
		TestCases: testCases,
	}

	t.Run("should build a valid mutate prompt without context", func(t *testing.T) {
		prompt, err := builder.BuildMutatePrompt(s, nil)
		require.NoError(t, err)
		assert.Contains(t, prompt, "[EXISTING SEED]")
		assert.Contains(t, prompt, s.Content)
		assert.Contains(t, prompt, "running command")
		assert.Contains(t, prompt, "expected result")
		assert.Contains(t, prompt, "system context")
		assert.NotContains(t, prompt, "[COVERAGE CONTEXT]")
	})

	t.Run("should build a valid mutate prompt with coverage context", func(t *testing.T) {
		mutationCtx := &MutationContext{
			CoverageIncreaseSummary: "Covered 10 new lines in function foo",
			CoverageIncreaseDetails: "Detailed coverage info here",
			TotalCoveragePercentage: 45.5,
			TotalCoveredLines:       100,
			TotalLines:              220,
		}
		prompt, err := builder.BuildMutatePrompt(s, mutationCtx)
		require.NoError(t, err)
		assert.Contains(t, prompt, "[EXISTING SEED]")
		assert.Contains(t, prompt, s.Content)
		assert.Contains(t, prompt, "[COVERAGE CONTEXT]")
		assert.Contains(t, prompt, "45.5%")
		assert.Contains(t, prompt, "100/220 lines covered")
		assert.Contains(t, prompt, "Covered 10 new lines in function foo")
	})

	t.Run("should return error if seed is nil", func(t *testing.T) {
		_, err := builder.BuildMutatePrompt(nil, nil)
		assert.Error(t, err)
	})
}

func TestBuilder_BuildAnalyzePrompt(t *testing.T) {
	builder := NewBuilder(3, "")
	testCases := []seed.TestCase{
		{RunningCommand: "./prog", ExpectedResult: "success"},
	}
	s := &seed.Seed{
		Content:   "int main() { return 0; }",
		TestCases: testCases,
	}

	feedback := "exit code: 1"

	t.Run("should build a valid analyze prompt", func(t *testing.T) {
		prompt, err := builder.BuildAnalyzePrompt(s, feedback)
		require.NoError(t, err)
		assert.Contains(t, prompt, "[SEED]")
		assert.Contains(t, prompt, s.Content)
		assert.Contains(t, prompt, "[EXECUTION FEEDBACK]")
		assert.Contains(t, prompt, feedback)
		assert.Contains(t, prompt, "system context")
	})

	t.Run("should return error if seed is nil", func(t *testing.T) {
		_, err := builder.BuildAnalyzePrompt(nil, feedback)
		assert.Error(t, err)
	})

	t.Run("should return error if feedback is empty", func(t *testing.T) {
		_, err := builder.BuildAnalyzePrompt(s, "")
		assert.Error(t, err)
	})
}

func TestBuilder_ParseLLMResponse(t *testing.T) {
	t.Run("should parse standard response with test cases", func(t *testing.T) {
		builder := NewBuilder(3, "")
		response := `int main() { return 0; }
// ||||| JSON_TESTCASES_START |||||
[{"running command": "./prog", "expected result": "success"}]`

		s, err := builder.ParseLLMResponse(response)
		require.NoError(t, err)
		assert.Equal(t, "int main() { return 0; }", s.Content)
		assert.Len(t, s.TestCases, 1)
		assert.Equal(t, "./prog", s.TestCases[0].RunningCommand)
	})

	t.Run("should parse code-only response when MaxTestCases is 0", func(t *testing.T) {
		builder := NewBuilder(0, "")
		response := `int main() {
    printf("hello");
    return 0;
}`

		s, err := builder.ParseLLMResponse(response)
		require.NoError(t, err)
		assert.Contains(t, s.Content, "int main()")
		assert.Empty(t, s.TestCases)
	})

	t.Run("should strip markdown code blocks", func(t *testing.T) {
		builder := NewBuilder(0, "")
		response := "```c\nint main() { return 0; }\n```"

		s, err := builder.ParseLLMResponse(response)
		require.NoError(t, err)
		assert.Equal(t, "int main() { return 0; }", s.Content)
	})

	t.Run("should parse function and merge with template", func(t *testing.T) {
		// Create a temporary template file
		tempDir, err := os.MkdirTemp("", "prompt_test_")
		require.NoError(t, err)
		defer os.RemoveAll(tempDir)

		templateContent := `#include <stdio.h>

// FUNCTION_PLACEHOLDER: my_func

int main() {
    my_func();
    return 0;
}`
		templatePath := filepath.Join(tempDir, "template.c")
		err = os.WriteFile(templatePath, []byte(templateContent), 0644)
		require.NoError(t, err)

		builder := NewBuilder(0, templatePath)
		response := `void my_func() {
    printf("Hello from function!\n");
}`

		s, err := builder.ParseLLMResponse(response)
		require.NoError(t, err)
		assert.Contains(t, s.Content, "#include <stdio.h>")
		assert.Contains(t, s.Content, "void my_func()")
		assert.Contains(t, s.Content, "printf(\"Hello from function!\\n\");")
		assert.Contains(t, s.Content, "int main()")
		assert.NotContains(t, s.Content, "FUNCTION_PLACEHOLDER")
		assert.Empty(t, s.TestCases)
	})
}

func TestBuilder_IsFunctionTemplateMode(t *testing.T) {
	t.Run("returns true when template is set", func(t *testing.T) {
		builder := NewBuilder(0, "/path/to/template.c")
		assert.True(t, builder.IsFunctionTemplateMode())
	})

	t.Run("returns false when template is empty", func(t *testing.T) {
		builder := NewBuilder(3, "")
		assert.False(t, builder.IsFunctionTemplateMode())
	})
}

func TestBuilder_RequiresTestCases(t *testing.T) {
	t.Run("returns true when MaxTestCases > 0 and no template", func(t *testing.T) {
		builder := NewBuilder(3, "")
		assert.True(t, builder.RequiresTestCases())
	})

	t.Run("returns false when MaxTestCases is 0", func(t *testing.T) {
		builder := NewBuilder(0, "")
		assert.False(t, builder.RequiresTestCases())
	})

	t.Run("returns true when template mode is enabled with MaxTestCases > 0", func(t *testing.T) {
		// Now function template + test cases is supported
		builder := NewBuilder(3, "/path/to/template.c")
		assert.True(t, builder.RequiresTestCases())
	})

	t.Run("returns false when template mode is enabled with MaxTestCases = 0", func(t *testing.T) {
		builder := NewBuilder(0, "/path/to/template.c")
		assert.False(t, builder.RequiresTestCases())
	})
}
