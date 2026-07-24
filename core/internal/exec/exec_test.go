package exec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandExecutor_Run(t *testing.T) {
	executor := NewCommandExecutor()

	t.Run("should execute a simple command successfully", func(t *testing.T) {
		result, err := executor.Run("echo", "hello world")
		require.NoError(t, err)
		assert.Equal(t, "hello world\n", result.Stdout)
		assert.Empty(t, result.Stderr)
		assert.Equal(t, 0, result.ExitCode)
	})

	t.Run("should capture stderr", func(t *testing.T) {
		// This command writes "hello stderr" to stderr and exits.
		result, err := executor.Run("sh", "-c", "echo 'hello stderr' 1>&2")
		require.NoError(t, err)
		assert.Empty(t, result.Stdout)
		assert.Equal(t, "hello stderr\n", result.Stderr)
		assert.Equal(t, 0, result.ExitCode)
	})

	t.Run("should handle non-zero exit codes", func(t *testing.T) {
		result, err := executor.Run("sh", "-c", "exit 42")
		require.NoError(t, err) // We don't expect an error from Run itself
		assert.Equal(t, 42, result.ExitCode)
	})

	t.Run("should return error for non-existent command", func(t *testing.T) {
		_, err := executor.Run("this_command_does_not_exist_12345")
		assert.Error(t, err)
	})
}
