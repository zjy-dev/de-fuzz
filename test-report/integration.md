# Integration Test Report

## Test Environment
- **Commit ID**: 6d508c61f6d3f1b2a94ee34c16aebaa1f1efacad
- **Commit Short**: 6d508c6
- **Test Time**: 2025-11-19 02:53:00 CST
- **CPU**: Intel(R) Core(TM) Ultra 9 275HX
- **CPU Cores**: 24
- **Memory**: 15Gi

## Integration Test Results

```
# github.com/zjy-dev/de-fuzz/internal/seed_executor [github.com/zjy-dev/de-fuzz/internal/seed_executor.test]
internal/seed_executor/executor_test.go:49:15: undefined: NewQemuExecutor
internal/seed_executor/executor_test.go:76:15: undefined: NewQemuExecutor
testing: warning: no tests to run
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/analysis	0.003s [no tests to run]
=== RUN   TestLoadConfig_Integration
    config_integration_test.go:48: Source parent path loaded: /root/fuzz-coverage
--- PASS: TestLoadConfig_Integration (0.00s)
=== RUN   TestGetCompilerConfigPath_Integration
--- PASS: TestGetCompilerConfigPath_Integration (0.00s)
=== RUN   TestGetCompilerConfigName_Integration
--- PASS: TestGetCompilerConfigName_Integration (0.00s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/config	0.004s
=== RUN   TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:48: gcovr path: /root/.local/bin/gcovr
    gcc_integration_test.go:53: Compiler config path: ../../configs/gcc-v12.2.0-x64-canary.yaml
    gcc_integration_test.go:59: Workspace directory: /tmp/gcc-compiler-cov-1241082874
    gcc_integration_test.go:92: Created seed source file: /tmp/gcc-compiler-cov-1241082874/seeds/canary_test.c
    gcc_integration_test.go:124: Gcovr command template: gcovr --exclude '.*\.(h|hpp|hxx)$' --gcov-executable "gcov-14 --demangled-names"  -r .. --json-pretty
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/clean_gcda_files
    gcc_integration_test.go:140: Testing Clean operation on: /root/fuzz-coverage/gcc-build-selective
    gcc_integration_test.go:145: Clean completed successfully
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/measure_seed1
    gcc_integration_test.go:159: Measuring coverage for seed: seed_001
=== NAME  TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:107: Compile command: /root/fuzz-coverage/gcc-build-selective/gcc/xgcc -B/root/fuzz-coverage/gcc-build-selective/gcc -fstack-protector-all -O0 -o /tmp/gcc-compiler-cov-1241082874/output.bin /tmp/gcc-compiler-cov-1241082874/seeds/canary_test.c
    gcc_integration_test.go:114: Compilation succeeded
=== NAME  TestGCCCoverage_CompilerCoverage_Integration/measure_seed1
    gcc_integration_test.go:175: Seed1 report size: 2382926 bytes
    gcc_integration_test.go:180: Compiled binary: /tmp/gcc-compiler-cov-1241082874/output.bin
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/has_increased_first_seed
    gcc_integration_test.go:189: Checking if first seed increased coverage
    gcc_integration_test.go:193: HasIncreased result: true (expected true)
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/merge_first_seed
    gcc_integration_test.go:202: Merging first seed into total.json
    gcc_integration_test.go:205: Merge completed successfully
    gcc_integration_test.go:210: total.json created at: /tmp/gcc-compiler-cov-1241082874/reports/total.json
    gcc_integration_test.go:216: total.json size: 2382926 bytes
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/get_total_report
    gcc_integration_test.go:223: Getting total report
    gcc_integration_test.go:231: Total report size: 2382926 bytes
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/measure_seed2_different_optimization
    gcc_integration_test.go:242: Cleaned .gcda files before seed2
    gcc_integration_test.go:286: Measuring coverage for seed: seed_002
=== NAME  TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:107: Compile command: /root/fuzz-coverage/gcc-build-selective/gcc/xgcc -B/root/fuzz-coverage/gcc-build-selective/gcc -fstack-protector-all -O0 -o /tmp/gcc-compiler-cov-1241082874/output.bin /tmp/gcc-compiler-cov-1241082874/seeds/canary_test.c
    gcc_integration_test.go:114: Compilation succeeded
=== NAME  TestGCCCoverage_CompilerCoverage_Integration/measure_seed2_different_optimization
    gcc_integration_test.go:296: Seed2 report size: 2383214 bytes
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/has_increased_seed2
    gcc_integration_test.go:305: Checking if seed2 increased coverage compared to total
Coverage Increase Report
=========================

Found 1 function(s) with increased coverage:

1. File: gcc-releases-gcc-12.2.0/gcc/cfgexpand.cc
   Function: expand_one_stack_var_at(tree_node*, rtx_def*, unsigned int, poly_int<1u, long>)
   Old Coverage: 19/23 lines (82.6%)
   New Coverage: 21/23 lines (91.3%)
   Lines Increased: 2
   Newly Covered Line Numbers: [985 998]


    gcc_integration_test.go:308: HasIncreased result for seed2: true
    gcc_integration_test.go:313: Seed2 increased coverage - will merge
    gcc_integration_test.go:318: Seed2 merged successfully
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/measure_identical_seed_no_increase
    gcc_integration_test.go:342: Measuring coverage for identical seed: seed_003_duplicate
=== NAME  TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:107: Compile command: /root/fuzz-coverage/gcc-build-selective/gcc/xgcc -B/root/fuzz-coverage/gcc-build-selective/gcc -fstack-protector-all -O0 -o /tmp/gcc-compiler-cov-1241082874/output.bin /tmp/gcc-compiler-cov-1241082874/seeds/canary_test.c
    gcc_integration_test.go:114: Compilation succeeded
=== NAME  TestGCCCoverage_CompilerCoverage_Integration/measure_identical_seed_no_increase
    gcc_integration_test.go:348: Checking if identical seed increased coverage
No coverage increases found.

    gcc_integration_test.go:351: HasIncreased result for identical seed: false (expected false)
=== NAME  TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:357: === Compiler coverage integration test completed successfully ===
    gcc_integration_test.go:358: All GCCCoverage methods tested with real instrumented compiler
    gcc_integration_test.go:359: Compiler: /root/fuzz-coverage/gcc-build-selective/gcc/xgcc
    gcc_integration_test.go:360: Gcovr exec path: /root/fuzz-coverage/gcc-build-selective
--- PASS: TestGCCCoverage_CompilerCoverage_Integration (2.27s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/clean_gcda_files (0.03s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/measure_seed1 (0.84s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/has_increased_first_seed (0.00s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/merge_first_seed (0.00s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/get_total_report (0.01s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/measure_seed2_different_optimization (0.51s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/has_increased_seed2 (0.33s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/measure_identical_seed_no_increase (0.54s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/coverage	2.274s
testing: warning: no tests to run
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/exec	0.002s [no tests to run]
testing: warning: no tests to run
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/fuzz	0.002s [no tests to run]
=== RUN   TestLLMConfigurationIntegration
=== RUN   TestLLMConfigurationIntegration/should_load_configuration_and_create_LLM_client
--- PASS: TestLLMConfigurationIntegration (0.00s)
    --- PASS: TestLLMConfigurationIntegration/should_load_configuration_and_create_LLM_client (0.00s)
=== RUN   TestDeepSeekRealAPIIntegration
    llm_integration_test.go:91: Skipping real API test: no valid API key configured
--- SKIP: TestDeepSeekRealAPIIntegration (0.00s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/llm	0.004s
testing: warning: no tests to run
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/oracle	0.002s [no tests to run]
testing: warning: no tests to run
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/prompt	0.003s [no tests to run]
testing: warning: no tests to run
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/report	0.002s [no tests to run]
testing: warning: no tests to run
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/seed	0.003s [no tests to run]
FAIL	github.com/zjy-dev/de-fuzz/internal/seed_executor [build failed]
FAIL
```
