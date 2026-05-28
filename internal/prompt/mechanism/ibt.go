package mechanism

import "path/filepath"

func init() {
	Register(&ibtContract{})
}

type ibtContract struct{}

func (c *ibtContract) OracleType() string { return "ibt" }

func (c *ibtContract) FunctionTemplatePath(isa string) string {
	return filepath.Join("initial_seeds", isa, "ibt", "function_template.c")
}

func (c *ibtContract) PlaceholderFunctionName() string { return "seed" }

func (c *ibtContract) RequiredMarkers() []string {
	return []string{"SEED_RETURNED"}
}

func (c *ibtContract) FuzzTimePromptExample() string {
	return `## CRITICAL OUTPUT REQUIREMENTS

**DO NOT include ANY explanations, analysis, or natural language text in your response.**
**Output ONLY the complete seed() function inside a markdown code block.**
**NO text before or after the code block.**
**NO main() function. NO #include statements.**

Example of CORRECT output:
` + "```c" + `
void seed(void) {
    void (*fp)(void) = some_func;
    fp();
    printf("SEED_RETURNED\n");
    fflush(stdout);
}
` + "```" + `
`
}

func (c *ibtContract) CriticalRulesAddendum() string {
	return `- **Keep IBT / CET protection ENABLED.** Do NOT emit ` + "`-fcf-protection=none`" + `,
  ` + "`-fno-cf-protection`" + `, ` + "`-mbranch-protection=none`" + `, or any flag that disables
  indirect-branch tracking. Seeds with such flags are rejected \u2014 the fuzzer studies
  defenses that are on but emit unintended ENDBR instructions, not disabled protections.`
}
