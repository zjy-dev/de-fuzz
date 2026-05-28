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

// IBTOracle is the public façade for the Intel CET / IBT
// (Indirect Branch Tracking) mechanism oracle.
//
// Internally it delegates to a `MechanismOracle` composed of a single
// static invariant checker (`UnintendedEndbrChecker`, INV-IBT-B03) at
// present. Future invariants from `docs/tech-docs/invariants/endbr-ibt.md`
// can be added by appending to the Checkers list.
//
// The oracle is *passive*: it does not require an Executor, only a
// compiled binary path.
type IBTOracle struct{}

// NewIBTOracle constructs a new IBT oracle from a YAML options map.
// No options are currently required.
func NewIBTOracle(options map[string]interface{}, _ llm.LLM, _ *prompt.Builder, _ string) (Oracle, error) {
	return &IBTOracle{}, nil
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

// mechanism builds the MechanismOracle that backs Analyze.
func (o *IBTOracle) mechanism() *MechanismOracle {
	return &MechanismOracle{
		Name: "IBT (Intel CET indirect branch tracking)",
		Checkers: []InvariantChecker{
			// Static: scan executable sections for ENDBR opcode bytes
			// inside function bodies but not at function entries.
			// Targets DREV-2026-004 and the broader INV-IBT-B03 invariant.
			&UnintendedEndbrChecker{},
		},
	}
}
