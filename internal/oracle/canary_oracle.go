package oracle

import (
	"fmt"
	"strings"

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

// CanaryOracle implements an oracle for detecting stack canary bypasses.
// It uses a binary search approach to find if there's a buffer size that
// causes SIGSEGV (ret modified) before SIGABRT (canary check).
type CanaryOracle struct {
	MaxBufferSize  int
	DefaultBufSize int // Default buffer size for buf_size parameter
}

// NewCanaryOracle creates a new canary-detection oracle.
func NewCanaryOracle(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error) {
	maxSize := DefaultMaxBufferSize
	bufSize := 64 // Default buffer size for buf_size parameter

	// Parse options
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

// Analyze uses binary search to detect stack canary bypasses.
// It requires ctx.Executor and ctx.BinaryPath to be set.
func (o *CanaryOracle) Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error) {
	// Validate context
	if ctx == nil || ctx.Executor == nil || ctx.BinaryPath == "" {
		return nil, fmt.Errorf("canary oracle requires AnalyzeContext with Executor and BinaryPath")
	}

	// Binary search for the minimum crash size
	minCrashSize, crashExitCode, hasSentinel := o.binarySearchCrash(ctx)

	// If no crash found, the canary protection is working correctly
	// (or the buffer is too small to reach the return address)
	if minCrashSize == -1 {
		return nil, nil
	}

	// Analyze the crash type
	switch crashExitCode {
	case ExitCodeSIGSEGV:
		// SIGSEGV can be either true canary bypass or false positive (indirect crash)
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
				"Stack canary bypass detected! Buffer overflow at size %d bytes caused SIGSEGV (exit code %d) "+
					"instead of SIGABRT. This indicates the return address was modified before the canary check.",
				minCrashSize, crashExitCode,
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
				"Stack canary bypass detected! Buffer overflow at size %d bytes caused SIGBUS (exit code %d) "+
					"instead of SIGABRT. This indicates the return address was modified to an unaligned address before the canary check.",
				minCrashSize, crashExitCode,
			),
		}, nil

	case ExitCodeSIGABRT:
		// SIGABRT means canary check caught the overflow - this is SAFE
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
				"Potential stack canary bypass detected! Buffer overflow at size %d bytes caused unexpected exit (exit code %d) "+
					"instead of SIGABRT. This may indicate the return address was corrupted before the canary check.",
				minCrashSize, crashExitCode,
			),
		}, nil
	}
}

// binarySearchCrash performs binary search to find the minimum input size that causes a crash.
// Returns (minCrashSize, exitCode, hasSentinel) or (-1, 0, false) if no crash found.
// hasSentinel indicates whether the sentinel marker was present in stdout at crash time.
func (o *CanaryOracle) binarySearchCrash(ctx *AnalyzeContext) (int, int, bool) {
	L := 0
	R := o.MaxBufferSize
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
			ansSentinel = strings.Contains(stdout, SentinelMarker)
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
			ansSentinel = strings.Contains(stdout, SentinelMarker)
		}
	}

	return ans, ansExitCode, ansSentinel
}
