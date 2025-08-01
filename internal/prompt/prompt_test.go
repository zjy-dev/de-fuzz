package prompt

import (
	"strings"
	"testing"

	"defuzz/internal/seed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuilder_BuildUnderstandPrompt(t *testing.T) {
	builder := NewBuilder()

	t.Run("should build a valid prompt with given isa and strategy", func(t *testing.T) {
		isa := "x86_64"
		strategy := "stackguard"
		prompt, err := builder.BuildUnderstandPrompt(isa, strategy)

		require.NoError(t, err)
		assert.True(t, strings.Contains(prompt, "Target ISA: x86_64"))
		assert.True(t, strings.Contains(prompt, "Defense Strategy: stackguard"))
	})

	t.Run("should return error if isa is empty", func(t *testing.T) {
		_, err := builder.BuildUnderstandPrompt("", "stackguard")
		assert.Error(t, err)
	})

	t.Run("should return error if strategy is empty", func(t *testing.T) {
		_, err := builder.BuildUnderstandPrompt("x86_64", "")
		assert.Error(t, err)
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
		Type:    "c",
		Content: "int main() { return 0; }",
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
		Type:    "c",
		Content: "int main() { return 0; }",
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
