package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfigWithOverrides_FortifyAArch64(t *testing.T) {
	cfg, err := LoadConfigWithOverrides("aarch64", "fortify")
	require.NoError(t, err)
	require.Equal(t, "aarch64", cfg.ISA)
	require.Equal(t, "fortify", cfg.Strategy)
	require.Equal(t, "fortify", cfg.Compiler.Oracle.Type)
	require.Equal(t, "initial_seeds/aarch64/fortify/function_template.c", cfg.Compiler.Fuzz.FunctionTemplate)
	require.True(t, cfg.Compiler.Fuzz.FlagStrategy.Enabled)
	require.Equal(t, 256, cfg.Compiler.Fuzz.MaxIterations)
	require.Len(t, cfg.Compiler.Fuzz.CFGFilePaths, 5)
}

func TestLoadConfigWithOverrides_FortifyRiscv64(t *testing.T) {
	cfg, err := LoadConfigWithOverrides("riscv64", "fortify")
	require.NoError(t, err)
	require.Equal(t, "riscv64", cfg.ISA)
	require.Equal(t, "fortify", cfg.Strategy)
	require.Equal(t, "fortify", cfg.Compiler.Oracle.Type)
	require.Equal(t, 256, cfg.Compiler.Fuzz.MaxIterations)
	require.Len(t, cfg.Compiler.Fuzz.CFGFilePaths, 5)
}

func TestLoadConfigWithOverrides_FortifyLoongArch64(t *testing.T) {
	cfg, err := LoadConfigWithOverrides("loongarch64", "fortify")
	require.NoError(t, err)
	require.Equal(t, "loongarch64", cfg.ISA)
	require.Equal(t, "fortify", cfg.Strategy)
	require.Equal(t, "fortify", cfg.Compiler.Oracle.Type)
	require.Equal(t, 256, cfg.Compiler.Fuzz.MaxIterations)
	require.Len(t, cfg.Compiler.Fuzz.CFGFilePaths, 5)
}
