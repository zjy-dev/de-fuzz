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
// Internally it delegates to a `MechanismOracle` composed of one or more
// `InvariantChecker`s, one per row in
// `docs/invariants/stack-canary.md`.
type CanaryOracle struct {
	// MaxBufferSize bounds the binary search upper end (fill_size domain).
	MaxBufferSize int
	// DefaultBufSize is passed as argv[1] to every probe (the buf_size
	// parameter in the seed template, see `docs/oracles/canary-oracle.md` §"函数模板").
	DefaultBufSize int
}

// NewCanaryOracle creates a new canary-detection oracle from a YAML options
// map. Schema:
//
//	max_buffer_size:  int  (default DefaultMaxBufferSize)
//	default_buf_size: int  (default 64)
func NewCanaryOracle(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error) {
	maxSize := DefaultMaxBufferSize
	bufSize := 64

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
	}

	return &CanaryOracle{
		MaxBufferSize:  maxSize,
		DefaultBufSize: bufSize,
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
			// INV-SP-H01: VLA / alloca seeds must produce a binary that
			// imports __stack_chk_fail. Source-vs-binary cross-check.
			&VLAAllocaInstrumentationChecker{
				InvariantID: "INV-SP-H01",
			},
			// INV-SP-V01: epilogue must compare guard *value*, not
			// guard address (GCC PR85434, ARM/Thumb). Disasm-based.
			&EpilogueGuardCompareChecker{
				InvariantID: "INV-SP-V01",
			},
			// INV-SP-S01: guard value/address must not spill to a
			// frame slot the attacker can rewrite (GCC PR85434
			// scheduling discussion, LLVM D64759). Disasm-based.
			&GuardSpillChecker{
				InvariantID: "INV-SP-S01",
			},
			// Dynamic (binary search; expensive):
			&DynamicBufferSearchChecker{
				InvariantID:    "INV-SP-L01",
				MechanismLabel: "Stack canary",
				MaxFillSize:    o.MaxBufferSize,
				DefaultBufSize: o.DefaultBufSize,
				SentinelMarker: SentinelMarker,
			},
			// Dynamic (single scrub probe; cheap relative to binary search).
			// INV-SP-S02: epilogue must clobber registers that transiently
			// held the guard. Detects the leak channel observed in
			// DREV-2026-001 on long-tail backends (loongarch64, riscv64,
			// mips, csky, xtensa, ...).
			&EpilogueCanaryScrubChecker{
				InvariantID: "INV-SP-S02",
			},
			// Dynamic (cache reader; no extra exec cost).
			// INV-SP-V02: __stack_chk_fail must be noreturn. Reads the
			// L01 binary-search cache: SIGABRT at the crash boundary
			// confirms the fail handler aborted as required.
			&StackChkFailNoreturnChecker{
				InvariantID: "INV-SP-V02",
			},
			// Dynamic (cache reader; no extra exec cost).
			// INV-SP-L02: VLA / alloca must be on the stack-low side
			// of the canary (CVE-2023-4039). Reuses L01 cache; only
			// fires when the seed actually contains VLA/alloca.
			&DynamicAllocLayoutChecker{
				InvariantID: "INV-SP-L02",
			},
			// Dynamic (cache reader; no extra exec cost).
			// INV-SP-L03: mixed vulnerable objects share a single
			// canary protection plane. Reuses L01 cache; only fires
			// when the seed has more than one flavor of vulnerable
			// object (fixed buffer + VLA / alloca, etc.).
			&MixedVulnerableObjectsChecker{
				InvariantID: "INV-SP-L03",
			},
			// Dynamic (cache reader; no extra exec cost).
			// INV-SP-L04: protector slot must not be relocated above
			// vulnerable locals (CERT VU#129209). Reuses L01 cache;
			// fires for any fixed-buffer-bearing seed.
			&ProtectorSlotRelocationChecker{
				InvariantID: "INV-SP-L04",
			},
		},
	}
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
