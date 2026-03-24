package fuzz

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/config"
	"github.com/zjy-dev/de-fuzz/internal/coverage"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

const negativeControlInterval = 20

var canaryLLMBlockedFlagFamilies = []string{
	"-fstack-protector*",
	"-fno-stack-protector*",
	"--param=ssp-buffer-size=*",
	"-fpic / -fPIC / -fpie / -fPIE",
	"-mstack-protector-guard*",
	"-fhardened",
}

// FlagScheduler deterministically rotates compiler flag profiles during fuzzing.
type FlagScheduler struct {
	allowLLMCFlags  bool
	mainProfiles    []*seed.FlagProfile
	defaultProfile  *seed.FlagProfile
	negative        *seed.FlagProfile
	targetCursor    map[string]int
	pendingNegative map[string]bool
	targetCount     int
}

// NewFlagScheduler builds a canary-specific flag scheduler from configuration.
func NewFlagScheduler(isa string, cfg config.FlagStrategyConfig) (*FlagScheduler, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.Mode != "" && cfg.Mode != "matrix" {
		return nil, fmt.Errorf("unsupported flag strategy mode: %s", cfg.Mode)
	}

	profiles, defaultProfile, err := buildProfilesForISA(isa, cfg)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("no flag profiles available for ISA %s", isa)
	}

	scheduler := &FlagScheduler{
		allowLLMCFlags:  cfg.AllowLLMCFlags,
		mainProfiles:    profiles,
		defaultProfile:  defaultProfile,
		targetCursor:    make(map[string]int),
		pendingNegative: make(map[string]bool),
	}

	if cfg.IncludeNegativeControls {
		scheduler.negative = buildNegativeProfile(cfg.NegativeControls)
	}

	return scheduler, nil
}

// AllowLLMCFlags reports whether LLM-suggested flags should affect the compiler argv.
func (s *FlagScheduler) AllowLLMCFlags() bool {
	if s == nil {
		return true
	}
	return s.allowLLMCFlags
}

// BlockedLLMFlagFamilies returns the flag families reserved for profile selection.
func (s *FlagScheduler) BlockedLLMFlagFamilies() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), canaryLLMBlockedFlagFamilies...)
}

// DefaultProfileForSeed returns the baseline profile used for non-targeted compilations.
func (s *FlagScheduler) DefaultProfileForSeed(source string) *seed.FlagProfile {
	if s == nil || s.defaultProfile == nil {
		return nil
	}
	if isProfileApplicable(s.defaultProfile, source, s.allowLLMCFlags) {
		return s.defaultProfile.Clone()
	}
	for _, profile := range s.mainProfiles {
		if isProfileApplicable(profile, source, s.allowLLMCFlags) {
			return profile.Clone()
		}
	}
	return s.defaultProfile.Clone()
}

// BeginTarget marks the start of a target-solving session.
func (s *FlagScheduler) BeginTarget(target *coverage.TargetInfo) {
	if s == nil || target == nil {
		return
	}
	s.targetCount++
	if s.negative != nil && s.targetCount%negativeControlInterval == 0 {
		s.pendingNegative[targetKey(target)] = true
	}
}

// NextProfileForTarget returns the next applicable profile for the given target and source.
func (s *FlagScheduler) NextProfileForTarget(target *coverage.TargetInfo, source string) *seed.FlagProfile {
	if s == nil || target == nil || len(s.mainProfiles) == 0 {
		return nil
	}

	key := targetKey(target)
	if s.pendingNegative[key] && s.negative != nil {
		delete(s.pendingNegative, key)
		return s.negative.Clone()
	}

	start := s.targetCursor[key]
	for offset := 0; offset < len(s.mainProfiles); offset++ {
		idx := (start + offset) % len(s.mainProfiles)
		profile := s.mainProfiles[idx]
		if !isProfileApplicable(profile, source, s.allowLLMCFlags) {
			continue
		}
		s.targetCursor[key] = (idx + 1) % len(s.mainProfiles)
		return profile.Clone()
	}

	return s.defaultProfile.Clone()
}

func targetKey(target *coverage.TargetInfo) string {
	return fmt.Sprintf("%s:%d", target.Function, target.BBID)
}

func buildProfilesForISA(isa string, cfg config.FlagStrategyConfig) ([]*seed.FlagProfile, *seed.FlagProfile, error) {
	if isa != "aarch64" {
		return nil, nil, fmt.Errorf("flag strategy currently supports only aarch64 canary")
	}

	common := cfg.Axes.Common
	byISA := cfg.Axes.ByISA[isa]
	if len(common) == 0 {
		return nil, nil, fmt.Errorf("flag strategy common axes are required")
	}
	if len(byISA) == 0 {
		return nil, nil, fmt.Errorf("flag strategy ISA axes missing for %s", isa)
	}

	policyValues := common["policy"]
	thresholdValues := common["threshold"]
	picValues := common["pic_mode"]
	guardValues := byISA["guard_source"]
	if len(policyValues) == 0 || len(thresholdValues) == 0 || len(picValues) == 0 {
		return nil, nil, fmt.Errorf("policy, threshold and pic_mode axes are required")
	}
	if len(guardValues) == 0 {
		guardValues = [][]string{nil}
	}

	isaOptions := cfg.ISAOptions[isa]
	profiles := make([]*seed.FlagProfile, 0)
	seen := make(map[string]bool)

	for _, policyFlags := range policyValues {
		for _, thresholdFlags := range thresholdValues {
			for _, picFlags := range picValues {
				for _, guardFlags := range guardValues {
					flags, axes := materializeProfile(policyFlags, thresholdFlags, picFlags, guardFlags, isaOptions)
					if len(flags) == 0 {
						continue
					}
					name := buildProfileName(axes)
					if seen[name] {
						continue
					}
					seen[name] = true
					profiles = append(profiles, &seed.FlagProfile{
						Name:       name,
						AxisValues: axes,
						Flags:      flags,
					})
				}
			}
		}
	}

	sort.SliceStable(profiles, func(i, j int) bool {
		return compareProfilePriority(profiles[i], profiles[j]) < 0
	})

	if len(profiles) == 0 {
		return nil, nil, fmt.Errorf("no materialized profiles generated")
	}

	return profiles, profiles[0].Clone(), nil
}

func materializeProfile(policyFlags, thresholdFlags, picFlags, guardFlags []string, isaOptions config.FlagStrategyISAOptionConfig) ([]string, map[string]string) {
	flags := make([]string, 0, len(policyFlags)+len(thresholdFlags)+len(picFlags)+len(guardFlags))
	axes := map[string]string{
		"policy":     axisLabel("policy", policyFlags),
		"threshold":  axisLabel("threshold", thresholdFlags),
		"pic_mode":   axisLabel("pic_mode", picFlags),
		"guard_mode": axisLabel("guard_mode", guardFlags),
	}

	flags = append(flags, policyFlags...)
	flags = append(flags, thresholdFlags...)
	flags = append(flags, picFlags...)

	for _, rawFlag := range guardFlags {
		flag := rawFlag
		if strings.Contains(flag, "<config-provided-valid-sysreg>") {
			if isaOptions.StackProtectorGuardReg == "" {
				return nil, nil
			}
			flag = strings.ReplaceAll(flag, "<config-provided-valid-sysreg>", isaOptions.StackProtectorGuardReg)
		}
		if strings.Contains(flag, "<same-sysreg>") {
			if isaOptions.StackProtectorGuardReg == "" {
				return nil, nil
			}
			flag = strings.ReplaceAll(flag, "<same-sysreg>", isaOptions.StackProtectorGuardReg)
		}
		flags = append(flags, flag)
	}

	return flags, axes
}

func axisLabel(axis string, flags []string) string {
	if len(flags) == 0 {
		return "default"
	}

	switch axis {
	case "policy":
		flag := flags[0]
		switch flag {
		case "-fstack-protector":
			return "ssp"
		case "-fstack-protector-strong":
			return "strong"
		case "-fstack-protector-all":
			return "all"
		case "-fstack-protector-explicit":
			return "explicit"
		default:
			return strings.TrimPrefix(flag, "-")
		}
	case "threshold":
		for _, flag := range flags {
			const prefix = "--param=ssp-buffer-size="
			if strings.HasPrefix(flag, prefix) {
				return strings.TrimPrefix(flag, prefix)
			}
		}
	case "pic_mode":
		return strings.TrimPrefix(flags[0], "-")
	case "guard_mode":
		hasSysreg := false
		for _, flag := range flags {
			if flag == "-mstack-protector-guard=global" {
				return "global"
			}
			if strings.HasPrefix(flag, "-mstack-protector-guard-offset=") {
				offset := strings.TrimPrefix(flag, "-mstack-protector-guard-offset=")
				if hasSysreg {
					return "sysreg-off" + offset
				}
			}
			if strings.HasPrefix(flag, "-mstack-protector-guard=sysreg") {
				hasSysreg = true
			}
		}
		if hasSysreg {
			for _, flag := range flags {
				if strings.HasPrefix(flag, "-mstack-protector-guard-offset=") {
					return "sysreg-off" + strings.TrimPrefix(flag, "-mstack-protector-guard-offset=")
				}
			}
			return "sysreg"
		}
	}

	parts := make([]string, 0, len(flags))
	for _, flag := range flags {
		parts = append(parts, strings.TrimPrefix(flag, "-"))
	}
	return strings.Join(parts, "+")
}

func buildProfileName(axes map[string]string) string {
	nameParts := []string{
		"policy-" + axes["policy"],
		"threshold-" + axes["threshold"],
		"pic-" + axes["pic_mode"],
		"guard-" + axes["guard_mode"],
	}
	return strings.Join(nameParts, "__")
}

func compareProfilePriority(left, right *seed.FlagProfile) int {
	leftRank := profileRank(left)
	rightRank := profileRank(right)
	if leftRank != rightRank {
		return leftRank - rightRank
	}
	if left.Name < right.Name {
		return -1
	}
	if left.Name > right.Name {
		return 1
	}
	return 0
}

func profileRank(profile *seed.FlagProfile) int {
	if profile == nil {
		return 1 << 30
	}

	policy := profile.AxisValues["policy"]
	threshold := profile.AxisValues["threshold"]
	pic := profile.AxisValues["pic_mode"]
	guard := profile.AxisValues["guard_mode"]

	switch {
	case policy == "strong" && threshold == "8" && pic == "default" && guard == "default":
		return 0
	case policy == "strong" && threshold == "1" && pic == "default" && guard == "default":
		return 1
	case policy == "strong" && threshold == "32" && pic == "default" && guard == "default":
		return 2
	case policy == "all" && threshold == "8" && pic == "default" && guard == "default":
		return 3
	case policy == "strong" && threshold == "8" && pic == "fPIC" && guard == "default":
		return 4
	case policy == "strong" && threshold == "8" && pic == "default" && guard == "global":
		return 5
	case policy == "strong" && threshold == "8" && pic == "default" && strings.HasPrefix(guard, "sysreg") && hasGuardOffset(profile, "0"):
		return 6
	case policy == "strong" && threshold == "8" && pic == "default" && strings.HasPrefix(guard, "sysreg") && hasGuardOffset(profile, "16"):
		return 7
	case policy == "explicit" && threshold == "8" && pic == "default" && guard == "default":
		return 8
	}

	return 100 + lexicalAxisRank(profile)
}

func lexicalAxisRank(profile *seed.FlagProfile) int {
	policyRank := map[string]int{
		"strong":   0,
		"all":      1,
		"ssp":      2,
		"explicit": 3,
	}
	guardRank := map[string]int{
		"default":      0,
		"global":       1,
		"sysreg-off0":  2,
		"sysreg-off16": 3,
	}

	threshold, err := strconv.Atoi(profile.AxisValues["threshold"])
	if err != nil {
		threshold = 999
	}
	rank := policyRank[profile.AxisValues["policy"]] * 1000
	rank += threshold * 10
	if profile.AxisValues["pic_mode"] == "fPIC" {
		rank += 1
	}
	rank += guardRank[profile.AxisValues["guard_mode"]] * 10000
	return rank
}

func hasGuardOffset(profile *seed.FlagProfile, want string) bool {
	if profile == nil {
		return false
	}
	for _, flag := range profile.Flags {
		if flag == "-mstack-protector-guard-offset="+want {
			return true
		}
	}
	return false
}

func buildNegativeProfile(controls [][]string) *seed.FlagProfile {
	flags := []string{"-fno-stack-protector"}
	if len(controls) > 0 && len(controls[0]) > 0 {
		flags = append([]string(nil), controls[0]...)
	}
	return &seed.FlagProfile{
		Name: "negative-control__fno-stack-protector",
		AxisValues: map[string]string{
			"policy":     "negative_control",
			"threshold":  "default",
			"pic_mode":   "default",
			"guard_mode": "default",
		},
		Flags:             flags,
		IsNegativeControl: true,
	}
}

func isProfileApplicable(profile *seed.FlagProfile, source string, allowLLMCFlags bool) bool {
	if profile == nil {
		return false
	}
	if profile.AxisValues["policy"] == "explicit" && !hasStackProtectAttribute(source) && !allowLLMCFlags {
		return false
	}
	return true
}

func hasStackProtectAttribute(source string) bool {
	return strings.Contains(source, "__attribute__((stack_protect))") ||
		strings.Contains(source, "__attribute__ ((stack_protect))")
}
