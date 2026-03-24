package seed

// FlagProfile captures a deterministic compiler flag selection used for a seed.
// It is stored separately from Seed.CFlags, which continue to represent LLM-suggested flags.
type FlagProfile struct {
	Name              string            `json:"name"`
	AxisValues        map[string]string `json:"axis_values,omitempty"`
	Flags             []string          `json:"flags,omitempty"`
	IsNegativeControl bool              `json:"is_negative_control,omitempty"`
}

// Clone returns a deep copy of the profile for safe per-seed mutation.
func (p *FlagProfile) Clone() *FlagProfile {
	if p == nil {
		return nil
	}

	clonedAxes := make(map[string]string, len(p.AxisValues))
	for key, value := range p.AxisValues {
		clonedAxes[key] = value
	}

	return &FlagProfile{
		Name:              p.Name,
		AxisValues:        clonedAxes,
		Flags:             append([]string(nil), p.Flags...),
		IsNegativeControl: p.IsNegativeControl,
	}
}
