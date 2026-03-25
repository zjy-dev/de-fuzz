package fuzz

import (
	"strings"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/config"
	"github.com/zjy-dev/de-fuzz/internal/coverage"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
)

func TestNewFlagScheduler_DefaultProfileOrder(t *testing.T) {
	scheduler, err := NewFlagScheduler("aarch64", testFlagStrategyConfig())
	if err != nil {
		t.Fatalf("failed to create scheduler: %v", err)
	}

	profile := scheduler.DefaultProfileForSeed("void seed(void) { char buf[16]; }")
	if profile == nil {
		t.Fatal("expected default profile")
	}
	if profile.Name != "policy-strong__threshold-8__pic-default__guard-default" {
		t.Fatalf("unexpected default profile %q", profile.Name)
	}
}

func TestFlagScheduler_SkipsExplicitProfileWithoutAttribute(t *testing.T) {
	scheduler, err := NewFlagScheduler("aarch64", testFlagStrategyConfig())
	if err != nil {
		t.Fatalf("failed to create scheduler: %v", err)
	}

	target := &coverage.TargetInfo{Function: "expand_used_vars", BBID: 7}
	explicitIndex := -1
	for idx, profile := range scheduler.mainProfiles {
		if profile.AxisValues["policy"] == "explicit" {
			explicitIndex = idx
			break
		}
	}
	if explicitIndex == -1 {
		t.Fatal("expected explicit profile in scheduler")
	}

	scheduler.targetCursor[targetKey(target)] = explicitIndex
	profile := scheduler.NextProfileForTarget(target, "void seed(void) { char buf[16]; }")
	if profile == nil {
		t.Fatal("expected profile")
	}
	if profile.AxisValues["policy"] == "explicit" {
		t.Fatalf("explicit profile should be skipped for sources without stack_protect attribute")
	}
}

func TestFlagScheduler_InsertsNegativeControlPeriodically(t *testing.T) {
	scheduler, err := NewFlagScheduler("aarch64", testFlagStrategyConfig())
	if err != nil {
		t.Fatalf("failed to create scheduler: %v", err)
	}

	var target *coverage.TargetInfo
	for idx := 1; idx <= negativeControlInterval; idx++ {
		target = &coverage.TargetInfo{Function: "expand_used_vars", BBID: idx}
		scheduler.BeginTarget(target)
	}

	profile := scheduler.NextProfileForTarget(target, "void seed(void) { char buf[16]; }")
	if profile == nil {
		t.Fatal("expected profile")
	}
	if !profile.IsNegativeControl {
		t.Fatalf("expected negative control profile, got %q", profile.Name)
	}
}

func TestClonePromptProfile_PreservesNegativeControl(t *testing.T) {
	ctx := &prompt.TargetContext{
		ActiveFlagProfileName:   "negative-control__fno-stack-protector",
		ActiveFlagProfileFlags:  []string{"-fno-stack-protector"},
		ActiveFlagProfileAxes:   map[string]string{"policy": "negative_control"},
		ActiveIsNegativeControl: true,
	}

	profile := clonePromptProfile(ctx)
	if profile == nil {
		t.Fatal("expected profile")
	}
	if !profile.IsNegativeControl {
		t.Fatal("expected negative control flag to be preserved")
	}
}

func TestNewFlagScheduler_X64Profiles(t *testing.T) {
	cfg := testFlagStrategyConfig()
	cfg.Axes.ByISA["x64"] = map[string][][]string{
		"guard_source": {
			{},
			{"-mstack-protector-guard=global"},
			{"-mstack-protector-guard=tls", "-mstack-protector-guard-reg=fs", "-mstack-protector-guard-offset=20"},
			{"-mstack-protector-guard=tls", "-mstack-protector-guard-reg=gs", "-mstack-protector-guard-offset=20"},
		},
	}

	scheduler, err := NewFlagScheduler("x64", cfg)
	if err != nil {
		t.Fatalf("failed to create x64 scheduler: %v", err)
	}
	if scheduler.defaultProfile == nil {
		t.Fatal("expected default profile")
	}
	foundTLS := false
	for _, profile := range scheduler.mainProfiles {
		if profile.AxisValues["guard_mode"] == "tls-fs-off20" {
			foundTLS = true
			break
		}
	}
	if !foundTLS {
		t.Fatal("expected x64 TLS guard profile")
	}
}

func TestNewFlagScheduler_RiscvProfiles(t *testing.T) {
	cfg := testFlagStrategyConfig()
	cfg.Axes.ByISA["riscv64"] = map[string][][]string{
		"guard_source": {
			{},
			{"-mstack-protector-guard=global"},
			{"-mstack-protector-guard=tls", "-mstack-protector-guard-reg=<config-provided-gpr>", "-mstack-protector-guard-offset=0"},
			{"-mstack-protector-guard=tls", "-mstack-protector-guard-reg=<same-gpr>", "-mstack-protector-guard-offset=16"},
		},
	}
	cfg.ISAOptions["riscv64"] = config.FlagStrategyISAOptionConfig{
		StackProtectorGuardReg: "tp",
		SupportsHardwareTLS:    true,
	}

	scheduler, err := NewFlagScheduler("riscv64", cfg)
	if err != nil {
		t.Fatalf("failed to create riscv64 scheduler: %v", err)
	}

	foundTLS := false
	for _, profile := range scheduler.mainProfiles {
		if strings.HasPrefix(profile.AxisValues["guard_mode"], "tls-tp") {
			foundTLS = true
			break
		}
	}
	if !foundTLS {
		t.Fatal("expected riscv64 TLS guard profile")
	}
}

func testFlagStrategyConfig() config.FlagStrategyConfig {
	return config.FlagStrategyConfig{
		Enabled:                 true,
		Mode:                    "matrix",
		AllowLLMCFlags:          false,
		IncludeNegativeControls: true,
		SelectionOrder:          "deterministic",
		NegativeControls: [][]string{
			{"-fno-stack-protector"},
		},
		Axes: config.FlagStrategyAxesConfig{
			Common: map[string][][]string{
				"policy": {
					{"-fstack-protector"},
					{"-fstack-protector-strong"},
					{"-fstack-protector-all"},
					{"-fstack-protector-explicit"},
				},
				"threshold": {
					{"--param=ssp-buffer-size=1"},
					{"--param=ssp-buffer-size=8"},
					{"--param=ssp-buffer-size=32"},
				},
				"pic_mode": {
					{},
					{"-fPIC"},
				},
			},
			ByISA: map[string]map[string][][]string{
				"aarch64": {
					"guard_source": {
						{},
						{"-mstack-protector-guard=global"},
						{"-mstack-protector-guard=sysreg", "-mstack-protector-guard-reg=<config-provided-valid-sysreg>", "-mstack-protector-guard-offset=0"},
						{"-mstack-protector-guard=sysreg", "-mstack-protector-guard-reg=<same-sysreg>", "-mstack-protector-guard-offset=16"},
					},
				},
			},
		},
		ISAOptions: map[string]config.FlagStrategyISAOptionConfig{
			"aarch64": {
				StackProtectorGuardReg: "tpidr_el0",
			},
		},
	}
}
