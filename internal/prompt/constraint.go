// Package prompt provides CFG-guided prompt generation for constraint solving.
package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zjy-dev/de-fuzz/internal/coverage"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// TargetContext holds context for CFG-guided mutation.
type TargetContext struct {
	// Target basic block information
	TargetFunction string // Name of the function containing the target BB
	TargetBBID     int    // Basic block ID
	TargetLines    []int  // Lines in the target basic block
	SuccessorCount int    // Number of successors (branching factor)

	// Base seed information
	BaseSeedID   int64  // ID of the seed to use as example
	BaseSeedCode string // Source code of the base seed
	BaseSeedLine int    // Closest covered line to the target

	// Context code
	FunctionCode   string // Full function code with line annotations
	UncoveredLines []int  // Uncovered lines in the function
	CoveredLines   []int  // Covered lines in the function

	// File information
	SourceFile string // Path to the source file
}

// DivergenceInfo holds divergence analysis results.
// Note: This project only does function-level divergence analysis using uftrace,
// so we don't track divergent line numbers.
type DivergenceInfo struct {
	// Divergence point (function-level only)
	DivergentFunction     string // Name of the function where divergence occurred
	DivergentFunctionCode string // Source code of the divergent function (REQUIRED for effective mutation)

	// Context
	MutatedSeedCode string // Code of the seed that failed
	BaseSeedCode    string // Code of the covered predecessor seed (for comparison)
}

// CompileErrorInfo holds information about a compilation failure.
// Used to provide feedback to LLM when generated code fails to compile.
type CompileErrorInfo struct {
	FailedSeedCode string // Code that failed to compile
	CompilerOutput string // Compiler error messages (stdout + stderr)
	ExitCode       int    // Compiler exit code
	RetryAttempt   int    // Current retry attempt number (1-based)
	MaxRetries     int    // Maximum retry attempts
}

// BuildConstraintSolvingPrompt creates a prompt to guide LLM to cover a specific basic block.
// It uses the base seed as an example and provides context about the target.
func (b *Builder) BuildConstraintSolvingPrompt(ctx *TargetContext) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("target context must be provided")
	}

	// Build the target description
	targetDesc := fmt.Sprintf(`## Target Basic Block

**Function:** %s
**Basic Block ID:** BB%d
**Branching Factor:** %d successors
**Target Lines:** %v
**Source File:** %s

`, ctx.TargetFunction, ctx.TargetBBID, ctx.SuccessorCount, ctx.TargetLines, filepath.Base(ctx.SourceFile))

	// Build the annotated function code section
	functionCodeSection := ""
	if ctx.FunctionCode != "" {
		functionCodeSection = fmt.Sprintf(`## Function Context

The following is the function code with coverage annotations:
- Lines prefixed with [✓] are already covered
- Lines prefixed with [✗] are NOT covered  
- Lines prefixed with [→] are the TARGET lines you need to reach

%s
%s
%s

`, "```cpp", ctx.FunctionCode, "```")
	}

	// Build the base seed section
	baseSeedSection := ""
	if ctx.BaseSeedCode != "" {
		baseSeedSection = fmt.Sprintf(`## Base Seed (MUST MODIFY)

This is your starting point. This seed covers line %d, which is close to your target (lines %v).
**You MUST modify this seed to reach the target lines. Do NOT write a completely new program.**
**Keep the same program structure and main() function. Only modify what's necessary to reach the target.**

%s
%s
%s

`, ctx.BaseSeedLine, ctx.TargetLines, "```c", ctx.BaseSeedCode, "```")
	}

	// Build output format based on configuration
	outputFormat := b.getOutputFormat()

	// Build CRITICAL RULES section based on mode
	criticalRules := ""
	if b.FunctionTemplate != "" {
		criticalRules = `**CRITICAL RULES (Function Template Mode):**
- **You are generating ONLY the seed() function body.**
- **DO NOT generate main() function.** The template already provides main().
- **DO NOT generate #include statements.** The template already has them.
- **DO NOT generate a complete program.** Only the seed() function.
- Focus on modifying the seed() function to trigger different compiler paths.`
	} else {
		criticalRules = `**CRITICAL RULES:**
- **DO NOT create a new program from scratch.** You must modify the provided base seed.
- **DO NOT add a new main() function.** The base seed already has one.
- **Keep the same overall structure.** Only change what's necessary to reach the target.
- Focus on modifying variables, conditions, or adding small code snippets.`
	}

	// Add language constraints to critical rules
	languageConstraints := `

**LANGUAGE CONSTRAINTS (VERY IMPORTANT):**
- Use ONLY C99/C11 standard C code.
- DO NOT use C++ features (references, auto, lambda, classes, templates, new/delete, etc.).
- Use standard C types and functions: int, char, void, malloc, free, memset, memcpy, etc.
- Example of WRONG code: int& ref = x; or auto func = [](int x) { return x; };
- Example of CORRECT code: int* ref = &x; or void* func(int x) { return (void*)(intptr_t)x; }`
	criticalRules += languageConstraints

	// Build output example based on mode
	outputExample := ""
	if b.FunctionTemplate != "" {
		outputExample = fmt.Sprintf(`## CRITICAL OUTPUT REQUIREMENTS

**DO NOT include ANY explanations, analysis, or natural language text in your response.**
**Output ONLY the seed() function inside a markdown code block.**
**NO text before or after the code block.**
**NO main() function. NO #include statements.**

Example of CORRECT output:
%s
void seed(int fill_size) {
    char buffer[64];
    // Your modifications here
    memset(buffer, 'A', fill_size);
}
%s

Example of WRONG output (DO NOT DO THIS):
%s
#include <stdio.h>  // WRONG - no includes
void seed(...) { }
int main() { }  // WRONG - no main function
%s
`, "```c", "```", "```c", "```")
	} else {
		outputExample = fmt.Sprintf(`## CRITICAL OUTPUT REQUIREMENTS

**DO NOT include ANY explanations, analysis, or natural language text in your response.**
**Output ONLY the code inside a markdown code block.**
**NO text before or after the code block.**

Example of CORRECT output format:
%s
// your modified C code here
int main() { ... }
%s

Example of WRONG output (DO NOT DO THIS):
Here is my analysis... [WRONG - no explanations allowed]
The code works by... [WRONG - no descriptions allowed]
`, "```c", "```")
	}

	prompt := fmt.Sprintf(`You are an expert at generating test cases for compiler fuzzing. Your task is to MODIFY an existing C program to trigger specific code paths in the compiler.

%s
%s
%s
## Your Task

1. Analyze the target basic block and understand what conditions would cause the compiler to take that code path.
2. Study the base seed that reaches nearby code.
3. **MODIFY the base seed** to cause the compiler to execute the target lines (%v).

%s

**Key Insights:**
- The target is in function %s at BB%d with %d possible branches.
- Focus on the conditions that lead to the target branch.
- Small, focused changes often work better than major rewrites.

%s

%s
`,
		targetDesc,
		functionCodeSection,
		baseSeedSection,
		ctx.TargetLines,
		criticalRules,
		ctx.TargetFunction,
		ctx.TargetBBID,
		ctx.SuccessorCount,
		outputFormat,
		outputExample,
	)

	return prompt, nil
}

// BuildRefinedPrompt creates a prompt with divergence information for retry.
// This is used when the initial mutation failed to cover the target.
func (b *Builder) BuildRefinedPrompt(ctx *TargetContext, div *DivergenceInfo) (string, error) {
	if ctx == nil || div == nil {
		return "", fmt.Errorf("target context and divergence info must be provided")
	}

	// Section 1: Target Function with highlighted target lines
	targetFunctionSection := ""
	if ctx.FunctionCode != "" {
		targetFunctionSection = fmt.Sprintf(`## 1. Target: Function %s (BB%d)

The compiler function you need to trigger. Lines marked with [→] are your TARGET.

%s
%s
%s

**Target Lines:** %v (marked with [→] above)
**Branching Factor:** %d possible paths from this basic block

`, ctx.TargetFunction, ctx.TargetBBID, "```cpp", ctx.FunctionCode, "```", ctx.TargetLines, ctx.SuccessorCount)
	} else {
		targetFunctionSection = fmt.Sprintf(`## 1. Target

**Function:** %s
**Basic Block:** BB%d
**Target Lines:** %v
**Branching Factor:** %d possible paths

`, ctx.TargetFunction, ctx.TargetBBID, ctx.TargetLines, ctx.SuccessorCount)
	}

	// Section 2: Divergence Analysis with function source code
	divergenceSection := ""
	if div.DivergentFunction != "" {
		divergenceSection = fmt.Sprintf(`## 2. Why Previous Attempt Failed

The compiler took a different code path at function: **%s**

`, div.DivergentFunction)

		if div.DivergentFunctionCode != "" {
			divergenceSection += fmt.Sprintf(`**Divergent Function Source Code** (study this to understand the branching condition):

%s
%s
%s

`, "```cpp", div.DivergentFunctionCode, "```")
		}

		divergenceSection += `**Analysis:** Your seed caused the compiler to branch differently than expected in this function.
Study the conditions in the divergent function to understand what code patterns trigger each branch.

`
	}

	// Section 3: Failed mutation (what didn't work)
	failedSection := ""
	if div.MutatedSeedCode != "" {
		failedSection = fmt.Sprintf(`## 3. Failed Mutation (DO NOT repeat this)

This seed was tried but took the WRONG compiler path:

%s
%s
%s

`, "```c", div.MutatedSeedCode, "```")
	}

	// Section 4: Base seed (what works as starting point)
	baseSeedSection := ""
	if div.BaseSeedCode != "" {
		baseSeedSection = fmt.Sprintf(`## 4. Working Base Seed (USE THIS AS STARTING POINT)

This seed successfully reaches nearby code (line %d). Start from this and make targeted modifications:

%s
%s
%s

`, ctx.BaseSeedLine, "```c", div.BaseSeedCode, "```")
	} else if ctx.BaseSeedCode != "" {
		baseSeedSection = fmt.Sprintf(`## 4. Working Base Seed (USE THIS AS STARTING POINT)

This seed successfully reaches nearby code (line %d). Start from this and make targeted modifications:

%s
%s
%s

`, ctx.BaseSeedLine, "```c", ctx.BaseSeedCode, "```")
	}

	// Section 5: Task and strategy
	taskSection := fmt.Sprintf(`## 5. Your Task

Create a NEW seed that:
1. Uses the **base seed** as starting point (Section 4)
2. Avoids the divergence at **%s** (Section 2)
3. Reaches the **target lines %v** in function **%s** (Section 1)

**Strategy:**
- Study the divergent function's conditions to understand what triggers each branch
- Make small, targeted changes to the base seed
- Consider: What C code patterns cause the compiler to take the target branch?

`, div.DivergentFunction, ctx.TargetLines, ctx.TargetFunction)

	// Output format and rules
	outputFormat := b.getOutputFormat()

	// Build CRITICAL RULES section based on mode
	criticalRules := ""
	if b.FunctionTemplate != "" {
		criticalRules = `**RULES:**
- Output ONLY the seed() function body
- NO main() function (template provides it)
- NO #include statements
- Use only C99/C11 standard C code (no C++ features)`
	} else {
		criticalRules = `**RULES:**
- Modify the base seed, do NOT create a new program
- Keep the same main() structure
- Use only C99/C11 standard C code (no C++ features)`
	}

	prompt := fmt.Sprintf(`%s%s%s%s%s
%s

%s

**OUTPUT: Only the code in a markdown code block. No explanations.**
`,
		targetFunctionSection,
		divergenceSection,
		failedSection,
		baseSeedSection,
		taskSection,
		criticalRules,
		outputFormat,
	)

	return prompt, nil
}

// BuildCompileErrorRetryPrompt creates a prompt for retrying after compile error.
// This preserves the original mutation intent while providing compile error feedback.
func (b *Builder) BuildCompileErrorRetryPrompt(
	ctx *TargetContext,
	errInfo *CompileErrorInfo,
) (string, error) {
	if ctx == nil || errInfo == nil {
		return "", fmt.Errorf("target context and error info must be provided")
	}

	// Section 1: Original Target (preserved from ctx)
	targetSection := ""
	if ctx.FunctionCode != "" {
		targetSection = fmt.Sprintf(`## 1. Target: Function %s (BB%d)

The compiler function you need to trigger. Lines marked with [→] are your TARGET.

%s
%s
%s

**Target Lines:** %v
**Branching Factor:** %d possible paths

`, ctx.TargetFunction, ctx.TargetBBID, "```cpp", ctx.FunctionCode, "```", ctx.TargetLines, ctx.SuccessorCount)
	} else {
		targetSection = fmt.Sprintf(`## 1. Target

**Function:** %s
**Basic Block:** BB%d
**Target Lines:** %v

`, ctx.TargetFunction, ctx.TargetBBID, ctx.TargetLines)
	}

	// Section 2: Compile Error Details
	compileErrorSection := fmt.Sprintf(`## 2. Compilation Failed (MUST FIX)

Your previous attempt failed to compile. **You MUST fix the compilation error.**

**Attempt:** %d of %d
**Exit Code:** %d

**Compiler Error Output:**
%s
%s
%s

**Failed Code (DO NOT repeat these errors):**
%s
%s
%s

`, errInfo.RetryAttempt, errInfo.MaxRetries, errInfo.ExitCode,
		"```", errInfo.CompilerOutput, "```",
		"```c", errInfo.FailedSeedCode, "```")

	// Section 3: Working Base Seed
	baseSeedSection := ""
	if ctx.BaseSeedCode != "" {
		baseSeedSection = fmt.Sprintf(`## 3. Working Base Seed (START FROM THIS)

This seed compiles and runs successfully. Use it as your starting point:

%s
%s
%s

`, "```c", ctx.BaseSeedCode, "```")
	}

	// Section 4: Task
	taskSection := fmt.Sprintf(`## 4. Your Task

1. **FIX the compilation error** shown in Section 2
2. **Still target lines %v** in function %s
3. Use the working base seed (Section 3) as reference for correct syntax

**Common fixes:**
- Check for undefined variables or functions
- Ensure proper C99/C11 syntax (no C++ features)
- Verify all includes are available in the template
- Check for missing semicolons or braces

`, ctx.TargetLines, ctx.TargetFunction)

	// Critical rules and output format
	criticalRules := ""
	if b.FunctionTemplate != "" {
		criticalRules = `**RULES:**
- Output ONLY the seed() function body
- NO main() function (template provides it)
- NO #include statements
- Use only C99/C11 standard C code (no C++ features like __thread in function scope)`
	} else {
		criticalRules = `**RULES:**
- Output complete, compilable C code
- Use only C99/C11 standard C code
- Keep the same main() structure as base seed`
	}

	outputFormat := b.getOutputFormat()

	prompt := fmt.Sprintf(`%s%s%s%s
%s

%s

**OUTPUT: Only the fixed code in a markdown code block. No explanations.**
`,
		targetSection,
		compileErrorSection,
		baseSeedSection,
		taskSection,
		criticalRules,
		outputFormat,
	)

	return prompt, nil
}

// getOutputFormat returns the appropriate output format instruction.
func (b *Builder) getOutputFormat() string {
	if b.FunctionTemplate != "" && b.MaxTestCases > 0 {
		return fmt.Sprintf(`## Output Format

**CRITICAL: Function Template Mode**
- You are in FUNCTION TEMPLATE mode.
- The main() function is ALREADY PROVIDED in the template.
- **DO NOT generate main() function.**
- **DO NOT generate a complete program.**
- **ONLY generate the seed() function body.**

Output ONLY:
1. The seed() function implementation
2. Followed by test cases in JSON format

Example of CORRECT output:
%s
void seed(int fill_size) {
    char buffer[64];
    memset(buffer, 'A', fill_size);
}
%s
// ||||| JSON_TESTCASES_START |||||
[{"running command": "./prog 100", "expected result": "..."}]

Maximum %d test case(s).`, "```c", "```", b.MaxTestCases)
	} else if b.FunctionTemplate != "" {
		return `## Output Format

**CRITICAL: Function Template Mode**
- You are in FUNCTION TEMPLATE mode.
- The main() function is ALREADY PROVIDED in the template.
- **DO NOT generate main() function.**
- **DO NOT generate a complete program.**
- **ONLY generate the seed() function body.**

Example of CORRECT output:
` + "```c" + `
void seed(int fill_size) {
    char buffer[64];
    memset(buffer, 'A', fill_size);
}
` + "```" + `

Example of WRONG output (DO NOT DO THIS):
` + "```c" + `
#include <stdio.h>
void seed(int fill_size) { ... }
int main() { ... }  // WRONG! Do not include main()
` + "```"
	} else if b.MaxTestCases > 0 {
		return fmt.Sprintf(`## Output Format

Output ONLY:
1. Complete C source code
2. Followed by test cases in JSON format

Example:
[C source code]
// ||||| JSON_TESTCASES_START |||||
[{"running command": "./prog", "expected result": "..."}]

Maximum %d test case(s).`, b.MaxTestCases)
	}
	return `## Output Format

Output ONLY complete C source code. No test cases needed.`
}

// GenerateAnnotatedFunctionCode generates function code with coverage annotations.
// coveredLines and targetLines are the line numbers to annotate.
func GenerateAnnotatedFunctionCode(sourceFile string, startLine, endLine int, coveredLines, targetLines []int) (string, error) {
	content, err := os.ReadFile(sourceFile)
	if err != nil {
		return "", fmt.Errorf("failed to read source file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	if startLine < 1 || endLine > len(lines) {
		return "", fmt.Errorf("line range out of bounds")
	}

	// Build line sets for quick lookup
	coveredSet := make(map[int]bool)
	for _, l := range coveredLines {
		coveredSet[l] = true
	}
	targetSet := make(map[int]bool)
	for _, l := range targetLines {
		targetSet[l] = true
	}

	var sb strings.Builder
	for i := startLine - 1; i < endLine && i < len(lines); i++ {
		lineNum := i + 1
		prefix := "   " // Default: no annotation

		if targetSet[lineNum] {
			prefix = "[→]" // Target line
		} else if coveredSet[lineNum] {
			prefix = "[✓]" // Covered
		} else {
			prefix = "[✗]" // Uncovered
		}

		sb.WriteString(fmt.Sprintf("%s %4d: %s\n", prefix, lineNum, lines[i]))
	}

	return sb.String(), nil
}

// BuildTargetContextFromCFG creates a TargetContext from CFG analysis results.
func BuildTargetContextFromCFG(
	target *coverage.TargetInfo,
	baseSeed *seed.Seed,
	analyzer *coverage.Analyzer,
) (*TargetContext, error) {
	if target == nil {
		return nil, fmt.Errorf("target info is required")
	}

	ctx := &TargetContext{
		TargetFunction: target.Function,
		TargetBBID:     target.BBID,
		TargetLines:    target.Lines,
		SuccessorCount: target.SuccessorCount,
		SourceFile:     target.File,
		BaseSeedLine:   target.BaseSeedLine,
	}

	// Add base seed code if available
	if baseSeed != nil {
		ctx.BaseSeedID = int64(baseSeed.Meta.ID)
		ctx.BaseSeedCode = baseSeed.Content
	}

	// Try to generate annotated function code
	if target.File != "" && len(target.Lines) > 0 {
		// Get covered lines from analyzer
		coveredMap := analyzer.GetCoveredLines()
		var coveredInFile []int
		for lid := range coveredMap {
			if lid.File == target.File {
				coveredInFile = append(coveredInFile, lid.Line)
			}
		}
		ctx.CoveredLines = coveredInFile

		// For now, just use target lines as context
		// In a full implementation, we'd get the function boundaries
		minLine := target.Lines[0] - 20
		if minLine < 1 {
			minLine = 1
		}
		maxLine := target.Lines[len(target.Lines)-1] + 20

		code, err := GenerateAnnotatedFunctionCode(
			target.File,
			minLine,
			maxLine,
			coveredInFile,
			target.Lines,
		)
		if err == nil {
			ctx.FunctionCode = code
		}
	}

	return ctx, nil
}
