package oracle

import (
	"fmt"

	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

const (
	// DefaultMaxFillSize is the default maximum fill size for binary search.
	// 4KB is usually enough to overflow most simple stack frames.
	DefaultMaxFillSize = 4096

	// FortifySentinelMarker is printed by the seed() function before returning.
	// Same value as `SentinelMarker`; kept as a separate constant for the
	// fortify mechanism so the two oracles can evolve their templates
	// independently if needed.
	FortifySentinelMarker = "SEED_RETURNED"
)

func init() {
	Register("fortify", NewFortifyOracle)
}

// FortifyOracle is the public façade for the `_FORTIFY_SOURCE` mechanism
// oracle. It now reuses `DynamicBufferSearchChecker` instead of duplicating
// the binary-search loop that previously lived here; see
// `@/home/yall/project/de-fuzz/docs/architecture/oracle-multi-invariant-redesign.md`
// §3.4 (the ~80-line `binarySearchCrash` block at the old
// `fortify_oracle.go:158-206` is now retired).
//
// Key difference from `CanaryOracle`:
//   - Fortify is proactive: it checks bounds BEFORE/DURING the copy operation;
//   - Canary is reactive: it checks the canary AFTER the overflow, before return;
//   - We compile fortify seeds with `-fno-stack-protector` to isolate the
//     `__chk_fail` SIGABRT (134) signal from `__stack_chk_fail`.
type FortifyOracle struct {
	MaxFillSize    int
	DefaultBufSize int // Default buffer size for buf_size parameter
}

// NewFortifyOracle creates a new Fortify-detection oracle.
func NewFortifyOracle(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error) {
	maxFillSize := DefaultMaxFillSize
	bufSize := 64 // Default buffer size for buf_size parameter

	if options != nil {
		if v, ok := options["max_fill_size"]; ok {
			switch val := v.(type) {
			case int:
				maxFillSize = val
			case float64:
				maxFillSize = int(val)
			}
		}
		if v, ok := options["default_buf_size"]; ok {
			switch val := v.(type) {
			case int:
				bufSize = val
			case float64:
				bufSize = int(val)
			}
		}
	}

	return &FortifyOracle{
		MaxFillSize:    maxFillSize,
		DefaultBufSize: bufSize,
	}, nil
}

// Analyze runs the fortify mechanism evaluation on the seed.
//
// Pre-checks (kept for backward compatibility):
//   - nil ctx, missing Executor, or missing BinaryPath all return an error
//     ("requires AnalyzeContext"). This contract is asserted by
//     TestFortifyOracle_Analyze_NilContext / MissingExecutor / MissingBinaryPath.
//
// The dynamic invariant work is delegated to `DynamicBufferSearchChecker`.
// Static fortify checkers (e.g., `__memcpy_chk` import detection mirroring
// INV-FORT-E04) can be added to the mechanism's Checkers slice without
// touching this façade.
func (o *FortifyOracle) Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error) {
	if ctx == nil || ctx.Executor == nil || ctx.BinaryPath == "" {
		return nil, fmt.Errorf("fortify oracle requires AnalyzeContext with Executor and BinaryPath")
	}
	return o.mechanism().Analyze(s, ctx, results)
}

// mechanism builds the MechanismOracle that backs Analyze. Today only the
// dynamic checker is wired in; static / enablement checkers (INV-FORT-E*,
// INV-FORT-B*) are follow-up work per the redesign doc §3.5.
func (o *FortifyOracle) mechanism() *MechanismOracle {
	return &MechanismOracle{
		Name: "_FORTIFY_SOURCE",
		Checkers: []InvariantChecker{
			&DynamicBufferSearchChecker{
				InvariantID:    "INV-FORT-L01",
				MechanismLabel: "_FORTIFY_SOURCE",
				SourceURL:      "https://gcc.gnu.org/onlinedocs/gcc/Object-Size-Checking.html",
				Sensitivity:    "stable",
				MaxFillSize:    o.MaxFillSize,
				DefaultBufSize: o.DefaultBufSize,
				SentinelMarker: FortifySentinelMarker,
			},
		},
		// Fortify currently has no negative-control flag matrix
		// configured at the mechanism level, so polarity stays positive.
		// (Fortify negative controls are typically `-D_FORTIFY_SOURCE=0`
		// or `-O0`; when those are added to the seed flag profile, wire
		// a Polarizer here similar to CanaryOracle.polarityFor.)
		Polarizer: nil,
	}
}

// Seed parameter is unused at this façade level but kept to make the type
// signature obvious for future extensions (e.g., per-seed FORTIFY level
// detection from `s.AppliedLLMCFlags`).
var _ = (*seed.Seed)(nil)
