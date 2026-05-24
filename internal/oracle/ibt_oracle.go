package oracle

import (
	"fmt"

	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

func init() {
	Register("ibt", NewIBTOracle)
}

// DefaultIBTNegativeCFlags are the seed-level CFlag tokens that, when
// applied, mark the seed as a negative control: under these flags the
// compiler is *expected* not to emit ENDBR landing pads, so any
// "unintended ENDBR" finding becomes meaningless and is downgraded by
// the polarity-applying aggregator.
var DefaultIBTNegativeCFlags = []string{
	"-fcf-protection=none",
	"-fno-cf-protection",
}

// IBTOracle is the public façade for the Intel CET / IBT
// (Indirect Branch Tracking) mechanism oracle.
//
// Internally it delegates to a `MechanismOracle` composed of a single
// static invariant checker (`UnintendedEndbrChecker`, INV-IBT-B03) at
// present. Future invariants from
// `@/home/yall/project/de-fuzz/docs/tech-docs/invariants/endbr-ibt.md`
// (e.g. INV-IBT-P01 — every globally-visible function entry must start
// with `endbr`) can be added by appending to the Checkers list.
//
// The oracle is *passive*: it does not require an Executor, only a
// compiled binary path. This matches the static analysis nature of
// "scan `.text` for byte patterns".
type IBTOracle struct {
	// NegativeCFlags is the list of seed-level flag tokens that flip
	// polarity to inverted. Defaults to `DefaultIBTNegativeCFlags` when
	// the field is left empty.
	NegativeCFlags []string
}

// NewIBTOracle constructs a new IBT oracle from a YAML options map.
//
// Schema (all optional):
//
//	negative_cflags: []string  (default DefaultIBTNegativeCFlags)
func NewIBTOracle(options map[string]interface{}, _ llm.LLM, _ *prompt.Builder, _ string) (Oracle, error) {
	o := &IBTOracle{}
	if options != nil {
		if v, ok := options["negative_cflags"]; ok {
			switch val := v.(type) {
			case []interface{}:
				for _, item := range val {
					if s, ok := item.(string); ok {
						o.NegativeCFlags = append(o.NegativeCFlags, s)
					}
				}
			case []string:
				o.NegativeCFlags = append(o.NegativeCFlags, val...)
			}
		}
	}
	if len(o.NegativeCFlags) == 0 {
		o.NegativeCFlags = append([]string(nil), DefaultIBTNegativeCFlags...)
	}
	return o, nil
}

// Analyze implements `Oracle.Analyze`. The IBT oracle is purely static;
// it does NOT require `ctx.Executor`. It does require `ctx.BinaryPath`
// because every checker eventually inspects the ELF.
func (o *IBTOracle) Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error) {
	if ctx == nil || ctx.BinaryPath == "" {
		return nil, fmt.Errorf("ibt oracle requires AnalyzeContext with BinaryPath")
	}
	return o.mechanism().Analyze(s, ctx, results)
}

// mechanism builds the MechanismOracle that backs Analyze. Exposed as a
// helper so tests / future composition layers can override the checker
// list.
func (o *IBTOracle) mechanism() *MechanismOracle {
	return &MechanismOracle{
		Name: "IBT (Intel CET indirect branch tracking)",
		Checkers: []InvariantChecker{
			// Static: scan executable sections for ENDBR opcode bytes
			// inside function bodies but not at function entries.
			// Targets DREV-2026-004 and the broader
			// INV-IBT-B03 invariant.
			&UnintendedEndbrChecker{},
		},
		Polarizer: PolarizerFunc(o.polarityFor),
	}
}

// polarityFor maps a seed to its IBT-mechanism polarity. Mirrors the
// canary oracle's `polarityFor`: any `negative_cflags` token applied to
// the seed flips polarity to inverted, indicating "this seed deliberately
// disables IBT, so a Fail is the expected result and not a bug".
func (o *IBTOracle) polarityFor(s *seed.Seed) Polarity {
	if o.isNegativeCase(s) {
		return PolarityInverted
	}
	return PolarityPositive
}

// isNegativeCase checks if the seed's flag profile or applied LLM
// CFlags mark it as a negative-control case (IBT deliberately disabled).
func (o *IBTOracle) isNegativeCase(s *seed.Seed) bool {
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
