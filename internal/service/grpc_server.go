// Package service implements the deterministic gRPC nodes of the agentic loop
// (build / coverage / oracle / checker-metadata). Each server is a thin adapter
// over the reused internal/ packages; none of them calls an agent or an LLM —
// determinism and zero-false-positive verdict logic live in internal/oracle and
// are not duplicated or altered here (plan FR-002, analyze C6).
package service

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"sort"

	"github.com/zjy-dev/de-fuzz/internal/compiler"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
	pb "github.com/zjy-dev/de-fuzz/internal/service/pb"
)

// defaultExecTimeoutSec bounds dynamic-checker binary executions.
const defaultExecTimeoutSec = 30

// ── CheckerMetadataService (T006) ────────────────────────────────────────

// CheckerMetadataServer exposes the SSOT checker metadata
// (internal/oracle/metadata.go) as a read-only gRPC view. The Python router
// pulls it once at startup to drive checker→ISA expansion and cheap/expensive
// routing (research R4).
type CheckerMetadataServer struct {
	pb.UnimplementedCheckerMetadataServiceServer
}

// ListCheckerMetadata returns every registered checker's routing metadata in
// deterministic (ID-sorted) order.
func (s *CheckerMetadataServer) ListCheckerMetadata(_ context.Context, _ *pb.ListCheckerMetadataRequest) (*pb.ListCheckerMetadataResponse, error) {
	all := oracle.AllCheckerMetadata()
	out := make([]*pb.CheckerMetadata, 0, len(all))
	for _, m := range all {
		out = append(out, &pb.CheckerMetadata{
			Id:             m.ID,
			ApplicableIsas: append([]string(nil), m.ApplicableISAs...),
			Mode:           string(m.Mode),
			Cost:           string(m.Cost),
			Category:       string(m.Category),
		})
	}
	return &pb.ListCheckerMetadataResponse{Checkers: out}, nil
}

// ── BuildService (T008) ──────────────────────────────────────────────────

// BuildServer compiles a seed across the (checker_id, isa) matrix expanded by
// the Python router. The ISA→toolchain mapping is config-driven; a missing ISA
// yields an error artifact rather than a crash (R8, FR-015).
type BuildServer struct {
	pb.UnimplementedBuildServiceServer
	toolchains *Toolchains
}

// NewBuildServer constructs a BuildServer over an ISA→toolchain config.
func NewBuildServer(tc *Toolchains) *BuildServer {
	return &BuildServer{toolchains: tc}
}

// Build compiles the seed once per distinct ISA in the requested cells and
// returns one artifact per cell (cells sharing an ISA share the binary).
func (s *BuildServer) Build(_ context.Context, req *pb.BuildRequest) (*pb.BuildResponse, error) {
	pbSeed := req.GetSeed()
	if pbSeed == nil {
		return nil, fmt.Errorf("build request missing seed")
	}
	sd := protoSeedToInternal(pbSeed)

	// Compile once per ISA; cache the outcome so repeated checker cells reuse it.
	type buildOutcome struct {
		binaryPath string
		success    bool
		errMsg     string
	}
	perISA := make(map[string]buildOutcome)

	artifacts := make([]*pb.BuildArtifact, 0, len(req.GetCells()))
	for _, cell := range req.GetCells() {
		isa := cell.GetIsa()
		outcome, done := perISA[isa]
		if !done {
			outcome = s.compileForISA(sd, isa)
			perISA[isa] = outcome
		}
		artifacts = append(artifacts, &pb.BuildArtifact{
			Cell:       &pb.BuildCell{CheckerId: cell.GetCheckerId(), Isa: isa},
			BinaryPath: outcome.binaryPath,
			Success:    outcome.success,
			Error:      outcome.errMsg,
		})
	}
	return &pb.BuildResponse{Artifacts: artifacts}, nil
}

// compileForISA resolves the ISA's toolchain and compiles the seed. A missing
// toolchain or a compiler failure is reported in the outcome, never panicked.
func (s *BuildServer) compileForISA(sd *seed.Seed, isa string) (out struct {
	binaryPath string
	success    bool
	errMsg     string
}) {
	tc, ok := s.toolchains.Lookup(isa)
	if !ok {
		out.errMsg = fmt.Sprintf("no toolchain configured for ISA %q", isa)
		return out
	}

	workDir, err := os.MkdirTemp("", fmt.Sprintf("defuzz-build-%s-", isa))
	if err != nil {
		out.errMsg = fmt.Sprintf("create work dir: %v", err)
		return out
	}

	cc := compiler.NewGCCCompiler(compiler.GCCCompilerConfig{
		GCCPath:          tc.GCCPath,
		WorkDir:          workDir,
		PrefixPath:       tc.Prefix,
		CFlags:           tc.CFlags,
		DisableLLMCFlags: true, // deterministic build node: no seed/LLM flags
	})

	res, err := cc.Compile(sd)
	if err != nil {
		out.errMsg = fmt.Sprintf("compile: %v", err)
		return out
	}
	if !res.Success {
		out.errMsg = fmt.Sprintf("compilation failed: %s", res.Stderr)
		return out
	}
	out.binaryPath = res.BinaryPath
	out.success = true
	return out
}

// ── OracleService (T007) ─────────────────────────────────────────────────

// OracleServer runs the configured mechanism's checkers over the built
// artifacts and returns the raw four-state verdicts. It is a thin wrapper over
// MechanismEvaluator: the zero-false-positive aggregation (NA/Error never a
// bug) lives in internal/oracle and is NOT reimplemented here (FR-019/020/021).
//
// Per the single-mechanism experiment principle, one OracleServer serves one
// defense mechanism (canary | ibt | fortify), fixed by config; selected
// checkers can only come from that mechanism's checker set.
type OracleServer struct {
	pb.UnimplementedOracleServiceServer
	mechanism  string
	options    map[string]interface{}
	toolchains *Toolchains
	timeoutSec int
}

// NewOracleServer constructs an OracleServer for a single defense mechanism.
func NewOracleServer(mechanism string, options map[string]interface{}, tc *Toolchains) *OracleServer {
	return &OracleServer{
		mechanism:  mechanism,
		options:    options,
		toolchains: tc,
		timeoutSec: defaultExecTimeoutSec,
	}
}

// artifactGroup collects the checker IDs that share one (isa, binary) so the
// mechanism's Static→Dynamic cache is reused across them in a single Evaluate.
type artifactGroup struct {
	isa        string
	binaryPath string
	checkerIDs map[string]bool
}

// Analyze evaluates each built artifact's checker against its binary and
// aggregates the verdicts. Any Fail → violated, with the first failing checker's
// deterministic evidence attached for the minimization branch (FR-025).
func (s *OracleServer) Analyze(_ context.Context, req *pb.OracleRequest) (*pb.OracleResponse, error) {
	orc, err := oracle.New(s.mechanism, s.options)
	if err != nil {
		return nil, fmt.Errorf("construct %q oracle: %w", s.mechanism, err)
	}
	evaluator, ok := orc.(oracle.MechanismEvaluator)
	if !ok {
		return nil, fmt.Errorf("oracle %q does not support per-checker evaluation", s.mechanism)
	}

	sd := protoSeedToInternal(req.GetSeed())

	// Group successful artifacts by (isa, binary) → union of checker IDs.
	groups := make(map[string]*artifactGroup)
	order := make([]string, 0)
	for _, art := range req.GetArtifacts() {
		if !art.GetSuccess() || art.GetBinaryPath() == "" {
			continue
		}
		cell := art.GetCell()
		isa := cell.GetIsa()
		key := isa + "\x00" + art.GetBinaryPath()
		g, exists := groups[key]
		if !exists {
			g = &artifactGroup{isa: isa, binaryPath: art.GetBinaryPath(), checkerIDs: map[string]bool{}}
			groups[key] = g
			order = append(order, key)
		}
		g.checkerIDs[cell.GetCheckerId()] = true
	}

	var results []*pb.InvariantResult
	for _, key := range order {
		g := groups[key]
		ctx := &oracle.AnalyzeContext{
			BinaryPath: g.binaryPath,
			Executor:   s.executorForISA(g.isa),
		}
		invResults, err := evaluator.Evaluate(sd, ctx, g.checkerIDs)
		if err != nil {
			// Surface infrastructure failure as Error verdicts for each
			// requested checker on this ISA; never a false-positive bug.
			for id := range g.checkerIDs {
				results = append(results, &pb.InvariantResult{
					Id:      id,
					Verdict: pb.Verdict_VERDICT_ERROR,
					Reason:  fmt.Sprintf("evaluate failed: %v", err),
					Isa:     g.isa,
				})
			}
			continue
		}
		for _, r := range invResults {
			results = append(results, invariantResultToProto(r, g.isa))
		}
	}

	resp := &pb.OracleResponse{Results: results}
	for _, r := range results {
		if r.GetVerdict() == pb.Verdict_VERDICT_FAIL {
			resp.Violated = true
			resp.FailingChecker = r.GetId()
			resp.FailingIsa = r.GetIsa()
			resp.Evidence = r.GetEvidence()
			break
		}
	}
	return resp, nil
}

// executorForISA returns a native or QEMU executor depending on the toolchain
// config. Static-only mechanisms ignore it; it may be nil for unconfigured ISAs.
func (s *OracleServer) executorForISA(isa string) oracle.Executor {
	tc, ok := s.toolchains.Lookup(isa)
	if !ok {
		return executor.NewOracleExecutorAdapter(s.timeoutSec)
	}
	if tc.Native || tc.QEMUPath == "" {
		return executor.NewOracleExecutorAdapter(s.timeoutSec)
	}
	return executor.NewQEMUOracleExecutorAdapter(tc.QEMUPath, tc.QEMUSysroot, s.timeoutSec)
}

// ── CoverageService (T009) ───────────────────────────────────────────────

// CoverageMeasurer is the deterministic coverage backend the gRPC node wraps.
// It is injected (rather than the seed-driven coverage.Coverage interface)
// because the proto carries built artifacts + the prior cumulative state, not a
// seed; the gcovr specifics stay in the wiring layer. A nil measurer makes
// CoverageService a deterministic no-op (echoes cumulative state, empty delta).
type CoverageMeasurer interface {
	Measure(artifacts []*pb.BuildArtifact, cumulative []byte) (newCumulative []byte, deltaJSON string, err error)
}

// CoverageServer is the sole place coverage is measured (FR-022). No agent path
// writes coverage; the orchestrator persists the returned cumulative state.
type CoverageServer struct {
	pb.UnimplementedCoverageServiceServer
	measurer CoverageMeasurer
}

// NewCoverageServer constructs a CoverageServer. A nil measurer yields a
// deterministic no-op suitable for bringing the loop up before the gcovr
// backend is configured.
func NewCoverageServer(m CoverageMeasurer) *CoverageServer {
	return &CoverageServer{measurer: m}
}

// Measure forces a coverage measurement and returns the updated cumulative
// state plus this round's delta.
func (s *CoverageServer) Measure(_ context.Context, req *pb.CoverageRequest) (*pb.CoverageResponse, error) {
	if s.measurer == nil {
		return &pb.CoverageResponse{CumulativeState: req.GetCumulativeState()}, nil
	}
	newCum, delta, err := s.measurer.Measure(req.GetArtifacts(), req.GetCumulativeState())
	if err != nil {
		return nil, fmt.Errorf("measure coverage: %w", err)
	}
	return &pb.CoverageResponse{CumulativeState: newCum, DeltaJson: delta}, nil
}

// ── helpers ──────────────────────────────────────────────────────────────

// protoSeedToInternal adapts a wire Seed to the internal seed.Seed used by the
// compiler and oracle. The internal numeric ID is a stable hash of the string
// ID, used only for compile-artifact filenames.
func protoSeedToInternal(p *pb.Seed) *seed.Seed {
	if p == nil {
		return &seed.Seed{}
	}
	return &seed.Seed{
		Meta: seed.Metadata{
			ID:       hashID(p.GetId()),
			ParentID: hashID(p.GetParentId()),
		},
		Content: p.GetSource(),
	}
}

// hashID maps a string seed ID to a stable uint64 for filename derivation.
func hashID(id string) uint64 {
	if id == "" {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return h.Sum64()
}

// invariantResultToProto converts an oracle four-state result to the wire form,
// tagging it with the ISA it was produced on.
func invariantResultToProto(r oracle.InvariantResult, isa string) *pb.InvariantResult {
	return &pb.InvariantResult{
		Id:       r.ID,
		Category: string(r.Category),
		Verdict:  verdictToProto(r.Verdict),
		Evidence: r.Evidence,
		Detail:   detailToStringMap(r.Detail),
		Reason:   r.Reason,
		Isa:      isa,
	}
}

// verdictToProto maps the internal four-state verdict to the proto enum.
func verdictToProto(v oracle.InvariantVerdict) pb.Verdict {
	switch v {
	case oracle.VerdictPass:
		return pb.Verdict_VERDICT_PASS
	case oracle.VerdictFail:
		return pb.Verdict_VERDICT_FAIL
	case oracle.VerdictNotApplicable:
		return pb.Verdict_VERDICT_NOT_APPLICABLE
	case oracle.VerdictError:
		return pb.Verdict_VERDICT_ERROR
	default:
		return pb.Verdict_VERDICT_ERROR
	}
}

// detailToStringMap renders the structured detail map to wire strings in a
// deterministic key order.
func detailToStringMap(d map[string]any) map[string]string {
	if len(d) == 0 {
		return nil
	}
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(d))
	for _, k := range keys {
		out[k] = fmt.Sprintf("%v", d[k])
	}
	return out
}
