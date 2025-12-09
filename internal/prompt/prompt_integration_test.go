//go:build integration

package prompt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// TestBuilder_Integration_BuildUnderstandPrompt tests building understand prompts with real files.
func TestBuilder_Integration_BuildUnderstandPrompt(t *testing.T) {
	// Create temp directory with auxiliary files
	tempDir, err := os.MkdirTemp("", "prompt_integration_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create stack_layout.md
	stackLayout := `# Stack Layout

## Overview
The stack grows downward on this architecture.

## Layout
| Offset | Content        |
|--------|----------------|
| +0     | Return Address |
| -8     | Saved RBP      |
| -16    | Canary Value   |
| -24    | Local Buffer   |

## Canary Details
- 8-byte canary value
- Placed between local variables and saved registers
- Checked before function returns
`
	err = os.WriteFile(filepath.Join(tempDir, "stack_layout.md"), []byte(stackLayout), 0644)
	require.NoError(t, err)

	builder := NewBuilder(0, "")

	prompt, err := builder.BuildUnderstandPrompt("x86_64", "stack_canary", tempDir)
	require.NoError(t, err)

	// Verify prompt contains key elements
	assert.Contains(t, prompt, "x86_64")
	assert.Contains(t, prompt, "stack_canary")
	assert.Contains(t, prompt, "Stack Layout")
	assert.Contains(t, prompt, "Canary Value")
	assert.Contains(t, prompt, "Goal")
	assert.Contains(t, prompt, "Attack Vectors")
	assert.Contains(t, prompt, "Seed Generation")
}

// TestBuilder_Integration_BuildUnderstandPrompt_MissingFiles tests with missing auxiliary files.
func TestBuilder_Integration_BuildUnderstandPrompt_MissingFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "prompt_missing_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	builder := NewBuilder(0, "")

	// Should still work, using default message
	prompt, err := builder.BuildUnderstandPrompt("aarch64", "pac", tempDir)
	require.NoError(t, err)

	assert.Contains(t, prompt, "aarch64")
	assert.Contains(t, prompt, "pac")
	assert.Contains(t, prompt, "Not available for now")
}

// TestBuilder_Integration_BuildUnderstandPrompt_EmptyParams tests error handling for empty params.
func TestBuilder_Integration_BuildUnderstandPrompt_EmptyParams(t *testing.T) {
	builder := NewBuilder(0, "")

	_, err := builder.BuildUnderstandPrompt("", "strategy", "/tmp")
	assert.Error(t, err)

	_, err = builder.BuildUnderstandPrompt("isa", "", "/tmp")
	assert.Error(t, err)

	_, err = builder.BuildUnderstandPrompt("", "", "/tmp")
	assert.Error(t, err)
}

// TestBuilder_Integration_BuildGeneratePrompt tests building generate prompts.
func TestBuilder_Integration_BuildGeneratePrompt(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "prompt_gen_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create stack layout file
	stackLayout := `# RISC-V Stack Layout
Stack grows downward
Frame pointer: x8/fp
Stack pointer: x2/sp
`
	err = os.WriteFile(filepath.Join(tempDir, "stack_layout.md"), []byte(stackLayout), 0644)
	require.NoError(t, err)

	builder := NewBuilder(0, "")

	prompt, err := builder.BuildGeneratePrompt(tempDir)
	require.NoError(t, err)

	// Verify prompt structure
	assert.Contains(t, prompt, "Generate a new")
	assert.Contains(t, prompt, "source.c")
	assert.Contains(t, prompt, "Test Cases")
	assert.Contains(t, prompt, "RISC-V Stack Layout")
	assert.Contains(t, prompt, "// ||||| JSON_TESTCASES_START |||||")
	assert.Contains(t, prompt, "running command")
	assert.Contains(t, prompt, "expected result")
}

// TestBuilder_Integration_BuildGeneratePrompt_NoStackLayout tests with no stack layout file.
func TestBuilder_Integration_BuildGeneratePrompt_NoStackLayout(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "prompt_gen_no_stack_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	builder := NewBuilder(0, "")

	prompt, err := builder.BuildGeneratePrompt(tempDir)
	require.NoError(t, err)

	assert.Contains(t, prompt, "Not available for now")
}

// TestBuilder_Integration_BuildMutatePrompt tests building mutation prompts.
func TestBuilder_Integration_BuildMutatePrompt(t *testing.T) {
	builder := NewBuilder(0, "")

	testSeed := &seed.Seed{
		Meta: seed.Metadata{
			ID: 1,
		},
		Content: `
#include <stdio.h>
#include <string.h>

int main() {
    char buffer[16];
    strcpy(buffer, "Hello");
    printf("%s\n", buffer);
    return 0;
}
`,
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "Hello"},
			{RunningCommand: "./a.out arg1", ExpectedResult: "Hello"},
		},
	}

	prompt, err := builder.BuildMutatePrompt(testSeed, nil)
	require.NoError(t, err)

	// Verify prompt contains seed content
	assert.Contains(t, prompt, "EXISTING SEED")
	assert.Contains(t, prompt, "strcpy(buffer")
	assert.Contains(t, prompt, "char buffer[16]")
	assert.Contains(t, prompt, "./a.out")
	assert.Contains(t, prompt, "Hello")
	assert.Contains(t, prompt, "mutate the existing seed")
	assert.Contains(t, prompt, "// ||||| JSON_TESTCASES_START |||||")
}
}

// TestBuilder_Integration_BuildMutatePrompt_NilSeed tests error handling for nil seed.
func TestBuilder_Integration_BuildMutatePrompt_NilSeed(t *testing.T) {
	builder := NewBuilder(0, "")

	_, err := builder.BuildMutatePrompt(nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "seed must be provided")
}

// TestBuilder_Integration_BuildMutatePrompt_EmptyTestCases tests with empty test cases.
func TestBuilder_Integration_BuildMutatePrompt_EmptyTestCases(t *testing.T) {
	builder := NewBuilder(0, "")

	testSeed := &seed.Seed{
		Content:   `int main() { return 0; }`,
		TestCases: []seed.TestCase{},
	}

	prompt, err := builder.BuildMutatePrompt(testSeed, nil)
	require.NoError(t, err)

	assert.Contains(t, prompt, "int main()")
	// When test cases are empty, the JSON array is formatted as "[\n\n]"
	assert.Contains(t, prompt, "// ||||| JSON_TESTCASES_START |||||")
}

// TestBuilder_Integration_BuildAnalyzePrompt tests building analysis prompts.
func TestBuilder_Integration_BuildAnalyzePrompt(t *testing.T) {
	builder := NewBuilder(0, "")

	testSeed := &seed.Seed{
		Content: `
#include <stdio.h>
int main() {
    char buf[8];
    gets(buf);  // Dangerous!
    return 0;
}
`,
		TestCases: []seed.TestCase{
			{RunningCommand: "echo AAAA | ./a.out", ExpectedResult: "Normal"},
			{RunningCommand: "echo AAAAAAAAAAAAAAAA | ./a.out", ExpectedResult: "Crash"},
		},
	}

	feedback := `Test case 1: Passed
Test case 2: *** stack smashing detected ***: terminated
Exit code: 134 (SIGABRT)`

	prompt, err := builder.BuildAnalyzePrompt(testSeed, feedback)
	require.NoError(t, err)

	// Verify prompt structure
	assert.Contains(t, prompt, "SEED")
	assert.Contains(t, prompt, "gets(buf)")
	assert.Contains(t, prompt, "EXECUTION FEEDBACK")
	assert.Contains(t, prompt, "stack smashing detected")
	assert.Contains(t, prompt, "SIGABRT")
	assert.Contains(t, prompt, "What vulnerability")
	assert.Contains(t, prompt, "echo AAAA")
}

// TestBuilder_Integration_BuildAnalyzePrompt_NilSeed tests error handling.
func TestBuilder_Integration_BuildAnalyzePrompt_NilSeed(t *testing.T) {
	builder := NewBuilder(0, "")

	_, err := builder.BuildAnalyzePrompt(nil, "some feedback")
	assert.Error(t, err)
}

// TestBuilder_Integration_BuildAnalyzePrompt_EmptyFeedback tests error handling.
func TestBuilder_Integration_BuildAnalyzePrompt_EmptyFeedback(t *testing.T) {
	builder := NewBuilder(0, "")

	testSeed := &seed.Seed{
		Content: `int main() { return 0; }`,
	}

	_, err := builder.BuildAnalyzePrompt(testSeed, "")
	assert.Error(t, err)
}

// TestBuilder_Integration_PromptChain tests building a full prompt chain.
func TestBuilder_Integration_PromptChain(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "prompt_chain_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create comprehensive stack layout
	stackLayout := `# AArch64 Stack Layout with PAC

## Pointer Authentication
- Uses cryptographic signature in upper bits of pointers
- PAC key stored in special registers
- Instructions: PACIA, PACIB, AUTIA, AUTIB

## Stack Frame
| Offset | Content         |
|--------|-----------------|
| +0     | PAC-signed LR   |
| -8     | Saved FP        |
| -16    | Local Variables |

## Attack Considerations
- PAC key manipulation
- Signing gadgets
- PAC collision attacks
`
	err = os.WriteFile(filepath.Join(tempDir, "stack_layout.md"), []byte(stackLayout), 0644)
	require.NoError(t, err)

	builder := NewBuilder(0, "")

	// Step 1: Build understand prompt
	understandPrompt, err := builder.BuildUnderstandPrompt("aarch64", "pac", tempDir)
	require.NoError(t, err)
	assert.Contains(t, understandPrompt, "PAC-signed LR")
	assert.Contains(t, understandPrompt, "aarch64")

	// Step 2: Build generate prompt
	generatePrompt, err := builder.BuildGeneratePrompt(tempDir)
	require.NoError(t, err)
	assert.Contains(t, generatePrompt, "Generate a new")
	assert.Contains(t, generatePrompt, "PAC")

	// Step 3: Build mutate prompt (simulating generated seed)
	generatedSeed := &seed.Seed{
		Content: `
#include <stdio.h>
void (*func_ptr)(void);
void target() { printf("Called\n"); }
int main() {
    func_ptr = target;
    func_ptr();
    return 0;
}
`,
		TestCases: []seed.TestCase{
			{RunningCommand: "./a.out", ExpectedResult: "Called"},
		},
	}

	mutatePrompt, err := builder.BuildMutatePrompt(generatedSeed, nil)
	require.NoError(t, err)
	assert.Contains(t, mutatePrompt, "func_ptr")

	// Step 4: Build analyze prompt
	feedback := "Execution completed. PAC authentication failed. SIGILL at 0x4001234"
	analyzePrompt, err := builder.BuildAnalyzePrompt(generatedSeed, feedback)
	require.NoError(t, err)
	assert.Contains(t, analyzePrompt, "PAC authentication failed")
	assert.Contains(t, analyzePrompt, "SIGILL")
}

// TestBuilder_Integration_SpecialCharacters tests handling of special characters.
func TestBuilder_Integration_SpecialCharacters(t *testing.T) {
	builder := NewBuilder(0, "")

	testSeed := &seed.Seed{
		Content: `
#include <stdio.h>
int main() {
    // Backslashes: \n \t \\
    char *s = "Hello \"World\"!";
    printf("%s\n", s);
    return 0;
}
`,
		TestCases: []seed.TestCase{
			{RunningCommand: "echo \"test\" | ./a.out", ExpectedResult: "Hello \"World\"!"},
		},
	}

	prompt, err := builder.BuildMutatePrompt(testSeed, nil)
	require.NoError(t, err)
	assert.Contains(t, prompt, "Hello \\\"World\\\"!")
}

// TestBuilder_Integration_LargePrompt tests handling of large prompts.
func TestBuilder_Integration_LargePrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large prompt test in short mode")
	}

	builder := NewBuilder(0, "")

	// Create a large seed
	largeContent := "#include <stdio.h>\n"
	for i := 0; i < 50; i++ {
		largeContent += `
void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}
`
	}
	largeContent += `
int main() {
`
	for i := 0; i < 50; i++ {
		largeContent += `    function_%d();
`
	}
	largeContent += "    return 0;\n}\n"

	testCases := make([]seed.TestCase, 20)
	for i := 0; i < 20; i++ {
		testCases[i] = seed.TestCase{
			RunningCommand: "./a.out",
			ExpectedResult: "Function output",
		}
	}

	testSeed := &seed.Seed{
		Content:   largeContent,
		TestCases: testCases,
	}

	prompt, err := builder.BuildMutatePrompt(testSeed, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, prompt)
	// Large prompts should still be handled
	assert.Greater(t, len(prompt), 1000)
}

// TestReadFileOrDefault_Integration tests the helper function.
func TestReadFileOrDefault_Integration(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "readfile_")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Test with existing file
	testContent := "Test content"
	testPath := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testPath, []byte(testContent), 0644)
	require.NoError(t, err)

	content, err := readFileOrDefault(testPath)
	require.NoError(t, err)
	assert.Equal(t, testContent, content)

	// Test with non-existent file
	content, err = readFileOrDefault(filepath.Join(tempDir, "nonexistent.txt"))
	require.NoError(t, err)
	assert.Equal(t, "Not available for now", content)
}
