// Package mechanism formalizes the relationship between a defense mechanism
// (e.g. "canary") and the prompt/seed-merge/output-validation pipeline.
package mechanism

// Contract is the interface every defense mechanism must satisfy.
// It provides the prompt builder and CLI startup path with:
//   - the C function template location;
//   - the function name the LLM must implement;
//   - post-merge marker validation strings;
//   - mechanism-specific prompt content.
type Contract interface {
	// OracleType returns the oracle type name, e.g. "canary".
	// Must match the key used in oracle.Register and cfg.Compiler.Oracle.Type.
	OracleType() string

	// FunctionTemplatePath returns the filesystem path to the C function
	// template for the given ISA (e.g. "riscv64"), e.g.
	// "initial_seeds/riscv64/canary/function_template.c".
	FunctionTemplatePath(isa string) string

	// PlaceholderFunctionName returns the name of the function that the LLM
	// must implement, e.g. "seed".
	PlaceholderFunctionName() string

	// RequiredMarkers returns strings that must be present in the merged
	// code after template substitution, e.g. ["SEED_RETURNED"].
	// An empty slice means no marker validation is required.
	RequiredMarkers() []string

	// FuzzTimePromptExample returns the complete output-example section to
	// inject into constraint-solving prompts when in function-template mode.
	FuzzTimePromptExample() string

	// CriticalRulesAddendum returns mechanism-specific rules to append to
	// the critical-rules block in constraint-solving prompts.
	// May be empty.
	CriticalRulesAddendum() string
}
