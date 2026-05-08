package oracle

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// MechanismOracle is the per-defense-mechanism aggregator that runs a list of
// `InvariantChecker`s and folds their `InvariantResult`s into a single
// `*Bug` for the existing `Oracle.Analyze` contract.
//
// One instance corresponds to one row in `docs/invariants/*.md` (canary,
// cfi, scs, ...). It is what mechanism oracles (e.g., `CanaryOracle`)
// delegate to internally.
//
// Scheduling is `Enablement → Static → Dynamic`, sequentially. Static
// checkers can be parallelized later (see
// `docs/architecture/oracle-multi-invariant-redesign.md` §3.2); we keep them
// sequential here because:
//   - a single Analyze rarely runs more than ~5 static checkers;
//   - the dynamic checker (binary search via QEMU) dominates wall-clock time;
//   - parallel inspection would require synchronizing the BinaryInspector,
//     which is currently single-reader by design.
//
// The aggregation policy is "OR with enablement gating":
//   - any Enablement Fail (with PolarityPositive) → return nil bug, log
//     diagnostics (mechanism is off, not a vulnerability);
//   - any Static / Dynamic Fail (after polarity application) → bug;
//   - all others (Pass / NotApplicable / Error) → no bug.
type MechanismOracle struct {
	// Name is a human-readable mechanism label, used in bug descriptions
	// and logs (e.g., "stack canary", "_FORTIFY_SOURCE"). Should match the
	// title in `docs/invariants/*.md`.
	Name string
	// Checkers is the ordered list of invariant checkers. Order within a
	// category is preserved (so determinism is in the operator's hands).
	Checkers []InvariantChecker
	// Polarizer decides per-seed polarity. May be nil; nil means
	// PolarityPositive for every seed (no negative-control awareness).
	Polarizer Polarizer
}

// Polarizer maps a seed to a per-seed polarity. Centralizes the negative
// control decision so individual mechanism oracles don't replicate flag
// scanning. Pure function: depends only on the seed, not on results.
type Polarizer interface {
	Polarity(s *seed.Seed) Polarity
}

// PolarizerFunc is the function-shaped adapter for Polarizer.
type PolarizerFunc func(s *seed.Seed) Polarity

func (f PolarizerFunc) Polarity(s *seed.Seed) Polarity { return f(s) }

// Analyze implements the `Oracle` contract. The implementation is fully
// generic over mechanism — the only mechanism-specific knowledge is in
// `Name` (for messaging) and the `Checkers` list (for logic).
func (m *MechanismOracle) Analyze(s *seed.Seed, ctx *AnalyzeContext, results []Result) (*Bug, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%s mechanism oracle requires AnalyzeContext", m.Name)
	}

	polarity := PolarityPositive
	if m.Polarizer != nil {
		polarity = m.Polarizer.Polarity(s)
	}

	cctx := &CheckContext{
		Seed:       s,
		BinaryPath: ctx.BinaryPath,
		Executor:   ctx.Executor,
		Polarity:   polarity,
		Cache:      make(map[string]any),
	}
	if ctx.BinaryPath != "" {
		cctx.Inspector = NewBinaryInspector(ctx.BinaryPath)
	}

	// Phase 1: Enablement (BLOCKING).
	enablement := m.runPhase(cctx, CategoryEnablement)
	if blocked, blockReason := isEnablementBlocking(enablement, polarity); blocked {
		// Mechanism is off (or expected to be off). This is NOT a bug; we
		// surface diagnostics through the structured path but return nil.
		// Aggregator could later decide to log NA-rate metrics here.
		_ = blockReason // reserved for future logger.Warn integration
		return nil, nil
	}

	// Phase 2: Static.
	static := m.runPhase(cctx, CategoryStatic)

	// Phase 3: Dynamic.
	dynamic := m.runPhase(cctx, CategoryDynamic)

	all := make([]InvariantResult, 0, len(enablement)+len(static)+len(dynamic))
	all = append(all, enablement...)
	all = append(all, static...)
	all = append(all, dynamic...)

	// Aggregate: any Fail (post-polarity) → bug.
	violations := filterByVerdict(all, VerdictFail)
	if len(violations) == 0 {
		return nil, nil
	}

	return &Bug{
		Seed:        s,
		Results:     results,
		Description: m.formatDescription(all, violations, polarity),
	}, nil
}

// runPhase executes every checker whose Category matches `category`, in
// declaration order. Each checker's Verdict is normalized through
// applyPolarity before being returned.
func (m *MechanismOracle) runPhase(ctx *CheckContext, category InvariantCategory) []InvariantResult {
	var out []InvariantResult
	for _, c := range m.Checkers {
		if c.Category() != category {
			continue
		}
		r := c.Check(ctx)
		// Defensive: ensure ID/Category survive even if a checker forgot to
		// populate them.
		if r.ID == "" {
			r.ID = c.ID()
		}
		if r.Category == "" {
			r.Category = category
		}
		r = applyPolarity(r, ctx.Polarity)
		out = append(out, r)
	}
	return out
}

// applyPolarity inverts a Pass/Fail verdict when polarity is inverted.
// NotApplicable and Error pass through unchanged because they describe the
// checker's *capability* to assert anything, not the assertion's truth.
//
// Polarity inversion only applies to checkers that are polarity-sensitive,
// signaled by the Detail entry "polarity_sensitive: true". Checkers that
// don't set that field are treated as polarity-INsensitive (e.g.,
// "main has no canary slot" stays Pass-on-no-slot regardless of -fno-stack-protector).
//
// The default is "polarity-insensitive" so adding a new checker without
// thinking about polarity is the safer default (matches the survey's
// position that most invariants are absolute, not relative to the seed flag).
func applyPolarity(r InvariantResult, polarity Polarity) InvariantResult {
	r.PolarityApplied = polarity
	if polarity == PolarityPositive {
		return r
	}
	sensitive := false
	if v, ok := r.Detail["polarity_sensitive"]; ok {
		if b, isBool := v.(bool); isBool {
			sensitive = b
		}
	}
	if !sensitive {
		return r
	}
	switch r.Verdict {
	case VerdictPass:
		// Under inverted polarity, "the mechanism held" is itself the
		// surprise — but we don't auto-promote to Fail because that makes
		// negative controls noisy. Downgrade to NA with a clear Reason.
		r.Verdict = VerdictNotApplicable
		if r.Reason == "" {
			r.Reason = "polarity inverted: assertion expected to fail in negative control, but held"
		}
	case VerdictFail:
		// "Mechanism failed" is the expected behavior under negative
		// polarity → it's a Pass.
		r.Verdict = VerdictPass
		if r.Evidence == "" {
			r.Evidence = "expected failure observed under negative-control polarity"
		}
	}
	return r
}

// isEnablementBlocking reports whether the enablement phase failed in a way
// that should short-circuit the rest of the pipeline.
//
// Today: any VerdictFail in the enablement phase (under positive polarity)
// blocks. Under inverted polarity, Fail is expected → does not block.
func isEnablementBlocking(results []InvariantResult, polarity Polarity) (bool, string) {
	for _, r := range results {
		if r.Verdict == VerdictFail && polarity == PolarityPositive {
			return true, fmt.Sprintf("%s: %s", r.ID, r.Evidence)
		}
	}
	return false, ""
}

// filterByVerdict returns only those results whose Verdict matches.
func filterByVerdict(rs []InvariantResult, want InvariantVerdict) []InvariantResult {
	var out []InvariantResult
	for _, r := range rs {
		if r.Verdict == want {
			out = append(out, r)
		}
	}
	return out
}

// formatDescription renders the structured Bug.Description text consumed by
// downstream metadata (`Seed.Metadata.BugDescription`). Format is stable so
// tests / log parsers can rely on it; see the canonical sample in
// `docs/architecture/oracle-multi-invariant-redesign.md` §3.3.
func (m *MechanismOracle) formatDescription(all, violations []InvariantResult, polarity Polarity) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %d invariant violation(s) detected (polarity=%s).\n",
		m.Name, len(violations), polarity)

	// Violations: full detail.
	b.WriteString("\nViolations:\n")
	sortedViol := append([]InvariantResult(nil), violations...)
	sort.Slice(sortedViol, func(i, j int) bool { return sortedViol[i].ID < sortedViol[j].ID })
	for _, r := range sortedViol {
		fmt.Fprintf(&b, "  - %s (%s)\n", r.ID, r.Category)
		if r.Evidence != "" {
			fmt.Fprintf(&b, "      Evidence: %s\n", r.Evidence)
		}
		if len(r.Detail) > 0 {
			fmt.Fprintf(&b, "      Detail: %s\n", formatDetail(r.Detail))
		}
		if r.SourceURL != "" {
			fmt.Fprintf(&b, "      Source: %s\n", r.SourceURL)
		}
		if r.Sensitivity != "" {
			fmt.Fprintf(&b, "      Sensitivity: %s\n", r.Sensitivity)
		}
	}

	// Compact summary of the rest.
	if passed := formatInvariantList(all, VerdictPass); passed != "" {
		fmt.Fprintf(&b, "\nPassed: %s\n", passed)
	}
	if na := buildNAList(all); na != "" {
		fmt.Fprintf(&b, "\nNot applicable:\n%s", na)
	}
	if errs := buildErrorList(all); errs != "" {
		fmt.Fprintf(&b, "\nErrors:\n%s", errs)
	}

	return b.String()
}

// formatDetail renders a Detail map as a deterministic key=value list.
func formatDetail(d map[string]any) string {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, d[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func buildNAList(rs []InvariantResult) string {
	var b strings.Builder
	for _, r := range rs {
		if r.Verdict != VerdictNotApplicable {
			continue
		}
		fmt.Fprintf(&b, "  - %s: %s\n", r.ID, fallback(r.Reason, "no reason given"))
	}
	return b.String()
}

func buildErrorList(rs []InvariantResult) string {
	var b strings.Builder
	for _, r := range rs {
		if r.Verdict != VerdictError {
			continue
		}
		fmt.Fprintf(&b, "  - %s: %s\n", r.ID, fallback(r.Reason, "unknown error"))
	}
	return b.String()
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
