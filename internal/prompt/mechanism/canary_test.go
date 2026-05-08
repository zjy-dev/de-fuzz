package mechanism_test

import (
	"strings"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/prompt/mechanism"
)

func TestCanaryContract_OracleType(t *testing.T) {
	c, ok := mechanism.Get("canary")
	if !ok {
		t.Fatal("canary contract not registered")
	}
	if c.OracleType() != "canary" {
		t.Errorf("OracleType() = %q, want %q", c.OracleType(), "canary")
	}
}

func TestCanaryContract_FunctionTemplatePath(t *testing.T) {
	c, ok := mechanism.Get("canary")
	if !ok {
		t.Fatal("canary contract not registered")
	}
	got := c.FunctionTemplatePath("riscv64")
	want := "initial_seeds/riscv64/canary/function_template.c"
	if got != want {
		t.Errorf("FunctionTemplatePath(%q) = %q, want %q", "riscv64", got, want)
	}
}

func TestCanaryContract_PlaceholderFunctionName(t *testing.T) {
	c, ok := mechanism.Get("canary")
	if !ok {
		t.Fatal("canary contract not registered")
	}
	if c.PlaceholderFunctionName() != "seed" {
		t.Errorf("PlaceholderFunctionName() = %q, want %q", c.PlaceholderFunctionName(), "seed")
	}
}

func TestCanaryContract_RequiredMarkers(t *testing.T) {
	c, ok := mechanism.Get("canary")
	if !ok {
		t.Fatal("canary contract not registered")
	}
	markers := c.RequiredMarkers()
	if len(markers) == 0 {
		t.Fatal("RequiredMarkers() returned empty slice")
	}
	found := false
	for _, m := range markers {
		if m == "SEED_RETURNED" {
			found = true
		}
	}
	if !found {
		t.Errorf("RequiredMarkers() does not contain %q; got %v", "SEED_RETURNED", markers)
	}
}

func TestCanaryContract_FuzzTimePromptExample(t *testing.T) {
	c, ok := mechanism.Get("canary")
	if !ok {
		t.Fatal("canary contract not registered")
	}
	ex := c.FuzzTimePromptExample()
	if ex == "" {
		t.Fatal("FuzzTimePromptExample() returned empty string")
	}
	checks := []string{"SEED_RETURNED", "seed(int buf_size", "CRITICAL OUTPUT"}
	for _, s := range checks {
		if !strings.Contains(ex, s) {
			t.Errorf("FuzzTimePromptExample() missing %q", s)
		}
	}
}

func TestCanaryContract_CriticalRulesAddendum(t *testing.T) {
	c, ok := mechanism.Get("canary")
	if !ok {
		t.Fatal("canary contract not registered")
	}
	addendum := c.CriticalRulesAddendum()
	if !strings.Contains(addendum, "stack_protect") {
		t.Errorf("CriticalRulesAddendum() missing stack_protect mention; got %q", addendum)
	}
}
