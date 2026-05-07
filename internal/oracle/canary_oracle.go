package oracle

import (
	"fmt"

	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

const (
	// DefaultMaxBufferSize is the default maximum buffer size for binary search.
	// 4KB is usually enough to overflow most simple stack frames.
	DefaultMaxBufferSize = 4096

	// Exit codes for crash detection
	ExitCodeSIGSEGV = 128 + 11 // 139 - Segmentation fault (ret modified)
	ExitCodeSIGABRT = 128 + 6  // 134 - Abort (canary check failed)
	ExitCodeSIGBUS  = 128 + 7  // 135 - Bus error (unaligned ret address)

	// SentinelMarker is printed by the function template after seed() returns.
	// If this marker is present in stdout when SIGSEGV occurs, it indicates
	// a true canary bypass (crash on function return). If absent, the crash
	// happened inside seed() which may be a false positive (indirect crash).
	SentinelMarker = "SEED_RETURNED"
)

func init() {
	Register("canary", NewCanaryOracle)
}

// CanaryOracle is the public façade for the stack-canary mechanism oracle.
//
// Internally it now delegates to a `MechanismOracle` composed of one or more
// `InvariantChecker`s, one per row in
// `@/home/yall/project/de-fuzz/docs/invariants/stack-canary.md`. The legacy
// fields (`MaxBufferSize`, `DefaultBufSize`, `NegativeCFlags`) are preserved
// so existing tests and config files keep working unchanged; see
// `@/home/yall/project/de-fuzz/docs/architecture/oracle-multi-invariant-redesign.md`
// §3.4 for the migration plan.
type CanaryOracle struct {
	// MaxBufferSize bounds the binary search upper end (fill_size domain).
	MaxBufferSize int
	// DefaultBufSize is passed as argv[1] to every probe (the buf_size
	// parameter in the seed template, see `docs/canary-oracle.md` §"函数模板").
	DefaultBufSize int
	// NegativeCFlags are seed-level flags whose presence flips the oracle
	// polarity: when `-fno-stack-protector` (or similar) is applied to the
	// seed, SIGSEGV is the EXPECTED outcome and not a bug. Polarity-sensitive
	// invariants get inverted; polarity-insensitive ones (like INV-SP-A01)
	// stay positive.
	NegativeCFlags []string
}

// NewCanaryOracle creates a new canary-detection oracle from a YAML options
// map. Schema:
//
//	max_buffer_size:  int  (default DefaultMaxBufferSize)
//	default_buf_size: int  (default 64)
//	negative_cflags:  []string (default empty)
func NewCanaryOracle(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error) {
	maxSize := DefaultMaxBufferSize
	bufSize := 64
	var negativeCFlags []string

	if options != nil {
		if v, ok := options["max_buffer_size"]; ok {
			switch val := v.(type) {
			case int:
				maxSize = val
			case float64:
				maxSize = int(val)
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
		if v, ok := options["negative_cflags"]; ok {
			switch val := v.(type) {
			case []interface{}:
				for _, item := range val {
					if s, ok := item.(string); ok {
						negativeCFlags = append(negativeCFlags, s)
					}
				}
			case []string:
				negativeCFlags = val
			}
		}
	}

	return &CanaryOracle{
		MaxBufferSize:  maxSize,
		DefaultBufSize: bufSize,
		NegativeCFlags: negativeCFlags,
	}, nil
}

// Analyze runs the full canary mechanism evaluation on the seed.
//
// Pre-checks (kept for backward compatibility with existing tests):
//   - nil ctx, missing Executor, or missing BinaryPath all return an error
//     (this is a contract with the fuzz engine: canary oracle is *active*
//     and refuses to silently no-op when its dependencies are absent).
//
// The actual invariant work is delegated to a freshly-built MechanismOracle.
// We construct it on every Analyze call rather than caching, because the
// configuration is cheap to assemble and per-Analyze freshness avoids
// hidden state across seeds (the dynamic-search Cache lives inside
// CheckContext, which is already per-call).
func (o *CanaryOracle) Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error) {
	if ctx == nil || ctx.Executor == nil || ctx.BinaryPath == "" {
		return nil, fmt.Errorf("canary oracle requires AnalyzeContext with Executor and BinaryPath")
	}
	return o.mechanism().Analyze(s, ctx, results)
}

// mechanism builds the MechanismOracle that backs Analyze. Exposed as a
// helper so tests / future composition layers can override the checker list.
func (o *CanaryOracle) mechanism() *MechanismOracle {
	return &MechanismOracle{
		Name: "stack canary",
		Checkers: []InvariantChecker{
			// Static (cheap, run first):
			&StackChkSymbolsChecker{},
			&MainNoCanaryChecker{},
			// Dynamic (binary search; expensive):
			&DynamicBufferSearchChecker{
				InvariantID:    "INV-SP-L01",
				MechanismLabel: "Stack canary",
				SourceURL:      "https://gcc.gnu.org/onlinedocs/gccint/Stack-Smashing-Protection.html",
				Sensitivity:    "stable",
				MaxFillSize:    o.MaxBufferSize,
				DefaultBufSize: o.DefaultBufSize,
				SentinelMarker: SentinelMarker,
			},
		},
		Polarizer: PolarizerFunc(o.polarityFor),
	}
}

// polarityFor maps a seed to its canary-mechanism polarity. Wraps the
// legacy `isNegativeCase` heuristic so the rest of the framework is
// polarity-aware without leaking canary-specific knowledge.
func (o *CanaryOracle) polarityFor(s *seed.Seed) Polarity {
	if o.isNegativeCase(s) {
		return PolarityInverted
	}
	return PolarityPositive
}

// isNegativeCase checks if the seed's CFlags or flag profile mark it as a
// negative-control case (canary protection deliberately disabled). When
// true, SIGSEGV / SIGBUS at the dynamic phase is expected behavior and the
// aggregator inverts the polarity-sensitive checkers.
//
// Kept on the public type for backward compatibility with
// `TestCanaryOracle_isNegativeCase`.
func (o *CanaryOracle) isNegativeCase(s *seed.Seed) bool {
	if s == nil {
		return false
	}
	if s.FlagProfile != nil && s.FlagProfile.IsNegativeControl {
		return true
	}
	if !s.LLMCFlagsApplied || len(o.NegativeCFlags) == 0 {
		return false
	}

	for _, seedFlag := range s.AppliedLLMCFlags {
		for _, negativeFlag := range o.NegativeCFlags {
			if seedFlag == negativeFlag {
				return true
			}
		}
	}
	return false
}

// binarySearchCrash is preserved as a thin wrapper over the
// DynamicBufferSearchChecker's internal search so that
// `TestCanaryOracle_BinarySearchAccuracy` keeps working without modification.
//
// It is no longer the canonical implementation — that lives on the checker.
// New callers should construct a `DynamicBufferSearchChecker` directly.
//
// Returns (minCrashSize, exitCode, hasSentinel); minCrashSize == -1 when
// no crash was observed within `[0, MaxBufferSize]`.
func (o *CanaryOracle) binarySearchCrash(ctx *AnalyzeContext) (int, int, bool) {
	checker := &DynamicBufferSearchChecker{
		InvariantID:    "INV-SP-L01",
		MechanismLabel: "Stack canary",
		MaxFillSize:    o.MaxBufferSize,
		DefaultBufSize: o.DefaultBufSize,
		SentinelMarker: SentinelMarker,
	}
	cctx := &CheckContext{
		BinaryPath: ctx.BinaryPath,
		Executor:   ctx.Executor,
		Cache:      make(map[string]any),
	}
	dyn := checker.binarySearchCrash(cctx)
	return dyn.MinCrashSize, dyn.CrashExitCode, dyn.HasSentinel
}
