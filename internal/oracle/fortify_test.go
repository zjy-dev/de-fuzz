package oracle

import (
	"debug/elf"
	"strings"
	"testing"
)

// TestFortifyChkPresenceChecker_Pass: binary has __memcpy_chk import → Pass.
func TestFortifyChkPresenceChecker_Pass(t *testing.T) {
	c := &FortifyChkPresenceChecker{}
	ctx := &CheckContext{
		Inspector: &fakeInspector{
			exists: true, isELF: true,
			imports: []string{"__memcpy_chk", "puts"},
			syms:    []string{"main", "seed", "__memcpy_chk"},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if r.ID != "INV-FORT-W01" {
		t.Errorf("wrong ID: %s", r.ID)
	}
}

// TestFortifyChkPresenceChecker_NA: zero chk symbols → NA with explanation.
func TestFortifyChkPresenceChecker_NA(t *testing.T) {
	c := &FortifyChkPresenceChecker{}
	ctx := &CheckContext{
		Inspector: &fakeInspector{
			exists: true, isELF: true,
			imports: []string{"puts"},
			syms:    []string{"main"},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NA, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "INV-FORT-W01") {
		t.Errorf("Reason should reference invariant ID; got %q", r.Reason)
	}
}

// TestFortifyChkPresenceChecker_NoInspector: missing inspector → NA, not panic.
func TestFortifyChkPresenceChecker_NoInspector(t *testing.T) {
	r := (&FortifyChkPresenceChecker{}).Check(&CheckContext{})
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NA, got %s", r.Verdict)
	}
}

// TestErrWarnChkChecker_FailWhenBareWithoutChk: bare err import + no chk → Fail.
func TestErrWarnChkChecker_FailWhenBareWithoutChk(t *testing.T) {
	c := &ErrWarnChkChecker{}
	ctx := &CheckContext{
		Inspector: &fakeInspector{
			exists: true, isELF: true,
			imports: []string{"err", "warn", "puts"},
			syms:    []string{"main", "seed"},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "err") {
		t.Errorf("Evidence should reference bare call: %q", r.Evidence)
	}
}

// TestErrWarnChkChecker_PassWhenChkPresent: bare + chk both present → Pass.
func TestErrWarnChkChecker_PassWhenChkPresent(t *testing.T) {
	c := &ErrWarnChkChecker{}
	ctx := &CheckContext{
		Inspector: &fakeInspector{
			exists: true, isELF: true,
			imports: []string{"err", "puts"},
			syms:    []string{"main", "__err_chk"},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass, got %s (reason=%s)", r.Verdict, r.Reason)
	}
}

// TestErrWarnChkChecker_NAWhenNoBareCall: no err/warn at all → NA.
func TestErrWarnChkChecker_NAWhenNoBareCall(t *testing.T) {
	c := &ErrWarnChkChecker{}
	ctx := &CheckContext{
		Inspector: &fakeInspector{
			exists: true, isELF: true,
			imports: []string{"puts"},
			syms:    []string{"main"},
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NA, got %s", r.Verdict)
	}
}

// TestObjsizeChecker_NAOnNonX64: O01/O02/O03 short-circuit on non-x86_64.
func TestObjsizeChecker_NAOnNonX64(t *testing.T) {
	cases := []InvariantChecker{
		&LastMemberObjectSizeChecker{},
		&CountedByObjectSizeChecker{},
		&StaleBDOSSizeChecker{},
	}
	ctx := &CheckContext{
		Inspector: &fakeInspector{
			exists: true, isELF: true,
			machine: elf.EM_AARCH64, class: elf.ELFCLASS64,
		},
	}
	for _, c := range cases {
		r := c.Check(ctx)
		if r.Verdict != VerdictNotApplicable {
			t.Errorf("%s expected NA on aarch64, got %s (reason=%s)", c.ID(), r.Verdict, r.Reason)
		}
	}
}

// TestObjsizeChecker_NAOnNoCallSites: x86_64 but no chk PLT funcs → NA.
func TestObjsizeChecker_NAOnNoCallSites(t *testing.T) {
	c := &LastMemberObjectSizeChecker{}
	ctx := &CheckContext{
		Inspector: &fakeInspector{
			exists: true, isELF: true,
			machine: elf.EM_X86_64, class: elf.ELFCLASS64,
		},
	}
	r := c.Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NA on empty x86_64 inspector, got %s (reason=%s)", r.Verdict, r.Reason)
	}
}

// fortifyMockExecutor keys responses on the argv token. Used by the
// dynamic-checker tests to drive R01/R02/C01 deterministically.
type fortifyMockExecutor struct {
	// argv-token -> (exitCode, stdout, stderr, err)
	responses map[string]fortifyResp
}

type fortifyResp struct {
	exit   int
	stdout string
	stderr string
	err    error
}

func (m *fortifyMockExecutor) ExecuteWithInput(_ string, _ string) (int, string, string, error) {
	return 0, "", "", nil
}
func (m *fortifyMockExecutor) ExecuteWithArgs(_ string, args ...string) (int, string, string, error) {
	if len(args) == 0 {
		return 0, "", "", nil
	}
	if r, ok := m.responses[args[0]]; ok {
		return r.exit, r.stdout, r.stderr, r.err
	}
	return 0, "", "", nil
}

// TestFortifyReadonlyAreaChecker_Bypass: BYPASS marker → Fail.
func TestFortifyReadonlyAreaChecker_Bypass(t *testing.T) {
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &fortifyMockExecutor{responses: map[string]fortifyResp{
			"procmaps": {exit: 0, stdout: "FORTIFY_R01_BYPASS reason=test\n"},
		}},
	}
	r := (&FortifyReadonlyAreaChecker{}).Check(ctx)
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail, got %s (reason=%s)", r.Verdict, r.Reason)
	}
}

// TestFortifyReadonlyAreaChecker_NA: NA marker → NA.
func TestFortifyReadonlyAreaChecker_NA(t *testing.T) {
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &fortifyMockExecutor{responses: map[string]fortifyResp{
			"procmaps": {exit: 0, stdout: "FORTIFY_R01_NA reason=cannot-fake\n"},
		}},
	}
	r := (&FortifyReadonlyAreaChecker{}).Check(ctx)
	if r.Verdict != VerdictNotApplicable {
		t.Fatalf("expected NA, got %s", r.Verdict)
	}
}

// TestFortifyReadonlyAreaChecker_Trapped: TRAPPED marker → Pass.
func TestFortifyReadonlyAreaChecker_Trapped(t *testing.T) {
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &fortifyMockExecutor{responses: map[string]fortifyResp{
			"procmaps": {exit: 0, stdout: "FORTIFY_R01_TRAPPED reason=ok\n"},
		}},
	}
	r := (&FortifyReadonlyAreaChecker{}).Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass, got %s", r.Verdict)
	}
}

// TestFortifyChkNoreturnChecker_FailOnReturn: FORTIFY_R02_RETURNED → Fail.
func TestFortifyChkNoreturnChecker_FailOnReturn(t *testing.T) {
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &fortifyMockExecutor{responses: map[string]fortifyResp{
			"chkfail": {exit: 0, stdout: "FORTIFY_R02_PROBE_BEGIN\nFORTIFY_R02_RETURNED tiny[0]=65\n"},
		}},
	}
	r := (&FortifyChkNoreturnChecker{}).Check(ctx)
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail, got %s (reason=%s)", r.Verdict, r.Reason)
	}
}

// TestFortifyChkNoreturnChecker_PassOnAbort: SIGABRT exit, no RETURNED → Pass.
func TestFortifyChkNoreturnChecker_PassOnAbort(t *testing.T) {
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &fortifyMockExecutor{responses: map[string]fortifyResp{
			"chkfail": {exit: 134, stdout: "FORTIFY_R02_PROBE_BEGIN\n"},
		}},
	}
	r := (&FortifyChkNoreturnChecker{}).Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass on SIGABRT, got %s (reason=%s)", r.Verdict, r.Reason)
	}
}

// TestFortifyVfprintfFlagChecker_FailOnAnyBypass: any printf entry that
// reports BYPASS → Fail (dominates).
func TestFortifyVfprintfFlagChecker_FailOnAnyBypass(t *testing.T) {
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &fortifyMockExecutor{responses: map[string]fortifyResp{
			"printf:printf":    {exit: 0, stdout: "FORTIFY_C01_TRAPPED entry=printf\n"},
			"printf:sprintf":   {exit: 0, stdout: "FORTIFY_C01_BYPASS entry=sprintf\n"},
			"printf:snprintf":  {exit: 0, stdout: "FORTIFY_C01_NA entry=snprintf reason=x\n"},
			"printf:vsnprintf": {exit: 0, stdout: "FORTIFY_C01_NA entry=vsnprintf reason=x\n"},
			"printf:syslog":    {exit: 0, stdout: "FORTIFY_C01_NA entry=syslog reason=x\n"},
		}},
	}
	r := (&FortifyVfprintfFlagChecker{}).Check(ctx)
	if r.Verdict != VerdictFail {
		t.Fatalf("expected Fail when any entry reports BYPASS, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if !strings.Contains(r.Evidence, "sprintf") {
		t.Errorf("Evidence should name the bypassing entry; got %q", r.Evidence)
	}
}

// TestFortifyVfprintfFlagChecker_PassOnAllTrapped: only TRAPPED + NA → Pass.
func TestFortifyVfprintfFlagChecker_PassOnAllTrapped(t *testing.T) {
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &fortifyMockExecutor{responses: map[string]fortifyResp{
			"printf:printf":    {stdout: "FORTIFY_C01_TRAPPED entry=printf\n"},
			"printf:sprintf":   {stdout: "FORTIFY_C01_TRAPPED entry=sprintf\n"},
			"printf:snprintf":  {stdout: "FORTIFY_C01_NA entry=snprintf reason=x\n"},
			"printf:vsnprintf": {stdout: "FORTIFY_C01_NA entry=vsnprintf reason=x\n"},
			"printf:syslog":    {stdout: "FORTIFY_C01_NA entry=syslog reason=x\n"},
		}},
	}
	r := (&FortifyVfprintfFlagChecker{}).Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass when all entries trap or NA, got %s (reason=%s)", r.Verdict, r.Reason)
	}
}

// TestFortifyVfprintfFlagChecker_NarrowEntries: Entries config narrows sweep.
func TestFortifyVfprintfFlagChecker_NarrowEntries(t *testing.T) {
	ctx := &CheckContext{
		BinaryPath: "/fake/binary",
		Executor: &fortifyMockExecutor{responses: map[string]fortifyResp{
			"printf:snprintf": {stdout: "FORTIFY_C01_TRAPPED entry=snprintf\n"},
		}},
	}
	c := &FortifyVfprintfFlagChecker{Entries: []string{"snprintf"}}
	r := c.Check(ctx)
	if r.Verdict != VerdictPass {
		t.Fatalf("expected Pass on narrowed entries, got %s (reason=%s)", r.Verdict, r.Reason)
	}
	if entries, ok := r.Detail["entries_examined"].([]string); !ok || len(entries) != 1 || entries[0] != "snprintf" {
		t.Errorf("Detail[entries_examined] should contain only the narrowed entry, got %v", r.Detail["entries_examined"])
	}
}

// TestNewFortifyOracle_PrintfEntries: option parsing for both []string
// and []interface{} (YAML decoder produces the latter).
func TestNewFortifyOracle_PrintfEntries(t *testing.T) {
	o, err := NewFortifyOracle(map[string]interface{}{
		"printf_entries": []interface{}{"printf", "snprintf"},
	}, nil, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fo := o.(*FortifyOracle)
	if len(fo.PrintfEntries) != 2 || fo.PrintfEntries[0] != "printf" || fo.PrintfEntries[1] != "snprintf" {
		t.Errorf("PrintfEntries not parsed correctly: %v", fo.PrintfEntries)
	}
}

// TestFortifyOracle_AnalyzeRequiresBinaryPath: missing BinaryPath errors out.
func TestFortifyOracle_AnalyzeRequiresBinaryPath(t *testing.T) {
	o, _ := NewFortifyOracle(nil, nil, nil, "")
	_, err := o.Analyze(nil, &AnalyzeContext{}, nil)
	if err == nil {
		t.Fatal("expected error when BinaryPath is empty")
	}
}

// TestFortifyOracle_RegisteredInRegistry: oracle.Register("fortify") wired.
func TestFortifyOracle_RegisteredInRegistry(t *testing.T) {
	o, err := New("fortify", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("expected fortify factory in registry, got error: %v", err)
	}
	if _, ok := o.(*FortifyOracle); !ok {
		t.Errorf("registry returned wrong type: %T", o)
	}
}

// TestChkSymbolFamilyName: parser maps __<family>_chk to <family>.
func TestChkSymbolFamilyName(t *testing.T) {
	cases := map[string]string{
		"__memcpy_chk":   "memcpy",
		"__snprintf_chk": "snprintf",
		"__strncpy_chk":  "strncpy",
		"memcpy":         "",
		"__memcpy":       "",
		"_chk":           "",
		"":               "",
	}
	for in, want := range cases {
		if got := chkSymbolFamilyName(in); got != want {
			t.Errorf("chkSymbolFamilyName(%q) = %q; want %q", in, got, want)
		}
	}
}
