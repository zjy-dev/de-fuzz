package mechanism_test

import (
	"strings"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/prompt/mechanism"
)

func TestIBTContract_OracleType(t *testing.T) {
	c, ok := mechanism.Get("ibt")
	if !ok {
		t.Fatal("ibt contract not registered")
	}
	if c.OracleType() != "ibt" {
		t.Errorf("OracleType() = %q, want %q", c.OracleType(), "ibt")
	}
}

func TestIBTContract_FunctionTemplatePath(t *testing.T) {
	c, _ := mechanism.Get("ibt")
	path := c.FunctionTemplatePath("x86_64")
	if !strings.Contains(path, "ibt") {
		t.Errorf("FunctionTemplatePath() = %q, expected to contain 'ibt'", path)
	}
}

func TestIBTContract_RequiredMarkers(t *testing.T) {
	c, _ := mechanism.Get("ibt")
	markers := c.RequiredMarkers()
	if len(markers) == 0 {
		t.Fatal("RequiredMarkers() must not be empty")
	}
	found := false
	for _, m := range markers {
		if m == "SEED_RETURNED" {
			found = true
		}
	}
	if !found {
		t.Error("RequiredMarkers() must include SEED_RETURNED")
	}
}

func TestIBTContract_CriticalRulesAddendum_ForbidsDisableFlags(t *testing.T) {
	c, _ := mechanism.Get("ibt")
	addendum := c.CriticalRulesAddendum()
	for _, forbidden := range []string{"-fcf-protection=none", "-fno-cf-protection", "-mbranch-protection=none"} {
		if !strings.Contains(addendum, forbidden) {
			t.Errorf("CriticalRulesAddendum() does not mention forbidden flag %q", forbidden)
		}
	}
}

func TestIBTContract_FuzzTimePromptExample_ContainsSeedReturned(t *testing.T) {
	c, _ := mechanism.Get("ibt")
	example := c.FuzzTimePromptExample()
	if !strings.Contains(example, "SEED_RETURNED") {
		t.Error("FuzzTimePromptExample() must contain SEED_RETURNED marker")
	}
}
