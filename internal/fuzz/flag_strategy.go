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

const (
	flagStrategyCanary  = "canary"
	flagStrategyFortify = "fortify"
)

var canaryLLMBlockedFlagFamilies = []string{
	"-fstack-protector*",
	"-fno-stack-protector*",
	"--param=ssp-buffer-size=*",
	"-fpic / -fPIC / -fpie / -fPIE",
	"-mstack-protector-guard*",
	"-fhardened",
}

var canaryLayoutLLMBlockedFlagFamilies = []string{
	"-fpack-struct*",
	"-fshort-enums",
}

var fortifyLLMBlockedFlagFamilies = []string{
	"-O*",
	"-D_FORTIFY_SOURCE=*",
	"-U_FORTIFY_SOURCE",
	"-fhardened",
	"-fstack-protector*",
	"-fno-stack-protector*",
}

// FlagScheduler deterministically rotates compiler flag profiles during fuzzing.
type FlagScheduler struct {
	strategy               string
	allowLLMCFlags         bool
	blockedLLMFlagFamilies []string
	mainProfiles           []*seed.FlagProfile
	defaultProfile         *seed.FlagProfile
	negative               *seed.FlagProfile
	targetCursor           map[string]int
	pendingNegative        map[string]bool
	targetCount            int
}

// NewFlagScheduler builds a canary-specific flag scheduler from configuration.
func NewFlagScheduler(isa string, cfg config.FlagStrategyConfig) (*FlagScheduler, error) {
	return NewFlagSchedulerForStrategy("", isa, cfg)
}

// NewFlagSchedulerForStrategy builds a deterministic compiler flag scheduler from configuration.
func NewFlagSchedulerForStrategy(strategy string, isa string, cfg config.FlagStrategyConfig) (*FlagScheduler, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.Mode != "" && cfg.Mode != "matrix" {
		return nil, fmt.Errorf("unsupported flag strategy mode: %s", cfg.Mode)
	}

	strategy = resolveFlagStrategyName(strategy, cfg)
	profiles, defaultProfile, err := buildProfilesForStrategy(strategy, isa, cfg)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("no %s flag profiles available for ISA %s", strategy, isa)
	}

	scheduler := &FlagScheduler{
		strategy:               strategy,
		allowLLMCFlags:         cfg.AllowLLMCFlags,
		blockedLLMFlagFamilies: blockedLLMFlagFamiliesForStrategy(strategy, profiles),
		mainProfiles:           profiles,
		defaultProfile:         defaultProfile,
		targetCursor:           make(map[string]int),
		pendingNegative:        make(map[string]bool),
	}

	if cfg.IncludeNegativeControls {
		scheduler.negative = buildNegativeProfile(cfg.NegativeControls, defaultProfile.AxisValues)
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
	if len(s.blockedLLMFlagFamilies) == 0 {
		return append([]string(nil), canaryLLMBlockedFlagFamilies...)
	}
	return append([]string(nil), s.blockedLLMFlagFamilies...)
}

// DefaultProfileForSeed returns the baseline profile used for non-targeted compilations.
func (s *FlagScheduler) DefaultProfileForSeed(source string) *seed.FlagProfile {
	if s == nil || s.defaultProfile == nil {
		return nil
	}
	if isProfileApplicable(s.strategy, s.defaultProfile, source, s.allowLLMCFlags) {
		return s.defaultProfile.Clone()
	}
	for _, profile := range s.mainProfiles {
		if isProfileApplicable(s.strategy, profile, source, s.allowLLMCFlags) {
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
		if !isProfileApplicable(s.strategy, profile, source, s.allowLLMCFlags) {
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

func resolveFlagStrategyName(strategy string, cfg config.FlagStrategyConfig) string {
	strategy = strings.TrimSpace(strings.ToLower(strategy))
	if strategy != "" {
		return strategy
	}

	switch {
	case len(cfg.Axes.Common["fortify_mode"]) > 0 || len(cfg.Axes.Common["optimization"]) > 0:
		return flagStrategyFortify
	default:
		return flagStrategyCanary
	}
}

func buildProfilesForStrategy(strategy string, isa string, cfg config.FlagStrategyConfig) ([]*seed.FlagProfile, *seed.FlagProfile, error) {
	switch strategy {
	case flagStrategyCanary:
		return buildCanaryProfilesForISA(isa, cfg)
	case flagStrategyFortify:
		return buildFortifyProfiles(isa, cfg)
	default:
		return nil, nil, fmt.Errorf("unsupported flag strategy for %q", strategy)
	}
}

func buildCanaryProfilesForISA(isa string, cfg config.FlagStrategyConfig) ([]*seed.FlagProfile, *seed.FlagProfile, error) {
	isaKey, isaAxes, isaOptions, err := resolveCanaryISAConfig(isa, cfg)
	if err != nil {
		return nil, nil, err
	}

	common := cfg.Axes.Common
	if len(common) == 0 {
		return nil, nil, fmt.Errorf("flag strategy common axes are required")
	}
	if len(isaAxes) == 0 {
		return nil, nil, fmt.Errorf("flag strategy ISA axes missing for %s", isaKey)
	}

	policyValues := common["policy"]
	thresholdValues := common["threshold"]
	picValues := common["pic_mode"]
	if len(policyValues) == 0 || len(thresholdValues) == 0 || len(picValues) == 0 {
		return nil, nil, fmt.Errorf("policy, threshold and pic_mode axes are required")
	}

	guardValues := axisValuesOrDefault(isaAxes["guard_source"])
	layoutValues := axisValuesOrDefault(isaAxes["layout"])
	includeLayoutAxis := hasAxis(isaAxes, "layout")

	profiles := make([]*seed.FlagProfile, 0)
	seen := make(map[string]bool)

	for _, policyFlags := range policyValues {
		for _, thresholdFlags := range thresholdValues {
			for _, picFlags := range picValues {
				for _, guardFlags := range guardValues {
					for _, layoutFlags := range layoutValues {
						flags, axes := materializeProfile(
							policyFlags,
							thresholdFlags,
							picFlags,
							guardFlags,
							layoutFlags,
							isaOptions,
							includeLayoutAxis,
						)
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
	}

	sort.SliceStable(profiles, func(i, j int) bool {
		return compareProfilePriority(flagStrategyCanary, profiles[i], profiles[j]) < 0
	})

	if len(profiles) == 0 {
		return nil, nil, fmt.Errorf("no materialized profiles generated")
	}

	return profiles, profiles[0].Clone(), nil
}

func buildFortifyProfiles(isa string, cfg config.FlagStrategyConfig) ([]*seed.FlagProfile, *seed.FlagProfile, error) {
	_ = isa

	common := cfg.Axes.Common
	if len(common) == 0 {
		return nil, nil, fmt.Errorf("flag strategy common axes are required")
	}

	optimizationValues := axisValuesOrDefault(common["optimization"])
	fortifyValues := axisValuesOrDefault(common["fortify_mode"])
	stackProtectorValues := axisValuesOrDefault(common["stack_protector_mode"])
	if len(common["optimization"]) == 0 || len(common["fortify_mode"]) == 0 || len(common["stack_protector_mode"]) == 0 {
		return nil, nil, fmt.Errorf("optimization, fortify_mode and stack_protector_mode axes are required")
	}

	profiles := make([]*seed.FlagProfile, 0)
	seen := make(map[string]bool)

	for _, optimizationFlags := range optimizationValues {
		for _, fortifyFlags := range fortifyValues {
			for _, stackProtectorFlags := range stackProtectorValues {
				flags := make([]string, 0, len(optimizationFlags)+len(fortifyFlags)+len(stackProtectorFlags))
				flags = append(flags, optimizationFlags...)
				flags = append(flags, fortifyFlags...)
				flags = append(flags, stackProtectorFlags...)

				axes := map[string]string{
					"optimization":         fortifyAxisLabel("optimization", optimizationFlags),
					"fortify_mode":         fortifyAxisLabel("fortify_mode", fortifyFlags),
					"stack_protector_mode": fortifyAxisLabel("stack_protector_mode", stackProtectorFlags),
				}
				name := buildFortifyProfileName(axes)
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

	sort.SliceStable(profiles, func(i, j int) bool {
		return compareProfilePriority(flagStrategyFortify, profiles[i], profiles[j]) < 0
	})

	if len(profiles) == 0 {
		return nil, nil, fmt.Errorf("no materialized fortify profiles generated")
	}

	return profiles, profiles[0].Clone(), nil
}

func resolveCanaryISAConfig(isa string, cfg config.FlagStrategyConfig) (string, map[string][][]string, config.FlagStrategyISAOptionConfig, error) {
	candidates := []string{isa}
	switch isa {
	case "x64":
		candidates = append(candidates, "x64/i386", "i386", "x86")
	case "i386":
		candidates = append(candidates, "x64/i386", "x64", "x86")
	case "riscv64":
		candidates = append(candidates, "riscv")
	case "rs6000":
		candidates = append(candidates, "powerpc64", "ppc64")
	}

	for _, candidate := range candidates {
		if axes, ok := cfg.Axes.ByISA[candidate]; ok {
			if options, exists := cfg.ISAOptions[candidate]; exists {
				return candidate, axes, options, nil
			}
			return candidate, axes, cfg.ISAOptions[isa], nil
		}
	}

	return "", nil, config.FlagStrategyISAOptionConfig{}, fmt.Errorf("flag strategy currently supports only configured canary ISAs; no axes found for %s", isa)
}

func materializeProfile(
	policyFlags, thresholdFlags, picFlags, guardFlags, layoutFlags []string,
	isaOptions config.FlagStrategyISAOptionConfig,
	includeLayoutAxis bool,
) ([]string, map[string]string) {
	flags := make([]string, 0, len(policyFlags)+len(thresholdFlags)+len(picFlags)+len(guardFlags)+len(layoutFlags))

	flags = append(flags, policyFlags...)
	flags = append(flags, thresholdFlags...)
	flags = append(flags, picFlags...)

	materializedGuardFlags := make([]string, 0, len(guardFlags))
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
		if strings.Contains(flag, "<config-provided-gpr>") {
			if isaOptions.StackProtectorGuardReg == "" {
				return nil, nil
			}
			flag = strings.ReplaceAll(flag, "<config-provided-gpr>", isaOptions.StackProtectorGuardReg)
		}
		if strings.Contains(flag, "<same-gpr>") {
			if isaOptions.StackProtectorGuardReg == "" {
				return nil, nil
			}
			flag = strings.ReplaceAll(flag, "<same-gpr>", isaOptions.StackProtectorGuardReg)
		}
		materializedGuardFlags = append(materializedGuardFlags, flag)
	}

	if guardRequiresHardwareTLS(materializedGuardFlags) && !isaOptions.SupportsHardwareTLS {
		return nil, nil
	}

	flags = append(flags, materializedGuardFlags...)
	flags = append(flags, layoutFlags...)

	axes := map[string]string{
		"policy":     axisLabel("policy", policyFlags),
		"threshold":  axisLabel("threshold", thresholdFlags),
		"pic_mode":   axisLabel("pic_mode", picFlags),
		"guard_mode": axisLabel("guard_mode", materializedGuardFlags),
	}
	if includeLayoutAxis {
		axes["layout_mode"] = axisLabel("layout_mode", layoutFlags)
	}

	return flags, axes
}

func guardRequiresHardwareTLS(flags []string) bool {
	hasTLS := false
	hasReg := false
	hasSymbol := false
	for _, flag := range flags {
		if strings.Contains(flag, "-mstack-protector-guard=tls") {
			hasTLS = true
		}
		if strings.HasPrefix(flag, "-mstack-protector-guard-reg=") {
			hasReg = true
		}
		if strings.HasPrefix(flag, "-mstack-protector-guard-symbol=") {
			hasSymbol = true
		}
	}
	return hasTLS && !hasReg && !hasSymbol
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
		hasTLS := false
		hasSysreg := false
		reg := ""
		for _, flag := range flags {
			if flag == "-mstack-protector-guard=global" {
				return "global"
			}
			if strings.HasPrefix(flag, "-mstack-protector-guard-reg=") {
				reg = strings.TrimPrefix(flag, "-mstack-protector-guard-reg=")
			}
			if strings.HasPrefix(flag, "-mstack-protector-guard-offset=") {
				offset := strings.TrimPrefix(flag, "-mstack-protector-guard-offset=")
				if hasSysreg {
					return "sysreg-off" + offset
				}
				if hasTLS {
					if reg != "" {
						return "tls-" + reg + "-off" + offset
					}
					return "tls-off" + offset
				}
			}
			if strings.HasPrefix(flag, "-mstack-protector-guard=sysreg") {
				hasSysreg = true
			}
			if strings.HasPrefix(flag, "-mstack-protector-guard=tls") {
				hasTLS = true
			}
			if strings.HasPrefix(flag, "-mstack-protector-guard-symbol=") {
				return "tls-symbol"
			}
		}
		if hasTLS {
			if reg != "" {
				return "tls-" + reg
			}
			return "tls"
		}
		if hasSysreg {
			for _, flag := range flags {
				if strings.HasPrefix(flag, "-mstack-protector-guard-offset=") {
					return "sysreg-off" + strings.TrimPrefix(flag, "-mstack-protector-guard-offset=")
				}
			}
			return "sysreg"
		}
	case "layout_mode":
		parts := make([]string, 0, len(flags))
		for _, flag := range flags {
			switch {
			case flag == "-fpack-struct":
				parts = append(parts, "pack")
			case strings.HasPrefix(flag, "-fpack-struct="):
				parts = append(parts, "pack"+strings.TrimPrefix(flag, "-fpack-struct="))
			case flag == "-fshort-enums":
				parts = append(parts, "short-enums")
			default:
				parts = append(parts, strings.TrimPrefix(flag, "-"))
			}
		}
		return strings.Join(parts, "+")
	}

	parts := make([]string, 0, len(flags))
	for _, flag := range flags {
		parts = append(parts, strings.TrimPrefix(flag, "-"))
	}
	return strings.Join(parts, "+")
}

func fortifyAxisLabel(axis string, flags []string) string {
	if len(flags) == 0 {
		return "default"
	}

	switch axis {
	case "optimization":
		return strings.TrimPrefix(flags[0], "-")
	case "fortify_mode":
		hasHardened := false
		level := ""
		hasUndef := false
		for _, flag := range flags {
			switch {
			case flag == "-fhardened":
				hasHardened = true
			case flag == "-U_FORTIFY_SOURCE":
				hasUndef = true
			case strings.HasPrefix(flag, "-D_FORTIFY_SOURCE="):
				level = strings.TrimPrefix(flag, "-D_FORTIFY_SOURCE=")
			}
		}
		switch {
		case hasHardened && hasUndef:
			return "hardened-no-fortify"
		case hasHardened && level != "":
			return "hardened-fortify" + level
		case hasHardened:
			return "hardened"
		case level != "":
			return "fortify" + level
		}
	case "stack_protector_mode":
		switch flags[0] {
		case "-fno-stack-protector":
			return "no-stack-protector"
		case "-fstack-protector":
			return "stack-protector"
		case "-fstack-protector-strong":
			return "stack-protector-strong"
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
	if layoutMode, ok := axes["layout_mode"]; ok {
		nameParts = append(nameParts, "layout-"+layoutMode)
	}
	return strings.Join(nameParts, "__")
}

func buildFortifyProfileName(axes map[string]string) string {
	nameParts := []string{
		"optimization-" + axes["optimization"],
		"fortify_mode-" + axes["fortify_mode"],
		"stack_protector_mode-" + axes["stack_protector_mode"],
	}
	return strings.Join(nameParts, "__")
}

func compareProfilePriority(strategy string, left, right *seed.FlagProfile) int {
	leftRank := profileRank(strategy, left)
	rightRank := profileRank(strategy, right)
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

func profileRank(strategy string, profile *seed.FlagProfile) int {
	if profile == nil {
		return 1 << 30
	}

	switch strategy {
	case flagStrategyFortify:
		return fortifyProfileRank(profile)
	default:
		return canaryProfileRank(profile)
	}
}

func canaryProfileRank(profile *seed.FlagProfile) int {
	policy := profile.AxisValues["policy"]
	threshold := profile.AxisValues["threshold"]
	pic := profile.AxisValues["pic_mode"]
	guard := axisValue(profile, "guard_mode", "default")
	layout := axisValue(profile, "layout_mode", "default")

	switch {
	case policy == "strong" && threshold == "8" && pic == "default" && guard == "default" && layout == "default":
		return 0
	case policy == "strong" && threshold == "1" && pic == "default" && guard == "default" && layout == "default":
		return 1
	case policy == "strong" && threshold == "32" && pic == "default" && guard == "default" && layout == "default":
		return 2
	case policy == "all" && threshold == "8" && pic == "default" && guard == "default" && layout == "default":
		return 3
	case policy == "strong" && threshold == "8" && pic == "fPIC" && guard == "default" && layout == "default":
		return 4
	case policy == "strong" && threshold == "8" && pic == "default" && guard == "default" && layout == "pack":
		return 5
	case policy == "strong" && threshold == "8" && pic == "default" && guard == "default" && layout == "pack1":
		return 6
	case policy == "strong" && threshold == "8" && pic == "default" && guard == "default" && layout == "pack2":
		return 7
	case policy == "strong" && threshold == "8" && pic == "default" && guard == "default" && layout == "pack4":
		return 8
	case policy == "strong" && threshold == "8" && pic == "default" && guard == "default" && layout == "short-enums":
		return 9
	case policy == "strong" && threshold == "8" && pic == "default" && guard == "default" && layout == "pack+short-enums":
		return 10
	case policy == "strong" && threshold == "8" && pic == "default" && guard == "global" && layout == "default":
		return 11
	case policy == "strong" && threshold == "8" && pic == "default" && strings.HasPrefix(guard, "sysreg") && layout == "default" && hasGuardOffset(profile, "0"):
		return 12
	case policy == "strong" && threshold == "8" && pic == "default" && strings.HasPrefix(guard, "sysreg") && layout == "default" && hasGuardOffset(profile, "16"):
		return 13
	case policy == "explicit" && threshold == "8" && pic == "default" && guard == "default" && layout == "default":
		return 14
	}

	return 100 + lexicalAxisRank(profile)
}

func fortifyProfileRank(profile *seed.FlagProfile) int {
	optimization := axisValue(profile, "optimization", "default")
	fortifyMode := axisValue(profile, "fortify_mode", "default")
	stackProtector := axisValue(profile, "stack_protector_mode", "default")

	switch {
	case optimization == "O2" && fortifyMode == "hardened" && stackProtector == "no-stack-protector":
		return 0
	case optimization == "O2" && fortifyMode == "fortify2" && stackProtector == "no-stack-protector":
		return 1
	case optimization == "O2" && fortifyMode == "fortify1" && stackProtector == "no-stack-protector":
		return 2
	case optimization == "O2" && fortifyMode == "fortify3" && stackProtector == "no-stack-protector":
		return 3
	case optimization == "O0" && fortifyMode == "hardened" && stackProtector == "no-stack-protector":
		return 4
	case optimization == "O2" && fortifyMode == "hardened-no-fortify" && stackProtector == "no-stack-protector":
		return 5
	case optimization == "O2" && fortifyMode == "hardened-fortify1" && stackProtector == "no-stack-protector":
		return 6
	case optimization == "O2" && fortifyMode == "hardened-fortify2" && stackProtector == "no-stack-protector":
		return 7
	case optimization == "O2" && fortifyMode == "hardened-fortify3" && stackProtector == "no-stack-protector":
		return 8
	}

	optimizationRank := map[string]int{"O0": 0, "O1": 1, "O2": 2, "O3": 3}
	fortifyRank := map[string]int{
		"fortify0":            0,
		"fortify1":            1,
		"fortify2":            2,
		"fortify3":            3,
		"hardened":            4,
		"hardened-no-fortify": 5,
		"hardened-fortify1":   6,
		"hardened-fortify2":   7,
		"hardened-fortify3":   8,
	}
	stackProtectorRank := map[string]int{
		"no-stack-protector":     0,
		"stack-protector":        1,
		"stack-protector-strong": 2,
	}

	return 100 +
		optimizationRank[optimization]*100 +
		fortifyRank[fortifyMode]*10 +
		stackProtectorRank[stackProtector]
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
		"tls":          2,
		"tls-fs-off20": 3,
		"tls-gs-off20": 4,
		"tls-symbol":   5,
		"tls-off0":     6,
		"tls-off16":    7,
		"tls-tp-off0":  8,
		"tls-tp-off16": 9,
		"sysreg-off0":  10,
		"sysreg-off16": 11,
	}
	layoutRank := map[string]int{
		"default":          0,
		"pack":             1,
		"pack1":            2,
		"pack2":            3,
		"pack4":            4,
		"short-enums":      5,
		"pack+short-enums": 6,
	}

	threshold, err := strconv.Atoi(profile.AxisValues["threshold"])
	if err != nil {
		threshold = 999
	}
	policy := axisValue(profile, "policy", "zzz")
	guard := axisValue(profile, "guard_mode", "default")
	layout := axisValue(profile, "layout_mode", "default")

	rank := policyRank[policy] * 1000
	rank += threshold * 10
	if axisValue(profile, "pic_mode", "default") == "fPIC" {
		rank += 1
	}
	rank += layoutRank[layout] * 100
	rank += guardRank[guard] * 10000
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

func buildNegativeProfile(controls [][]string, defaultAxes map[string]string) *seed.FlagProfile {
	flags := []string{"-fno-stack-protector"}
	if len(controls) > 0 && len(controls[0]) > 0 {
		flags = append([]string(nil), controls[0]...)
	}
	axes := map[string]string{
		"policy":     "negative_control",
		"threshold":  "default",
		"pic_mode":   "default",
		"guard_mode": "default",
	}
	if _, ok := defaultAxes["layout_mode"]; ok {
		axes["layout_mode"] = "default"
	}
	return &seed.FlagProfile{
		Name:              "negative-control__fno-stack-protector",
		AxisValues:        axes,
		Flags:             flags,
		IsNegativeControl: true,
	}
}

func isProfileApplicable(strategy string, profile *seed.FlagProfile, source string, allowLLMCFlags bool) bool {
	if profile == nil {
		return false
	}
	switch strategy {
	case flagStrategyCanary:
		if profile.AxisValues["policy"] == "explicit" && !hasStackProtectAttribute(source) && !allowLLMCFlags {
			return false
		}
	}
	return true
}

func hasStackProtectAttribute(source string) bool {
	return strings.Contains(source, "__attribute__((stack_protect))") ||
		strings.Contains(source, "__attribute__ ((stack_protect))")
}

func axisValuesOrDefault(values [][]string) [][]string {
	if len(values) == 0 {
		return [][]string{nil}
	}
	return values
}

func hasAxis(axes map[string][][]string, name string) bool {
	_, ok := axes[name]
	return ok
}

func axisValue(profile *seed.FlagProfile, axis, fallback string) string {
	if profile == nil || len(profile.AxisValues) == 0 {
		return fallback
	}
	if value, ok := profile.AxisValues[axis]; ok && value != "" {
		return value
	}
	return fallback
}

func blockedLLMFlagFamiliesForStrategy(strategy string, profiles []*seed.FlagProfile) []string {
	switch strategy {
	case flagStrategyFortify:
		return append([]string(nil), fortifyLLMBlockedFlagFamilies...)
	default:
		families := append([]string(nil), canaryLLMBlockedFlagFamilies...)
		if profilesReserveLayoutFlags(profiles) {
			families = append(families, canaryLayoutLLMBlockedFlagFamilies...)
		}
		return families
	}
}

func profilesReserveLayoutFlags(profiles []*seed.FlagProfile) bool {
	for _, profile := range profiles {
		if profile == nil || len(profile.AxisValues) == 0 {
			continue
		}
		if _, ok := profile.AxisValues["layout_mode"]; ok {
			return true
		}
	}
	return false
}
