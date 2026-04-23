package app

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/config"
)

func chdirRepoRoot(t *testing.T) {
	t.Helper()
	root, err := filepath.Abs("../../..")
	require.NoError(t, err)
	t.Chdir(root)
}

func TestResolveCompilerCFlags_UsesNeutralFallback(t *testing.T) {
	require.Equal(t, []string{"-O0"}, resolveCompilerCFlags(nil))
	require.Equal(t, []string{"-O0"}, resolveCompilerCFlags([]string{}))
}

func TestResolveCompilerCFlags_PreservesConfiguredFlags(t *testing.T) {
	flags := []string{"-O2", "--sysroot=/tmp/sysroot"}
	require.Equal(t, flags, resolveCompilerCFlags(flags))
}

func TestCollectTargetFunctionsForCFGPaths_SingleCFGFiltersOtherFiles(t *testing.T) {
	targetFunctions, skippedTargets, missingFiles := collectTargetFunctionsForCFGPaths(
		[]string{"build/gcc/cfgexpand.cc.015t.cfg"},
		[]config.TargetFunction{
			{File: "gcc/gcc/cfgexpand.cc", Functions: []string{"stack_protect_classify_type", "expand_used_vars"}},
			{File: "gcc/gcc/function.cc", Functions: []string{"stack_protect_epilogue"}},
			{File: "gcc/gcc/calls.cc", Functions: []string{"expand_call"}},
		},
	)

	require.Equal(t, []string{"stack_protect_classify_type", "expand_used_vars"}, targetFunctions)
	require.Equal(t, 2, skippedTargets)
	require.Equal(t, []string{"calls.cc", "function.cc"}, missingFiles)
}

func TestCollectTargetFunctionsForCFGPaths_MultiCFGSkipsMissingFiles(t *testing.T) {
	targetFunctions, skippedTargets, missingFiles := collectTargetFunctionsForCFGPaths(
		[]string{
			"build/gcc/cfgexpand.cc.015t.cfg",
			"build/gcc/function.cc.015t.cfg",
		},
		[]config.TargetFunction{
			{File: "gcc/gcc/cfgexpand.cc", Functions: []string{"stack_protect_classify_type"}},
			{File: "gcc/gcc/function.cc", Functions: []string{"stack_protect_epilogue"}},
			{File: "gcc/gcc/targhooks.cc", Functions: []string{"default_stack_protect_guard"}},
		},
	)

	require.Equal(t, []string{"stack_protect_classify_type", "stack_protect_epilogue"}, targetFunctions)
	require.Equal(t, 1, skippedTargets)
	require.Equal(t, []string{"targhooks.cc"}, missingFiles)
}

func TestLoadConfigWithOverrides_CanaryV15UsesLLMOnlyFlagMode(t *testing.T) {
	chdirRepoRoot(t)
	cfg, err := config.LoadConfigWithOverrides("aarch64", "canary")
	require.NoError(t, err)
	require.False(t, cfg.Compiler.Fuzz.FlagStrategy.Enabled)
	require.Equal(t, []string{"-O0"}, resolveCompilerCFlags(nil))
}

func TestCollectTargetFunctionsForCFGPaths_AArch64CanaryConfigHasNoMissingTargets(t *testing.T) {
	chdirRepoRoot(t)
	cfg, err := config.LoadConfigWithOverrides("aarch64", "canary")
	require.NoError(t, err)

	targetFunctions, skippedTargets, missingFiles := collectTargetFunctionsForCFGPaths(cfg.Compiler.Fuzz.CFGFilePaths, cfg.Compiler.Targets)
	require.NotEmpty(t, targetFunctions)
	require.Equal(t, 0, skippedTargets)
	require.Empty(t, missingFiles)
}

func TestCollectTargetFunctionsForCFGPaths_AArch64FortifyConfigHasNoMissingTargets(t *testing.T) {
	chdirRepoRoot(t)
	cfg, err := config.LoadConfigWithOverrides("aarch64", "fortify")
	require.NoError(t, err)

	targetFunctions, skippedTargets, missingFiles := collectTargetFunctionsForCFGPaths(cfg.Compiler.Fuzz.CFGFilePaths, cfg.Compiler.Targets)
	require.NotEmpty(t, targetFunctions)
	require.Equal(t, 0, skippedTargets)
	require.Empty(t, missingFiles)
}
