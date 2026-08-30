package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadToolchainsSupportsGCCAndClangPaths(t *testing.T) {
	path := writeToolchainsFixture(t, `
toolchains:
  x86_64:
    gcc_path: /trusted/gcc
    clang_path: /trusted/clang
    native: true
`)

	toolchains, err := LoadToolchains(path)
	require.NoError(t, err)
	tc, ok := toolchains.Lookup("x86_64")
	require.True(t, ok)
	assert.Equal(t, "/trusted/gcc", tc.GCCPath)
	assert.Equal(t, "/trusted/clang", tc.ClangPath)
}

func TestLoadToolchainsSupportsQEMUArguments(t *testing.T) {
	path := writeToolchainsFixture(t, `
toolchains:
  aarch64:
    gcc_path: /trusted/aarch64-linux-gnu-gcc
    driver_args: [--image, defuzz-gcc17:aarch64, --]
    qemu_path: /trusted/qemu-aarch64
    qemu_args: [-cpu, max]
    qemu_sysroot: /trusted/aarch64-sysroot
    native: false
`)

	toolchains, err := LoadToolchains(path)
	require.NoError(t, err)
	tc, ok := toolchains.Lookup("aarch64")
	require.True(t, ok)
	assert.Equal(t, []string{"-cpu", "max"}, tc.QEMUArgs)
	assert.Equal(t, []string{"--image", "defuzz-gcc17:aarch64", "--"}, tc.DriverArgs)
	assert.Equal(t, "/trusted/qemu-aarch64", tc.QEMUPath)
	assert.Equal(t, "/trusted/aarch64-sysroot", tc.QEMUSysroot)
	assert.False(t, tc.Native)
}

func TestNonNativeToolchainRequiresResolvableQEMU(t *testing.T) {
	_, _, err := (Toolchain{Native: false}).oracleExecutor(1)
	require.ErrorContains(t, err, "requires qemu_path")

	_, _, err = (Toolchain{Native: false, QEMUPath: "defuzz-qemu-does-not-exist"}).oracleExecutor(1)
	require.ErrorContains(t, err, "resolve qemu_path")
}

func TestLoadToolchainsRetainsLegacyGCCOnlyConfig(t *testing.T) {
	path := writeToolchainsFixture(t, `
toolchains:
  aarch64:
    gcc_path: aarch64-linux-gnu-gcc
`)

	toolchains, err := LoadToolchains(path)
	require.NoError(t, err)
	tc, ok := toolchains.Lookup("aarch64")
	require.True(t, ok)
	assert.Equal(t, "aarch64-linux-gnu-gcc", tc.GCCPath)
	assert.Empty(t, tc.ClangPath)
}

func TestLoadToolchainsRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name, yaml, field string
	}{
		{name: "root", yaml: "toolchains: {}\nunknown_root: true\n", field: "unknown_root"},
		{name: "toolchain", yaml: "toolchains:\n  x86_64:\n    gcc_path: gcc\n    compiler_path: cc\n", field: "compiler_path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadToolchains(writeToolchainsFixture(t, test.yaml))
			require.ErrorContains(t, err, "parse toolchains config")
			assert.ErrorContains(t, err, test.field)
		})
	}
}

func writeToolchainsFixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "toolchains.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}
