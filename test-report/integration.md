# Integration Test Report

## Test Environment
- **Commit ID**: d2d7e4345504b337ad07d51fc036474f1ed3a173
- **Commit Short**: d2d7e43
- **Test Time**: 2025-12-16 18:09:09 CST
- **CPU**: Intel(R) Core(TM) Ultra 9 275HX
- **CPU Cores**: 24
- **Memory**: 15Gi

## Integration Test Results

```
=== RUN   TestGCCCompiler_Integration_CompileSimpleProgram
--- PASS: TestGCCCompiler_Integration_CompileSimpleProgram (0.09s)
=== RUN   TestGCCCompiler_Integration_CompileWithWarnings
--- PASS: TestGCCCompiler_Integration_CompileWithWarnings (0.11s)
=== RUN   TestGCCCompiler_Integration_CompileError
--- PASS: TestGCCCompiler_Integration_CompileError (0.01s)
=== RUN   TestGCCCompiler_Integration_MultipleSeeds
--- PASS: TestGCCCompiler_Integration_MultipleSeeds (0.24s)
=== RUN   TestGCCCompiler_Integration_ComplexProgram
--- PASS: TestGCCCompiler_Integration_ComplexProgram (0.11s)
=== RUN   TestCrossGCCCompiler_Integration_Aarch64
--- PASS: TestCrossGCCCompiler_Integration_Aarch64 (0.21s)
=== RUN   TestCrossGCCCompiler_Integration_Riscv64
--- PASS: TestCrossGCCCompiler_Integration_Riscv64 (0.25s)
=== RUN   TestGCCCompiler_Integration_StackProtection
--- PASS: TestGCCCompiler_Integration_StackProtection (0.09s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/compiler	(cached)
=== RUN   TestLoadConfig_Integration
    config_integration_test.go:50: Source parent path loaded: /root/fuzz-coverage
--- PASS: TestLoadConfig_Integration (0.00s)
=== RUN   TestGetCompilerConfigPath_Integration
--- PASS: TestGetCompilerConfigPath_Integration (0.00s)
=== RUN   TestGetCompilerConfigName_Integration
--- PASS: TestGetCompilerConfigName_Integration (0.00s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/config	(cached)
=== RUN   TestFileManager_Integration_FullWorkflow
--- PASS: TestFileManager_Integration_FullWorkflow (0.00s)
=== RUN   TestFileManager_Integration_Persistence
--- PASS: TestFileManager_Integration_Persistence (0.00s)
=== RUN   TestFileManager_Integration_Recovery
--- PASS: TestFileManager_Integration_Recovery (0.00s)
=== RUN   TestFileManager_Integration_ConcurrentAccess
--- PASS: TestFileManager_Integration_ConcurrentAccess (0.00s)
=== RUN   TestFileManager_Integration_LargeCorpus
--- PASS: TestFileManager_Integration_LargeCorpus (0.01s)
=== RUN   TestFileManager_Integration_SeedStates
--- PASS: TestFileManager_Integration_SeedStates (0.00s)
=== RUN   TestFileManager_Integration_CoverageTracking
--- PASS: TestFileManager_Integration_CoverageTracking (0.00s)
=== RUN   TestFileManager_Integration_EmptyCorpus
--- PASS: TestFileManager_Integration_EmptyCorpus (0.00s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/corpus	(cached)
=== RUN   TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:49: gcovr path: /root/.local/bin/gcovr
    gcc_integration_test.go:54: Compiler config path: ../../configs/gcc-v12.2.0-x64-canary.yaml
    gcc_integration_test.go:60: Workspace directory: /tmp/gcc-compiler-cov-1709200874
    gcc_integration_test.go:74: Total report path (test): /tmp/gcc-compiler-cov-1709200874/reports/total.json
    gcc_integration_test.go:75: Total report path (config): 
    gcc_integration_test.go:82: Gcovr command: gcovr --exclude ".*\.(h|hpp|hxx)$" --gcov-executable "gcov-14 --demangled-names" -r ..
    gcc_integration_test.go:110: Created GCC compiler with path: /root/fuzz-coverage/gcc-build-selective/gcc/xgcc, prefix: /root/fuzz-coverage/gcc-build-selective/gcc
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/clean_gcda_files
    gcc_integration_test.go:158: Testing Clean operation on: /root/fuzz-coverage/gcc-build-selective
    gcc_integration_test.go:163: Clean completed successfully
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/measure_seed1
    gcc_integration_test.go:174: Measuring coverage for seed ID: 1
=== NAME  TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:121: Compilation succeeded: /tmp/gcc-compiler-cov-1709200874/seeds/seed_1
=== NAME  TestGCCCoverage_CompilerCoverage_Integration/measure_seed1
    gcc_integration_test.go:190: Seed1 report size: 961254 bytes
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/has_increased_first_seed
    gcc_integration_test.go:199: Checking if first seed increased coverage
    gcc_integration_test.go:203: HasIncreased result: true (expected true)
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/merge_first_seed
    gcc_integration_test.go:212: Merging first seed into total.json
    gcc_integration_test.go:215: Merge completed successfully
    gcc_integration_test.go:220: total.json created at: /tmp/gcc-compiler-cov-1709200874/reports/total.json
    gcc_integration_test.go:226: total.json size: 961254 bytes
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/get_total_report
    gcc_integration_test.go:233: Getting total report
    gcc_integration_test.go:241: Total report size: 961254 bytes
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/measure_seed2_different_pattern
    gcc_integration_test.go:252: Cleaned .gcda files before seed2
    gcc_integration_test.go:289: Measuring coverage for seed ID: 2
=== NAME  TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:121: Compilation succeeded: /tmp/gcc-compiler-cov-1709200874/seeds/seed_2
=== NAME  TestGCCCoverage_CompilerCoverage_Integration/measure_seed2_different_pattern
    gcc_integration_test.go:299: Seed2 report size: 961542 bytes
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/has_increased_seed2
    gcc_integration_test.go:308: Checking if seed2 increased coverage compared to total
    gcc_integration_test.go:311: HasIncreased result for seed2: true
    gcc_integration_test.go:316: Seed2 increased coverage - will merge
    gcc_integration_test.go:321: Seed2 merged successfully
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/measure_identical_seed_no_increase
    gcc_integration_test.go:338: Measuring coverage for identical seed ID: 3
=== NAME  TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:121: Compilation succeeded: /tmp/gcc-compiler-cov-1709200874/seeds/seed_3
=== NAME  TestGCCCoverage_CompilerCoverage_Integration/measure_identical_seed_no_increase
    gcc_integration_test.go:344: Checking if identical seed increased coverage
    gcc_integration_test.go:347: HasIncreased result for identical seed: false (expected false)
=== NAME  TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:353: === Compiler coverage integration test completed successfully ===
    gcc_integration_test.go:354: All GCCCoverage methods tested with real instrumented compiler
    gcc_integration_test.go:355: Compiler: /root/fuzz-coverage/gcc-build-selective/gcc/xgcc
    gcc_integration_test.go:356: Gcovr exec path: /root/fuzz-coverage/gcc-build-selective
    gcc_integration_test.go:357: Gcovr command: gcovr --exclude ".*\.(h|hpp|hxx)$" --gcov-executable "gcov-14 --demangled-names" -r ..
    gcc_integration_test.go:358: Total report path: /tmp/gcc-compiler-cov-1709200874/reports/total.json
--- PASS: TestGCCCoverage_CompilerCoverage_Integration (2.69s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/clean_gcda_files (0.06s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/measure_seed1 (1.38s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/has_increased_first_seed (0.00s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/merge_first_seed (0.00s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/get_total_report (0.00s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/measure_seed2_different_pattern (0.46s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/has_increased_seed2 (0.32s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/measure_identical_seed_no_increase (0.45s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/coverage	(cached)
=== RUN   TestCommandExecutor_Integration_Echo
--- PASS: TestCommandExecutor_Integration_Echo (0.00s)
=== RUN   TestCommandExecutor_Integration_Cat
--- PASS: TestCommandExecutor_Integration_Cat (0.00s)
=== RUN   TestCommandExecutor_Integration_Ls
--- PASS: TestCommandExecutor_Integration_Ls (0.00s)
=== RUN   TestCommandExecutor_Integration_NonZeroExit
--- PASS: TestCommandExecutor_Integration_NonZeroExit (0.00s)
=== RUN   TestCommandExecutor_Integration_Stderr
--- PASS: TestCommandExecutor_Integration_Stderr (0.00s)
=== RUN   TestCommandExecutor_Integration_CommandNotFound
--- PASS: TestCommandExecutor_Integration_CommandNotFound (0.02s)
=== RUN   TestCommandExecutor_Integration_Grep
--- PASS: TestCommandExecutor_Integration_Grep (0.00s)
=== RUN   TestCommandExecutor_Integration_Wc
--- PASS: TestCommandExecutor_Integration_Wc (0.00s)
=== RUN   TestCommandExecutor_Integration_Pwd
--- PASS: TestCommandExecutor_Integration_Pwd (0.00s)
=== RUN   TestCommandExecutor_Integration_Env
--- PASS: TestCommandExecutor_Integration_Env (0.00s)
=== RUN   TestCommandExecutor_Integration_Sh
--- PASS: TestCommandExecutor_Integration_Sh (0.00s)
=== RUN   TestCommandExecutor_Integration_LargeOutput
--- PASS: TestCommandExecutor_Integration_LargeOutput (0.00s)
=== RUN   TestCommandExecutor_Integration_Sleep
--- PASS: TestCommandExecutor_Integration_Sleep (0.10s)
=== RUN   TestCommandExecutor_Integration_Head
--- PASS: TestCommandExecutor_Integration_Head (0.00s)
=== RUN   TestCommandExecutor_Integration_Uname
--- PASS: TestCommandExecutor_Integration_Uname (0.00s)
=== RUN   TestCommandExecutor_Integration_CompileAndRun
--- PASS: TestCommandExecutor_Integration_CompileAndRun (0.08s)
=== RUN   TestCommandExecutor_Integration_Pipeline
--- PASS: TestCommandExecutor_Integration_Pipeline (0.00s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/exec	(cached)
testing: warning: no tests to run
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/fuzz	(cached) [no tests to run]
=== RUN   TestLLMConfigurationIntegration
=== RUN   TestLLMConfigurationIntegration/should_load_configuration_and_create_LLM_client
--- PASS: TestLLMConfigurationIntegration (0.00s)
    --- PASS: TestLLMConfigurationIntegration/should_load_configuration_and_create_LLM_client (0.00s)
=== RUN   TestDeepSeekRealAPIIntegration
    llm_integration_test.go:93: Skipping real API test: no valid API key configured
--- SKIP: TestDeepSeekRealAPIIntegration (0.00s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/llm	(cached)
=== RUN   TestCanaryOracle_Integration_NoCanaryProtection
    canary_oracle_integration_test.go:88: Oracle report: <nil>
--- PASS: TestCanaryOracle_Integration_NoCanaryProtection (0.09s)
=== RUN   TestCanaryOracle_Integration_WithCanaryProtection
    canary_oracle_integration_test.go:170: Oracle report: <nil>
--- PASS: TestCanaryOracle_Integration_WithCanaryProtection (0.11s)
=== RUN   TestCanaryOracle_Integration_BinarySearch
    canary_oracle_integration_test.go:246: Oracle report: <nil>
    canary_oracle_integration_test.go:247: Expected crash around size 32
--- PASS: TestCanaryOracle_Integration_BinarySearch (0.08s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/oracle	(cached)
=== RUN   TestBuilder_Integration_BuildUnderstandPrompt
--- PASS: TestBuilder_Integration_BuildUnderstandPrompt (0.00s)
=== RUN   TestBuilder_Integration_BuildUnderstandPrompt_MissingFiles
--- PASS: TestBuilder_Integration_BuildUnderstandPrompt_MissingFiles (0.00s)
=== RUN   TestBuilder_Integration_BuildUnderstandPrompt_EmptyParams
--- PASS: TestBuilder_Integration_BuildUnderstandPrompt_EmptyParams (0.00s)
=== RUN   TestBuilder_Integration_BuildGeneratePrompt
--- PASS: TestBuilder_Integration_BuildGeneratePrompt (0.00s)
=== RUN   TestBuilder_Integration_BuildGeneratePrompt_NoTestCases
--- PASS: TestBuilder_Integration_BuildGeneratePrompt_NoTestCases (0.00s)
=== RUN   TestBuilder_Integration_BuildGeneratePrompt_NoStackLayout
--- PASS: TestBuilder_Integration_BuildGeneratePrompt_NoStackLayout (0.00s)
=== RUN   TestBuilder_Integration_BuildMutatePrompt

================================================================================
[DEBUG] BuildMutatePrompt - Generated Prompt:
--------------------------------------------------------------------------------

[EXISTING SEED]

#include <stdio.h>
#include <string.h>

int main() {
    char buffer[16];
    strcpy(buffer, "Hello");
    printf("%s\n", buffer);
    return 0;
}

// ||||| JSON_TESTCASES_START |||||
[
  {
    "running command": "./a.out",
    "expected result": "Hello"
  },
  {
    "running command": "./a.out arg1",
    "expected result": "Hello"
  }
]
[/EXISTING SEED]

Based on the system context, mutate the existing seed to create a new variant that is more likely to find a bug or increase coverage.
Please make focused changes that could expose different vulnerability patterns.

**Output Format (MUST follow exactly):**

Your response must contain only mutated C source code:

[mutated C source code here]

**IMPORTANT:** 
- Output ONLY the mutated C source code.
- Do NOT include any markdown code blocks, headers, or other formatting.
- Do NOT include test cases - they are not needed for this configuration.

================================================================================

--- PASS: TestBuilder_Integration_BuildMutatePrompt (0.00s)
=== RUN   TestBuilder_Integration_BuildMutatePrompt_NilSeed
--- PASS: TestBuilder_Integration_BuildMutatePrompt_NilSeed (0.00s)
=== RUN   TestBuilder_Integration_BuildMutatePrompt_EmptyTestCases

================================================================================
[DEBUG] BuildMutatePrompt - Generated Prompt:
--------------------------------------------------------------------------------

[EXISTING SEED]
int main() { return 0; }
// ||||| JSON_TESTCASES_START |||||
[

]
[/EXISTING SEED]

Based on the system context, mutate the existing seed to create a new variant that is more likely to find a bug or increase coverage.
Please make focused changes that could expose different vulnerability patterns.

**Output Format (MUST follow exactly):**

Your response must contain only mutated C source code:

[mutated C source code here]

**IMPORTANT:** 
- Output ONLY the mutated C source code.
- Do NOT include any markdown code blocks, headers, or other formatting.
- Do NOT include test cases - they are not needed for this configuration.

================================================================================

--- PASS: TestBuilder_Integration_BuildMutatePrompt_EmptyTestCases (0.00s)
=== RUN   TestBuilder_Integration_BuildAnalyzePrompt
--- PASS: TestBuilder_Integration_BuildAnalyzePrompt (0.00s)
=== RUN   TestBuilder_Integration_BuildAnalyzePrompt_NilSeed
--- PASS: TestBuilder_Integration_BuildAnalyzePrompt_NilSeed (0.00s)
=== RUN   TestBuilder_Integration_BuildAnalyzePrompt_EmptyFeedback
--- PASS: TestBuilder_Integration_BuildAnalyzePrompt_EmptyFeedback (0.00s)
=== RUN   TestBuilder_Integration_PromptChain

================================================================================
[DEBUG] BuildMutatePrompt - Generated Prompt:
--------------------------------------------------------------------------------

[EXISTING SEED]

#include <stdio.h>
void (*func_ptr)(void);
void target() { printf("Called\n"); }
int main() {
    func_ptr = target;
    func_ptr();
    return 0;
}

// ||||| JSON_TESTCASES_START |||||
[
  {
    "running command": "./a.out",
    "expected result": "Called"
  }
]
[/EXISTING SEED]

Based on the system context, mutate the existing seed to create a new variant that is more likely to find a bug or increase coverage.
Please make focused changes that could expose different vulnerability patterns.

**Output Format (MUST follow exactly):**

Your response must contain only mutated C source code:

[mutated C source code here]

**IMPORTANT:** 
- Output ONLY the mutated C source code.
- Do NOT include any markdown code blocks, headers, or other formatting.
- Do NOT include test cases - they are not needed for this configuration.

================================================================================

--- PASS: TestBuilder_Integration_PromptChain (0.00s)
=== RUN   TestBuilder_Integration_SpecialCharacters

================================================================================
[DEBUG] BuildMutatePrompt - Generated Prompt:
--------------------------------------------------------------------------------

[EXISTING SEED]

#include <stdio.h>
int main() {
    // Backslashes: \n \t \\
    char *s = "Hello \"World\"!";
    printf("%s\n", s);
    return 0;
}

// ||||| JSON_TESTCASES_START |||||
[
  {
    "running command": "echo "test" | ./a.out",
    "expected result": "Hello "World"!"
  }
]
[/EXISTING SEED]

Based on the system context, mutate the existing seed to create a new variant that is more likely to find a bug or increase coverage.
Please make focused changes that could expose different vulnerability patterns.

**Output Format (MUST follow exactly):**

Your response must contain only mutated C source code:

[mutated C source code here]

**IMPORTANT:** 
- Output ONLY the mutated C source code.
- Do NOT include any markdown code blocks, headers, or other formatting.
- Do NOT include test cases - they are not needed for this configuration.

================================================================================

--- PASS: TestBuilder_Integration_SpecialCharacters (0.00s)
=== RUN   TestBuilder_Integration_LargePrompt

================================================================================
[DEBUG] BuildMutatePrompt - Generated Prompt:
--------------------------------------------------------------------------------

[EXISTING SEED]
#include <stdio.h>

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

void function_%d() {
    int x = %d;
    printf("Function %d: x = %%d\n", x);
}

int main() {
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    function_%d();
    return 0;
}

// ||||| JSON_TESTCASES_START |||||
[
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  },
  {
    "running command": "./a.out",
    "expected result": "Function output"
  }
]
[/EXISTING SEED]

Based on the system context, mutate the existing seed to create a new variant that is more likely to find a bug or increase coverage.
Please make focused changes that could expose different vulnerability patterns.

**Output Format (MUST follow exactly):**

Your response must contain only mutated C source code:

[mutated C source code here]

**IMPORTANT:** 
- Output ONLY the mutated C source code.
- Do NOT include any markdown code blocks, headers, or other formatting.
- Do NOT include test cases - they are not needed for this configuration.

================================================================================

--- PASS: TestBuilder_Integration_LargePrompt (0.00s)
=== RUN   TestReadFileOrDefault_Integration
--- PASS: TestReadFileOrDefault_Integration (0.00s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/prompt	0.004s
=== RUN   TestMarkdownReporter_Integration_Save
--- PASS: TestMarkdownReporter_Integration_Save (0.00s)
=== RUN   TestMarkdownReporter_Integration_SaveMultipleBugs
--- PASS: TestMarkdownReporter_Integration_SaveMultipleBugs (0.00s)
=== RUN   TestMarkdownReporter_Integration_CreateDirectory
--- PASS: TestMarkdownReporter_Integration_CreateDirectory (0.00s)
=== RUN   TestMarkdownReporter_Integration_EmptyResults
--- PASS: TestMarkdownReporter_Integration_EmptyResults (0.00s)
=== RUN   TestMarkdownReporter_Integration_LargeReport
--- PASS: TestMarkdownReporter_Integration_LargeReport (0.00s)
=== RUN   TestMarkdownReporter_Integration_SpecialCharacters
--- PASS: TestMarkdownReporter_Integration_SpecialCharacters (0.00s)
=== RUN   TestMarkdownReporter_Integration_ReportFormat
--- PASS: TestMarkdownReporter_Integration_ReportFormat (0.00s)
=== RUN   TestMarkdownReporter_Integration_Concurrency
--- PASS: TestMarkdownReporter_Integration_Concurrency (0.00s)
=== RUN   TestReporterInterface_Integration
--- PASS: TestReporterInterface_Integration (0.00s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/report	(cached)
=== RUN   TestSeed_Integration_SaveAndLoad
--- PASS: TestSeed_Integration_SaveAndLoad (0.00s)
=== RUN   TestSeed_Integration_SaveAndLoadWithMetadata
--- PASS: TestSeed_Integration_SaveAndLoadWithMetadata (0.00s)
=== RUN   TestSeed_Integration_LoadMultipleSeeds
--- PASS: TestSeed_Integration_LoadMultipleSeeds (0.00s)
=== RUN   TestSeed_Integration_Understanding
--- PASS: TestSeed_Integration_Understanding (0.00s)
=== RUN   TestSeed_Integration_ComplexTestCases
--- PASS: TestSeed_Integration_ComplexTestCases (0.00s)
=== RUN   TestSeed_Integration_LineageTracking
--- PASS: TestSeed_Integration_LineageTracking (0.00s)
=== RUN   TestSeed_Integration_SeedStates
--- PASS: TestSeed_Integration_SeedStates (0.00s)
=== RUN   TestSeed_Integration_LargeSeedContent
--- PASS: TestSeed_Integration_LargeSeedContent (0.00s)
=== RUN   TestSeed_Integration_SpecialCharacters
--- PASS: TestSeed_Integration_SpecialCharacters (0.00s)
=== RUN   TestSeed_Integration_NonExistentDirectory
--- PASS: TestSeed_Integration_NonExistentDirectory (0.00s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/seed	(cached)
=== RUN   TestLocalExecutor_Integration_Execute
--- PASS: TestLocalExecutor_Integration_Execute (0.11s)
=== RUN   TestLocalExecutor_Integration_ExitCode
--- PASS: TestLocalExecutor_Integration_ExitCode (0.08s)
=== RUN   TestLocalExecutor_Integration_Stderr
--- PASS: TestLocalExecutor_Integration_Stderr (0.08s)
=== RUN   TestLocalExecutor_Integration_Timeout
--- PASS: TestLocalExecutor_Integration_Timeout (1.09s)
=== RUN   TestLocalExecutor_Integration_StackSmashing
    executor_integration_test.go:243: Stack smashing test exit code: -1
--- PASS: TestLocalExecutor_Integration_StackSmashing (0.09s)
=== RUN   TestQEMUExecutor_Integration_AArch64
--- PASS: TestQEMUExecutor_Integration_AArch64 (0.11s)
=== RUN   TestQEMUExecutor_Integration_RISCV64
--- PASS: TestQEMUExecutor_Integration_RISCV64 (0.14s)
=== RUN   TestParseCommand_Integration
=== RUN   TestParseCommand_Integration/Simple_./a.out
=== RUN   TestParseCommand_Integration/With_arguments
=== RUN   TestParseCommand_Integration/With_$BINARY_placeholder
=== RUN   TestParseCommand_Integration/With_./program_placeholder
=== RUN   TestParseCommand_Integration/Empty_command
=== RUN   TestParseCommand_Integration/Just_binary_path
=== RUN   TestParseCommand_Integration/Multiple_arguments_with_spaces
--- PASS: TestParseCommand_Integration (0.00s)
    --- PASS: TestParseCommand_Integration/Simple_./a.out (0.00s)
    --- PASS: TestParseCommand_Integration/With_arguments (0.00s)
    --- PASS: TestParseCommand_Integration/With_$BINARY_placeholder (0.00s)
    --- PASS: TestParseCommand_Integration/With_./program_placeholder (0.00s)
    --- PASS: TestParseCommand_Integration/Empty_command (0.00s)
    --- PASS: TestParseCommand_Integration/Just_binary_path (0.00s)
    --- PASS: TestParseCommand_Integration/Multiple_arguments_with_spaces (0.00s)
=== RUN   TestExecutorInterface_Integration
--- PASS: TestExecutorInterface_Integration (0.18s)
=== RUN   TestLocalExecutor_Integration_MultipleTestCases
--- PASS: TestLocalExecutor_Integration_MultipleTestCases (0.08s)
=== RUN   TestLocalExecutor_Integration_NoTestCases
--- PASS: TestLocalExecutor_Integration_NoTestCases (0.07s)
=== RUN   TestLocalExecutor_Integration_NoTimeout
--- PASS: TestLocalExecutor_Integration_NoTimeout (0.07s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/seed_executor	(cached)
=== RUN   TestFileManager_Integration_SaveAndLoad
--- PASS: TestFileManager_Integration_SaveAndLoad (0.00s)
=== RUN   TestFileManager_Integration_LoadNonExistent
--- PASS: TestFileManager_Integration_LoadNonExistent (0.00s)
=== RUN   TestFileManager_Integration_NextID
--- PASS: TestFileManager_Integration_NextID (0.00s)
=== RUN   TestFileManager_Integration_Concurrency
--- PASS: TestFileManager_Integration_Concurrency (0.00s)
=== RUN   TestFileManager_Integration_CreateDirectory
--- PASS: TestFileManager_Integration_CreateDirectory (0.00s)
=== RUN   TestFileManager_Integration_JSONFormat
--- PASS: TestFileManager_Integration_JSONFormat (0.00s)
=== RUN   TestFileManager_Integration_Recover
--- PASS: TestFileManager_Integration_Recover (0.00s)
=== RUN   TestFileManager_Integration_CorruptedFile
--- PASS: TestFileManager_Integration_CorruptedFile (0.00s)
=== RUN   TestFileManager_Integration_GetFilePath
--- PASS: TestFileManager_Integration_GetFilePath (0.00s)
=== RUN   TestFileManager_Integration_UpdateCoverage
--- PASS: TestFileManager_Integration_UpdateCoverage (0.00s)
=== RUN   TestFileManager_Integration_ManagerInterface
--- PASS: TestFileManager_Integration_ManagerInterface (0.00s)
=== RUN   TestFileManager_Integration_MultipleLoadSave
--- PASS: TestFileManager_Integration_MultipleLoadSave (0.00s)
=== RUN   TestQueueStats_Integration
--- PASS: TestQueueStats_Integration (0.00s)
=== RUN   TestGlobalState_Integration
--- PASS: TestGlobalState_Integration (0.00s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/state	(cached)
testing: warning: no tests to run
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/vm	(cached) [no tests to run]
```
