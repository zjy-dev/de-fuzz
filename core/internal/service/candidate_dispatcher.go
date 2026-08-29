package service

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zjy-dev/de-fuzz/internal/compiler"
	"github.com/zjy-dev/de-fuzz/internal/oracle"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	executor "github.com/zjy-dev/de-fuzz/internal/seed_executor"
)

// CandidateMode controls the exit-code interpretation applied by the CLI.
type CandidateMode string

const (
	CandidateModeOnline  CandidateMode = "online"
	CandidateModeVerify  CandidateMode = "verify"
	CandidateModeCatalog CandidateMode = "catalog"
)

// Candidate is the trusted subset of an audit candidate consumed by Go.
// Unknown JSON fields remain covered by the raw-byte fingerprint but are not
// executable configuration.
type Candidate struct {
	ID                string         `json:"id"`
	Toolchain         string         `json:"toolchain"`
	Mechanism         string         `json:"mechanism"`
	ISA               StringList     `json:"isa"`
	CheckerIDs        StringList     `json:"checker_ids"`
	RelatedInvariants StringList     `json:"related_invariants"`
	MinimalTrigger    MinimalTrigger `json:"minimal_trigger"`
}

// MinimalTrigger is the only candidate section used to build a fixture.
type MinimalTrigger struct {
	Source   string     `json:"source"`
	Flags    StringList `json:"flags"`
	ISA      StringList `json:"isa"`
	Target   string     `json:"target"`
	Language string     `json:"language"`
}

// UnmarshalJSON preserves array elements as individual argv tokens while
// supporting the legacy scalar flag string with conservative quote parsing.
func (m *MinimalTrigger) UnmarshalJSON(data []byte) error {
	type triggerWire struct {
		Source   string          `json:"source"`
		Flags    json.RawMessage `json:"flags"`
		ISA      StringList      `json:"isa"`
		Target   string          `json:"target"`
		Language string          `json:"language"`
	}
	var wire triggerWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	m.Source, m.ISA, m.Target, m.Language = wire.Source, wire.ISA, wire.Target, wire.Language
	if len(wire.Flags) == 0 || bytes.Equal(wire.Flags, []byte("null")) {
		return nil
	}
	var values []string
	if err := json.Unmarshal(wire.Flags, &values); err == nil {
		m.Flags = values
		return nil
	}
	var scalar string
	if err := json.Unmarshal(wire.Flags, &scalar); err != nil {
		return fmt.Errorf("minimal_trigger.flags must be a string or array of strings")
	}
	parsed, err := splitFlags(scalar)
	if err != nil {
		return fmt.Errorf("minimal_trigger.flags: %w", err)
	}
	m.Flags = parsed
	return nil
}

// StringList accepts either the report schema's scalar shorthand or an array.
type StringList []string

func (s *StringList) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*s = nil
		return nil
	}
	var values []string
	if err := json.Unmarshal(data, &values); err == nil {
		*s = values
		return nil
	}
	var scalar string
	if err := json.Unmarshal(data, &scalar); err != nil {
		return fmt.Errorf("must be a string or array of strings")
	}
	*s = []string{scalar}
	return nil
}

// CandidateDispatchRequest is the reusable API input. CandidateJSON must be
// exactly the bytes whose digest was handed to the dispatcher.
type CandidateDispatchRequest struct {
	Mode                CandidateMode
	CandidateJSON       []byte
	ExpectedFingerprint string
	// Compiler is trusted orchestration input, not candidate-controlled data.
	// Dispatch requires it to match Candidate.Toolchain before any build.
	Compiler       string
	Toolchains     *Toolchains
	BundleManifest string
}

// CandidateBuild records the complete shell-free compiler invocation result.
type CandidateBuild struct {
	ISA            string   `json:"isa"`
	Success        bool     `json:"success"`
	BinaryPath     string   `json:"binary_path,omitempty"`
	Compiler       string   `json:"compiler,omitempty"`
	Args           []string `json:"args,omitempty"`
	EffectiveFlags []string `json:"effective_flags,omitempty"`
	Stdout         string   `json:"stdout,omitempty"`
	Stderr         string   `json:"stderr,omitempty"`
	Error          string   `json:"error,omitempty"`
	cleanupDir     string
}

// CandidateResult is one checker result tagged with its build ISA.
type CandidateResult struct {
	ID       string                   `json:"id"`
	ISA      string                   `json:"isa"`
	Category oracle.InvariantCategory `json:"category,omitempty"`
	Verdict  string                   `json:"verdict"`
	Evidence string                   `json:"evidence,omitempty"`
	Detail   map[string]any           `json:"detail,omitempty"`
	Reason   string                   `json:"reason,omitempty"`
}

// CandidateDispatchResponse is emitted verbatim by the CLI.
type CandidateDispatchResponse struct {
	CandidateFingerprint       string            `json:"candidate_fingerprint"`
	EchoedCandidateFingerprint string            `json:"echoed_candidate_fingerprint"`
	Verdict                    string            `json:"verdict"`
	Feedback                   string            `json:"feedback"`
	Evidence                   []string          `json:"evidence"`
	Results                    []CandidateResult `json:"results"`
	Builds                     []CandidateBuild  `json:"builds"`
	BundleManifest             string            `json:"bundle_manifest,omitempty"`
}

// CheckerCatalog is the compiled runtime routing catalog used by Part II.
type CheckerCatalog struct {
	SchemaVersion int                      `json:"schema_version"`
	Kind          string                   `json:"kind"`
	Checkers      []oracle.CheckerMetadata `json:"checkers"`
}

// CandidateBuilder is injectable for unit tests. Production uses CompilerBuilder.
type CandidateBuilder interface {
	Build(candidate *Candidate, isa string, toolchain Toolchain, flags []string) (CandidateBuild, error)
}

// EvaluatorFactory constructs the oracle implementation declared by metadata.
type EvaluatorFactory func(oracleName string) (oracle.MechanismEvaluator, error)

// CandidateDispatcher performs fingerprint validation, routing, build, and
// deterministic oracle evaluation.
type CandidateDispatcher struct {
	Toolchains       *Toolchains
	Builder          CandidateBuilder
	EvaluatorFactory EvaluatorFactory
	Metadata         []oracle.CheckerMetadata
	CatalogAllowlist map[string]bool
}

// NewCandidateDispatcher returns a production dispatcher.
func NewCandidateDispatcher(toolchains *Toolchains) *CandidateDispatcher {
	return &CandidateDispatcher{Toolchains: toolchains}
}

// RuntimeCheckerCatalog returns the deterministic catalog compiled into Go.
func RuntimeCheckerCatalog() CheckerCatalog {
	return CheckerCatalog{
		SchemaVersion: 1,
		Kind:          "defuzz-checker-catalog",
		Checkers:      oracle.AllCheckerMetadata(),
	}
}

type bundleManifestEnvelope struct {
	SchemaVersion          int      `json:"schema_version"`
	Kind                   string   `json:"kind"`
	Status                 string   `json:"status"`
	BundleID               string   `json:"bundle_id"`
	SourceRoot             string   `json:"source_root"`
	SourceRootSHA256       string   `json:"source_root_sha256"`
	SourceTreeSHA256       string   `json:"source_tree_sha256"`
	FinalTreeSHA256        string   `json:"final_tree_sha256"`
	SourceInvariantsSHA256 string   `json:"source_invariants_sha256"`
	RequestedMechanisms    []string `json:"requested_mechanisms"`
	RequestedISAs          []string `json:"requested_isas"`
	CoverageComplete       bool     `json:"coverage_complete"`
	BudgetExhausted        bool     `json:"budget_exhausted"`
	IncludedInvariantIDs   []string `json:"included_invariant_ids"`
	FailedInvariantIDs     []string `json:"failed_invariant_ids"`
	Invariants             []struct {
		InvariantID       string   `json:"invariant_id"`
		FinalStatus       string   `json:"final_status"`
		InfrastructureErr bool     `json:"infrastructure_error"`
		ParentTreeSHA256  string   `json:"parent_tree_sha256"`
		ResultTreeSHA256  string   `json:"result_tree_sha256"`
		Files             []string `json:"files"`
	} `json:"invariants"`
	Artifacts struct {
		CumulativePatch  *bundleArtifact `json:"cumulative_patch"`
		Catalog          *bundleArtifact `json:"catalog"`
		Dispatcher       *bundleArtifact `json:"dispatcher"`
		ScopedInvariants *bundleArtifact `json:"scoped_invariants"`
		InputScope       *bundleArtifact `json:"input_scope"`
	} `json:"artifacts"`
	Validation struct {
		Status string         `json:"status"`
		Build  map[string]any `json:"build"`
	} `json:"validation"`
}

type bundleArtifact struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes *int64 `json:"size_bytes"`
	Kind      string `json:"kind"`
}

type bundleCatalogEnvelope struct {
	SchemaVersion int                  `json:"schema_version"`
	Kind          string               `json:"kind"`
	Checkers      []bundleCatalogEntry `json:"checkers"`
}

type bundleCatalogEntry struct {
	ID             string                   `json:"id"`
	CheckerID      string                   `json:"checker_id"`
	InvariantID    string                   `json:"invariant_id"`
	Oracle         string                   `json:"oracle"`
	Mechanism      string                   `json:"mechanism"`
	Requires       []string                 `json:"requires"`
	ApplicableISAs []string                 `json:"applicable_isas"`
	Mode           oracle.CheckerMode       `json:"mode"`
	Cost           oracle.CheckerCost       `json:"cost"`
	Category       oracle.InvariantCategory `json:"category"`
}

// LoadBundleCatalog verifies the complete bundle trust boundary and that every
// allowlisted route is exactly the route compiled into this executable.
func LoadBundleCatalog(manifestPath, catalogPath string) (map[string]bool, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve running dispatcher: %w", err)
	}
	return loadBundleCatalog(manifestPath, catalogPath, executablePath)
}

func loadBundleCatalog(manifestPath, catalogPath, executablePath string) (map[string]bool, error) {
	manifestResolved, manifestBytes, err := readRegularNoSymlink(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("bundle manifest: %w", err)
	}
	rawManifest, err := decodeJSONObject(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("parse bundle manifest: %w", err)
	}
	if err := validateObjectKeys(rawManifest, []string{
		"schema_version", "kind", "status", "bundle_id", "source_root",
		"source_root_sha256", "source_tree_sha256", "final_tree_sha256",
		"source_invariants_sha256", "requested_mechanisms", "requested_isas",
		"coverage_complete", "budget_exhausted", "included_invariant_ids",
		"failed_invariant_ids", "invariants", "artifacts", "validation",
	}); err != nil {
		return nil, fmt.Errorf("bundle manifest: %w", err)
	}
	for _, field := range []string{"requested_mechanisms", "requested_isas"} {
		if err := validateRequestedScopeValues(field, rawManifest); err != nil {
			return nil, err
		}
	}
	if err := validateArtifactObjects(rawManifest); err != nil {
		return nil, fmt.Errorf("bundle manifest: %w", err)
	}
	var manifest bundleManifestEnvelope
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("parse bundle manifest: %w", err)
	}
	if err := validateBundleManifest(manifest, rawManifest); err != nil {
		return nil, err
	}
	root := filepath.Dir(manifestResolved)
	artifacts := []struct {
		role     string
		metadata *bundleArtifact
	}{
		{"cumulative_patch", manifest.Artifacts.CumulativePatch},
		{"catalog", manifest.Artifacts.Catalog},
		{"dispatcher", manifest.Artifacts.Dispatcher},
		{"scoped_invariants", manifest.Artifacts.ScopedInvariants},
		{"input_scope", manifest.Artifacts.InputScope},
	}
	resolved := make(map[string]string, len(artifacts))
	contents := make(map[string][]byte, len(artifacts))
	for _, artifact := range artifacts {
		path, content, artifactErr := validateBundleArtifact(root, artifact.role, artifact.metadata)
		if artifactErr != nil {
			return nil, artifactErr
		}
		for previousRole, previousPath := range resolved {
			if sameRegularFile(previousPath, path) {
				return nil, fmt.Errorf("checker-bundle artifact paths resolve to the same file: %q and %q", previousRole, artifact.role)
			}
		}
		resolved[artifact.role] = path
		contents[artifact.role] = content
	}
	catalogResolved, _, err := readRegularNoSymlink(catalogPath)
	if err != nil {
		return nil, fmt.Errorf("checker catalog argument: %w", err)
	}
	if !sameRegularFile(resolved["catalog"], catalogResolved) {
		return nil, fmt.Errorf("--catalog does not name the manifest catalog artifact")
	}
	executableResolved, _, err := readRegularNoSymlink(executablePath)
	if err != nil {
		return nil, fmt.Errorf("running dispatcher: %w", err)
	}
	if !sameRegularFile(resolved["dispatcher"], executableResolved) {
		return nil, fmt.Errorf("bundle dispatcher artifact is not the running executable")
	}

	if !utf8.Valid(contents["catalog"]) {
		return nil, fmt.Errorf("parse checker catalog: invalid UTF-8")
	}
	var catalog bundleCatalogEnvelope
	if err := json.Unmarshal(contents["catalog"], &catalog); err != nil {
		return nil, fmt.Errorf("parse checker catalog: %w", err)
	}
	if catalog.SchemaVersion != 1 || catalog.Kind != "defuzz-checker-catalog" {
		return nil, fmt.Errorf("checker catalog must be schema version 1 and kind defuzz-checker-catalog")
	}
	runtime := make(map[string]oracle.CheckerMetadata)
	for _, metadata := range oracle.AllCheckerMetadata() {
		runtime[metadata.ID] = metadata
	}
	allowlist := make(map[string]bool, len(catalog.Checkers))
	for _, entry := range catalog.Checkers {
		id, err := catalogEntryID(entry)
		if err != nil {
			return nil, err
		}
		if allowlist[id] {
			return nil, fmt.Errorf("checker catalog contains duplicate checker %q", id)
		}
		compiled, ok := runtime[id]
		if !ok {
			return nil, fmt.Errorf("checker catalog references %q, which is not compiled into the dispatcher", id)
		}
		if entry.Oracle != compiled.Oracle || entry.Mechanism != compiled.Mechanism ||
			entry.Mode != compiled.Mode || entry.Cost != compiled.Cost || entry.Category != compiled.Category ||
			!sameStringSet(entry.Requires, compiled.Requires) || !sameStringSet(entry.ApplicableISAs, compiled.ApplicableISAs) {
			return nil, fmt.Errorf("checker catalog route for %q differs from compiled metadata", id)
		}
		allowlist[id] = true
	}
	for _, entry := range catalog.Checkers {
		id, _ := catalogEntryID(entry)
		for _, required := range runtime[id].Requires {
			if !allowlist[required] {
				return nil, fmt.Errorf("checker catalog includes %q without required checker %q", id, required)
			}
		}
	}
	if !sameStringSet(sortedKeys(allowlist), uniqueStrings(manifest.IncludedInvariantIDs)) {
		return nil, fmt.Errorf("checker catalog IDs do not exactly match bundle included_invariant_ids")
	}
	return allowlist, nil
}

func validateBundleManifest(manifest bundleManifestEnvelope, raw map[string]any) error {
	if manifest.SchemaVersion != 1 || manifest.Kind != "defuzz-checker-bundle" || manifest.Status != "ready" {
		return fmt.Errorf("bundle manifest must be schema version 1, kind defuzz-checker-bundle, status ready")
	}
	if !validSHA256(manifest.BundleID) {
		return fmt.Errorf("bundle manifest bundle_id must be a lowercase SHA-256")
	}
	withoutID := make(map[string]any, len(raw)-1)
	for key, value := range raw {
		if key != "bundle_id" {
			withoutID[key] = value
		}
	}
	canonical, err := canonicalPythonJSON(withoutID)
	if err != nil {
		return fmt.Errorf("canonicalize bundle manifest: %w", err)
	}
	digest := sha256.Sum256(canonical)
	actualBundleID := hex.EncodeToString(digest[:])
	if subtle.ConstantTimeCompare([]byte(actualBundleID), []byte(manifest.BundleID)) != 1 {
		return fmt.Errorf("checker-bundle bundle_id mismatch: expected %s, got %s", manifest.BundleID, actualBundleID)
	}
	for name, value := range map[string]string{
		"source_root_sha256":       manifest.SourceRootSHA256,
		"source_tree_sha256":       manifest.SourceTreeSHA256,
		"final_tree_sha256":        manifest.FinalTreeSHA256,
		"source_invariants_sha256": manifest.SourceInvariantsSHA256,
	} {
		if !validSHA256(value) {
			return fmt.Errorf("bundle manifest %s must be a lowercase SHA-256", name)
		}
	}
	if manifest.BudgetExhausted {
		return fmt.Errorf("ready bundle must not be budget exhausted")
	}
	if manifest.Validation.Status != "passed" || manifest.Validation.Build == nil || manifest.Validation.Build["status"] != "passed" {
		return fmt.Errorf("ready bundle validation and dispatcher build must have passed")
	}
	if manifest.Artifacts.CumulativePatch == nil || manifest.Artifacts.Catalog == nil || manifest.Artifacts.Dispatcher == nil ||
		manifest.Artifacts.ScopedInvariants == nil || manifest.Artifacts.InputScope == nil {
		return fmt.Errorf("ready bundle requires cumulative_patch, catalog, dispatcher, scoped_invariants, and input_scope artifacts")
	}
	if hasDuplicates(manifest.IncludedInvariantIDs) || hasDuplicates(manifest.FailedInvariantIDs) {
		return fmt.Errorf("bundle invariant ID lists must not contain duplicates")
	}
	for _, id := range manifest.IncludedInvariantIDs {
		if contains(manifest.FailedInvariantIDs, id) {
			return fmt.Errorf("bundle included and failed invariant IDs must be disjoint")
		}
	}
	var passed, failed []string
	seenInvariant := make(map[string]bool, len(manifest.Invariants))
	hasIncomplete := false
	for _, invariant := range manifest.Invariants {
		if invariant.InvariantID == "" || seenInvariant[invariant.InvariantID] {
			return fmt.Errorf("bundle invariant_id values must be non-empty and unique")
		}
		seenInvariant[invariant.InvariantID] = true
		if !validSHA256(invariant.ParentTreeSHA256) || !validSHA256(invariant.ResultTreeSHA256) {
			return fmt.Errorf("bundle invariant %q has an invalid tree SHA-256", invariant.InvariantID)
		}
		if invariant.InfrastructureErr {
			return fmt.Errorf("ready bundle invariant %q has an infrastructure error", invariant.InvariantID)
		}
		switch invariant.FinalStatus {
		case "passed":
			passed = append(passed, invariant.InvariantID)
		case "failed":
			failed = append(failed, invariant.InvariantID)
			hasIncomplete = true
		case "unprocessed":
			hasIncomplete = true
			return fmt.Errorf("ready bundle invariant %q is unprocessed", invariant.InvariantID)
		default:
			return fmt.Errorf("bundle invariant %q has invalid final_status %q", invariant.InvariantID, invariant.FinalStatus)
		}
	}
	if !sameStringSet(passed, manifest.IncludedInvariantIDs) || !sameStringSet(failed, manifest.FailedInvariantIDs) {
		return fmt.Errorf("bundle invariant status rows do not match included/failed ID lists")
	}
	if len(manifest.IncludedInvariantIDs) == 0 {
		return fmt.Errorf("ready bundle must include at least one invariant")
	}
	if manifest.CoverageComplete == hasIncomplete {
		return fmt.Errorf("bundle coverage_complete is inconsistent with invariant statuses")
	}
	return nil
}

// Dispatch evaluates one raw candidate document. Input/configuration failures
// are returned as errors; checker ERROR verdicts remain ordinary responses.
func (d *CandidateDispatcher) Dispatch(req CandidateDispatchRequest) (CandidateDispatchResponse, error) {
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(req.CandidateJSON))
	response := CandidateDispatchResponse{
		CandidateFingerprint:       fingerprint,
		EchoedCandidateFingerprint: req.ExpectedFingerprint,
		Evidence:                   []string{},
		Results:                    []CandidateResult{},
		Builds:                     []CandidateBuild{},
		BundleManifest:             req.BundleManifest,
	}
	if req.Mode != CandidateModeOnline && req.Mode != CandidateModeVerify {
		return response, fmt.Errorf("unsupported dispatcher mode %q", req.Mode)
	}
	if !validSHA256(req.ExpectedFingerprint) {
		return response, fmt.Errorf("candidate fingerprint must be 64 lowercase hexadecimal characters")
	}
	if fingerprint != req.ExpectedFingerprint {
		return response, fmt.Errorf("candidate fingerprint mismatch: expected %s, got %s", req.ExpectedFingerprint, fingerprint)
	}

	var candidate Candidate
	decoder := json.NewDecoder(bytes.NewReader(req.CandidateJSON))
	if err := decoder.Decode(&candidate); err != nil {
		return response, fmt.Errorf("parse candidate JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return response, fmt.Errorf("parse candidate JSON: trailing content after the candidate object")
	}
	if candidate.MinimalTrigger.Source == "" {
		return response, fmt.Errorf("candidate minimal_trigger.source is empty")
	}
	trustedCompiler, err := normalizeCompiler(req.Compiler)
	if err != nil {
		return response, fmt.Errorf("trusted compiler: %w", err)
	}
	candidateCompiler, err := normalizeCompiler(candidate.Toolchain)
	if err != nil {
		return response, fmt.Errorf("candidate.toolchain: %w", err)
	}
	if candidateCompiler != trustedCompiler {
		return response, fmt.Errorf("candidate.toolchain %q does not match trusted compiler %q", candidateCompiler, trustedCompiler)
	}
	flags, err := normalizeCandidateFlags(candidate.MinimalTrigger.Flags)
	if err != nil {
		return response, err
	}
	if len(flags) == 0 {
		return response, fmt.Errorf("candidate minimal_trigger.flags is empty")
	}
	if _, err := languageFlags(candidate.MinimalTrigger.Language); err != nil {
		return response, err
	}

	routes, unsupported, err := d.resolveRoutes(&candidate, req.Mode)
	if err != nil {
		return response, err
	}
	if unsupported {
		response.Verdict = "NOT_APPLICABLE"
		response.Feedback = fmt.Sprintf("unsupported mechanism %q", candidate.Mechanism)
		return response, nil
	}
	if len(routes) == 0 {
		response.Verdict = "NOT_APPLICABLE"
		response.Feedback = "no applicable checker/ISA routes"
		return response, nil
	}
	checkedOracles := make(map[string]bool)
	for _, route := range routes {
		if checkedOracles[route.Oracle] {
			continue
		}
		checkedOracles[route.Oracle] = true
		if violations := seed.FindDefenseDisablingFlags(route.Oracle, flags); len(violations) > 0 {
			return response, fmt.Errorf("candidate flags disable %s: %s", route.Oracle, strings.Join(violations, ", "))
		}
	}

	tcs := req.Toolchains
	if tcs == nil {
		tcs = d.Toolchains
	}
	if tcs == nil {
		return response, fmt.Errorf("toolchains are not configured")
	}
	builder := d.Builder
	if builder == nil {
		builder = CompilerBuilder{Compiler: trustedCompiler}
	}
	factory := d.EvaluatorFactory
	if factory == nil {
		factory = defaultEvaluatorFactory
	}

	sd := &seed.Seed{Meta: seed.Metadata{ID: hashID(candidate.ID)}, Content: candidate.MinimalTrigger.Source, CFlags: flags}
	byISA := groupRoutes(routes)
	isas := sortedKeys(byISA)
	selectedToolchains := make(map[string]Toolchain, len(isas))
	for _, isa := range isas {
		tc, ok := tcs.Lookup(isa)
		if !ok {
			return response, fmt.Errorf("no toolchain configured for ISA %q", isa)
		}
		if _, pathErr := tc.compilerPath(trustedCompiler); pathErr != nil {
			return response, fmt.Errorf("toolchain for ISA %q: %w", isa, pathErr)
		}
		selectedToolchains[isa] = tc
	}
	for _, isa := range isas {
		tc := selectedToolchains[isa]
		// Static checkers inspect the produced ELF, not a running program, so a
		// self-contained minimal trigger need not define `main`. Building
		// compile-only for static-only routes matches the object-file analysis
		// contract and avoids spurious link failures on entry-point-free
		// snippets; any dynamic route still forces a full link + execution.
		buildFlags := flags
		if staticOnlyRoutes(byISA[isa]) && !contains(flags, "-c") {
			buildFlags = append(append([]string(nil), flags...), "-c")
		}
		build, buildErr := builder.Build(&candidate, isa, tc, buildFlags)
		if build.cleanupDir != "" {
			defer os.RemoveAll(build.cleanupDir)
		}
		response.Builds = append(response.Builds, build)
		if buildErr != nil {
			return response, fmt.Errorf("build ISA %q: %w", isa, buildErr)
		}
		if !build.Success {
			return response, fmt.Errorf("build ISA %q failed: %s", isa, fallbackText(build.Error, build.Stderr))
		}

		byOracle := make(map[string]map[string]bool)
		for _, route := range byISA[isa] {
			if byOracle[route.Oracle] == nil {
				byOracle[route.Oracle] = make(map[string]bool)
			}
			byOracle[route.Oracle][route.ID] = true
		}
		for _, oracleName := range sortedKeys(byOracle) {
			evaluator, factoryErr := factory(oracleName)
			if factoryErr != nil {
				return response, fmt.Errorf("construct oracle %q: %w", oracleName, factoryErr)
			}
			results, evalErr := evaluator.Evaluate(sd, &oracle.AnalyzeContext{
				BinaryPath: build.BinaryPath,
				Executor:   candidateExecutor(tc),
			}, byOracle[oracleName])
			if evalErr != nil {
				return response, fmt.Errorf("evaluate oracle %q on ISA %q: %w", oracleName, isa, evalErr)
			}
			returned := make(map[string]bool, len(results))
			for _, result := range results {
				if !byOracle[oracleName][result.ID] {
					return response, fmt.Errorf("oracle %q returned unrequested checker %q", oracleName, result.ID)
				}
				returned[result.ID] = true
				response.Results = append(response.Results, CandidateResult{
					ID: result.ID, ISA: isa, Category: result.Category, Verdict: publicVerdict(result.Verdict),
					Evidence: result.Evidence, Detail: result.Detail, Reason: result.Reason,
				})
			}
			for checkerID := range byOracle[oracleName] {
				if !returned[checkerID] {
					return response, fmt.Errorf("oracle %q did not return selected checker %q", oracleName, checkerID)
				}
			}
		}
	}
	response.Verdict, response.Feedback, response.Evidence = aggregateCandidateResults(response.Results)
	return response, nil
}

// ExitCode implements the external protocol: online transports all four
// verdicts successfully; verify treats only a reproduced FAIL as success.
func ExitCode(mode CandidateMode, verdict string, infrastructureError bool) int {
	if infrastructureError {
		return 2
	}
	if mode == CandidateModeOnline || mode == CandidateModeCatalog {
		return 0
	}
	if verdict == "ERROR" {
		return 2
	}
	if mode == CandidateModeVerify && verdict == "FAIL" {
		return 0
	}
	return 1
}

type checkerRoute struct {
	oracle.CheckerMetadata
	ISA string
}

func (d *CandidateDispatcher) resolveRoutes(candidate *Candidate, mode CandidateMode) ([]checkerRoute, bool, error) {
	metadata := d.Metadata
	if len(metadata) == 0 {
		metadata = oracle.AllCheckerMetadata()
	}
	if err := oracle.ValidateCheckerCatalog(metadata); err != nil {
		return nil, false, fmt.Errorf("invalid checker catalog: %w", err)
	}
	byID := make(map[string]oracle.CheckerMetadata, len(metadata))
	for _, item := range metadata {
		byID[item.ID] = item
	}

	requested := uniqueStrings(candidate.CheckerIDs)
	// Online feedback is deliberately limited to Part II's checker bundle.
	// Final verification is a separate trust boundary: when a candidate names
	// canonical related invariants, verify them with the trusted checkers
	// compiled into the manifest-bound dispatcher. This lets a newly discovered
	// violation be independently reproduced even when its checker was not one
	// of the generated online-guidance checkers.
	useBundleAllowlist := true
	if mode == CandidateModeVerify && len(candidate.RelatedInvariants) > 0 {
		requested = uniqueStrings(candidate.RelatedInvariants)
		useBundleAllowlist = false
	} else if len(requested) == 0 {
		requested = uniqueStrings(candidate.RelatedInvariants)
	}
	canonicalMechanism, knownMechanism := canonicalMechanism(candidate.Mechanism)
	if !knownMechanism {
		canonicalMechanism = normalizeMechanism(candidate.Mechanism)
	}
	if len(requested) == 0 {
		for _, item := range metadata {
			if len(d.CatalogAllowlist) > 0 && !d.CatalogAllowlist[item.ID] {
				continue
			}
			if item.Mechanism == canonicalMechanism || item.Oracle == canonicalMechanism {
				requested = append(requested, item.ID)
			}
		}
		if len(requested) == 0 {
			return nil, true, nil
		}
	}
	if len(requested) == 0 {
		return nil, false, fmt.Errorf("candidate does not select any checker")
	}

	selected := make(map[string]bool)
	var addWithDependencies func(string) error
	addWithDependencies = func(id string) error {
		item, ok := byID[id]
		if !ok {
			return fmt.Errorf("unknown checker id %q", id)
		}
		if selected[id] {
			return nil
		}
		for _, required := range item.Requires {
			if err := addWithDependencies(required); err != nil {
				return err
			}
		}
		selected[id] = true
		return nil
	}
	for _, id := range requested {
		if useBundleAllowlist && len(d.CatalogAllowlist) > 0 && !d.CatalogAllowlist[id] {
			return nil, false, fmt.Errorf("checker %q is not included in the checker bundle", id)
		}
		if err := addWithDependencies(id); err != nil {
			return nil, false, err
		}
	}

	isas := uniqueStrings(candidate.ISA)
	if len(candidate.MinimalTrigger.ISA) > 0 {
		isas = uniqueStrings(candidate.MinimalTrigger.ISA)
	}
	if len(isas) == 0 && candidate.MinimalTrigger.Target != "" {
		isas = []string{candidate.MinimalTrigger.Target}
	}
	for index, isa := range isas {
		isas[index] = normalizeISA(isa)
	}
	isas = uniqueStrings(isas)
	if len(isas) == 0 {
		return nil, false, fmt.Errorf("candidate ISA is empty")
	}

	var routes []checkerRoute
	ids := sortedKeys(selected)
	for _, id := range ids {
		item := byID[id]
		if canonicalMechanism != "" && canonicalMechanism != item.Mechanism && canonicalMechanism != item.Oracle {
			return nil, false, fmt.Errorf("checker %q belongs to mechanism %q, not %q", id, item.Mechanism, candidate.Mechanism)
		}
		routeISAs := isas
		if item.Mode == oracle.ModeDifferential {
			routeISAs = item.ApplicableISAs
		}
		for _, isa := range routeISAs {
			if !contains(item.ApplicableISAs, isa) {
				return nil, false, fmt.Errorf("checker %q is not applicable to ISA %q", id, isa)
			}
			routes = append(routes, checkerRoute{CheckerMetadata: item, ISA: isa})
		}
	}
	return routes, false, nil
}

// CompilerBuilder applies candidate flags as argv tokens, never through a shell.
// The shared compiler adapter is named GCCCompiler for historical reasons, but
// it executes the explicit compiler path supplied here and is safe for Clang.
type CompilerBuilder struct {
	Compiler string
}

func (b CompilerBuilder) Build(candidate *Candidate, isa string, tc Toolchain, flags []string) (CandidateBuild, error) {
	compilerFamily, err := normalizeCompiler(b.Compiler)
	if err != nil {
		return CandidateBuild{ISA: isa}, err
	}
	compilerPath, err := tc.compilerPath(compilerFamily)
	if err != nil {
		return CandidateBuild{ISA: isa}, err
	}
	workDir, err := os.MkdirTemp("", "defuzz-candidate-"+safePathPart(isa)+"-")
	if err != nil {
		return CandidateBuild{ISA: isa}, err
	}
	// Artifacts intentionally survive process exit so evidence paths remain
	// inspectable; callers that embed the service may remove them afterwards.
	configFlags := append([]string(nil), tc.CFlags...)
	if tc.Sysroot != "" {
		configFlags = append(configFlags, "--sysroot="+tc.Sysroot)
	}
	language, err := languageFlags(candidate.MinimalTrigger.Language)
	if err != nil {
		return CandidateBuild{ISA: isa}, err
	}
	configFlags = append(configFlags, language...)
	cc := compiler.NewGCCCompiler(compiler.GCCCompilerConfig{
		GCCPath: compilerPath, WorkDir: workDir, PrefixPath: tc.Prefix, CFlags: configFlags,
	})
	sd := &seed.Seed{Meta: seed.Metadata{ID: hashID(candidate.ID)}, Content: candidate.MinimalTrigger.Source, CFlags: append([]string(nil), flags...)}
	result, err := cc.Compile(sd)
	build := CandidateBuild{ISA: isa, cleanupDir: workDir}
	if result != nil {
		build.Success = result.Success
		build.BinaryPath = result.BinaryPath
		build.Compiler = result.CompilerPath
		build.Args = append([]string(nil), result.Args...)
		build.EffectiveFlags = append([]string(nil), result.EffectiveFlags...)
		build.Stdout = result.Stdout
		build.Stderr = result.Stderr
	}
	if err != nil {
		build.Error = err.Error()
	}
	if result != nil && !result.Success {
		build.Error = fallbackText(result.Stderr, "compiler exited unsuccessfully")
	}
	return build, err
}

// GCCBuilder preserves the previous exported builder for callers that explicitly
// depend on the GCC-only path. New candidate dispatch uses CompilerBuilder.
type GCCBuilder struct{}

func (GCCBuilder) Build(candidate *Candidate, isa string, tc Toolchain, flags []string) (CandidateBuild, error) {
	return (CompilerBuilder{Compiler: "gcc"}).Build(candidate, isa, tc, flags)
}

func normalizeCompiler(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "gcc", "gnu-gcc":
		return "gcc", nil
	case "llvm", "clang", "compiler-rt", "lld":
		return "llvm", nil
	case "":
		return "", fmt.Errorf("compiler is required (expected gcc or llvm)")
	default:
		return "", fmt.Errorf("unknown compiler %q (expected gcc, gnu-gcc, llvm, clang, compiler-rt, or lld)", value)
	}
}

func (tc Toolchain) compilerPath(compilerFamily string) (string, error) {
	switch compilerFamily {
	case "gcc":
		if strings.TrimSpace(tc.GCCPath) == "" {
			return "", fmt.Errorf("gcc_path is not configured")
		}
		return tc.GCCPath, nil
	case "llvm":
		if strings.TrimSpace(tc.ClangPath) == "" {
			return "", fmt.Errorf("clang_path is not configured")
		}
		return tc.ClangPath, nil
	default:
		return "", fmt.Errorf("unsupported canonical compiler %q", compilerFamily)
	}
}

func defaultEvaluatorFactory(name string) (oracle.MechanismEvaluator, error) {
	instance, err := oracle.New(name, nil)
	if err != nil {
		return nil, err
	}
	evaluator, ok := instance.(oracle.MechanismEvaluator)
	if !ok {
		return nil, fmt.Errorf("oracle does not implement MechanismEvaluator")
	}
	return evaluator, nil
}

func candidateExecutor(tc Toolchain) oracle.Executor {
	if tc.Native || tc.QEMUPath == "" {
		return executor.NewOracleExecutorAdapter(defaultExecTimeoutSec)
	}
	return executor.NewQEMUOracleExecutorAdapter(tc.QEMUPath, tc.QEMUSysroot, defaultExecTimeoutSec)
}

func normalizeCandidateFlags(raw StringList) ([]string, error) {
	flags := append([]string(nil), raw...)
	for _, flag := range flags {
		if reason := dangerousFlag(flag); reason != "" {
			return nil, fmt.Errorf("unsafe candidate compiler flag %q: %s", flag, reason)
		}
	}
	return flags, nil
}

func splitFlags(value string) ([]string, error) {
	var out []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			out = append(out, current.String())
			current.Reset()
		}
	}
	for _, r := range value {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		current.WriteRune(r)
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("unterminated quote or escape")
	}
	flush()
	return out, nil
}

func dangerousFlag(flag string) string {
	if flag == "" || strings.ContainsRune(flag, 0) {
		return "empty or NUL-containing argument"
	}
	if !strings.HasPrefix(flag, "-") {
		return "positional compiler arguments are forbidden"
	}
	lower := strings.ToLower(flag)
	if flag == "-o" || strings.HasPrefix(flag, "-o") {
		return "output path control is forbidden"
	}
	if flag == "-B" || strings.HasPrefix(flag, "-B") {
		return "toolchain search-path control is forbidden"
	}
	for _, prefix := range []string{"--output", "-wrapper", "-specs", "--specs", "-fplugin", "-plugin", "--sysroot", "-isysroot", "--gcc-toolchain", "-resource-dir", "-x"} {
		if lower == prefix || strings.HasPrefix(lower, prefix+"=") {
			return "output/toolchain/plugin control is forbidden"
		}
	}
	for _, prefix := range []string{"-include", "-imacros", "-idirafter", "-iquote", "-xclang", "-mllvm", "-serialize-diagnostics", "-dependency-file", "-mf", "-mj"} {
		if lower == prefix || strings.HasPrefix(lower, prefix+"=") {
			return "candidate-controlled input/output paths or backend passthrough are forbidden"
		}
	}
	for _, flag := range []string{"-e", "-s", "-m", "-mm", "-md", "-mmd"} {
		if lower == flag {
			return "preprocess/assembly/dependency-only output modes are forbidden"
		}
	}
	for _, prefix := range []string{"-wl,", "-wa,", "-wp,", "-xlinker", "-xassembler", "-xpreprocessor", "@"} {
		if strings.HasPrefix(lower, prefix) {
			return "response files and linker/assembler/preprocessor passthrough are forbidden"
		}
	}
	for _, prefix := range []string{"-save-temps", "-dump", "-ftime-trace=", "-fprofile-dir=", "-fprofile-prefix-path=", "-fprofile-note=", "-ffile-prefix-map="} {
		if strings.HasPrefix(lower, prefix) {
			return "candidate-controlled filesystem output is forbidden"
		}
	}
	return ""
}

func languageFlags(language string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "", "c":
		return nil, nil
	case "c++", "cpp", "cxx":
		return []string{"-x", "c++"}, nil
	default:
		return nil, fmt.Errorf("unsupported minimal_trigger.language %q", language)
	}
}

func aggregateCandidateResults(results []CandidateResult) (string, string, []string) {
	verdict := "NOT_APPLICABLE"
	rank := map[string]int{"NOT_APPLICABLE": 0, "PASS": 1, "ERROR": 2, "FAIL": 3}
	evidence := make([]string, 0)
	for _, result := range results {
		if rank[result.Verdict] > rank[verdict] {
			verdict = result.Verdict
		}
		message := result.Evidence
		if message == "" {
			message = result.Reason
		}
		if message != "" {
			evidence = append(evidence, fmt.Sprintf("%s[%s]: %s", result.ID, result.ISA, message))
		}
	}
	feedback := fmt.Sprintf("%d checker result(s); aggregate verdict %s", len(results), verdict)
	return verdict, feedback, evidence
}

func publicVerdict(verdict oracle.InvariantVerdict) string {
	if verdict == oracle.VerdictNotApplicable {
		return "NOT_APPLICABLE"
	}
	return verdict.String()
}

func canonicalMechanism(value string) (string, bool) {
	switch normalizeMechanism(value) {
	case "canary", "stack-protector":
		return "stack-protector", true
	case "fortify", "fortify-source":
		return "fortify-source", true
	case "ibt", "cet", "cet-ibt":
		return "ibt", true
	default:
		return "", false
	}
}

func normalizeMechanism(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-"))
}

func normalizeISA(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	for alias, canonical := range map[string]string{
		"x86-64": "x86_64",
		"x8664":  "x86_64",
		"amd64":  "x86_64",
		"x64":    "x86_64",
	} {
		if normalized == alias || strings.HasPrefix(normalized, alias+"-") {
			return canonical
		}
	}
	switch normalized {
	case "arm64", "aarch-64":
		return "aarch64"
	case "risc-v64", "risc-v-64":
		return "riscv64"
	}
	// GNU-style triples place the architecture first. Restrict this fallback to
	// the canonical architectures understood by the toolchain registry.
	if fields := strings.Split(normalized, "-"); len(fields) > 1 {
		switch fields[0] {
		case "aarch64", "arm64":
			return "aarch64"
		case "riscv64":
			return "riscv64"
		case "loongarch64", "mips64", "mips", "arm", "i386", "xtensa", "csky":
			return fields[0]
		}
	}
	return normalized
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func groupRoutes(routes []checkerRoute) map[string][]checkerRoute {
	out := make(map[string][]checkerRoute)
	for _, route := range routes {
		out[route.ISA] = append(out[route.ISA], route)
	}
	return out
}

// staticOnlyRoutes reports whether every checker selected for an ISA is a
// static ELF-inspection checker. Such routes never execute the artifact, so a
// compile-only object is sufficient and avoids linking entry-point-free
// minimal triggers.
func staticOnlyRoutes(routes []checkerRoute) bool {
	if len(routes) == 0 {
		return false
	}
	for _, route := range routes {
		if route.Category != oracle.CategoryStatic {
			return false
		}
	}
	return true
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func safePathPart(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, value)
}

func fallbackText(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unknown error"
}

func catalogEntryID(entry bundleCatalogEntry) (string, error) {
	ids := uniqueStrings([]string{entry.ID, entry.CheckerID, entry.InvariantID})
	if len(ids) != 1 {
		return "", fmt.Errorf("checker catalog entry must have one consistent id/checker_id/invariant_id value")
	}
	return ids[0], nil
}

func sameStringSet(left, right []string) bool {
	a := uniqueStrings(left)
	b := uniqueStrings(right)
	sort.Strings(a)
	sort.Strings(b)
	return len(a) == len(b) && strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

func hasDuplicates(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func validateRequestedScopeValues(field string, raw map[string]any) error {
	values, ok := raw[field].([]any)
	if !ok {
		return fmt.Errorf("bundle manifest %s must be an array of strings", field)
	}
	seen := make(map[string]bool, len(values))
	for _, rawValue := range values {
		value, ok := rawValue.(string)
		if !ok {
			return fmt.Errorf("bundle manifest %s must be an array of strings", field)
		}
		if value == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("bundle manifest %s values must be non-empty and have no surrounding whitespace", field)
		}
		if seen[value] {
			return fmt.Errorf("bundle manifest %s values must be unique", field)
		}
		seen[value] = true
	}
	return nil
}

func decodeJSONObject(data []byte) (map[string]any, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("trailing content after JSON object")
	}
	return object, nil
}

func validateObjectKeys(object map[string]any, allowed []string) error {
	wanted := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		wanted[key] = true
	}
	for key := range object {
		if !wanted[key] {
			return fmt.Errorf("unknown top-level field %q", key)
		}
	}
	for _, key := range allowed {
		if _, exists := object[key]; !exists {
			return fmt.Errorf("missing top-level field %q", key)
		}
	}
	return nil
}

func validateArtifactObjects(manifest map[string]any) error {
	rawArtifacts, ok := manifest["artifacts"].(map[string]any)
	if !ok {
		return fmt.Errorf("artifacts must be an object")
	}
	roles := []string{"cumulative_patch", "catalog", "dispatcher", "scoped_invariants", "input_scope"}
	if err := validateObjectKeys(rawArtifacts, roles); err != nil {
		return fmt.Errorf("artifacts: %w", err)
	}
	allowedFields := map[string]bool{"path": true, "sha256": true, "size_bytes": true, "kind": true}
	for _, role := range roles {
		rawArtifact, ok := rawArtifacts[role].(map[string]any)
		if !ok {
			return fmt.Errorf("artifact %q must be an object", role)
		}
		for key := range rawArtifact {
			if !allowedFields[key] {
				return fmt.Errorf("artifact %q has unknown field %q", role, key)
			}
		}
		for _, required := range []string{"path", "sha256"} {
			if _, exists := rawArtifact[required]; !exists {
				return fmt.Errorf("artifact %q is missing field %q", role, required)
			}
		}
	}
	return nil
}

// canonicalPythonJSON matches json.dumps(..., ensure_ascii=False,
// sort_keys=True, separators=(",", ":"), allow_nan=False) for the
// manifest value domain (objects, arrays, strings, booleans, null, numbers).
func canonicalPythonJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	if err := writeCanonicalJSON(&out, value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeCanonicalJSON(out *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		if typed {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case string:
		encoded, _ := json.Marshal(typed)
		encoded = bytes.ReplaceAll(encoded, []byte(`\u003c`), []byte("<"))
		encoded = bytes.ReplaceAll(encoded, []byte(`\u003e`), []byte(">"))
		encoded = bytes.ReplaceAll(encoded, []byte(`\u0026`), []byte("&"))
		encoded = bytes.ReplaceAll(encoded, []byte(`\u2028`), []byte(string(rune(0x2028))))
		encoded = bytes.ReplaceAll(encoded, []byte(`\u2029`), []byte(string(rune(0x2029))))
		out.Write(encoded)
	case json.Number:
		number := typed.String()
		if !strings.ContainsAny(number, ".eE") {
			if _, ok := new(big.Int).SetString(number, 10); !ok {
				return fmt.Errorf("invalid JSON number %q", typed)
			}
			out.WriteString(number)
			break
		}
		floating, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return fmt.Errorf("invalid JSON number %q", typed)
		}
		number = pythonFloatString(floating, 64)
		out.WriteString(number)
	case int:
		out.WriteString(strconv.Itoa(typed))
	case int8:
		out.WriteString(strconv.FormatInt(int64(typed), 10))
	case int16:
		out.WriteString(strconv.FormatInt(int64(typed), 10))
	case int32:
		out.WriteString(strconv.FormatInt(int64(typed), 10))
	case int64:
		out.WriteString(strconv.FormatInt(typed, 10))
	case uint:
		out.WriteString(strconv.FormatUint(uint64(typed), 10))
	case uint8:
		out.WriteString(strconv.FormatUint(uint64(typed), 10))
	case uint16:
		out.WriteString(strconv.FormatUint(uint64(typed), 10))
	case uint32:
		out.WriteString(strconv.FormatUint(uint64(typed), 10))
	case uint64:
		out.WriteString(strconv.FormatUint(typed, 10))
	case float32:
		return writeCanonicalFloat(out, float64(typed), 32)
	case float64:
		return writeCanonicalFloat(out, typed, 64)
	case []any:
		out.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				out.WriteByte(',')
			}
			if err := writeCanonicalJSON(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		keys := sortedKeys(typed)
		out.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				out.WriteByte(',')
			}
			if err := writeCanonicalJSON(out, key); err != nil {
				return err
			}
			out.WriteByte(':')
			if err := writeCanonicalJSON(out, typed[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return fmt.Errorf("unsupported JSON value %T", value)
	}
	return nil
}

func writeCanonicalFloat(out *bytes.Buffer, value float64, bitSize int) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("non-finite JSON number")
	}
	number := pythonFloatString(value, bitSize)
	out.WriteString(number)
	return nil
}

func pythonFloatString(value float64, bitSize int) string {
	if value == 0 {
		if math.Signbit(value) {
			return "-0.0"
		}
		return "0.0"
	}
	abs := math.Abs(value)
	exponent := int(math.Floor(math.Log10(abs)))
	format := byte('f')
	if exponent < -4 || exponent >= 16 {
		format = 'e'
	}
	number := strconv.FormatFloat(value, format, -1, bitSize)
	if index := strings.IndexByte(number, 'e'); index >= 0 {
		exponentValue, err := strconv.Atoi(number[index+1:])
		if err == nil {
			number = number[:index] + fmt.Sprintf("e%+03d", exponentValue)
		}
	}
	if !strings.ContainsAny(number, ".e") {
		number += ".0"
	}
	return number
}

func validateBundleArtifact(root, role string, artifact *bundleArtifact) (string, []byte, error) {
	if artifact == nil {
		return "", nil, fmt.Errorf("ready bundle is missing %s artifact", role)
	}
	if !validSHA256(artifact.SHA256) {
		return "", nil, fmt.Errorf("checker-bundle %s artifact has invalid SHA-256", role)
	}
	if artifact.SizeBytes != nil && *artifact.SizeBytes < 0 {
		return "", nil, fmt.Errorf("checker-bundle %s artifact has negative size", role)
	}
	path, err := safeBundleArtifactPath(root, artifact.Path)
	if err != nil {
		return "", nil, fmt.Errorf("checker-bundle %s artifact: %w", role, err)
	}
	resolved, content, err := readRegularNoSymlink(path)
	if err != nil {
		return "", nil, fmt.Errorf("checker-bundle %s artifact: %w", role, err)
	}
	digest := sha256.Sum256(content)
	actual := hex.EncodeToString(digest[:])
	if subtle.ConstantTimeCompare([]byte(actual), []byte(artifact.SHA256)) != 1 {
		return "", nil, fmt.Errorf("checker-bundle %s artifact SHA-256 mismatch for %q: expected %s, got %s", role, artifact.Path, artifact.SHA256, actual)
	}
	if artifact.SizeBytes != nil && *artifact.SizeBytes != int64(len(content)) {
		return "", nil, fmt.Errorf("checker-bundle %s artifact size mismatch for %q: expected %d, got %d", role, artifact.Path, *artifact.SizeBytes, len(content))
	}
	return resolved, content, nil
}

func safeBundleArtifactPath(root, relative string) (string, error) {
	windowsDrive := len(relative) >= 2 && relative[1] == ':' && ((relative[0] >= 'a' && relative[0] <= 'z') || (relative[0] >= 'A' && relative[0] <= 'Z'))
	if relative == "" || filepath.IsAbs(relative) || windowsDrive || strings.ContainsRune(relative, 0) || strings.Contains(relative, "\\") {
		return "", fmt.Errorf("path must be a non-empty relative path")
	}
	if strings.Contains(relative, "//") || pathpkg.Clean(relative) != relative || relative == "." {
		return "", fmt.Errorf("path must use normalized POSIX syntax")
	}
	clean := filepath.FromSlash(relative)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes bundle root")
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(rootResolved, clean)
	current := rootResolved
	for _, component := range strings.Split(relative, "/") {
		current = filepath.Join(current, component)
		info, lstatErr := os.Lstat(current)
		if lstatErr != nil {
			return "", lstatErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("artifact path must not contain symlinks")
		}
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes bundle root")
	}
	return resolved, nil
}

func sameRegularFile(left, right string) bool {
	a, errA := os.Stat(left)
	b, errB := os.Stat(right)
	return errA == nil && errB == nil && a.Mode().IsRegular() && b.Mode().IsRegular() && os.SameFile(a, b)
}

func readRegularNoSymlink(path string) (string, []byte, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("must not be a symlink: %s", absolute)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("not a regular file: %s", absolute)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return "", nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return "", nil, fmt.Errorf("file changed while opening: %s", absolute)
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return "", nil, err
	}
	return absolute, content, nil
}

// ResolveCandidatePath turns a caller path into an absolute, regular file.
func ResolveCandidatePath(path string) (string, error) {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	return resolved, nil
}
