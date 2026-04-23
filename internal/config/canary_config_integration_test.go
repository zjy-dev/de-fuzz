package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfigWithOverrides_CanaryAArch64MultiCFGAndLLMOnlyMode(t *testing.T) {
	cfg, err := LoadConfigWithOverrides("aarch64", "canary")
	require.NoError(t, err)
	require.Equal(t, "aarch64", cfg.ISA)
	require.Equal(t, "canary", cfg.Strategy)
	require.False(t, cfg.Compiler.Fuzz.FlagStrategy.Enabled)
	require.Len(t, cfg.Compiler.Fuzz.CFGFilePaths, 5)
}

func TestLoadConfigWithOverrides_CanaryRiscv64MultiCFGAndLLMOnlyMode(t *testing.T) {
	cfg, err := LoadConfigWithOverrides("riscv64", "canary")
	require.NoError(t, err)
	require.Equal(t, "riscv64", cfg.ISA)
	require.Equal(t, "canary", cfg.Strategy)
	require.False(t, cfg.Compiler.Fuzz.FlagStrategy.Enabled)
	require.Len(t, cfg.Compiler.Fuzz.CFGFilePaths, 5)
}

func TestLoadConfigWithOverrides_CanaryLoongArch64MultiCFGAndLLMOnlyMode(t *testing.T) {
	cfg, err := LoadConfigWithOverrides("loongarch64", "canary")
	require.NoError(t, err)
	require.Equal(t, "loongarch64", cfg.ISA)
	require.Equal(t, "canary", cfg.Strategy)
	require.False(t, cfg.Compiler.Fuzz.FlagStrategy.Enabled)
	require.Len(t, cfg.Compiler.Fuzz.CFGFilePaths, 4)
}

func TestLoadConfigWithOverrides_CanaryPpc64leMultiCFGAndLLMOnlyMode(t *testing.T) {
	cfg, err := LoadConfigWithOverrides("ppc64le", "canary")
	require.NoError(t, err)
	require.Equal(t, "ppc64le", cfg.ISA)
	require.Equal(t, "canary", cfg.Strategy)
	require.False(t, cfg.Compiler.Fuzz.FlagStrategy.Enabled)
	require.Len(t, cfg.Compiler.Fuzz.CFGFilePaths, 6)
}
