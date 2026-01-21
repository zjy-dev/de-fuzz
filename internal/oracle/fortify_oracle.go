package oracle

import (
	"fmt"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

const (
	// DefaultMaxFillSize is the default maximum fill size for binary search.
	// 4KB is usually enough to overflow most simple stack frames.
	DefaultMaxFillSize = 4096

	// FortifySentinelMarker is printed by the seed() function before returning.
	// If this marker is present in stdout when SIGSEGV occurs, it indicates
	// a true Fortify bypass (crash on function return). If absent, the crash
	// happened inside seed() which may be a false positive (indirect crash).
	FortifySentinelMarker = "SEED_RETURNED"
)

func init() {
	Register("fortify", NewFortifyOracle)
}

// FortifyOracle implements an oracle for detecting _FORTIFY_SOURCE bypasses.
// It uses a binary search approach to find if there's a fill size that
// causes SIGSEGV (ret modified) before SIGABRT (__chk_fail).
//
// Key difference from CanaryOracle:
// - Fortify is proactive: it checks bounds BEFORE/DURING the copy operation
// - Canary is reactive: it checks the canary AFTER the overflow, before return
// - We must compile with -fno-stack-protector to isolate Fortify's behavior
type FortifyOracle struct {
	MaxFillSize    int
	DefaultBufSize int // Default buffer size for buf_size parameter
}

// NewFortifyOracle creates a new Fortify-detection oracle.
func NewFortifyOracle(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error) {
	maxFillSize := DefaultMaxFillSize
	bufSize := 64 // Default buffer size for buf_size parameter

	// Parse options
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

// Analyze uses binary search to detect _FORTIFY_SOURCE bypasses.
// It requires ctx.Executor and ctx.BinaryPath to be set.
//
// Detection logic:
// - SIGABRT (134): Fortify's __chk_fail triggered -> SAFE
// - SIGSEGV (139) with sentinel: Return address corrupted -> VULNERABLE
// - SIGSEGV (139) without sentinel: Indirect crash -> Ignore (false positive)
func (o *FortifyOracle) Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error) {
	// Validate context
	if ctx == nil || ctx.Executor == nil || ctx.BinaryPath == "" {
		return nil, fmt.Errorf("fortify oracle requires AnalyzeContext with Executor and BinaryPath")
	}

	// Binary search for the minimum crash size
	minCrashSize, crashExitCode, hasSentinel := o.binarySearchCrash(ctx)

	// If no crash found, either:
	// 1. The buffer is too small to reach return address
	// 2. Fortify is not enabled (no protection, no crash)
	// 3. The max_fill_size is too small
	if minCrashSize == -1 {
		return nil, nil
	}

	// Analyze the crash type
	switch crashExitCode {
	case ExitCodeSIGSEGV:
		// SIGSEGV can be either true Fortify bypass or false positive (indirect crash)
		// True bypass: seed() returned (sentinel present) then crashed on return
		// False positive: crashed inside seed() due to corrupted local variables
		if !hasSentinel {
			// No sentinel = crash happened inside seed(), likely false positive
			// This can happen when buffer overflow corrupts local variables
			// (e.g., fill_size parameter) causing indirect crashes
			return nil, nil
		}
		// Sentinel present = seed() returned, crash on function return = true bypass
		return &Bug{
			Seed:    s,
			Results: results,
			Description: fmt.Sprintf(
				"_FORTIFY_SOURCE bypass detected! Buffer overflow at fill_size %d bytes (buf_size=%d) caused SIGSEGV (exit code %d) "+
					"instead of SIGABRT. This indicates Fortify failed to detect the overflow and the return address was modified. "+
					"Possible causes: VLA/alloca usage, complex pointer aliasing, or compiler optimization issues.",
				minCrashSize, o.DefaultBufSize, crashExitCode,
			),
		}, nil

	case ExitCodeSIGBUS:
		// SIGBUS also indicates return address corruption (unaligned jump)
		// Apply same sentinel check for consistency
		if !hasSentinel {
			return nil, nil
		}
		return &Bug{
			Seed:    s,
			Results: results,
			Description: fmt.Sprintf(
				"_FORTIFY_SOURCE bypass detected! Buffer overflow at fill_size %d bytes (buf_size=%d) caused SIGBUS (exit code %d) "+
					"instead of SIGABRT. This indicates Fortify failed to detect the overflow and the return address was modified to an unaligned address.",
				minCrashSize, o.DefaultBufSize, crashExitCode,
			),
		}, nil

	case ExitCodeSIGABRT:
		// SIGABRT means Fortify's __chk_fail caught the overflow - this is SAFE
		// Note: We compile with -fno-stack-protector, so this MUST be from Fortify
		return nil, nil

	default:
		// Any other crash type - apply sentinel check
		if !hasSentinel {
			return nil, nil
		}
		return &Bug{
			Seed:    s,
			Results: results,
			Description: fmt.Sprintf(
				"Potential _FORTIFY_SOURCE bypass detected! Buffer overflow at fill_size %d bytes (buf_size=%d) caused unexpected exit (exit code %d) "+
					"instead of SIGABRT. This may indicate Fortify failed to detect the overflow.",
				minCrashSize, o.DefaultBufSize, crashExitCode,
			),
		}, nil
	}
}

// binarySearchCrash performs binary search to find the minimum fill_size that causes a crash.
// Returns (minCrashSize, exitCode, hasSentinel) or (-1, 0, false) if no crash found.
// hasSentinel indicates whether the sentinel marker was present in stdout at crash time.
func (o *FortifyOracle) binarySearchCrash(ctx *AnalyzeContext) (int, int, bool) {
	L := 0
	R := o.MaxFillSize
	ans := -1
	ansExitCode := 0
	ansSentinel := false

	for L <= R {
		mid := (L + R) / 2
		// Pass both buf_size (fixed) and fill_size (binary search variable)
		bufSizeArg := fmt.Sprintf("%d", o.DefaultBufSize)
		fillSizeArg := fmt.Sprintf("%d", mid)

		exitCode, stdout, _, err := ctx.Executor.ExecuteWithArgs(ctx.BinaryPath, bufSizeArg, fillSizeArg)
		if err != nil {
			// Execution error, try larger size
			L = mid + 1
			continue
		}

		if exitCode != 0 {
			// Found a crash, record it and try smaller size
			ans = mid
			ansExitCode = exitCode
			ansSentinel = strings.Contains(stdout, FortifySentinelMarker)
			R = mid - 1
		} else {
			// No crash, try larger size
			L = mid + 1
		}
	}

	// If we found a crash, verify and get the actual exit code and sentinel status
	// (in case the binary search landed on a boundary)
	if ans != -1 {
		bufSizeArg := fmt.Sprintf("%d", o.DefaultBufSize)
		fillSizeArg := fmt.Sprintf("%d", ans)
		exitCode, stdout, _, err := ctx.Executor.ExecuteWithArgs(ctx.BinaryPath, bufSizeArg, fillSizeArg)
		if err == nil && exitCode != 0 {
			ansExitCode = exitCode
			ansSentinel = strings.Contains(stdout, FortifySentinelMarker)
		}
	}

	return ans, ansExitCode, ansSentinel
}
