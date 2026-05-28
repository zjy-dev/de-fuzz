package mechanism

import "path/filepath"

func init() {
	Register(&canaryContract{})
}

type canaryContract struct{}

func (c *canaryContract) OracleType() string { return "canary" }

func (c *canaryContract) FunctionTemplatePath(isa string) string {
	return filepath.Join("initial_seeds", isa, "canary", "function_template.c")
}

func (c *canaryContract) PlaceholderFunctionName() string { return "seed" }

func (c *canaryContract) RequiredMarkers() []string {
	return []string{"SEED_RETURNED"}
}

func (c *canaryContract) FuzzTimePromptExample() string {
	return `## CRITICAL OUTPUT REQUIREMENTS

**DO NOT include ANY explanations, analysis, or natural language text in your response.**
**Output ONLY the complete seed() function inside a markdown code block.**
**You CAN include function attributes like __attribute__((stack_protect)) if needed.**
**NO text before or after the code block.**
**NO main() function. NO #include statements.**

Example of CORRECT output:
` + "```c" + `
void seed(int buf_size, int fill_size) {
    char buffer[64];
    memset(buffer, 'A', fill_size);
    printf("SEED_RETURNED\n");
    fflush(stdout);
}
` + "```" + `

Example with function attribute (for -fstack-protector-explicit):
` + "```c" + `
__attribute__((stack_protect)) void seed(int buf_size, int fill_size) {
    char buffer[64];
    memset(buffer, 'A', fill_size);
    printf("SEED_RETURNED\n");
    fflush(stdout);
}
` + "```" + `
// ||||| CFLAGS_START |||||
-fstack-protector-explicit
// ||||| CFLAGS_END |||||
`
}

func (c *canaryContract) CriticalRulesAddendum() string {
	return `- **You CAN add function attributes** like __attribute__((stack_protect)) if needed.
- **Keep stack-canary protection ENABLED.** Do NOT emit ` + "`-fno-stack-protector`" + `, ` + "`-fno-stack-protector-all`" + `, ` + "`-fno-stack-protector-strong`" + `,
  ` + "`--param=ssp-buffer-size=0`" + `, or any other flag that disables stack-protector.
  Seeds with such flags are rejected outright — the fuzzer studies silent failures, not disabled protections.`
}
