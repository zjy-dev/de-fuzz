package mechanism

import "path/filepath"

func init() {
	Register(&fortifyContract{})
}

// fortifyContract wires `_FORTIFY_SOURCE` / Object Size Checking into the
// prompt + seed-merge + flag-validation pipeline.
//
// The contract is positive-control only: every checker registered under
// the `fortify` oracle treats the mechanism as REQUIRED to be on, and the
// flag-level filter (see `internal/seed/defense_flags.go`) rejects seeds
// that emit any flag known to disable / silently weaken FORTIFY (e.g.
// `-D_FORTIFY_SOURCE=0`, `-U_FORTIFY_SOURCE`, `-O0`).
type fortifyContract struct{}

func (c *fortifyContract) OracleType() string { return "fortify" }

func (c *fortifyContract) FunctionTemplatePath(isa string) string {
	return filepath.Join("initial_seeds", isa, "fortify", "function_template.c")
}

func (c *fortifyContract) PlaceholderFunctionName() string { return "seed" }

func (c *fortifyContract) RequiredMarkers() []string {
	return []string{"SEED_RETURNED"}
}

func (c *fortifyContract) FuzzTimePromptExample() string {
	return `## CRITICAL OUTPUT REQUIREMENTS

**DO NOT include ANY explanations, analysis, or natural language text in your response.**
**Output ONLY the complete seed() function inside a markdown code block.**
**NO text before or after the code block.**
**NO main() function. NO #include statements.**

The signature is: void seed(const char *mode, int n)
- mode: dispatched by the template; affects which fortify path is exercised
- n  : caller-supplied size; pass through to the libc call

Example of CORRECT output:
` + "```c" + `
void seed(const char *mode, int n) {
    char buf[16];
    /* Force the compiler to lose object-size context: route through a
       opaque pointer so __builtin_object_size cannot fold to a constant. */
    char *dst = buf;
    memcpy(dst, "AAAAAAAAAAAAAAAAAAAAAAAA", (size_t)n);
    printf("SEED_RETURNED\n");
    fflush(stdout);
}
` + "```" + `
`
}

func (c *fortifyContract) CriticalRulesAddendum() string {
	return `- **Keep _FORTIFY_SOURCE ENABLED at level >= 2.** Do NOT emit ` +
		"`-D_FORTIFY_SOURCE=0`" + `, ` + "`-U_FORTIFY_SOURCE`" + `,
  ` + "`-D_FORTIFY_SOURCE=1`" + `, or ` + "`-O0`" + ` (FORTIFY requires at least -O1 to take effect).
  Seeds with such flags are rejected outright — the fuzzer studies silent
  bypasses of FORTIFY, not disabled / weakened protections.`
}
