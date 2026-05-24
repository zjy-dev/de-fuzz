package oracle

import (
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// ---- NewIBTOracle ----

func TestNewIBTOracle_DefaultNegativeCFlags(t *testing.T) {
	o, err := NewIBTOracle(nil, nil, nil, "")
	if err != nil {
		t.Fatalf("NewIBTOracle(nil): %v", err)
	}
	ibt := o.(*IBTOracle)
	if len(ibt.NegativeCFlags) == 0 {
		t.Fatal("NegativeCFlags must not be empty when no options given")
	}
	for _, want := range DefaultIBTNegativeCFlags {
		found := false
		for _, got := range ibt.NegativeCFlags {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("default NegativeCFlags missing %q", want)
		}
	}
}

func TestNewIBTOracle_CustomFlagsSliceInterface(t *testing.T) {
	o, err := NewIBTOracle(map[string]interface{}{
		"negative_cflags": []interface{}{"-fno-cet", "-mbranch-protection=none"},
	}, nil, nil, "")
	if err != nil {
		t.Fatalf("NewIBTOracle: %v", err)
	}
	ibt := o.(*IBTOracle)
	if len(ibt.NegativeCFlags) != 2 {
		t.Errorf("expected 2 custom flags, got %d: %v", len(ibt.NegativeCFlags), ibt.NegativeCFlags)
	}
}

func TestNewIBTOracle_CustomFlagsStringSlice(t *testing.T) {
	o, err := NewIBTOracle(map[string]interface{}{
		"negative_cflags": []string{"-fno-cet"},
	}, nil, nil, "")
	if err != nil {
		t.Fatalf("NewIBTOracle: %v", err)
	}
	ibt := o.(*IBTOracle)
	if len(ibt.NegativeCFlags) != 1 || ibt.NegativeCFlags[0] != "-fno-cet" {
		t.Errorf("unexpected NegativeCFlags: %v", ibt.NegativeCFlags)
	}
}

// ---- isNegativeCase ----

func TestIBTOracle_IsNegativeCase_NilSeed(t *testing.T) {
	o := &IBTOracle{NegativeCFlags: DefaultIBTNegativeCFlags}
	if o.isNegativeCase(nil) {
		t.Error("nil seed must not be a negative case")
	}
}

func TestIBTOracle_IsNegativeCase_FlagProfileTrue(t *testing.T) {
	o := &IBTOracle{NegativeCFlags: DefaultIBTNegativeCFlags}
	s := &seed.Seed{FlagProfile: &seed.FlagProfile{IsNegativeControl: true}}
	if !o.isNegativeCase(s) {
		t.Error("FlagProfile.IsNegativeControl=true must be a negative case")
	}
}

func TestIBTOracle_IsNegativeCase_FlagProfileFalse(t *testing.T) {
	o := &IBTOracle{NegativeCFlags: DefaultIBTNegativeCFlags}
	s := &seed.Seed{FlagProfile: &seed.FlagProfile{IsNegativeControl: false}}
	if o.isNegativeCase(s) {
		t.Error("FlagProfile.IsNegativeControl=false must not be a negative case")
	}
}

func TestIBTOracle_IsNegativeCase_AppliedFlagMatch(t *testing.T) {
	o := &IBTOracle{NegativeCFlags: DefaultIBTNegativeCFlags}
	s := &seed.Seed{
		AppliedLLMCFlags: []string{"-fcf-protection=none"},
		LLMCFlagsApplied: true,
	}
	if !o.isNegativeCase(s) {
		t.Error("-fcf-protection=none must be a negative case")
	}
}

func TestIBTOracle_IsNegativeCase_AppliedFlagNoMatch(t *testing.T) {
	o := &IBTOracle{NegativeCFlags: DefaultIBTNegativeCFlags}
	s := &seed.Seed{
		AppliedLLMCFlags: []string{"-O2", "-march=native"},
		LLMCFlagsApplied: true,
	}
	if o.isNegativeCase(s) {
		t.Error("unrelated flags must not be a negative case")
	}
}

func TestIBTOracle_IsNegativeCase_NotApplied(t *testing.T) {
	o := &IBTOracle{NegativeCFlags: DefaultIBTNegativeCFlags}
	s := &seed.Seed{
		AppliedLLMCFlags: []string{"-fcf-protection=none"},
		LLMCFlagsApplied: false,
	}
	if o.isNegativeCase(s) {
		t.Error("LLMCFlagsApplied=false must not trigger negative case even with matching flag")
	}
}

func TestIBTOracle_IsNegativeCase_EmptyOracleNegativeCFlags(t *testing.T) {
	o := &IBTOracle{NegativeCFlags: nil}
	s := &seed.Seed{
		AppliedLLMCFlags: []string{"-fcf-protection=none"},
		LLMCFlagsApplied: true,
	}
	if o.isNegativeCase(s) {
		t.Error("empty NegativeCFlags on oracle must never match")
	}
}

// ---- polarityFor ----

func TestIBTOracle_PolarityFor_Positive(t *testing.T) {
	o := &IBTOracle{NegativeCFlags: DefaultIBTNegativeCFlags}
	if p := o.polarityFor(&seed.Seed{}); p != PolarityPositive {
		t.Errorf("plain seed: want PolarityPositive, got %v", p)
	}
}

func TestIBTOracle_PolarityFor_Inverted(t *testing.T) {
	o := &IBTOracle{NegativeCFlags: DefaultIBTNegativeCFlags}
	s := &seed.Seed{
		AppliedLLMCFlags: []string{"-fcf-protection=none"},
		LLMCFlagsApplied: true,
	}
	if p := o.polarityFor(s); p != PolarityInverted {
		t.Errorf("negative seed: want PolarityInverted, got %v", p)
	}
}

// ---- Analyze error paths ----

func TestIBTOracle_Analyze_NilContext(t *testing.T) {
	o := &IBTOracle{NegativeCFlags: DefaultIBTNegativeCFlags}
	_, err := o.Analyze(&seed.Seed{}, nil, nil)
	if err == nil {
		t.Error("nil AnalyzeContext must return error")
	}
}

func TestIBTOracle_Analyze_EmptyBinaryPath(t *testing.T) {
	o := &IBTOracle{NegativeCFlags: DefaultIBTNegativeCFlags}
	_, err := o.Analyze(&seed.Seed{}, &AnalyzeContext{BinaryPath: ""}, nil)
	if err == nil {
		t.Error("empty BinaryPath must return error")
	}
}

// ---- registry ----

func TestIBTOracle_RegisteredAsIBT(t *testing.T) {
	o, err := New("ibt", nil, nil, nil, "")
	if err != nil {
		t.Fatalf("oracle 'ibt' not found in registry: %v", err)
	}
	if _, ok := o.(*IBTOracle); !ok {
		t.Errorf("registry 'ibt' returned %T, want *IBTOracle", o)
	}
}
