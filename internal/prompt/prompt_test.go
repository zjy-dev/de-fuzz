package prompt

import (
	"os"
	"path/filepath"
	"testing"

	"defuzz/internal/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuilder_BuildUnderstandPrompt(t *testing.T) {
	builder := NewBuilder()

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
		assert.Contains(t, prompt, "[ISA Stack Layout]")
		assert.Contains(t, prompt, "[Defense Strategy Source Code]")
		assert.Contains(t, prompt, "Not available for now") // Default content when files don't exist
	})

	t.Run("should include file contents when auxiliary files exist", func(t *testing.T) {
		isa := "arm64"
		strategy := "aslr"

		// Create auxiliary files
		stackLayoutContent := "ARM64 stack layout information"
		sourceCodeContent := "#include <stdio.h>\nint main() { return 0; }"

		stackLayoutPath := filepath.Join(tempDir, "stack_layout.md")
		sourceCodePath := filepath.Join(tempDir, "defense_strategy.c")

		err := os.WriteFile(stackLayoutPath, []byte(stackLayoutContent), 0644)
		require.NoError(t, err)
		err = os.WriteFile(sourceCodePath, []byte(sourceCodeContent), 0644)
		require.NoError(t, err)

		prompt, err := builder.BuildUnderstandPrompt(isa, strategy, tempDir)

		require.NoError(t, err)
		assert.Contains(t, prompt, "Target ISA: arm64")
		assert.Contains(t, prompt, "Defense Strategy: aslr")
		assert.Contains(t, prompt, stackLayoutContent)
		assert.Contains(t, prompt, sourceCodeContent)
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
	builder := NewBuilder()
	ctx := "test context"
	seedType := "c"

	t.Run("should build a valid generate prompt", func(t *testing.T) {
		prompt, err := builder.BuildGeneratePrompt(ctx, seedType)
		require.NoError(t, err)
		assert.Contains(t, prompt, "[CONTEXT]")
		assert.Contains(t, prompt, ctx)
		assert.Contains(t, prompt, `generate a new, complete, and valid seed of type "c"`)
	})

	t.Run("should return error if context is empty", func(t *testing.T) {
		_, err := builder.BuildGeneratePrompt("", seedType)
		assert.Error(t, err)
	})

	t.Run("should return error if seedType is empty", func(t *testing.T) {
		_, err := builder.BuildGeneratePrompt(ctx, "")
		assert.Error(t, err)
	})
}

func TestBuilder_BuildMutatePrompt(t *testing.T) {
	builder := NewBuilder()
	ctx := "test context"
	s := &seed.Seed{
		Type:     "c",
		Content:  "int main() { return 0; }",
		Makefile: "all:\n\tgcc source.c -o prog",
	}

	t.Run("should build a valid mutate prompt", func(t *testing.T) {
		prompt, err := builder.BuildMutatePrompt(ctx, s)
		require.NoError(t, err)
		assert.Contains(t, prompt, "[CONTEXT]")
		assert.Contains(t, prompt, ctx)
		assert.Contains(t, prompt, "[EXISTING SEED]")
		assert.Contains(t, prompt, s.Content)
		assert.Contains(t, prompt, s.Makefile)
	})

	t.Run("should return error if context is empty", func(t *testing.T) {
		_, err := builder.BuildMutatePrompt("", s)
		assert.Error(t, err)
	})

	t.Run("should return error if seed is nil", func(t *testing.T) {
		_, err := builder.BuildMutatePrompt(ctx, nil)
		assert.Error(t, err)
	})
}

func TestBuilder_BuildAnalyzePrompt(t *testing.T) {
	builder := NewBuilder()
	ctx := "test context"
	s := &seed.Seed{
		Type:     "c",
		Content:  "int main() { return 0; }",
		Makefile: "all:\n\tgcc source.c -o prog",
	}
	feedback := "exit code: 1"

	t.Run("should build a valid analyze prompt", func(t *testing.T) {
		prompt, err := builder.BuildAnalyzePrompt(ctx, s, feedback)
		require.NoError(t, err)
		assert.Contains(t, prompt, "[CONTEXT]")
		assert.Contains(t, prompt, ctx)
		assert.Contains(t, prompt, "[SEED]")
		assert.Contains(t, prompt, s.Content)
		assert.Contains(t, prompt, "[EXECUTION FEEDBACK]")
		assert.Contains(t, prompt, feedback)
		assert.Contains(t, prompt, `Respond with "BUG" if a bug is present, or "NO_BUG" if not.`)
	})

	t.Run("should return error if context is empty", func(t *testing.T) {
		_, err := builder.BuildAnalyzePrompt("", s, feedback)
		assert.Error(t, err)
	})

	t.Run("should return error if seed is nil", func(t *testing.T) {
		_, err := builder.BuildAnalyzePrompt(ctx, nil, feedback)
		assert.Error(t, err)
	})

	t.Run("should return error if feedback is empty", func(t *testing.T) {
		_, err := builder.BuildAnalyzePrompt(ctx, s, "")
		assert.Error(t, err)
	})
}
