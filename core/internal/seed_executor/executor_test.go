package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQEMUExecutorPlacesTrustedArgumentsBeforeBinary(t *testing.T) {
	dir := t.TempDir()
	runner := filepath.Join(dir, "qemu-runner")
	require.NoError(t, os.WriteFile(runner, []byte("#!/bin/sh\nprintf '%s\n' \"$@\"\n"), 0o755))

	adapter := NewQEMUOracleExecutorAdapterWithArgs(
		runner, "/target/sysroot", []string{"-cpu", "max"}, 5,
	)
	exit, stdout, stderr, err := adapter.ExecuteWithArgs("/target/program", "one", "two")

	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	assert.Empty(t, stderr)
	assert.Equal(t, []string{"-cpu", "max", "-L", "/target/sysroot", "/target/program", "one", "two"}, strings.Split(strings.TrimSpace(stdout), "\n"))
}
