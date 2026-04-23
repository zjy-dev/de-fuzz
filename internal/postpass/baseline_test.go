package postpass

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

func TestStripOwnedFlags(t *testing.T) {
	flags := []string{
		"-O0",
		"--sysroot=/tmp/sysroot",
		"-fstack-protector-strong",
		"--param=ssp-buffer-size=8",
		"-g",
		"-fpack-struct=1",
	}

	kept, removed := StripOwnedFlags(flags, StripRules{
		Prefixes: []string{
			"-fstack-protector",
			"--param=ssp-buffer-size=",
			"-fpack-struct",
		},
	})

	require.Equal(t, []string{"-O0", "--sysroot=/tmp/sysroot", "-g"}, kept)
	require.Equal(t, []string{"-fstack-protector-strong", "--param=ssp-buffer-size=8", "-fpack-struct=1"}, removed)
}

func TestReconstructBaselinePrefersConfigAndAppliedLLMFlags(t *testing.T) {
	record := &seed.CompilationRecord{
		ConfigCFlags:     []string{"-O0", "--sysroot=/tmp/sysroot", "-fstack-protector-strong"},
		AppliedLLMCFlags: []string{"-Wall", "-fshort-enums"},
		EffectiveFlags:   []string{"-B/toolchain", "-O0"},
		PrefixFlags:      []string{"-B/toolchain"},
	}

	baseline, removed := ReconstructBaseline(record, StripRules{
		Exact: []string{"-fshort-enums"},
		Prefixes: []string{
			"-fstack-protector",
		},
	})

	require.Equal(t, []string{"-O0", "--sysroot=/tmp/sysroot", "-Wall"}, baseline)
	require.Equal(t, []string{"-fstack-protector-strong", "-fshort-enums"}, removed)
}

func TestReconstructBaselineFallsBackToEffectiveFlags(t *testing.T) {
	record := &seed.CompilationRecord{
		EffectiveFlags: []string{
			"-B/toolchain",
			"-O2",
			"-fstack-protector-all",
			"-D_FORTIFY_SOURCE=2",
			"-g",
		},
		PrefixFlags: []string{"-B/toolchain"},
	}

	baseline, removed := ReconstructBaseline(record, StripRules{
		Prefixes: []string{
			"-fstack-protector",
			"-D_FORTIFY_SOURCE=",
		},
	})

	require.Equal(t, []string{"-O2", "-g"}, baseline)
	require.Equal(t, []string{"-fstack-protector-all", "-D_FORTIFY_SOURCE=2"}, removed)
}
