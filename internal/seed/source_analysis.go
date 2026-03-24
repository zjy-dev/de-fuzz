package seed

import "regexp"

// CanarySourceAnalysis captures source-level properties that affect SSP semantics.
type CanarySourceAnalysis struct {
	SeedDisablesStackProtector bool
	SeedRequestsStackProtect   bool
	UsesAlloca                 bool
	UsesVLA                    bool
}

var (
	reSeedNoStackProtector = regexp.MustCompile(`(?s)__attribute__\s*\(\(\s*no_stack_protector\s*\)\)\s*(?:[A-Za-z_][A-Za-z0-9_]*\s+)*void\s+seed\s*\(`)
	reSeedStackProtect     = regexp.MustCompile(`(?s)__attribute__\s*\(\(\s*stack_protect\s*\)\)\s*(?:[A-Za-z_][A-Za-z0-9_]*\s+)*void\s+seed\s*\(`)
	reSeedVLA              = regexp.MustCompile(`(?m)\[[[:space:]]*buf_size[[:space:]]*\]`)
	reUsesAlloca           = regexp.MustCompile(`\balloca\s*\(`)
)

// AnalyzeCanarySource extracts source-level signals used to suppress SSP false positives.
func AnalyzeCanarySource(content string) CanarySourceAnalysis {
	return CanarySourceAnalysis{
		SeedDisablesStackProtector: reSeedNoStackProtector.MatchString(content),
		SeedRequestsStackProtect:   reSeedStackProtect.MatchString(content),
		UsesAlloca:                 reUsesAlloca.MatchString(content),
		UsesVLA:                    reSeedVLA.MatchString(content),
	}
}
