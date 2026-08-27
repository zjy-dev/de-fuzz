package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zjy-dev/gcovr-json-util/v2/pkg/gcovr"

	"github.com/zjy-dev/de-fuzz/internal/compiler"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
)

// MCP read-only tools for agents (contracts/mcp-tools.md). The MCP server shares
// the same process and internal/ packages as the gRPC server but exposes no
// adjudication: every tool here is strictly read-only (FR-021). Tool-call logging
// into the blackboard is the Python client's responsibility (R5, T017).

const (
	maxSourceMatches  = 50
	maxSnippetLen     = 240
	maxSearchFileSize = 2 << 20 // 2 MiB: skip larger/binary files
)

var searchableExts = map[string]bool{
	".c": true, ".cc": true, ".cpp": true, ".cxx": true,
	".h": true, ".hh": true, ".hpp": true, ".inc": true,
	".md": true, ".def": true,
}

// SearchSourceInput is the read-only source search request.
type SearchSourceInput struct {
	Query string `json:"query" jsonschema:"substring or symbol to find in the defense implementation source"`
	Scope string `json:"scope,omitempty" jsonschema:"optional path prefix (relative to source root) to limit the search"`
}

// SourceMatch is a single hit.
type SourceMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// SearchSourceOutput holds deterministic, bounded matches.
type SearchSourceOutput struct {
	Matches []SourceMatch `json:"matches"`
}

// QueryInvariantsInput filters the SSOT checker metadata view.
type QueryInvariantsInput struct {
	CheckerID string `json:"checker_id,omitempty" jsonschema:"optional exact checker ID, e.g. INV-SP-L01"`
	Mechanism string `json:"mechanism,omitempty" jsonschema:"optional mechanism filter: canary|ibt|fortify"`
}

// CheckerMetadataView mirrors the SSOT (internal/oracle/metadata.go) for agents.
type CheckerMetadataView struct {
	ID             string   `json:"id"`
	Oracle         string   `json:"oracle"`
	Mechanism      string   `json:"mechanism"`
	Requires       []string `json:"requires"`
	ApplicableISAs []string `json:"applicable_isas"`
	Mode           string   `json:"mode"`
	Cost           string   `json:"cost"`
	Category       string   `json:"category"`
	Description    string   `json:"description"`
}

// QueryInvariantsOutput is the checker list.
type QueryInvariantsOutput struct {
	Checkers []CheckerMetadataView `json:"checkers"`
}

// NewMCPServer builds the agent-facing MCP server with the read-only tools.
// sourceRoot is the defense-implementation source tree search_source walks; an
// empty root makes search_source return no matches (never an error). mechanism
// and toolchains back the minimizer's compile_exec re-verification; a nil
// toolchains leaves compile_exec unable to build (it returns still_triggers=false
// rather than crashing, R8).
func NewMCPServer(sourceRoot, mechanism string, toolchains *Toolchains) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "defuzz-core", Version: "v0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_source",
		Description: "Read-only search of the defense implementation source to understand its structure.",
	}, searchSourceHandler(sourceRoot))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_invariants",
		Description: "Read-only query of checker metadata (SSOT). Returns applicable_isas for semantics; the Generator selects checkers only, never ISA.",
	}, queryInvariantsHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "coverage_diff",
		Description: "Read-only diff of already-measured coverage (no measurement path). Diffs the orchestrator-supplied cumulative vs delta gcovr JSON for the feedback agent.",
	}, coverageDiffHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "creduce_run",
		Description: "Deterministic delta-debugging reduction of a source PoC (creduce). The LLM only guides; reduction itself is deterministic.",
	}, creduceRunHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "compile_exec",
		Description: "Compile + re-verify a candidate PoC still triggers the original failing checker on its ISA (guards against reducing into a different bug).",
	}, compileExecHandler(mechanism, toolchains))

	return server
}

func searchSourceHandler(sourceRoot string) mcp.ToolHandlerFor[SearchSourceInput, SearchSourceOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in SearchSourceInput) (*mcp.CallToolResult, SearchSourceOutput, error) {
		out := SearchSourceOutput{Matches: []SourceMatch{}}
		if sourceRoot == "" || strings.TrimSpace(in.Query) == "" {
			return nil, out, nil
		}

		root := sourceRoot
		if in.Scope != "" {
			root = filepath.Join(sourceRoot, filepath.Clean("/"+in.Scope))
		}

		matches := searchTree(ctx, sourceRoot, root, in.Query)
		out.Matches = matches
		return nil, out, nil
	}
}

func searchTree(ctx context.Context, base, root, query string) []SourceMatch {
	matches := make([]SourceMatch, 0, maxSourceMatches)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, never crash (R8)
		}
		if len(matches) >= maxSourceMatches {
			return filepath.SkipAll
		}
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		if !searchableExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.Size() > maxSearchFileSize {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			rel = path
		}
		for i, line := range strings.Split(string(data), "\n") {
			if len(matches) >= maxSourceMatches {
				break
			}
			if strings.Contains(line, query) {
				matches = append(matches, SourceMatch{
					Path:    rel,
					Line:    i + 1,
					Snippet: truncate(strings.TrimSpace(line)),
				})
			}
		}
		return nil
	})

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].Line < matches[j].Line
	})
	return matches
}

func truncate(s string) string {
	if len(s) > maxSnippetLen {
		return s[:maxSnippetLen]
	}
	return s
}

func queryInvariantsHandler(_ context.Context, _ *mcp.CallToolRequest, in QueryInvariantsInput) (*mcp.CallToolResult, QueryInvariantsOutput, error) {
	out := QueryInvariantsOutput{Checkers: []CheckerMetadataView{}}
	for _, m := range oracle.AllCheckerMetadata() {
		if in.CheckerID != "" && m.ID != in.CheckerID {
			continue
		}
		if in.Mechanism != "" && m.Mechanism != in.Mechanism && m.Oracle != in.Mechanism {
			continue
		}
		out.Checkers = append(out.Checkers, CheckerMetadataView{
			ID:             m.ID,
			Oracle:         m.Oracle,
			Mechanism:      m.Mechanism,
			Requires:       append([]string(nil), m.Requires...),
			ApplicableISAs: append([]string(nil), m.ApplicableISAs...),
			Mode:           string(m.Mode),
			Cost:           string(m.Cost),
			Category:       string(m.Category),
		})
	}
	return nil, out, nil
}

// ── coverage_diff (T033) ──────────────────────────────────────────────────

// CoverageDiffInput carries already-measured gcovr JSON reports to diff. The
// endpoint has NO measurement code path (R6/FR-022): both reports are supplied
// by the orchestrator (the coverage node already measured them). Empty inputs
// yield an empty diff, never an error.
type CoverageDiffInput struct {
	Base string `json:"base,omitempty" jsonschema:"base (cumulative) gcovr JSON report already measured by the coverage node"`
	New  string `json:"new,omitempty" jsonschema:"new gcovr JSON report already measured by the coverage node"`
}

// CoverageDiffOutput is the read-only delta summary for the feedback agent.
type CoverageDiffOutput struct {
	Delta             string `json:"delta"`
	CumulativeSummary string `json:"cumulative_summary"`
}

func coverageDiffHandler(_ context.Context, _ *mcp.CallToolRequest, in CoverageDiffInput) (*mcp.CallToolResult, CoverageDiffOutput, error) {
	out := CoverageDiffOutput{}
	if strings.TrimSpace(in.Base) == "" || strings.TrimSpace(in.New) == "" {
		return nil, out, nil // nothing to diff; agent falls back to last_delta
	}

	var base, next gcovr.GcovrReport
	if err := json.Unmarshal([]byte(in.Base), &base); err != nil {
		return nil, out, nil // malformed input is never a tool error (R8)
	}
	if err := json.Unmarshal([]byte(in.New), &next); err != nil {
		return nil, out, nil
	}

	inc, err := gcovr.ComputeCoverageIncrease(&base, &next)
	if err != nil || inc == nil {
		return nil, out, nil
	}
	out.Delta = gcovr.FormatReport(inc)

	if cov, err := gcovr.CalculateCoverage(&next); err == nil && cov != nil {
		out.CumulativeSummary = fmt.Sprintf(
			"%.2f%% lines (%d/%d)",
			cov.CoveragePercentage, cov.TotalCoveredLines, cov.TotalLines,
		)
	}
	return nil, out, nil
}

// ── creduce_run + compile_exec (T036) ─────────────────────────────────────

// CreduceRunInput is the source to reduce plus the interestingness command
// (creduce convention: a shell command that exits 0 iff the candidate is still
// interesting). The LLM only supplies semantic intent; reduction is creduce's
// deterministic delta-debugging (FR-026).
type CreduceRunInput struct {
	Source             string `json:"source" jsonschema:"C source PoC to reduce"`
	InterestingnessCmd string `json:"interestingness_cmd" jsonschema:"shell command exiting 0 while the candidate still triggers the original failing checker"`
}

// CreduceRunOutput is the reduced source. If creduce is unavailable the source
// is returned unchanged with iterations=0 (graceful, never an error — R8).
type CreduceRunOutput struct {
	ReducedSource string `json:"reduced_source"`
	Iterations    int    `json:"iterations"`
}

func creduceRunHandler(_ context.Context, _ *mcp.CallToolRequest, in CreduceRunInput) (*mcp.CallToolResult, CreduceRunOutput, error) {
	out := CreduceRunOutput{ReducedSource: in.Source}

	creducePath, err := exec.LookPath("creduce")
	if err != nil || strings.TrimSpace(in.Source) == "" || strings.TrimSpace(in.InterestingnessCmd) == "" {
		return nil, out, nil // no creduce / nothing to do: pass the source through
	}

	workDir, err := os.MkdirTemp("", "defuzz-creduce-")
	if err != nil {
		return nil, out, nil
	}
	defer os.RemoveAll(workDir)

	srcPath := filepath.Join(workDir, "poc.c")
	if err := os.WriteFile(srcPath, []byte(in.Source), 0o644); err != nil {
		return nil, out, nil
	}
	scriptPath := filepath.Join(workDir, "interesting.sh")
	script := "#!/bin/sh\n" + in.InterestingnessCmd + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return nil, out, nil
	}

	cmd := exec.Command(creducePath, scriptPath, "poc.c")
	cmd.Dir = workDir
	if err := cmd.Run(); err != nil {
		return nil, out, nil // reduction failed: keep the original source
	}

	reduced, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, out, nil
	}
	out.ReducedSource = string(reduced)
	out.Iterations = 1
	return nil, out, nil
}

// CompileExecInput re-verifies a reduced candidate still triggers the SAME
// failing checker on the same ISA (guards against reducing into a different
// bug, FR-026). checker_id pins which checker's Fail counts as "still triggers".
type CompileExecInput struct {
	Source    string `json:"source" jsonschema:"candidate C source to compile and re-verify"`
	ISA       string `json:"isa" jsonschema:"target ISA, e.g. x86_64 | aarch64"`
	CheckerID string `json:"checker_id,omitempty" jsonschema:"the original failing checker ID; still_triggers is true iff this checker fails again"`
}

// CompileExecOutput reports the build/execution result and whether the original
// bug is still triggered.
type CompileExecOutput struct {
	ExitCode      int    `json:"exit_code"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	StillTriggers bool   `json:"still_triggers"`
}

func compileExecHandler(mechanism string, toolchains *Toolchains) mcp.ToolHandlerFor[CompileExecInput, CompileExecOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in CompileExecInput) (*mcp.CallToolResult, CompileExecOutput, error) {
		out := CompileExecOutput{ExitCode: -1}

		tc, ok := toolchains.Lookup(in.ISA)
		if !ok {
			out.Stderr = fmt.Sprintf("no toolchain configured for ISA %q", in.ISA)
			return nil, out, nil
		}

		workDir, err := os.MkdirTemp("", "defuzz-mincheck-")
		if err != nil {
			out.Stderr = fmt.Sprintf("create work dir: %v", err)
			return nil, out, nil
		}
		defer os.RemoveAll(workDir)

		sd := &seed.Seed{Meta: seed.Metadata{ID: hashID("minimized")}, Content: in.Source}
		cc := compiler.NewGCCCompiler(compiler.GCCCompilerConfig{
			GCCPath:          tc.GCCPath,
			WorkDir:          workDir,
			PrefixPath:       tc.Prefix,
			CFlags:           tc.CFlags,
			DisableLLMCFlags: true,
		})
		res, err := cc.Compile(sd)
		if err != nil {
			out.Stderr = fmt.Sprintf("compile: %v", err)
			return nil, out, nil
		}
		if !res.Success {
			out.Stderr = res.Stderr
			return nil, out, nil // doesn't compile → can't still trigger
		}
		out.ExitCode = 0
		out.Stdout = res.Stdout

		// Re-run only the original failing checker; its Fail == still triggers.
		orc, err := oracle.New(mechanism, nil)
		if err != nil {
			out.Stderr = fmt.Sprintf("construct %q oracle: %v", mechanism, err)
			return nil, out, nil
		}
		evaluator, ok := orc.(oracle.MechanismEvaluator)
		if !ok {
			out.Stderr = fmt.Sprintf("oracle %q has no per-checker evaluation", mechanism)
			return nil, out, nil
		}
		allowed := map[string]bool{}
		if in.CheckerID != "" {
			allowed[in.CheckerID] = true
		}
		ctx := &oracle.AnalyzeContext{
			BinaryPath: res.BinaryPath,
			Executor:   executorFor(tc),
		}
		results, err := evaluator.Evaluate(sd, ctx, allowed)
		if err != nil {
			out.Stderr = fmt.Sprintf("evaluate: %v", err)
			return nil, out, nil
		}
		for _, r := range results {
			if r.Verdict == oracle.VerdictFail && (in.CheckerID == "" || r.ID == in.CheckerID) {
				out.StillTriggers = true
				break
			}
		}
		return nil, out, nil
	}
}

// executorFor mirrors OracleServer.executorForISA: native runs directly, others
// run under QEMU when configured.
func executorFor(tc Toolchain) oracle.Executor {
	if tc.Native || tc.QEMUPath == "" {
		return executor.NewOracleExecutorAdapter(defaultExecTimeoutSec)
	}
	return executor.NewQEMUOracleExecutorAdapter(tc.QEMUPath, tc.QEMUSysroot, defaultExecTimeoutSec)
}
