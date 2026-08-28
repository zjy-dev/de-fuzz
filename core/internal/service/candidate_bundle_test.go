package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bundleTestFixture struct {
	root                 string
	manifestPath         string
	catalogPath          string
	dispatcherPath       string
	patchPath            string
	scopedInvariantsPath string
	inputScopePath       string
	payload              map[string]any
}

func newBundleTestFixture(t *testing.T) *bundleTestFixture {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "artifacts"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "inputs"), 0o755))

	var checker map[string]any
	for _, candidate := range RuntimeCheckerCatalog().Checkers {
		if candidate.ID == "INV-IBT-B01" {
			data, err := json.Marshal(candidate)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &checker))
			checker["checker_id"] = candidate.ID
			checker["invariant_id"] = candidate.ID
			checker["files"] = []any{}
			break
		}
	}
	require.NotNil(t, checker)
	catalogPayload := map[string]any{
		"schema_version":     1,
		"kind":               "defuzz-checker-catalog",
		"source_tree_sha256": strings.Repeat("1", 64),
		"result_tree_sha256": strings.Repeat("2", 64),
		"checkers":           []any{checker},
	}
	catalogBytes, err := json.Marshal(catalogPayload)
	require.NoError(t, err)
	catalogBytes = append(catalogBytes, '\n')
	patchBytes := []byte("diff --git a/checker.go b/checker.go\n")
	dispatcherBytes := []byte("fixture dispatcher\n")
	scopedInvariantsBytes := []byte(`{"invariant_id":"INV-IBT-B01","mechanism":"ibt"}` + "\n")
	sourceInvariantsSHA256 := strings.Repeat("8", 64)
	inputScopeBytes, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"kind":           "defuzz-checker-input-scope",
		"source_artifact": map[string]any{
			"path": "/source/accepted-invariants.jsonl", "sha256": sourceInvariantsSHA256,
		},
		"requested":              map[string]any{"mechanisms": []any{"ibt"}, "isas": []any{"x86_64"}},
		"scope_requested":        true,
		"selected_invariant_ids": []any{"INV-IBT-B01"},
	})
	require.NoError(t, err)
	inputScopeBytes = append(inputScopeBytes, '\n')

	fixture := &bundleTestFixture{
		root: root, manifestPath: filepath.Join(root, "checker-bundle-manifest.json"),
		catalogPath:          filepath.Join(root, "artifacts", "catalog.json"),
		dispatcherPath:       filepath.Join(root, "bin", "dispatcher"),
		patchPath:            filepath.Join(root, "artifacts", "checkers.patch"),
		scopedInvariantsPath: filepath.Join(root, "inputs", "scoped-accepted-invariants.jsonl"),
		inputScopePath:       filepath.Join(root, "inputs", "checker-input-scope.json"),
	}
	require.NoError(t, os.WriteFile(fixture.catalogPath, catalogBytes, 0o644))
	require.NoError(t, os.WriteFile(fixture.patchPath, patchBytes, 0o644))
	require.NoError(t, os.WriteFile(fixture.dispatcherPath, dispatcherBytes, 0o755))
	require.NoError(t, os.WriteFile(fixture.scopedInvariantsPath, scopedInvariantsBytes, 0o644))
	require.NoError(t, os.WriteFile(fixture.inputScopePath, inputScopeBytes, 0o644))

	fixture.payload = map[string]any{
		"schema_version": 1, "kind": "defuzz-checker-bundle", "status": "ready",
		"bundle_id": strings.Repeat("0", 64), "source_root": "/source",
		"source_root_sha256":       strings.Repeat("3", 64),
		"source_tree_sha256":       strings.Repeat("4", 64),
		"final_tree_sha256":        strings.Repeat("5", 64),
		"source_invariants_sha256": sourceInvariantsSHA256,
		"requested_mechanisms":     []any{"ibt"},
		"requested_isas":           []any{"x86_64"},
		"coverage_complete":        true, "budget_exhausted": false,
		"included_invariant_ids": []any{"INV-IBT-B01"}, "failed_invariant_ids": []any{},
		"invariants": []any{map[string]any{
			"invariant_id": "INV-IBT-B01", "final_status": "passed",
			"infrastructure_error": false, "parent_tree_sha256": strings.Repeat("6", 64),
			"result_tree_sha256": strings.Repeat("7", 64), "files": []any{"core/internal/oracle/checker.go"},
			"lineage": map[string]any{"confidence": json.Number("0.5")},
		}},
		"artifacts": map[string]any{
			"cumulative_patch":  artifactTestRecord("artifacts/checkers.patch", patchBytes, "cumulative-patch"),
			"catalog":           artifactTestRecord("artifacts/catalog.json", catalogBytes, "checker-catalog"),
			"dispatcher":        artifactTestRecord("bin/dispatcher", dispatcherBytes, "checker-dispatcher"),
			"scoped_invariants": artifactTestRecord("inputs/scoped-accepted-invariants.jsonl", scopedInvariantsBytes, "scoped-accepted-invariants"),
			"input_scope":       artifactTestRecord("inputs/checker-input-scope.json", inputScopeBytes, "checker-input-scope"),
		},
		"validation": map[string]any{"status": "passed", "commands": []any{}, "build": map[string]any{"status": "passed"}},
	}
	fixture.writeManifest(t, true)
	return fixture
}

func artifactTestRecord(path string, content []byte, kind string) map[string]any {
	digest := sha256.Sum256(content)
	return map[string]any{"path": path, "sha256": hex.EncodeToString(digest[:]), "size_bytes": len(content), "kind": kind}
}

func (f *bundleTestFixture) writeManifest(t *testing.T, refreshID bool) {
	t.Helper()
	if refreshID {
		copyWithoutID := make(map[string]any, len(f.payload)-1)
		for key, value := range f.payload {
			if key != "bundle_id" {
				copyWithoutID[key] = value
			}
		}
		canonical, err := canonicalPythonJSON(copyWithoutID)
		require.NoError(t, err)
		digest := sha256.Sum256(canonical)
		f.payload["bundle_id"] = hex.EncodeToString(digest[:])
	}
	data, err := json.Marshal(f.payload)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.manifestPath, append(data, '\n'), 0o644))
}

func TestCanonicalPythonJSONMatchesPythonDumps(t *testing.T) {
	value := map[string]any{
		"z": json.Number("1"), "a": "hello <&>" + string(rune(0x2028)) + "end",
		"n": nil, "arr": []any{true, json.Number("2.5")},
	}
	canonical, err := canonicalPythonJSON(value)
	require.NoError(t, err)
	assert.Equal(t, `{"a":"hello <&>`+string(rune(0x2028))+`end","arr":[true,2.5],"n":null,"z":1}`, string(canonical))
	digest := sha256.Sum256(canonical)
	assert.Equal(t, "263360ed70f1d8a4565ac5606baa480fe4b6d1e411c3054be62d14233396509e", hex.EncodeToString(digest[:]))
}

func TestCanonicalPythonJSONFormatsPythonCompatibleFloats(t *testing.T) {
	canonical, err := canonicalPythonJSON([]any{1.0, 1e20, 1e21, 1e-5, 1e-7, -0.0})
	require.NoError(t, err)
	assert.Equal(t, `[1.0,1e+20,1e+21,1e-05,1e-07,0.0]`, string(canonical))
}

func TestLoadBundleCatalogValidatesCompleteBundle(t *testing.T) {
	fixture := newBundleTestFixture(t)
	allowlist, err := loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"INV-IBT-B01": true}, allowlist)
}

func TestLoadBundleCatalogRejectsBundleIDTampering(t *testing.T) {
	fixture := newBundleTestFixture(t)
	fixture.payload["bundle_id"] = strings.Repeat("f", 64)
	fixture.writeManifest(t, false)
	_, err := loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
	require.ErrorContains(t, err, "bundle_id mismatch")
}

func TestLoadBundleCatalogRejectsReadyManifestWithUnprocessedInvariant(t *testing.T) {
	fixture := newBundleTestFixture(t)
	invariant := fixture.payload["invariants"].([]any)[0].(map[string]any)
	invariant["final_status"] = "unprocessed"
	fixture.payload["included_invariant_ids"] = []any{}
	fixture.payload["coverage_complete"] = false
	fixture.writeManifest(t, true)

	_, err := loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
	require.ErrorContains(t, err, "unprocessed")
}

func TestLoadBundleCatalogRequiresProvenanceFields(t *testing.T) {
	for _, field := range []string{"source_invariants_sha256", "requested_mechanisms", "requested_isas"} {
		t.Run(field, func(t *testing.T) {
			fixture := newBundleTestFixture(t)
			delete(fixture.payload, field)
			fixture.writeManifest(t, true)

			_, err := loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
			require.ErrorContains(t, err, `missing top-level field "`+field+`"`)
		})
	}
}

func TestLoadBundleCatalogValidatesSourceInvariantsSHA256(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "uppercase", value: strings.Repeat("A", 64)},
		{name: "short", value: strings.Repeat("0", 63)},
		{name: "null", value: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBundleTestFixture(t)
			fixture.payload["source_invariants_sha256"] = test.value
			fixture.writeManifest(t, true)

			_, err := loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
			require.ErrorContains(t, err, "source_invariants_sha256 must be a lowercase SHA-256")
		})
	}
}

func TestLoadBundleCatalogValidatesRequestedScope(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value any
		want  string
	}{
		{name: "mechanisms null", field: "requested_mechanisms", value: nil, want: "must be an array of strings"},
		{name: "isas scalar", field: "requested_isas", value: "x86_64", want: "must be an array of strings"},
		{name: "blank", field: "requested_mechanisms", value: []any{""}, want: "must be non-empty"},
		{name: "surrounding whitespace", field: "requested_isas", value: []any{" x86_64"}, want: "no surrounding whitespace"},
		{name: "duplicate", field: "requested_mechanisms", value: []any{"ibt", "ibt"}, want: "must be unique"},
		{name: "non-string", field: "requested_isas", value: []any{json.Number("1")}, want: "must be an array of strings"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBundleTestFixture(t)
			fixture.payload[test.field] = test.value
			fixture.writeManifest(t, true)

			_, err := loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestLoadBundleCatalogRequiresProvenanceArtifacts(t *testing.T) {
	for _, role := range []string{"scoped_invariants", "input_scope"} {
		t.Run(role, func(t *testing.T) {
			fixture := newBundleTestFixture(t)
			fixture.payload["artifacts"].(map[string]any)[role] = nil
			fixture.writeManifest(t, true)

			_, err := loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
			require.ErrorContains(t, err, `artifact "`+role+`" must be an object`)
		})
	}
}

func TestLoadBundleCatalogRejectsEveryArtifactTamper(t *testing.T) {
	for _, role := range []string{"cumulative_patch", "catalog", "dispatcher", "scoped_invariants", "input_scope"} {
		t.Run(role, func(t *testing.T) {
			fixture := newBundleTestFixture(t)
			relative := fixture.payload["artifacts"].(map[string]any)[role].(map[string]any)["path"].(string)
			require.NoError(t, os.WriteFile(filepath.Join(fixture.root, filepath.FromSlash(relative)), []byte("tampered"), 0o755))
			_, err := loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
			require.ErrorContains(t, err, role+" artifact SHA-256 mismatch")
		})
	}
}

func TestLoadBundleCatalogRejectsArtifactSizeTampering(t *testing.T) {
	for _, role := range []string{"cumulative_patch", "catalog", "dispatcher", "scoped_invariants", "input_scope"} {
		t.Run(role, func(t *testing.T) {
			fixture := newBundleTestFixture(t)
			artifact := fixture.payload["artifacts"].(map[string]any)[role].(map[string]any)
			artifact["size_bytes"] = artifact["size_bytes"].(int) + 1
			fixture.writeManifest(t, true)
			_, err := loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
			require.ErrorContains(t, err, role+" artifact size mismatch")
		})
	}
}

func TestLoadBundleCatalogRejectsUnsafeArtifactPaths(t *testing.T) {
	for _, path := range []string{"../outside", "/absolute", "C:/windows", "artifacts//catalog.json", "artifacts/./catalog.json", `artifacts\catalog.json`} {
		t.Run(path, func(t *testing.T) {
			fixture := newBundleTestFixture(t)
			fixture.payload["artifacts"].(map[string]any)["catalog"].(map[string]any)["path"] = path
			fixture.writeManifest(t, true)
			_, err := loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
			require.Error(t, err)
		})
	}
}

func TestLoadBundleCatalogRejectsManifestAndArtifactSymlinks(t *testing.T) {
	t.Run("manifest", func(t *testing.T) {
		fixture := newBundleTestFixture(t)
		link := filepath.Join(fixture.root, "manifest-link.json")
		require.NoError(t, os.Symlink(fixture.manifestPath, link))
		_, err := loadBundleCatalog(link, fixture.catalogPath, fixture.dispatcherPath)
		require.ErrorContains(t, err, "must not be a symlink")
	})
	t.Run("artifact", func(t *testing.T) {
		fixture := newBundleTestFixture(t)
		target := filepath.Join(fixture.root, "catalog-target.json")
		content, err := os.ReadFile(fixture.catalogPath)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(target, content, 0o644))
		require.NoError(t, os.Remove(fixture.catalogPath))
		require.NoError(t, os.Symlink(target, fixture.catalogPath))
		_, err = loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
		require.ErrorContains(t, err, "must not contain symlinks")
	})
}

func TestLoadBundleCatalogRejectsProvenanceArtifactSymlink(t *testing.T) {
	fixture := newBundleTestFixture(t)
	target := filepath.Join(fixture.root, "scope-target.json")
	content, err := os.ReadFile(fixture.inputScopePath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(target, content, 0o644))
	require.NoError(t, os.Remove(fixture.inputScopePath))
	require.NoError(t, os.Symlink(target, fixture.inputScopePath))

	_, err = loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
	require.ErrorContains(t, err, "must not contain symlinks")
}

func TestLoadBundleCatalogRejectsDifferentRunningDispatcher(t *testing.T) {
	fixture := newBundleTestFixture(t)
	other := filepath.Join(fixture.root, "bin", "other")
	require.NoError(t, os.WriteFile(other, []byte("other dispatcher\n"), 0o755))
	_, err := loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, other)
	require.ErrorContains(t, err, "not the running executable")
}

func TestLoadBundleCatalogRejectsArtifactsResolvingToSameFile(t *testing.T) {
	fixture := newBundleTestFixture(t)
	patchBytes, err := os.ReadFile(fixture.patchPath)
	require.NoError(t, err)
	require.NoError(t, os.Remove(fixture.dispatcherPath))
	require.NoError(t, os.Link(fixture.patchPath, fixture.dispatcherPath))
	fixture.payload["artifacts"].(map[string]any)["dispatcher"] = artifactTestRecord("bin/dispatcher", patchBytes, "checker-dispatcher")
	fixture.writeManifest(t, true)

	_, err = loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
	require.ErrorContains(t, err, "resolve to the same file")
}

func TestLoadBundleCatalogRejectsProvenanceArtifactsResolvingToSameFile(t *testing.T) {
	fixture := newBundleTestFixture(t)
	scopedBytes, err := os.ReadFile(fixture.scopedInvariantsPath)
	require.NoError(t, err)
	require.NoError(t, os.Remove(fixture.inputScopePath))
	require.NoError(t, os.Link(fixture.scopedInvariantsPath, fixture.inputScopePath))
	fixture.payload["artifacts"].(map[string]any)["input_scope"] = artifactTestRecord(
		"inputs/checker-input-scope.json", scopedBytes, "checker-input-scope",
	)
	fixture.writeManifest(t, true)

	_, err = loadBundleCatalog(fixture.manifestPath, fixture.catalogPath, fixture.dispatcherPath)
	require.ErrorContains(t, err, "resolve to the same file")
}

func TestLoadBundleCatalogAcceptsRunningDispatcherIdentity(t *testing.T) {
	fixture := newBundleTestFixture(t)
	running, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.Remove(fixture.dispatcherPath))
	if err := os.Link(running, fixture.dispatcherPath); err != nil {
		t.Skipf("hard-linking the running test executable is unsupported: %v", err)
	}
	contents, err := os.ReadFile(fixture.dispatcherPath)
	require.NoError(t, err)
	fixture.payload["artifacts"].(map[string]any)["dispatcher"] = artifactTestRecord("bin/dispatcher", contents, "checker-dispatcher")
	fixture.writeManifest(t, true)

	allowlist, err := LoadBundleCatalog(fixture.manifestPath, fixture.catalogPath)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"INV-IBT-B01": true}, allowlist)
}
