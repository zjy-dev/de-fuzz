package oracle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registeredCheckerIDs collects every checker ID wired into a mechanism
// oracle. metadata.go MUST cover exactly this set (no orphans, no gaps).
func registeredCheckerIDs(t *testing.T) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	mechanisms := []*MechanismOracle{
		(&CanaryOracle{}).mechanism(),
		(&IBTOracle{}).mechanism(),
		(&FortifyOracle{}).mechanism(),
	}
	for _, m := range mechanisms {
		for _, c := range m.Checkers {
			ids[c.ID()] = true
		}
	}
	return ids
}

// TestMetadataCoversAllRegisteredCheckers asserts the SSOT is complete:
// every checker wired into a mechanism has metadata.
func TestMetadataCoversAllRegisteredCheckers(t *testing.T) {
	for id := range registeredCheckerIDs(t) {
		_, ok := LookupCheckerMetadata(id)
		assert.Truef(t, ok, "checker %s is registered but missing from metadata.go", id)
	}
}

// TestMetadataHasNoOrphans asserts the SSOT has no stale entries:
// every metadata row maps to a registered checker.
func TestMetadataHasNoOrphans(t *testing.T) {
	registered := registeredCheckerIDs(t)
	for _, m := range AllCheckerMetadata() {
		assert.Truef(t, registered[m.ID], "metadata.go lists %s but no mechanism registers it", m.ID)
	}
}

// TestMetadataFieldsValid asserts each row is internally well-formed.
func TestMetadataFieldsValid(t *testing.T) {
	for _, m := range AllCheckerMetadata() {
		require.NotEmptyf(t, m.Oracle, "%s: oracle must be non-empty", m.ID)
		require.NotEmptyf(t, m.Mechanism, "%s: mechanism must be non-empty", m.ID)
		require.NotEmptyf(t, m.ApplicableISAs, "%s: applicable_isas must be non-empty", m.ID)
		assert.Containsf(t, []CheckerMode{ModeSingle, ModeDifferential}, m.Mode, "%s: bad mode %q", m.ID, m.Mode)
		assert.Containsf(t, []CheckerCost{CostCheap, CostExpensive}, m.Cost, "%s: bad cost %q", m.ID, m.Cost)
		assert.Containsf(t, []InvariantCategory{CategoryStatic, CategoryDynamic}, m.Category, "%s: bad category %q", m.ID, m.Category)
	}
}

func TestMetadataDependenciesExistAndAreAcyclic(t *testing.T) {
	all := AllCheckerMetadata()
	require.NoError(t, ValidateCheckerCatalog(all))
	byID := make(map[string]CheckerMetadata, len(all))
	for _, metadata := range all {
		byID[metadata.ID] = metadata
	}
	for _, id := range []string{"INV-SP-V02", "INV-SP-L02", "INV-SP-L03", "INV-SP-L04"} {
		assert.Equal(t, []string{"INV-SP-L01"}, byID[id].Requires)
	}
}

func TestValidateCheckerCatalogRejectsMissingDependencyAndCycle(t *testing.T) {
	base := CheckerMetadata{
		ID: "A", Oracle: "test", Mechanism: "test",
		ApplicableISAs: []string{ISAx8664}, Mode: ModeSingle, Cost: CostCheap, Category: CategoryStatic,
	}
	missing := base
	missing.Requires = []string{"missing"}
	assert.ErrorContains(t, ValidateCheckerCatalog([]CheckerMetadata{missing}), "unknown checker")

	a := base
	a.Requires = []string{"B"}
	b := base
	b.ID = "B"
	b.Requires = []string{"A"}
	assert.ErrorContains(t, ValidateCheckerCatalog([]CheckerMetadata{a, b}), "cycle")
}

// TestAllCheckerMetadataSorted asserts deterministic ID-sorted output.
func TestAllCheckerMetadataSorted(t *testing.T) {
	all := AllCheckerMetadata()
	for i := 1; i < len(all); i++ {
		assert.LessOrEqual(t, all[i-1].ID, all[i].ID, "AllCheckerMetadata must be ID-sorted")
	}
}
