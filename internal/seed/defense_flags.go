package seed

import "strings"

// defenseDisablingFlags maps oracle type to the flag tokens that explicitly disable
// the corresponding defense mechanism. When the LLM emits one of these, the seed is
// rejected (treated as a virtual compile error) so the retry loop can re-prompt with
// a clear rule violation message.
var defenseDisablingFlags = map[string][]string{
	"canary": {
		"-fno-stack-protector",
		"-fno-stack-protector-all",
		"-fno-stack-protector-strong",
		"-fno-stack-protector-explicit",
	},
	"ibt": {
		"-fcf-protection=none",
		"-fno-cf-protection",
		"-mbranch-protection=none",
	},
}

// defenseDisablingPrefixes maps oracle type to flag prefixes that disable the defense.
var defenseDisablingPrefixes = map[string][]string{
	"canary": {
		"--param=ssp-buffer-size=0",
	},
}

// FindDefenseDisablingFlags returns the subset of cflags that explicitly disable
// the defense mechanism identified by oracleType. An empty result means no violation.
func FindDefenseDisablingFlags(oracleType string, cflags []string) []string {
	if len(cflags) == 0 {
		return nil
	}
	exact := defenseDisablingFlags[oracleType]
	prefixes := defenseDisablingPrefixes[oracleType]
	if len(exact) == 0 && len(prefixes) == 0 {
		return nil
	}

	var violations []string
	for _, f := range cflags {
		for _, e := range exact {
			if f == e {
				violations = append(violations, f)
				goto next
			}
		}
		for _, p := range prefixes {
			if strings.HasPrefix(f, p) {
				violations = append(violations, f)
				goto next
			}
		}
	next:
	}
	return violations
}
