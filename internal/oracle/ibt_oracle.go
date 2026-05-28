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
// Internally it delegates to a `MechanismOracle` composed of the IBT
// invariant checkers from `docs/tech-docs/invariants/endbr-ibt.md`. New
// invariants from that document can be added by appending to the
// Checkers list.
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
			// INV-IBT-B01: scan executable sections for ENDBR opcode
			// bytes inside function bodies but not at function entries.
			// Targets DREV-2026-004.
			&UnintendedEndbrChecker{},
			// INV-IBT-P01: every indirect-callable function entry must
			// begin with ENDBR.
			&IndirectCallableEndbrChecker{},
			// INV-IBT-P02: instruction immediately after a setjmp call
			// site must be ENDBR.
			&SetjmpReturnEndbrChecker{},
			// INV-IBT-P03: C++ EH landing pads must start with ENDBR.
			&EHLandingPadEndbrChecker{},
			// INV-IBT-P04: IFUNC resolver entries must start with ENDBR.
			&IFUNCResolverEndbrChecker{},
			// INV-IBT-P05: GCC nested-function trampoline template must
			// start with ENDBR.
			&NestedFuncTrampolineEndbrChecker{},
			// INV-IBT-P06: NOTRACK indirect branch targets must start
			// with ENDBR (or be jump-table entries).
			&IndirectBranchTargetEndbrChecker{},
			// INV-IBT-N01: NOTRACK prefix only on jump-table indirect
			// branches with a rodata target.
			&NotrackPrefixGuardChecker{},
			// INV-IBT-N02: FineIBT signature hashes must not collide.
			&FineIBTHashCollisionChecker{},
			// INV-IBT-M01: GNU property bits and runtime arch_prctl
			// state agree that IBT is enforced.
			&IBTRuntimeEnforcementChecker{},
		},
	}
}
