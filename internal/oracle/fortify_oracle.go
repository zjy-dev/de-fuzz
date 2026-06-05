package oracle

import (
	"fmt"

	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

func init() {
	Register("fortify", NewFortifyOracle)
}

// FortifyOracle is the public façade for the `_FORTIFY_SOURCE` /
// Object Size Checking mechanism oracle.
//
// It composes the eight invariants screened in
// `docs/tech-docs/invariants/fortify-source.md` §6 into a single
// `MechanismOracle`:
//
//	Static  : INV-FORT-W01, INV-FORT-C02      (symbol-level)
//	          INV-FORT-O01, INV-FORT-O02, INV-FORT-O03 (disasm)
//	Dynamic : INV-FORT-R01, INV-FORT-R02, INV-FORT-C01
//
// The oracle is positive-control only: every checker treats the
// mechanism as REQUIRED to be on, and the seed-flag filter
// (`internal/seed/defense_flags.go`) rejects seeds that would disable
// FORTIFY (`-D_FORTIFY_SOURCE=0`, `-U_FORTIFY_SOURCE`, `-O0`).
type FortifyOracle struct {
	// PrintfEntries narrows the printf-family entries swept by
	// INV-FORT-C01. Empty means "use all entries from `printfEntries`".
	// Configurable via YAML `oracle.options.fortify.printf_entries`.
	PrintfEntries []string
}

// NewFortifyOracle creates a new FORTIFY oracle from a YAML options map.
// Schema:
//
//	printf_entries: []string  (optional; default = printfEntries)
func NewFortifyOracle(options map[string]interface{}, _ llm.LLM, _ *prompt.Builder, _ string) (Oracle, error) {
	o := &FortifyOracle{}
	if options == nil {
		return o, nil
	}
	if v, ok := options["printf_entries"]; ok {
		switch xs := v.(type) {
		case []string:
			o.PrintfEntries = append(o.PrintfEntries, xs...)
		case []interface{}:
			for _, x := range xs {
				if s, ok := x.(string); ok {
					o.PrintfEntries = append(o.PrintfEntries, s)
				}
			}
		}
	}
	return o, nil
}

// Analyze implements the `Oracle` contract. Both BinaryPath (for static
// checkers) and Executor (for dynamic checkers) are required; if either
// is missing the underlying checkers return NA which folds into a
// no-bug verdict — but we keep the early error to mirror the canary
// oracle's "active oracle" discipline.
func (o *FortifyOracle) Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error) {
	if ctx == nil || ctx.BinaryPath == "" {
		return nil, fmt.Errorf("fortify oracle requires AnalyzeContext with BinaryPath")
	}
	return o.mechanism().Analyze(s, ctx, results)
}

// mechanism builds the MechanismOracle that backs Analyze. Exposed so
// tests / future composition layers can override the checker list.
func (o *FortifyOracle) mechanism() *MechanismOracle {
	return &MechanismOracle{
		Name: "_FORTIFY_SOURCE / object size checking",
		Checkers: []InvariantChecker{
			// Static, symbol-level (cheapest):
			&FortifyChkPresenceChecker{}, // INV-FORT-W01
			&ErrWarnChkChecker{},         // INV-FORT-C02
			// Static, disasm-based:
			&LastMemberObjectSizeChecker{}, // INV-FORT-O01
			&CountedByObjectSizeChecker{},  // INV-FORT-O02
			&StaleBDOSSizeChecker{},        // INV-FORT-O03
			// Dynamic:
			&FortifyReadonlyAreaChecker{},                         // INV-FORT-R01
			&FortifyChkNoreturnChecker{},                          // INV-FORT-R02
			&FortifyVfprintfFlagChecker{Entries: o.PrintfEntries}, // INV-FORT-C01
		},
	}
}
