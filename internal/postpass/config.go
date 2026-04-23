package postpass

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	appconfig "github.com/zjy-dev/de-fuzz/internal/config"
	"github.com/zjy-dev/de-fuzz/internal/seed"
	"gopkg.in/yaml.v3"
)

// ConfigFile holds strategy-specific option traversal matrices.
type ConfigFile struct {
	Strategies map[string]StrategyMatrix `yaml:"strategies"`
}

// StrategyMatrix describes strategy-owned flags and traversal groups.
type StrategyMatrix struct {
	GroupOrder []string                                         `yaml:"group_order"`
	Workers    int                                              `yaml:"workers"`
	StripRules StripRules                                       `yaml:"strip_rules"`
	Groups     GroupCatalog                                     `yaml:"groups"`
	ISAOptions map[string]appconfig.FlagStrategyISAOptionConfig `yaml:"isa_options"`
}

// StripRules defines which baseline flags are owned by the active strategy.
type StripRules struct {
	Exact    []string `yaml:"exact"`
	Prefixes []string `yaml:"prefixes"`
}

// GroupCatalog contains common and ISA-specific option groups.
type GroupCatalog struct {
	Common map[string][]Variant            `yaml:"common"`
	ByISA  map[string]map[string][]Variant `yaml:"by_isa"`
}

// Variant is a named flag bundle inside an option group.
type Variant struct {
	Name  string   `yaml:"name"`
	Flags []string `yaml:"flags"`
}

// MaterializedCombo is a concrete option combination for a specific ISA.
type MaterializedCombo struct {
	Name          string
	GroupValues   map[string]string
	StrategyFlags []string
}

// LoadConfig loads the post-pass matrix config from disk.
func LoadConfig(path string) (*ConfigFile, error) {
	if err := appconfig.LoadEnvFromDotEnvRecursive("."); err != nil {
		return nil, fmt.Errorf("load .env: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read postpass config %s: %w", path, err)
	}

	expanded := os.Expand(string(data), func(key string) string {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		return "${" + key + "}"
	})

	var cfg ConfigFile
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse postpass config %s: %w", path, err)
	}
	if len(cfg.Strategies) == 0 {
		return nil, fmt.Errorf("postpass config %s does not define any strategies", path)
	}
	return &cfg, nil
}

// Strategy returns the strategy matrix by name.
func (c *ConfigFile) Strategy(name string) (*StrategyMatrix, error) {
	if c == nil {
		return nil, fmt.Errorf("postpass config is nil")
	}
	matrix, ok := c.Strategies[name]
	if !ok {
		return nil, fmt.Errorf("postpass strategy %q not found", name)
	}
	return &matrix, nil
}

// Materialize builds deterministic concrete combinations for the requested ISA.
func (m *StrategyMatrix) Materialize(isa string) ([]MaterializedCombo, error) {
	if m == nil {
		return nil, fmt.Errorf("strategy matrix is nil")
	}

	isaKey := resolveISAKey(isa, m.Groups.ByISA)
	if len(m.Groups.ByISA) > 0 && isaKey == "" {
		return nil, fmt.Errorf("no postpass ISA group configured for %q", isa)
	}
	isaGroups := map[string][]Variant(nil)
	if isaKey != "" {
		isaGroups = m.Groups.ByISA[isaKey]
	}

	groupOrder := append([]string(nil), m.GroupOrder...)
	if len(groupOrder) == 0 {
		groupSet := make(map[string]bool)
		for name := range m.Groups.Common {
			groupSet[name] = true
		}
		for name := range isaGroups {
			groupSet[name] = true
		}
		for name := range groupSet {
			groupOrder = append(groupOrder, name)
		}
		sort.Strings(groupOrder)
	}

	if len(groupOrder) == 0 {
		return []MaterializedCombo{{
			Name:          "default",
			GroupValues:   map[string]string{},
			StrategyFlags: nil,
		}}, nil
	}

	type groupVariants struct {
		name     string
		variants []Variant
	}

	ordered := make([]groupVariants, 0, len(groupOrder))
	for _, groupName := range groupOrder {
		var variants []Variant
		if isaGroups != nil {
			if isaSpecific, ok := isaGroups[groupName]; ok {
				variants = cloneVariants(isaSpecific)
			}
		}
		if len(variants) == 0 {
			if common, ok := m.Groups.Common[groupName]; ok {
				variants = cloneVariants(common)
			}
		}
		if len(variants) == 0 {
			variants = []Variant{{Name: "default"}}
		}
		ordered = append(ordered, groupVariants{name: groupName, variants: variants})
	}

	var combos []MaterializedCombo
	var walk func(int, map[string]string, []string) error
	walk = func(index int, values map[string]string, flags []string) error {
		if index == len(ordered) {
			combo := MaterializedCombo{
				GroupValues:   cloneStringMap(values),
				StrategyFlags: append([]string(nil), flags...),
			}
			combo.Name = buildComboName(groupOrder, combo.GroupValues)
			if combo.Name == "" {
				combo.Name = "default"
			}
			combos = append(combos, combo)
			return nil
		}

		group := ordered[index]
		for _, variant := range group.variants {
			materialized, err := materializeVariantFlags(variant.Flags, m.ISAOptions[isaKey])
			if err != nil {
				return fmt.Errorf("materialize group %q variant %q: %w", group.name, variant.Name, err)
			}
			if len(materialized) == 0 && guardRequiresHardwareTLS(variant.Flags, m.ISAOptions[isaKey]) {
				continue
			}

			nextValues := cloneStringMap(values)
			name := strings.TrimSpace(variant.Name)
			if name == "" {
				name = "default"
			}
			nextValues[group.name] = name

			nextFlags := append(append([]string(nil), flags...), materialized...)
			if err := walk(index+1, nextValues, nextFlags); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(0, map[string]string{}, nil); err != nil {
		return nil, err
	}

	return combos, nil
}

// ReconstructBaseline derives stable baseline flags from a persisted compile record.
func ReconstructBaseline(record *seed.CompilationRecord, rules StripRules) ([]string, []string) {
	if record == nil {
		return nil, nil
	}

	base := make([]string, 0, len(record.ConfigCFlags)+len(record.AppliedLLMCFlags))
	base = append(base, record.ConfigCFlags...)
	base = append(base, record.AppliedLLMCFlags...)

	if len(base) == 0 && len(record.EffectiveFlags) > 0 {
		for _, flag := range record.EffectiveFlags {
			if containsString(record.PrefixFlags, flag) {
				continue
			}
			base = append(base, flag)
		}
	}

	return StripOwnedFlags(base, rules)
}

// StripOwnedFlags removes strategy-owned flags while preserving order.
func StripOwnedFlags(flags []string, rules StripRules) ([]string, []string) {
	kept := make([]string, 0, len(flags))
	removed := make([]string, 0)
	for _, flag := range flags {
		if shouldStrip(flag, rules) {
			removed = append(removed, flag)
			continue
		}
		kept = append(kept, flag)
	}
	return kept, removed
}

// SafeName returns a filesystem-safe name for a materialized combo.
func (c MaterializedCombo) SafeName() string {
	name := c.Name
	if name == "" {
		name = "default"
	}
	replacer := strings.NewReplacer(
		string(filepath.Separator), "-",
		" ", "-",
		":", "-",
		"+", "-",
		"*", "-",
		"=", "-",
	)
	name = replacer.Replace(name)
	name = strings.Trim(name, "-_.")
	if name == "" {
		return "default"
	}
	return name
}

func cloneVariants(in []Variant) []Variant {
	out := make([]Variant, 0, len(in))
	for _, variant := range in {
		out = append(out, Variant{
			Name:  variant.Name,
			Flags: append([]string(nil), variant.Flags...),
		})
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func buildComboName(order []string, values map[string]string) string {
	parts := make([]string, 0, len(order))
	for _, group := range order {
		value, ok := values[group]
		if !ok || value == "" {
			value = "default"
		}
		parts = append(parts, group+"-"+value)
	}
	return strings.Join(parts, "__")
}

func resolveISAKey(isa string, byISA map[string]map[string][]Variant) string {
	if len(byISA) == 0 {
		return ""
	}
	for _, candidate := range isaCandidates(isa) {
		if _, ok := byISA[candidate]; ok {
			return candidate
		}
	}
	return ""
}

func isaCandidates(isa string) []string {
	candidates := []string{isa}
	switch isa {
	case "x64":
		candidates = append(candidates, "x64/i386", "i386", "x86")
	case "i386":
		candidates = append(candidates, "x64/i386", "x64", "x86")
	case "riscv64":
		candidates = append(candidates, "riscv")
	case "rs6000":
		candidates = append(candidates, "ppc64le", "powerpc64le", "powerpc64", "ppc64")
	case "powerpc64le", "ppc64le":
		candidates = append(candidates, "ppc64le", "powerpc64le", "rs6000")
	case "powerpc64", "ppc64":
		candidates = append(candidates, "powerpc64", "ppc64", "rs6000")
	}
	return candidates
}

func materializeVariantFlags(flags []string, options appconfig.FlagStrategyISAOptionConfig) ([]string, error) {
	materialized := make([]string, 0, len(flags))
	for _, raw := range flags {
		flag := raw
		if strings.Contains(flag, "<config-provided-valid-sysreg>") ||
			strings.Contains(flag, "<same-sysreg>") ||
			strings.Contains(flag, "<config-provided-gpr>") ||
			strings.Contains(flag, "<same-gpr>") {
			if options.StackProtectorGuardReg == "" {
				return nil, fmt.Errorf("missing isa option stack_protector_guard_reg for %q", raw)
			}
		}

		flag = strings.ReplaceAll(flag, "<config-provided-valid-sysreg>", options.StackProtectorGuardReg)
		flag = strings.ReplaceAll(flag, "<same-sysreg>", options.StackProtectorGuardReg)
		flag = strings.ReplaceAll(flag, "<config-provided-gpr>", options.StackProtectorGuardReg)
		flag = strings.ReplaceAll(flag, "<same-gpr>", options.StackProtectorGuardReg)

		if strings.Contains(flag, "<config-provided-valid-sysreg>") ||
			strings.Contains(flag, "<same-sysreg>") ||
			strings.Contains(flag, "<config-provided-gpr>") ||
			strings.Contains(flag, "<same-gpr>") {
			return nil, fmt.Errorf("unresolved ISA placeholder in %q", raw)
		}

		materialized = append(materialized, flag)
	}

	if guardRequiresHardwareTLS(materialized, options) {
		return nil, nil
	}

	return materialized, nil
}

func guardRequiresHardwareTLS(flags []string, options appconfig.FlagStrategyISAOptionConfig) bool {
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
	return hasTLS && !hasReg && !hasSymbol && !options.SupportsHardwareTLS
}

func shouldStrip(flag string, rules StripRules) bool {
	for _, exact := range rules.Exact {
		if flag == exact {
			return true
		}
	}
	for _, prefix := range rules.Prefixes {
		if strings.HasPrefix(flag, prefix) {
			return true
		}
	}
	return false
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
