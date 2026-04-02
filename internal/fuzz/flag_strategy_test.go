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

func TestNewFlagScheduler_LoongArchLayoutProfiles(t *testing.T) {
	cfg := testFlagStrategyConfig()
	cfg.Axes.ByISA["loongarch64"] = map[string][][]string{
		"layout": {
			{},
			{"-fpack-struct"},
			{"-fpack-struct=1"},
			{"-fpack-struct=2"},
			{"-fpack-struct=4"},
			{"-fshort-enums"},
			{"-fpack-struct", "-fshort-enums"},
		},
	}

	scheduler, err := NewFlagScheduler("loongarch64", cfg)
	if err != nil {
		t.Fatalf("failed to create loongarch64 scheduler: %v", err)
	}
	if scheduler.defaultProfile == nil {
		t.Fatal("expected default profile")
	}
	if scheduler.defaultProfile.Name != "policy-strong__threshold-8__pic-default__guard-default__layout-default" {
		t.Fatalf("unexpected default profile %q", scheduler.defaultProfile.Name)
	}

	foundPack := false
	foundPack1 := false
	foundShortEnums := false
	foundCombined := false
	for _, profile := range scheduler.mainProfiles {
		switch profile.AxisValues["layout_mode"] {
		case "pack":
			foundPack = true
		case "pack1":
			foundPack1 = true
		case "short-enums":
			foundShortEnums = true
		case "pack+short-enums":
			foundCombined = true
		}
	}
	if !foundPack || !foundPack1 || !foundShortEnums || !foundCombined {
		t.Fatalf("expected loongarch64 layout profiles, got pack=%t pack1=%t short-enums=%t combined=%t", foundPack, foundPack1, foundShortEnums, foundCombined)
	}
}

func TestFlagScheduler_BlockedLLMFlagFamilies_ExtendsForLayoutProfiles(t *testing.T) {
	cfg := testFlagStrategyConfig()
	cfg.Axes.ByISA["loongarch64"] = map[string][][]string{
		"layout": {
			{},
			{"-fpack-struct"},
		},
	}

	scheduler, err := NewFlagScheduler("loongarch64", cfg)
	if err != nil {
		t.Fatalf("failed to create loongarch64 scheduler: %v", err)
	}

	blocked := scheduler.BlockedLLMFlagFamilies()
	if !containsString(blocked, "-fpack-struct*") {
		t.Fatalf("expected -fpack-struct* in blocked families: %v", blocked)
	}
	if !containsString(blocked, "-fshort-enums") {
		t.Fatalf("expected -fshort-enums in blocked families: %v", blocked)
	}
}

func TestFlagScheduler_BlockedLLMFlagFamilies_NoLayoutProfilesKeepsCanarySet(t *testing.T) {
	scheduler, err := NewFlagScheduler("aarch64", testFlagStrategyConfig())
	if err != nil {
		t.Fatalf("failed to create scheduler: %v", err)
	}

	blocked := scheduler.BlockedLLMFlagFamilies()
	if containsString(blocked, "-fpack-struct*") {
		t.Fatalf("did not expect layout families for non-layout scheduler: %v", blocked)
	}
	if containsString(blocked, "-fshort-enums") {
		t.Fatalf("did not expect layout families for non-layout scheduler: %v", blocked)
	}
}

func TestNewFlagScheduler_FortifyDefaultProfileOrder(t *testing.T) {
	scheduler, err := NewFlagSchedulerForStrategy("fortify", "aarch64", testFortifyFlagStrategyConfig())
	if err != nil {
		t.Fatalf("failed to create fortify scheduler: %v", err)
	}

	profile := scheduler.DefaultProfileForSeed("void seed(int buf_size, int fill_size) { char buf[16]; memcpy(buf, buf, fill_size); }")
	if profile == nil {
		t.Fatal("expected default profile")
	}
	if profile.Name != "optimization-O2__fortify_mode-hardened__stack_protector_mode-no-stack-protector" {
		t.Fatalf("unexpected default fortify profile %q", profile.Name)
	}
}

func TestFlagScheduler_FortifyBlockedLLMFamilies(t *testing.T) {
	scheduler, err := NewFlagSchedulerForStrategy("fortify", "aarch64", testFortifyFlagStrategyConfig())
	if err != nil {
		t.Fatalf("failed to create fortify scheduler: %v", err)
	}

	blocked := scheduler.BlockedLLMFlagFamilies()
	for _, family := range []string{"-O*", "-D_FORTIFY_SOURCE=*", "-U_FORTIFY_SOURCE", "-fhardened", "-fstack-protector*", "-fno-stack-protector*"} {
		if !containsString(blocked, family) {
			t.Fatalf("expected %s in blocked families: %v", family, blocked)
		}
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

func testFortifyFlagStrategyConfig() config.FlagStrategyConfig {
	return config.FlagStrategyConfig{
		Enabled:                 true,
		Mode:                    "matrix",
		AllowLLMCFlags:          true,
		IncludeNegativeControls: false,
		SelectionOrder:          "deterministic",
		Axes: config.FlagStrategyAxesConfig{
			Common: map[string][][]string{
				"optimization": {
					{"-O0"},
					{"-O1"},
					{"-O2"},
					{"-O3"},
				},
				"fortify_mode": {
					{"-D_FORTIFY_SOURCE=0"},
					{"-D_FORTIFY_SOURCE=1"},
					{"-D_FORTIFY_SOURCE=2"},
					{"-D_FORTIFY_SOURCE=3"},
					{"-fhardened"},
					{"-fhardened", "-U_FORTIFY_SOURCE"},
					{"-fhardened", "-D_FORTIFY_SOURCE=1"},
				},
				"stack_protector_mode": {
					{"-fno-stack-protector"},
				},
			},
		},
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
