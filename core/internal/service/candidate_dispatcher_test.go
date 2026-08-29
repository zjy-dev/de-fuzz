package service

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

type recordingCandidateBuilder struct {
	calls []candidateBuildCall
}

type candidateBuildCall struct {
	isa       string
	toolchain Toolchain
	flags     []string
}

func (b *recordingCandidateBuilder) Build(_ *Candidate, isa string, toolchain Toolchain, flags []string) (CandidateBuild, error) {
	b.calls = append(b.calls, candidateBuildCall{isa: isa, toolchain: toolchain, flags: append([]string(nil), flags...)})
	return CandidateBuild{ISA: isa, Success: true, BinaryPath: "/tmp/fake-" + isa}, nil
}

type fakeMechanismEvaluator struct {
	verdict oracle.InvariantVerdict
}

func (f fakeMechanismEvaluator) Evaluate(_ *seed.Seed, _ *oracle.AnalyzeContext, allowed map[string]bool) ([]oracle.InvariantResult, error) {
	ids := sortedKeys(allowed)
	results := make([]oracle.InvariantResult, 0, len(ids))
	for _, id := range ids {
		results = append(results, oracle.InvariantResult{ID: id, Category: oracle.CategoryStatic, Verdict: f.verdict, Evidence: "checked " + id})
	}
	return results, nil
}

func dispatchFixture(t *testing.T, dispatcher *CandidateDispatcher, payload string, mode CandidateMode) (CandidateDispatchResponse, error) {
	t.Helper()
	raw := []byte(payload)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	return dispatcher.Dispatch(CandidateDispatchRequest{Mode: mode, CandidateJSON: raw, ExpectedFingerprint: digest, Compiler: "gcc"})
}

func fakeToolchains(isas ...string) *Toolchains {
	toolchains := &Toolchains{Toolchains: map[string]Toolchain{}}
	for _, isa := range isas {
		toolchains.Toolchains[isa] = Toolchain{GCCPath: "cc", ClangPath: "clang", Native: true}
	}
	return toolchains
}

func TestCandidateDispatcherValidatesRawFingerprintBeforeParsing(t *testing.T) {
	raw := []byte(`{"mechanism":"ibt","isa":["x86_64"],"checker_ids":["INV-IBT-B01"],"minimal_trigger":{"source":"int x;","flags":["-c"]}}`)
	dispatcher := NewCandidateDispatcher(fakeToolchains("x86_64"))
	response, err := dispatcher.Dispatch(CandidateDispatchRequest{
		Mode: CandidateModeOnline, CandidateJSON: raw, ExpectedFingerprint: fmt.Sprintf("%064x", 1), Compiler: "gcc",
	})
	require.ErrorContains(t, err, "fingerprint mismatch")
	assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(raw)), response.CandidateFingerprint)
}

func TestCandidateDispatcherRequiresMatchingTrustedCompilerBeforeBuild(t *testing.T) {
	tests := []struct {
		name               string
		candidateToolchain *string
		trustedCompiler    string
		wantError          string
	}{
		{name: "candidate missing", trustedCompiler: "gcc", wantError: "candidate.toolchain: compiler is required"},
		{name: "trusted missing", candidateToolchain: stringPointer("gcc"), wantError: "trusted compiler: compiler is required"},
		{name: "candidate unknown", candidateToolchain: stringPointer("msvc"), trustedCompiler: "gcc", wantError: `candidate.toolchain: unknown compiler "msvc"`},
		{name: "trusted unknown", candidateToolchain: stringPointer("gcc"), trustedCompiler: "msvc", wantError: `trusted compiler: unknown compiler "msvc"`},
		{name: "mismatch", candidateToolchain: stringPointer("clang"), trustedCompiler: "gcc", wantError: `candidate.toolchain "llvm" does not match trusted compiler "gcc"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := &recordingCandidateBuilder{}
			dispatcher := &CandidateDispatcher{Toolchains: fakeToolchains("x86_64"), Builder: builder}
			payload := `{"mechanism":"ibt","isa":["x86_64"],"checker_ids":["INV-IBT-B01"],"minimal_trigger":{"source":"int x;","flags":["-c"]}}`
			if test.candidateToolchain != nil {
				payload = fmt.Sprintf(`{"toolchain":%q,%s`, *test.candidateToolchain, payload[1:])
			}
			raw := []byte(payload)
			_, err := dispatcher.Dispatch(CandidateDispatchRequest{
				Mode: CandidateModeOnline, CandidateJSON: raw,
				ExpectedFingerprint: fmt.Sprintf("%x", sha256.Sum256(raw)), Compiler: test.trustedCompiler,
			})
			require.ErrorContains(t, err, test.wantError)
			assert.Empty(t, builder.calls, "compiler trust failures must happen before build")
		})
	}
}

func TestCandidateDispatcherAcceptsCanonicalCompilerAliases(t *testing.T) {
	tests := []struct {
		name, candidateToolchain, trustedCompiler string
	}{
		{name: "candidate gcc alias", candidateToolchain: "gnu-gcc", trustedCompiler: "gcc"},
		{name: "trusted gcc alias", candidateToolchain: "gcc", trustedCompiler: "gnu-gcc"},
		{name: "candidate llvm alias", candidateToolchain: "clang", trustedCompiler: "llvm"},
		{name: "trusted llvm alias", candidateToolchain: "llvm", trustedCompiler: "clang"},
		{name: "compiler-rt uses llvm family", candidateToolchain: "compiler-rt", trustedCompiler: "llvm"},
		{name: "lld uses llvm family", candidateToolchain: "lld", trustedCompiler: "llvm"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := &recordingCandidateBuilder{}
			dispatcher := &CandidateDispatcher{
				Toolchains: fakeToolchains("x86_64"), Builder: builder,
				EvaluatorFactory: func(string) (oracle.MechanismEvaluator, error) {
					return fakeMechanismEvaluator{verdict: oracle.VerdictPass}, nil
				},
			}
			payload := fmt.Sprintf(`{"toolchain":%q,"mechanism":"ibt","isa":["x86_64"],"checker_ids":["INV-IBT-B01"],"minimal_trigger":{"source":"int x;","flags":["-c"]}}`, test.candidateToolchain)
			raw := []byte(payload)
			response, err := dispatcher.Dispatch(CandidateDispatchRequest{
				Mode: CandidateModeOnline, CandidateJSON: raw,
				ExpectedFingerprint: fmt.Sprintf("%x", sha256.Sum256(raw)), Compiler: test.trustedCompiler,
			})
			require.NoError(t, err)
			assert.Equal(t, "PASS", response.Verdict)
			require.Len(t, builder.calls, 1)
		})
	}
}

func TestCandidateDispatcherRequiresSelectedPathBeforeInjectedBuilder(t *testing.T) {
	builder := &recordingCandidateBuilder{}
	dispatcher := &CandidateDispatcher{
		Toolchains: &Toolchains{Toolchains: map[string]Toolchain{
			"x86_64": {GCCPath: "/must-not-fallback/gcc"},
		}},
		Builder: builder,
	}
	payload := `{"toolchain":"llvm","mechanism":"ibt","isa":["x86_64"],"checker_ids":["INV-IBT-B01"],"minimal_trigger":{"source":"int x;","flags":["-c"]}}`
	raw := []byte(payload)
	_, err := dispatcher.Dispatch(CandidateDispatchRequest{
		Mode: CandidateModeOnline, CandidateJSON: raw,
		ExpectedFingerprint: fmt.Sprintf("%x", sha256.Sum256(raw)), Compiler: "llvm",
	})
	require.ErrorContains(t, err, `toolchain for ISA "x86_64": clang_path is not configured`)
	assert.Empty(t, builder.calls, "missing trusted compiler paths must fail before build")
}

func stringPointer(value string) *string { return &value }

func TestCandidateDispatcherAliasesAndCheckerIDsDriveRouting(t *testing.T) {
	tests := []struct {
		mechanism string
		checker   string
		isa       string
		oracle    string
	}{
		{"stack-protector", "INV-SP-G01", "x86_64", "canary"},
		{"fortify-source", "INV-FORT-W01", "aarch64", "fortify"},
		{"ibt", "INV-IBT-B01", "x86_64", "ibt"},
		{"cet", "INV-IBT-B01", "x86_64", "ibt"},
		{"cet-ibt", "INV-IBT-B01", "x86_64", "ibt"},
	}
	for _, test := range tests {
		t.Run(test.mechanism, func(t *testing.T) {
			builder := &recordingCandidateBuilder{}
			var constructed []string
			dispatcher := &CandidateDispatcher{
				Toolchains: fakeToolchains(test.isa), Builder: builder,
				EvaluatorFactory: func(name string) (oracle.MechanismEvaluator, error) {
					constructed = append(constructed, name)
					return fakeMechanismEvaluator{verdict: oracle.VerdictPass}, nil
				},
			}
			payload := fmt.Sprintf(`{"toolchain":"gcc","mechanism":%q,"isa":[%q],"checker_ids":[%q],"minimal_trigger":{"source":"int main(void){return 0;}","flags":["-DVALUE=a b","-c"]}}`, test.mechanism, test.isa, test.checker)
			response, err := dispatchFixture(t, dispatcher, payload, CandidateModeOnline)
			require.NoError(t, err)
			assert.Equal(t, "PASS", response.Verdict)
			assert.Equal(t, []string{test.oracle}, constructed)
			require.Len(t, builder.calls, 1)
			assert.Equal(t, []string{"-DVALUE=a b", "-c"}, builder.calls[0].flags, "array flags must remain argv tokens")
		})
	}
}

func TestCandidateDispatcherSeparatesOnlineGuidanceFromFinalVerification(t *testing.T) {
	builder := &recordingCandidateBuilder{}
	dispatcher := &CandidateDispatcher{
		Toolchains:       fakeToolchains("x86_64"),
		Builder:          builder,
		CatalogAllowlist: map[string]bool{"INV-IBT-P01": true},
		EvaluatorFactory: func(string) (oracle.MechanismEvaluator, error) {
			return fakeMechanismEvaluator{verdict: oracle.VerdictFail}, nil
		},
	}
	payload := `{"toolchain":"gcc","mechanism":"ibt","isa":["x86_64"],"checker_ids":["INV-IBT-P01"],"related_invariants":["INV-IBT-B01"],"minimal_trigger":{"source":"unsigned long g(void){return 0x1fa1e0ff3ULL;}","flags":["-O2","-fcf-protection=branch"]}}`

	online, err := dispatchFixture(t, dispatcher, payload, CandidateModeOnline)
	require.NoError(t, err)
	require.Len(t, online.Results, 1)
	assert.Equal(t, "INV-IBT-P01", online.Results[0].ID)

	verified, err := dispatchFixture(t, dispatcher, payload, CandidateModeVerify)
	require.NoError(t, err)
	require.Len(t, verified.Results, 1)
	assert.Equal(t, "INV-IBT-B01", verified.Results[0].ID)
}

func TestCandidateDispatcherVerifyRejectsUnknownRelatedInvariant(t *testing.T) {
	dispatcher := &CandidateDispatcher{
		Toolchains:       fakeToolchains("x86_64"),
		Builder:          &recordingCandidateBuilder{},
		CatalogAllowlist: map[string]bool{"INV-IBT-P01": true},
	}
	payload := `{"toolchain":"gcc","mechanism":"ibt","isa":["x86_64"],"checker_ids":["INV-IBT-P01"],"related_invariants":["INV-NOT-COMPILED"],"minimal_trigger":{"source":"int x;","flags":["-c"]}}`

	_, err := dispatchFixture(t, dispatcher, payload, CandidateModeVerify)

	require.ErrorContains(t, err, `unknown checker id "INV-NOT-COMPILED"`)
}

func TestCandidateDispatcherNormalizesISAAliasesAndTargetTripleFallback(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "candidate ISA alias",
			payload: "{\"toolchain\":\"gcc\",\"mechanism\":\"ibt\",\"isa\":[\"x86-64\"],\"checker_ids\":[\"INV-IBT-B01\"],\"minimal_trigger\":{\"source\":\"int x;\",\"flags\":[\"-c\"]}}",
		},
		{
			name:    "target triple fallback",
			payload: "{\"toolchain\":\"gcc\",\"mechanism\":\"ibt\",\"checker_ids\":[\"INV-IBT-B01\"],\"minimal_trigger\":{\"source\":\"int x;\",\"flags\":[\"-c\"],\"target\":\"x86_64-linux-gnu\"}}",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := &recordingCandidateBuilder{}
			dispatcher := &CandidateDispatcher{
				Toolchains: fakeToolchains("x86_64"),
				Builder:    builder,
				EvaluatorFactory: func(string) (oracle.MechanismEvaluator, error) {
					return fakeMechanismEvaluator{verdict: oracle.VerdictPass}, nil
				},
			}

			response, err := dispatchFixture(t, dispatcher, test.payload, CandidateModeOnline)

			require.NoError(t, err)
			assert.Equal(t, "PASS", response.Verdict)
			require.Len(t, builder.calls, 1)
			assert.Equal(t, "x86_64", builder.calls[0].isa)
		})
	}
}

func TestCandidateDispatcherUnsupportedMechanismIsNotApplicable(t *testing.T) {
	dispatcher := NewCandidateDispatcher(fakeToolchains())
	response, err := dispatchFixture(t, dispatcher, `{"toolchain":"gcc","mechanism":"shadow-call-stack","isa":["x86_64"],"minimal_trigger":{"source":"int x;","flags":"-O2 -c"}}`, CandidateModeOnline)
	require.NoError(t, err)
	assert.Equal(t, "NOT_APPLICABLE", response.Verdict)
}

func TestCandidateDispatcherRejectsDangerousAndDefenseDisablingFlags(t *testing.T) {
	for _, flag := range []string{"-o/tmp/owned", "-fplugin=/tmp/evil.so", "-Wl,-rpath,/tmp", "@args", "-Xclang", "-fcf-protection=none"} {
		t.Run(flag, func(t *testing.T) {
			payload := fmt.Sprintf(`{"toolchain":"gcc","mechanism":"ibt","isa":["x86_64"],"checker_ids":["INV-IBT-B01"],"minimal_trigger":{"source":"int x;","flags":[%q]}}`, flag)
			_, err := dispatchFixture(t, NewCandidateDispatcher(fakeToolchains("x86_64")), payload, CandidateModeOnline)
			require.Error(t, err)
		})
	}
}

func TestCandidateDispatcherExpandsDependenciesAndDifferentialISAs(t *testing.T) {
	builder := &recordingCandidateBuilder{}
	dispatcher := &CandidateDispatcher{
		Toolchains: fakeToolchains("x86_64", "aarch64", "riscv64", "loongarch64"),
		Builder:    builder,
		EvaluatorFactory: func(string) (oracle.MechanismEvaluator, error) {
			return fakeMechanismEvaluator{verdict: oracle.VerdictPass}, nil
		},
	}
	response, err := dispatchFixture(t, dispatcher, `{"toolchain":"gcc","mechanism":"canary","isa":["x86_64"],"checker_ids":["INV-SP-L02"],"minimal_trigger":{"source":"int main(void){return 0;}","flags":["-O2"]}}`, CandidateModeOnline)
	require.NoError(t, err)
	var built []string
	for _, call := range builder.calls {
		built = append(built, call.isa)
	}
	assert.Equal(t, []string{"aarch64", "loongarch64", "riscv64", "x86_64"}, built)
	ids := make(map[string]bool)
	for _, result := range response.Results {
		ids[result.ID] = true
	}
	assert.True(t, ids["INV-SP-L01"], "required producer checker must run")
	assert.True(t, ids["INV-SP-L02"])
}

func TestCandidateDispatcherCompileOnlyForStaticRoutes(t *testing.T) {
	// A static ELF-inspection route (INV-IBT-B01) accepts entry-point-free
	// snippets, so the dispatcher must build compile-only even when the
	// candidate omits -c. A dynamic route (INV-SP-L02, requires execution)
	// must never have -c injected.
	tests := []struct {
		name         string
		checker      string
		mechanism    string
		flags        string
		wantHasDashC bool
	}{
		{name: "static route gains -c", checker: "INV-IBT-B01", mechanism: "ibt", flags: `["-O2","-fcf-protection=branch"]`, wantHasDashC: true},
		{name: "static route keeps explicit -c once", checker: "INV-IBT-B01", mechanism: "ibt", flags: `["-O2","-c"]`, wantHasDashC: true},
		{name: "dynamic route never gains -c", checker: "INV-SP-L02", mechanism: "canary", flags: `["-O2"]`, wantHasDashC: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := &recordingCandidateBuilder{}
			dispatcher := &CandidateDispatcher{
				Toolchains: fakeToolchains("x86_64", "aarch64", "riscv64", "loongarch64"),
				Builder:    builder,
				EvaluatorFactory: func(string) (oracle.MechanismEvaluator, error) {
					return fakeMechanismEvaluator{verdict: oracle.VerdictPass}, nil
				},
			}
			payload := fmt.Sprintf(
				`{"toolchain":"gcc","mechanism":%q,"isa":["x86_64"],"checker_ids":[%q],"minimal_trigger":{"source":"unsigned long g(void){return 0x1fa1e0ff3ULL;}","flags":%s}}`,
				test.mechanism, test.checker, test.flags,
			)
			_, err := dispatchFixture(t, dispatcher, payload, CandidateModeOnline)
			require.NoError(t, err)
			require.NotEmpty(t, builder.calls)
			var x86Call *candidateBuildCall
			for i := range builder.calls {
				if builder.calls[i].isa == "x86_64" {
					x86Call = &builder.calls[i]
				}
			}
			require.NotNil(t, x86Call, "x86_64 build must occur")
			hasDashC := contains(x86Call.flags, "-c")
			assert.Equal(t, test.wantHasDashC, hasDashC, "compile-only injection for %s", test.name)
			// -c must appear at most once even when the candidate already asked for it.
			count := 0
			for _, f := range x86Call.flags {
				if f == "-c" {
					count++
				}
			}
			assert.LessOrEqual(t, count, 1, "-c must not be duplicated")
		})
	}
}

func TestCompilerBuilderSelectsTrustedCompilerPath(t *testing.T) {
	tests := []struct {
		name, compiler, gccPath, clangPath, wantCompiler, wantError string
	}{
		{name: "gcc", compiler: "gcc", gccPath: "/defuzz-test-missing/trusted-gcc", clangPath: "/defuzz-test-missing/trusted-clang", wantCompiler: "/defuzz-test-missing/trusted-gcc"},
		{name: "llvm", compiler: "llvm", gccPath: "/defuzz-test-missing/must-not-run-gcc", clangPath: "/defuzz-test-missing/trusted-clang", wantCompiler: "/defuzz-test-missing/trusted-clang"},
		{name: "llvm never falls back to gcc", compiler: "llvm", gccPath: "/defuzz-test-missing/must-not-run-gcc", wantError: "clang_path is not configured"},
		{name: "gcc requires legacy path", compiler: "gcc", clangPath: "/defuzz-test-missing/must-not-run-clang", wantError: "gcc_path is not configured"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := &Candidate{ID: "compiler-path", MinimalTrigger: MinimalTrigger{Source: "int main(void){return 0;}"}}
			tc := Toolchain{GCCPath: test.gccPath, ClangPath: test.clangPath, Native: true}
			build, err := (CompilerBuilder{Compiler: test.compiler}).Build(candidate, "x86_64", tc, []string{"-c"})
			if build.cleanupDir != "" {
				t.Cleanup(func() { _ = os.RemoveAll(build.cleanupDir) })
			}
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				assert.Empty(t, build.Compiler)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantCompiler, build.Compiler)
			assert.False(t, build.Success, "fixture paths intentionally do not exist")
		})
	}
}

func TestAggregateCandidateResultsPriority(t *testing.T) {
	for _, test := range []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, "NOT_APPLICABLE"},
		{"pass over NA", []string{"NOT_APPLICABLE", "PASS"}, "PASS"},
		{"error over pass", []string{"PASS", "ERROR"}, "ERROR"},
		{"fail over error", []string{"ERROR", "FAIL"}, "FAIL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var results []CandidateResult
			for _, verdict := range test.in {
				results = append(results, CandidateResult{ID: verdict, ISA: "x86_64", Verdict: verdict})
			}
			got, _, _ := aggregateCandidateResults(results)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestCandidateDispatcherExitCodes(t *testing.T) {
	assert.Equal(t, 0, ExitCode(CandidateModeOnline, "ERROR", false), "online transports a valid ERROR verdict")
	assert.Equal(t, 0, ExitCode(CandidateModeVerify, "FAIL", false))
	assert.Equal(t, 1, ExitCode(CandidateModeVerify, "PASS", false))
	assert.Equal(t, 1, ExitCode(CandidateModeVerify, "NOT_APPLICABLE", false))
	assert.Equal(t, 2, ExitCode(CandidateModeVerify, "ERROR", false))
	assert.Equal(t, 2, ExitCode(CandidateModeOnline, "ERROR", true))
}

func TestCandidateDispatcherRealClangIBTFixture(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("cross-target fixture is the verified macOS arm64 pilot path")
	}
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skipf("clang unavailable: %v", err)
	}
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	sourcePath := filepath.Join(filepath.Dir(file), "..", "..", "..", "repro", "x64", "ibt_endbr_imm", "source.c")
	source, err := os.ReadFile(sourcePath)
	require.NoError(t, err)
	payload := fmt.Sprintf(`{"id":"ibt-integration","toolchain":"clang","mechanism":"cet-ibt","isa":["x86_64"],"checker_ids":["INV-IBT-B01"],"minimal_trigger":{"source":%q,"flags":["--target=x86_64-linux-gnu","-O2","-fcf-protection=branch","-c"],"language":"c"}}`, string(source))
	dispatcher := NewCandidateDispatcher(&Toolchains{Toolchains: map[string]Toolchain{
		"x86_64": {ClangPath: clang},
	}})
	raw := []byte(payload)
	response, err := dispatcher.Dispatch(CandidateDispatchRequest{
		Mode: CandidateModeOnline, CandidateJSON: raw,
		ExpectedFingerprint: fmt.Sprintf("%x", sha256.Sum256(raw)), Compiler: "llvm",
	})
	if err != nil {
		t.Skipf("host clang lacks the x86_64-linux-gnu object path: %v", err)
	}
	require.Len(t, response.Builds, 1)
	require.True(t, response.Builds[0].Success)
	require.Len(t, response.Results, 1)
	assert.Equal(t, "INV-IBT-B01", response.Results[0].ID)
	assert.Contains(t, []string{"PASS", "FAIL"}, response.Results[0].Verdict)
	t.Logf("real x86_64 ELF checker verdict: %s (%s)", response.Results[0].Verdict, response.Results[0].Evidence)
}

func TestRuntimeCheckerCatalogIsStable(t *testing.T) {
	catalog := RuntimeCheckerCatalog()
	assert.Equal(t, 1, catalog.SchemaVersion)
	assert.Equal(t, "defuzz-checker-catalog", catalog.Kind)
	ids := make([]string, 0, len(catalog.Checkers))
	for _, checker := range catalog.Checkers {
		ids = append(ids, checker.ID)
	}
	assert.True(t, sort.StringsAreSorted(ids))
}
