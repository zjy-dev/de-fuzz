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
	minCrashSize, crashExitCode := o.binarySearchCrash(ctx)

	// If no crash found, the canary protection is working correctly
	// (or the buffer is too small to reach the return address)
	if minCrashSize == -1 {
		return nil, nil
	}

	// Analyze the crash type
	switch crashExitCode {
	case ExitCodeSIGSEGV:
		// SIGSEGV at crash point means return address was modified
		// before canary was checked - this is a BUG!
		return &Bug{
			Seed:    s,
			Results: results,
			Description: fmt.Sprintf(
				"Stack canary bypass detected! Buffer overflow at size %d bytes caused SIGSEGV (exit code %d) "+
					"instead of SIGABRT. This indicates the return address was modified before the canary check.",
				minCrashSize, crashExitCode,
			),
		}, nil

	case ExitCodeSIGABRT:
		// SIGABRT means canary check caught the overflow - this is SAFE
		return nil, nil

	default:
		// Other crash types - might be worth investigating but not a canary bypass
		return nil, nil
	}
}

// binarySearchCrash performs binary search to find the minimum input size that causes a crash.
// Returns (minCrashSize, exitCode) or (-1, 0) if no crash found.
func (o *CanaryOracle) binarySearchCrash(ctx *AnalyzeContext) (int, int) {
	L := 0
	R := o.MaxBufferSize
	ans := -1
	ansExitCode := 0

	for L <= R {
		mid := (L + R) / 2
		// Pass both buf_size (fixed) and fill_size (binary search variable)
		bufSizeArg := fmt.Sprintf("%d", o.DefaultBufSize)
		fillSizeArg := fmt.Sprintf("%d", mid)

		exitCode, _, _, err := ctx.Executor.ExecuteWithArgs(ctx.BinaryPath, bufSizeArg, fillSizeArg)
		if err != nil {
			// Execution error, try larger size
			L = mid + 1
			continue
		}

		if exitCode != 0 {
			// Found a crash, record it and try smaller size
			ans = mid
			ansExitCode = exitCode
			R = mid - 1
		} else {
			// No crash, try larger size
			L = mid + 1
		}
	}

	// If we found a crash, verify and get the actual exit code
	// (in case the binary search landed on a boundary)
	if ans != -1 {
		bufSizeArg := fmt.Sprintf("%d", o.DefaultBufSize)
		fillSizeArg := fmt.Sprintf("%d", ans)
		exitCode, _, _, err := ctx.Executor.ExecuteWithArgs(ctx.BinaryPath, bufSizeArg, fillSizeArg)
		if err == nil && exitCode != 0 {
			ansExitCode = exitCode
		}
	}

	return ans, ansExitCode
}
