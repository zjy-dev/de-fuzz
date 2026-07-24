package oracle

import (
	"strings"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// stubChecker is a minimal InvariantChecker for aggregator tests.
type stubChecker struct {
	id       string
	category InvariantCategory
	verdict  InvariantVerdict
	evidence string
	reason   string
	// callCount is incremented every Check() call, for cache tests.
	callCount *int
}

func (s *stubChecker) ID() string                  { return s.id }
func (s *stubChecker) Category() InvariantCategory { return s.category }
func (s *stubChecker) Check(ctx *CheckContext) InvariantResult {
	if s.callCount != nil {
		*s.callCount++
	}
	return InvariantResult{
		ID:       s.id,
		Category: s.category,
		Verdict:  s.verdict,
		Evidence: s.evidence,
		Reason:   s.reason,
	}
}

// TestMechanism_NoCheckers asserts the aggregator returns nil bug when there
// are no checkers (degenerate case worth pinning down).
func TestMechanism_NoCheckers(t *testing.T) {
	m := &MechanismOracle{Name: "test"}
	bug, err := m.Analyze(&seed.Seed{}, &AnalyzeContext{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug != nil {
		t.Fatalf("expected nil bug, got %+v", bug)
	}
}

// TestMechanism_NilContext asserts the contract that ctx is required.
func TestMechanism_NilContext(t *testing.T) {
	m := &MechanismOracle{Name: "test"}
	_, err := m.Analyze(nil, nil, nil)
	if err == nil {
		t.Fatal("expected error on nil context")
	}
	if !strings.Contains(err.Error(), "AnalyzeContext") {
		t.Errorf("error message should mention AnalyzeContext, got: %v", err)
	}
}

// TestMechanism_AllPass asserts no bug when every checker passes.
func TestMechanism_AllPass(t *testing.T) {
	m := &MechanismOracle{
		Name: "test",
		Checkers: []InvariantChecker{
			&stubChecker{id: "INV-A", category: CategoryStatic, verdict: VerdictPass},
			&stubChecker{id: "INV-B", category: CategoryDynamic, verdict: VerdictPass},
		},
	}
	bug, err := m.Analyze(&seed.Seed{}, &AnalyzeContext{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug != nil {
		t.Fatalf("expected nil bug, got %+v", bug)
	}
}

// TestMechanism_OneFailReportsBug asserts OR aggregation: a single Fail in
// any phase produces a bug containing the violation ID.
func TestMechanism_OneFailReportsBug(t *testing.T) {
	m := &MechanismOracle{
		Name: "test-mech",
		Checkers: []InvariantChecker{
			&stubChecker{id: "INV-A", category: CategoryStatic, verdict: VerdictPass},
			&stubChecker{id: "INV-B", category: CategoryDynamic, verdict: VerdictFail, evidence: "boom"},
		},
	}
	bug, err := m.Analyze(&seed.Seed{}, &AnalyzeContext{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug == nil {
		t.Fatal("expected bug, got nil")
	}
	if !strings.Contains(bug.Description, "INV-B") {
		t.Errorf("description should mention INV-B, got: %s", bug.Description)
	}
	if !strings.Contains(bug.Description, "boom") {
		t.Errorf("description should mention evidence, got: %s", bug.Description)
	}
	if !strings.Contains(bug.Description, "INV-A") {
		t.Errorf("description should list passed invariants too, got: %s", bug.Description)
	}
}

// TestMechanism_ResultsForwardedToBug asserts the engine-supplied Result
// slice is forwarded into Bug.Results so downstream consumers can correlate
// dynamic execution data with the verdict.
func TestMechanism_ResultsForwardedToBug(t *testing.T) {
	want := []Result{{ExitCode: 134, Stderr: "*** stack smashing detected ***"}}
	m := &MechanismOracle{
		Name: "test",
		Checkers: []InvariantChecker{
			&stubChecker{id: "INV-X", category: CategoryDynamic, verdict: VerdictFail, evidence: "x"},
		},
	}
	bug, err := m.Analyze(&seed.Seed{}, &AnalyzeContext{}, want)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug == nil {
		t.Fatal("expected bug")
	}
	if len(bug.Results) != 1 || bug.Results[0].ExitCode != 134 {
		t.Errorf("Results not forwarded to Bug, got: %+v", bug.Results)
	}
}

// TestMechanism_NotApplicableNoBug asserts NotApplicable verdicts never
// produce a bug, even if no other checker passes.
func TestMechanism_NotApplicableNoBug(t *testing.T) {
	m := &MechanismOracle{
		Name: "test",
		Checkers: []InvariantChecker{
			&stubChecker{id: "INV-NA", category: CategoryStatic, verdict: VerdictNotApplicable, reason: "missing inspector"},
		},
	}
	bug, err := m.Analyze(&seed.Seed{}, &AnalyzeContext{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bug != nil {
		t.Fatalf("NA-only run must produce nil bug, got: %s", bug.Description)
	}
}
