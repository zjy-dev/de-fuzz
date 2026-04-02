package postpass

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfigAndResolveStrategy(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join("..", "..", "configs", "postpass.yaml"))
	require.NoError(t, err)

	canary, err := cfg.Strategy("canary")
	require.NoError(t, err)
	require.NotNil(t, canary)
	require.Equal(t, 4, canary.Workers)

	fortify, err := cfg.Strategy("fortify")
	require.NoError(t, err)
	require.NotNil(t, fortify)
	require.Equal(t, 4, fortify.Workers)
}

func TestMaterializeCanaryAArch64(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join("..", "..", "configs", "postpass.yaml"))
	require.NoError(t, err)

	canary, err := cfg.Strategy("canary")
	require.NoError(t, err)

	combos, err := canary.Materialize("aarch64")
	require.NoError(t, err)
	require.Len(t, combos, 4*5*2*4)

	found := false
	for _, combo := range combos {
		if combo.Name == "policy-strong__threshold-8__pic_mode-default__guard_source-sysreg-off0__layout-default" {
			found = true
			require.Contains(t, combo.StrategyFlags, "-fstack-protector-strong")
			require.Contains(t, combo.StrategyFlags, "--param=ssp-buffer-size=8")
			require.Contains(t, combo.StrategyFlags, "-mstack-protector-guard-reg=tpidr_el0")
			require.Contains(t, combo.StrategyFlags, "-mstack-protector-guard-offset=0")
			require.NotContains(t, combo.StrategyFlags, "<config-provided-valid-sysreg>")
		}
	}
	require.True(t, found)
}

func TestMaterializeFortify(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join("..", "..", "configs", "postpass.yaml"))
	require.NoError(t, err)

	fortify, err := cfg.Strategy("fortify")
	require.NoError(t, err)

	combos, err := fortify.Materialize("x64")
	require.NoError(t, err)
	require.Len(t, combos, 3*3*1)

	require.Equal(t, "optimization-O1__fortify_level-fortify1__stack_protector_mode-no-stack-protector", combos[0].Name)
}

func TestMaterializeStrategyWithoutGroupsReturnsDefault(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join("..", "..", "configs", "postpass.yaml"))
	require.NoError(t, err)

	crash, err := cfg.Strategy("crash")
	require.NoError(t, err)

	combos, err := crash.Materialize("x64")
	require.NoError(t, err)
	require.Len(t, combos, 1)
	require.Equal(t, "default", combos[0].Name)
}

func TestMaterializeUnknownISAErrorsWhenStrategyHasISAOverrides(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join("..", "..", "configs", "postpass.yaml"))
	require.NoError(t, err)

	canary, err := cfg.Strategy("canary")
	require.NoError(t, err)

	_, err = canary.Materialize("mips64")
	require.Error(t, err)
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.yaml")
	require.NoError(t, os.WriteFile(path, []byte("strategies:\n  canary: ["), 0644))

	_, err := LoadConfig(path)
	require.Error(t, err)
}

func TestComboApplicableCanaryExplicitRequiresAttribute(t *testing.T) {
	combo := MaterializedCombo{
		Name: "policy-explicit__threshold-8__pic_mode-default__guard_source-default__layout-default",
		GroupValues: map[string]string{
			"policy": "explicit",
		},
	}

	require.False(t, comboApplicable("canary", combo, `void seed(int buf_size, int fill_size) {}`))
	require.True(t, comboApplicable("canary", combo, `__attribute__((stack_protect)) void seed(int buf_size, int fill_size) {}`))
}
