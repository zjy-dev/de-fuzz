package config

// FlagStrategyConfig models a rule-driven compiler flag scheduler used during fuzzing.
type FlagStrategyConfig struct {
	Enabled                 bool                                   `mapstructure:"enabled"`
	Mode                    string                                 `mapstructure:"mode"`
	AllowLLMCFlags          bool                                   `mapstructure:"allow_llm_cflags"`
	IncludeNegativeControls bool                                   `mapstructure:"include_negative_controls"`
	SelectionOrder          string                                 `mapstructure:"selection_order"`
	NegativeControls        [][]string                             `mapstructure:"negative_controls"`
	Axes                    FlagStrategyAxesConfig                 `mapstructure:"axes"`
	ISAOptions              map[string]FlagStrategyISAOptionConfig `mapstructure:"isa_options"`
}

// FlagStrategyAxesConfig defines common and ISA-specific flag axes.
type FlagStrategyAxesConfig struct {
	Common map[string][][]string            `mapstructure:"common"`
	ByISA  map[string]map[string][][]string `mapstructure:"by_isa"`
}

// FlagStrategyISAOptionConfig carries target-specific inputs needed to materialize profiles.
type FlagStrategyISAOptionConfig struct {
	StackProtectorGuardReg string `mapstructure:"stack_protector_guard_reg"`
	SupportsHardwareTLS    bool   `mapstructure:"supports_hardware_tls"`
}
