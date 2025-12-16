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
type DivergenceInfo struct {
	// Divergence point
	DivergentFunction string // Name of the function where divergence occurred
	DivergentFile     string // Source file path
	DivergentLine     int    // Line number where divergence starts

	// Context
	DivergentFunctionCode string // Source code of the divergent function
	MutatedSeedCode       string // Code of the seed that failed
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
		baseSeedSection = fmt.Sprintf(`## Example Seed (Base)

This seed covers line %d, which is close to your target (lines %v).
Study this seed and modify it to reach the target lines.

%s
%s
%s

`, ctx.BaseSeedLine, ctx.TargetLines, "```c", ctx.BaseSeedCode, "```")
	}

	// Build output format based on configuration
	outputFormat := b.getOutputFormat()

	prompt := fmt.Sprintf(`You are an expert at generating test cases for compiler fuzzing. Your task is to create a C program that will trigger specific code paths in the compiler.

%s
%s
%s
## Your Task

1. Analyze the target basic block and understand what conditions would cause the compiler to take that code path.
2. Study the example seed that reaches nearby code.
3. Modify or create a new C program that will cause the compiler to execute the target lines (%v).

**Key Insights:**
- The target is in function %s at BB%d with %d possible branches.
- Focus on the conditions that lead to the target branch.
- Small, focused changes often work better than major rewrites.

%s

**Important:**
- Output ONLY the C source code (and test cases if required).
- The code must be compilable with GCC.
- Make sure the code triggers the specific compiler behavior you're targeting.
`,
		targetDesc,
		functionCodeSection,
		baseSeedSection,
		ctx.TargetLines,
		ctx.TargetFunction,
		ctx.TargetBBID,
		ctx.SuccessorCount,
		outputFormat,
	)

	return prompt, nil
}

// BuildRefinedPrompt creates a prompt with divergence information for retry.
// This is used when the initial mutation failed to cover the target.
func (b *Builder) BuildRefinedPrompt(ctx *TargetContext, div *DivergenceInfo) (string, error) {
	if ctx == nil || div == nil {
		return "", fmt.Errorf("target context and divergence info must be provided")
	}

	// Build divergence analysis section
	divergenceSection := fmt.Sprintf(`## Divergence Analysis

The previous mutation attempt **FAILED** to reach the target. Here's why:

**Divergence Point:**
- The execution diverged in function: %s
- File: %s
- At approximately line: %d

This means the mutated seed took a different path than expected starting at this function.

`, div.DivergentFunction, filepath.Base(div.DivergentFile), div.DivergentLine)

	// Add divergent function code if available
	if div.DivergentFunctionCode != "" {
		divergenceSection += fmt.Sprintf(`**Divergent Function Code:**
%s
%s
%s

`, "```cpp", div.DivergentFunctionCode, "```")
	}

	// Add the failed mutation
	failedSection := ""
	if div.MutatedSeedCode != "" {
		failedSection = fmt.Sprintf(`## Failed Mutation

The following seed was tried but did NOT reach the target:

%s
%s
%s

**Why it failed:** The code took a different branch at the divergence point shown above.

`, "```c", div.MutatedSeedCode, "```")
	}

	// Build target reminder
	targetReminder := fmt.Sprintf(`## Target Reminder

- **Function:** %s
- **Target Lines:** %v
- **Current obstacle:** The execution diverges at %s before reaching the target.

`, ctx.TargetFunction, ctx.TargetLines, div.DivergentFunction)

	// Build base seed section
	baseSeedSection := ""
	if ctx.BaseSeedCode != "" {
		baseSeedSection = fmt.Sprintf(`## Working Example

This seed successfully reaches line %d (near the target):

%s
%s
%s

`, ctx.BaseSeedLine, "```c", ctx.BaseSeedCode, "```")
	}

	outputFormat := b.getOutputFormat()

	prompt := fmt.Sprintf(`You are an expert at debugging and refining compiler test cases. A previous attempt to reach a specific code path failed. Your task is to analyze why and create a better solution.

%s
%s
%s
%s
## Your Task

1. Understand why the previous mutation diverged at %s.
2. Study the divergence point code to see what conditions caused the wrong branch.
3. Modify your approach to avoid the divergence and reach the target.

**Strategy:**
- Focus on the condition at the divergence point.
- The base seed works - make smaller, more targeted changes.
- Consider what input/code patterns would satisfy the condition to take the correct branch.

%s

**Important:**
- Output ONLY the C source code (and test cases if required).
- Learn from the failed attempt - don't make the same mistake.
- The goal is to reach lines %v in function %s.
`,
		divergenceSection,
		failedSection,
		targetReminder,
		baseSeedSection,
		div.DivergentFunction,
		outputFormat,
		ctx.TargetLines,
		ctx.TargetFunction,
	)

	return prompt, nil
}

// getOutputFormat returns the appropriate output format instruction.
func (b *Builder) getOutputFormat() string {
	if b.FunctionTemplate != "" && b.MaxTestCases > 0 {
		return fmt.Sprintf(`## Output Format

Output ONLY:
1. The function implementation
2. Followed by test cases in JSON format

Example:
[function body code]
// ||||| JSON_TESTCASES_START |||||
[{"running command": "./prog", "expected result": "..."}]

Maximum %d test case(s).`, b.MaxTestCases)
	} else if b.FunctionTemplate != "" {
		return `## Output Format

Output ONLY the function implementation (no test cases needed).`
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
	analyzer *coverage.CFGGuidedAnalyzer,
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
