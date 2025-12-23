# Integration Test Report

## Test Environment
- **Commit ID**: 609300f59b101ccfa6a5bcaf43bf58350529b859
- **Commit Short**: 609300f
- **Test Time**: 2025-12-17 01:56:01 CST
- **CPU**: Intel(R) Core(TM) Ultra 9 275HX
- **CPU Cores**: 24
- **Memory**: 15Gi

## Integration Test Results

```
=== RUN   TestGCCCompiler_Integration_CompileSimpleProgram
--- PASS: TestGCCCompiler_Integration_CompileSimpleProgram (0.22s)
=== RUN   TestGCCCompiler_Integration_CompileWithWarnings
--- PASS: TestGCCCompiler_Integration_CompileWithWarnings (0.11s)
=== RUN   TestGCCCompiler_Integration_CompileError
--- PASS: TestGCCCompiler_Integration_CompileError (0.01s)
=== RUN   TestGCCCompiler_Integration_MultipleSeeds
--- PASS: TestGCCCompiler_Integration_MultipleSeeds (0.32s)
=== RUN   TestGCCCompiler_Integration_ComplexProgram
--- PASS: TestGCCCompiler_Integration_ComplexProgram (0.11s)
=== RUN   TestCrossGCCCompiler_Integration_Aarch64
--- PASS: TestCrossGCCCompiler_Integration_Aarch64 (0.22s)
=== RUN   TestCrossGCCCompiler_Integration_Riscv64
--- PASS: TestCrossGCCCompiler_Integration_Riscv64 (0.25s)
=== RUN   TestGCCCompiler_Integration_StackProtection
--- PASS: TestGCCCompiler_Integration_StackProtection (0.09s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/compiler	(cached)
=== RUN   TestLoadConfig_Integration
    config_integration_test.go:50: Source parent path loaded: /root/project/de-fuzz/gcc-v12.2.0-x64
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
=== RUN   TestCFGGuidedAnalyzer_Integration_FullWorkflow
=== RUN   TestCFGGuidedAnalyzer_Integration_FullWorkflow/initial_no_coverage
    cfg_guided_integration_test.go:51: First target: stack_protect_classify_type:BB2 (succs=3, lines=[1819 1820 1822 1854 1824 1847])
=== RUN   TestCFGGuidedAnalyzer_Integration_FullWorkflow/record_initial_coverage
    cfg_guided_integration_test.go:77: Coverage for stack_protect_classify_type: 1/18 BBs
=== RUN   TestCFGGuidedAnalyzer_Integration_FullWorkflow/progressive_coverage
    cfg_guided_integration_test.go:117: Seed 2: recorded lines
    cfg_guided_integration_test.go:117: Seed 3: recorded lines
    cfg_guided_integration_test.go:117: Seed 4: recorded lines
    cfg_guided_integration_test.go:123: Updated coverage: 5/18 BBs (27.8%)
=== RUN   TestCFGGuidedAnalyzer_Integration_FullWorkflow/target_selection_prioritization
    cfg_guided_integration_test.go:136: Next target: stack_protect_classify_type:BB2 (succs=3)
=== RUN   TestCFGGuidedAnalyzer_Integration_FullWorkflow/save_and_load_mapping
=== RUN   TestCFGGuidedAnalyzer_Integration_FullWorkflow/generate_target_context
    cfg_guided_integration_test.go:189: Base seed: 1 (line 1819, distance 0)
--- PASS: TestCFGGuidedAnalyzer_Integration_FullWorkflow (0.07s)
    --- PASS: TestCFGGuidedAnalyzer_Integration_FullWorkflow/initial_no_coverage (0.00s)
    --- PASS: TestCFGGuidedAnalyzer_Integration_FullWorkflow/record_initial_coverage (0.00s)
    --- PASS: TestCFGGuidedAnalyzer_Integration_FullWorkflow/progressive_coverage (0.00s)
    --- PASS: TestCFGGuidedAnalyzer_Integration_FullWorkflow/target_selection_prioritization (0.00s)
    --- PASS: TestCFGGuidedAnalyzer_Integration_FullWorkflow/save_and_load_mapping (0.03s)
    --- PASS: TestCFGGuidedAnalyzer_Integration_FullWorkflow/generate_target_context (0.00s)
=== RUN   TestCFGGuidedAnalyzer_Integration_MultipleFunctions
    cfg_guided_integration_test.go:221: Analyzing 4 functions:
    cfg_guided_integration_test.go:225:   stack_protect_classify_type: 0/18 BBs
    cfg_guided_integration_test.go:225:   stack_protect_decl_phase: 0/18 BBs
    cfg_guided_integration_test.go:225:   stack_protect_decl_phase_1: 0/1 BBs
    cfg_guided_integration_test.go:225:   stack_protect_decl_phase_2: 0/1 BBs
    cfg_guided_integration_test.go:228: Total BBs across all functions: 38
    cfg_guided_integration_test.go:238: Iteration 1: target stack_protect_classify_type:BB2 (succs=3)
    cfg_guided_integration_test.go:238: Iteration 2: target stack_protect_decl_phase:BB2 (succs=2)
    cfg_guided_integration_test.go:238: Iteration 3: target stack_protect_classify_type:BB3 (succs=2)
    cfg_guided_integration_test.go:238: Iteration 4: target stack_protect_decl_phase:BB4 (succs=2)
    cfg_guided_integration_test.go:238: Iteration 5: target stack_protect_classify_type:BB6 (succs=2)
    cfg_guided_integration_test.go:238: Iteration 6: target stack_protect_classify_type:BB10 (succs=2)
    cfg_guided_integration_test.go:238: Iteration 7: target stack_protect_decl_phase:BB11 (succs=2)
    cfg_guided_integration_test.go:238: Iteration 8: target stack_protect_decl_phase:BB14 (succs=2)
    cfg_guided_integration_test.go:238: Iteration 9: target stack_protect_classify_type:BB15 (succs=2)
    cfg_guided_integration_test.go:238: Iteration 10: target stack_protect_decl_phase:BB17 (succs=2)
    cfg_guided_integration_test.go:238: Iteration 11: target stack_protect_classify_type:BB18 (succs=2)
    cfg_guided_integration_test.go:238: Iteration 12: target stack_protect_decl_phase_2:BB2 (succs=1)
    cfg_guided_integration_test.go:238: Iteration 13: target stack_protect_decl_phase_1:BB2 (succs=1)
    cfg_guided_integration_test.go:238: Iteration 14: target stack_protect_decl_phase:BB3 (succs=1)
    cfg_guided_integration_test.go:238: Iteration 15: target stack_protect_classify_type:BB8 (succs=1)
    cfg_guided_integration_test.go:238: Iteration 16: target stack_protect_classify_type:BB9 (succs=1)
    cfg_guided_integration_test.go:238: Iteration 17: target stack_protect_classify_type:BB11 (succs=1)
    cfg_guided_integration_test.go:238: Iteration 18: target stack_protect_classify_type:BB12 (succs=1)
    cfg_guided_integration_test.go:238: Iteration 19: target stack_protect_classify_type:BB13 (succs=1)
    cfg_guided_integration_test.go:238: Iteration 20: target stack_protect_decl_phase:BB13 (succs=1)
    cfg_guided_integration_test.go:253: 
        Final coverage:
    cfg_guided_integration_test.go:260:   stack_protect_classify_type: 16/18 BBs (88.9%)
    cfg_guided_integration_test.go:260:   stack_protect_decl_phase: 14/18 BBs (77.8%)
    cfg_guided_integration_test.go:260:   stack_protect_decl_phase_1: 1/1 BBs (100.0%)
    cfg_guided_integration_test.go:260:   stack_protect_decl_phase_2: 1/1 BBs (100.0%)
    cfg_guided_integration_test.go:263: Overall: 32/38 BBs covered (84.2%)
--- PASS: TestCFGGuidedAnalyzer_Integration_MultipleFunctions (0.03s)
=== RUN   TestCFGGuidedAnalyzer_Integration_GetCoveredLines
--- PASS: TestCFGGuidedAnalyzer_Integration_GetCoveredLines (0.03s)
=== RUN   TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:49: gcovr path: /root/.local/bin/gcovr
    gcc_integration_test.go:54: Compiler config path: ../../configs/gcc-v12.2.0-x64-canary.yaml
    gcc_integration_test.go:60: Workspace directory: /tmp/gcc-compiler-cov-2604438966
    gcc_integration_test.go:74: Total report path (test): /tmp/gcc-compiler-cov-2604438966/reports/total.json
    gcc_integration_test.go:75: Total report path (config): 
    gcc_integration_test.go:82: Gcovr command: gcovr --exclude ".*\.(h|hpp|hxx)$" --gcov-executable "gcov-14 --demangled-names" -r ..
    gcc_integration_test.go:110: Created GCC compiler with path: /root/project/de-fuzz/gcc-v12.2.0-x64/gcc-build/gcc/xgcc, prefix: /root/project/de-fuzz/gcc-v12.2.0-x64/gcc-build/gcc
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/clean_gcda_files
    gcc_integration_test.go:157: Testing Clean operation on: /root/project/de-fuzz/gcc-v12.2.0-x64/gcc-build
    gcc_integration_test.go:162: Clean completed successfully
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/measure_seed1
    gcc_integration_test.go:173: Measuring coverage for seed ID: 1
=== NAME  TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:121: Compilation succeeded: /tmp/gcc-compiler-cov-2604438966/seeds/seed_1
=== NAME  TestGCCCoverage_CompilerCoverage_Integration/measure_seed1
    gcc_integration_test.go:189: Seed1 report size: 2382926 bytes
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/has_increased_first_seed
    gcc_integration_test.go:198: Checking if first seed increased coverage
    gcc_integration_test.go:202: HasIncreased result: true (expected true)
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/merge_first_seed
    gcc_integration_test.go:211: Merging first seed into total.json
    gcc_integration_test.go:214: Merge completed successfully
    gcc_integration_test.go:219: total.json created at: /tmp/gcc-compiler-cov-2604438966/reports/total.json
    gcc_integration_test.go:225: total.json size: 2382926 bytes
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/get_total_report
    gcc_integration_test.go:232: Getting total report
    gcc_integration_test.go:240: Total report size: 2382926 bytes
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/measure_seed2_different_pattern
    gcc_integration_test.go:251: Cleaned .gcda files before seed2
    gcc_integration_test.go:288: Measuring coverage for seed ID: 2
=== NAME  TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:121: Compilation succeeded: /tmp/gcc-compiler-cov-2604438966/seeds/seed_2
=== NAME  TestGCCCoverage_CompilerCoverage_Integration/measure_seed2_different_pattern
    gcc_integration_test.go:298: Seed2 report size: 2383214 bytes
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/has_increased_seed2
    gcc_integration_test.go:307: Checking if seed2 increased coverage compared to total
    gcc_integration_test.go:310: HasIncreased result for seed2: true
    gcc_integration_test.go:315: Seed2 increased coverage - will merge
    gcc_integration_test.go:320: Seed2 merged successfully
=== RUN   TestGCCCoverage_CompilerCoverage_Integration/measure_identical_seed_no_increase
    gcc_integration_test.go:337: Measuring coverage for identical seed ID: 3
=== NAME  TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:121: Compilation succeeded: /tmp/gcc-compiler-cov-2604438966/seeds/seed_3
=== NAME  TestGCCCoverage_CompilerCoverage_Integration/measure_identical_seed_no_increase
    gcc_integration_test.go:343: Checking if identical seed increased coverage
    gcc_integration_test.go:346: HasIncreased result for identical seed: false (expected false)
=== NAME  TestGCCCoverage_CompilerCoverage_Integration
    gcc_integration_test.go:352: === Compiler coverage integration test completed successfully ===
    gcc_integration_test.go:353: All GCCCoverage methods tested with real instrumented compiler
    gcc_integration_test.go:354: Compiler: /root/project/de-fuzz/gcc-v12.2.0-x64/gcc-build/gcc/xgcc
    gcc_integration_test.go:355: Gcovr exec path: /root/project/de-fuzz/gcc-v12.2.0-x64/gcc-build
    gcc_integration_test.go:356: Gcovr command: gcovr --exclude ".*\.(h|hpp|hxx)$" --gcov-executable "gcov-14 --demangled-names" -r ..
    gcc_integration_test.go:357: Total report path: /tmp/gcc-compiler-cov-2604438966/reports/total.json
--- PASS: TestGCCCoverage_CompilerCoverage_Integration (2.14s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/clean_gcda_files (0.02s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/measure_seed1 (0.68s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/has_increased_first_seed (0.00s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/merge_first_seed (0.00s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/get_total_report (0.01s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/measure_seed2_different_pattern (0.49s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/has_increased_seed2 (0.35s)
    --- PASS: TestGCCCoverage_CompilerCoverage_Integration/measure_identical_seed_no_increase (0.58s)
=== RUN   TestCoverageMapping_Integration_PersistenceAndRecovery
=== RUN   TestCoverageMapping_Integration_PersistenceAndRecovery/create_and_record
=== RUN   TestCoverageMapping_Integration_PersistenceAndRecovery/load_and_verify
=== RUN   TestCoverageMapping_Integration_PersistenceAndRecovery/concurrent_recording
--- PASS: TestCoverageMapping_Integration_PersistenceAndRecovery (0.00s)
    --- PASS: TestCoverageMapping_Integration_PersistenceAndRecovery/create_and_record (0.00s)
    --- PASS: TestCoverageMapping_Integration_PersistenceAndRecovery/load_and_verify (0.00s)
    --- PASS: TestCoverageMapping_Integration_PersistenceAndRecovery/concurrent_recording (0.00s)
=== RUN   TestCoverageMapping_Integration_BatchRecording
--- PASS: TestCoverageMapping_Integration_BatchRecording (0.01s)
=== RUN   TestCoverageMapping_Integration_FindClosestCoveredLine
=== RUN   TestCoverageMapping_Integration_FindClosestCoveredLine/find_closest_above
=== RUN   TestCoverageMapping_Integration_FindClosestCoveredLine/find_closest_same_file_only
=== RUN   TestCoverageMapping_Integration_FindClosestCoveredLine/no_lines_in_file
=== RUN   TestCoverageMapping_Integration_FindClosestCoveredLine/no_lines_before_target
--- PASS: TestCoverageMapping_Integration_FindClosestCoveredLine (0.00s)
    --- PASS: TestCoverageMapping_Integration_FindClosestCoveredLine/find_closest_above (0.00s)
    --- PASS: TestCoverageMapping_Integration_FindClosestCoveredLine/find_closest_same_file_only (0.00s)
    --- PASS: TestCoverageMapping_Integration_FindClosestCoveredLine/no_lines_in_file (0.00s)
    --- PASS: TestCoverageMapping_Integration_FindClosestCoveredLine/no_lines_before_target (0.00s)
=== RUN   TestCoverageMapping_Integration_LargeScale
    mapping_integration_test.go:266: Recording 100000 lines across 1000 seeds...
    mapping_integration_test.go:286: Coverage stats: 100000 covered lines, 100000 files
    mapping_integration_test.go:295: Mapping file size: 2568026 bytes (2.45 MB)
--- PASS: TestCoverageMapping_Integration_LargeScale (0.15s)
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
--- PASS: TestCommandExecutor_Integration_CommandNotFound (0.03s)
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
--- PASS: TestCommandExecutor_Integration_CompileAndRun (0.10s)
=== RUN   TestCommandExecutor_Integration_Pipeline
--- PASS: TestCommandExecutor_Integration_Pipeline (0.01s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/exec	(cached)
=== RUN   TestCFGGuidedEngine_Integration_BasicFlow
=== RUN   TestCFGGuidedEngine_Integration_BasicFlow/process_initial_seeds
=== RUN   TestCFGGuidedEngine_Integration_BasicFlow/engine_statistics
--- PASS: TestCFGGuidedEngine_Integration_BasicFlow (0.04s)
    --- PASS: TestCFGGuidedEngine_Integration_BasicFlow/process_initial_seeds (0.00s)
    --- PASS: TestCFGGuidedEngine_Integration_BasicFlow/engine_statistics (0.00s)
=== RUN   TestCFGGuidedEngine_Integration_TargetSelection
=== RUN   TestCFGGuidedEngine_Integration_TargetSelection/select_initial_target
    cfg_guided_engine_integration_test.go:208: Initial target: stack_protect_classify_type:BB2 (succs=3, lines=[1819 1820 1822 1854 1824 1847])
=== RUN   TestCFGGuidedEngine_Integration_TargetSelection/progressive_target_selection
    cfg_guided_engine_integration_test.go:231: Iteration 1: stack_protect_classify_type:BB2 (succs=3)
    cfg_guided_engine_integration_test.go:231: Iteration 2: stack_protect_decl_phase:BB2 (succs=2)
    cfg_guided_engine_integration_test.go:231: Iteration 3: stack_protect_classify_type:BB3 (succs=2)
    cfg_guided_engine_integration_test.go:231: Iteration 4: stack_protect_decl_phase:BB4 (succs=2)
    cfg_guided_engine_integration_test.go:231: Iteration 5: stack_protect_classify_type:BB6 (succs=2)
    cfg_guided_engine_integration_test.go:231: Iteration 6: stack_protect_classify_type:BB10 (succs=2)
    cfg_guided_engine_integration_test.go:231: Iteration 7: stack_protect_decl_phase:BB11 (succs=2)
    cfg_guided_engine_integration_test.go:231: Iteration 8: stack_protect_decl_phase:BB14 (succs=2)
    cfg_guided_engine_integration_test.go:231: Iteration 9: stack_protect_classify_type:BB15 (succs=2)
    cfg_guided_engine_integration_test.go:231: Iteration 10: stack_protect_decl_phase:BB17 (succs=2)
    cfg_guided_engine_integration_test.go:243: Selected 10 unique targets
--- PASS: TestCFGGuidedEngine_Integration_TargetSelection (0.03s)
    --- PASS: TestCFGGuidedEngine_Integration_TargetSelection/select_initial_target (0.00s)
    --- PASS: TestCFGGuidedEngine_Integration_TargetSelection/progressive_target_selection (0.00s)
=== RUN   TestCFGGuidedEngine_Integration_MappingPersistence
=== RUN   TestCFGGuidedEngine_Integration_MappingPersistence/record_coverage
    cfg_guided_engine_integration_test.go:286: Coverage before save: {Covered:5 Total:18}
=== RUN   TestCFGGuidedEngine_Integration_MappingPersistence/load_and_verify
    cfg_guided_engine_integration_test.go:303: Coverage after load: 5/18 BBs
    cfg_guided_engine_integration_test.go:308: Loaded 10 covered lines
--- PASS: TestCFGGuidedEngine_Integration_MappingPersistence (0.07s)
    --- PASS: TestCFGGuidedEngine_Integration_MappingPersistence/record_coverage (0.03s)
    --- PASS: TestCFGGuidedEngine_Integration_MappingPersistence/load_and_verify (0.03s)
=== RUN   TestCFGGuidedEngine_Integration_CoverageProgression
    cfg_guided_engine_integration_test.go:370: Iteration 0: target stack_protect_classify_type:BB2
    cfg_guided_engine_integration_test.go:370: Iteration 10: target stack_protect_classify_type:BB18
    cfg_guided_engine_integration_test.go:370: Iteration 20: target stack_protect_classify_type:BB14
    cfg_guided_engine_integration_test.go:357: All BBs covered after 27 iterations!
    cfg_guided_engine_integration_test.go:384: 
        Coverage progression over 4 checkpoints:
    cfg_guided_engine_integration_test.go:386:   Start: 0 BBs
    cfg_guided_engine_integration_test.go:387:   End:   32 BBs
    cfg_guided_engine_integration_test.go:388:   Gain:  32 BBs
--- PASS: TestCFGGuidedEngine_Integration_CoverageProgression (0.03s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/fuzz	(cached)
=== RUN   TestLLMConfigurationIntegration
=== RUN   TestLLMConfigurationIntegration/should_load_configuration_and_create_LLM_client
--- PASS: TestLLMConfigurationIntegration (0.00s)
    --- PASS: TestLLMConfigurationIntegration/should_load_configuration_and_create_LLM_client (0.00s)
=== RUN   TestDeepSeekRealAPIIntegration
    llm_integration_test.go:93: Skipping real API test: no valid API key configured
--- SKIP: TestDeepSeekRealAPIIntegration (0.00s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/llm	(cached)
?   	github.com/zjy-dev/de-fuzz/internal/logger	[no test files]
=== RUN   TestCanaryOracle_Integration_NoCanaryProtection
    canary_oracle_integration_test.go:88: Oracle report: <nil>
--- PASS: TestCanaryOracle_Integration_NoCanaryProtection (0.13s)
=== RUN   TestCanaryOracle_Integration_WithCanaryProtection
    canary_oracle_integration_test.go:170: Oracle report: <nil>
--- PASS: TestCanaryOracle_Integration_WithCanaryProtection (0.10s)
=== RUN   TestCanaryOracle_Integration_BinarySearch
    canary_oracle_integration_test.go:246: Oracle report: <nil>
    canary_oracle_integration_test.go:247: Expected crash around size 32
--- PASS: TestCanaryOracle_Integration_BinarySearch (0.11s)
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
--- PASS: TestBuilder_Integration_BuildMutatePrompt (0.00s)
=== RUN   TestBuilder_Integration_BuildMutatePrompt_NilSeed
--- PASS: TestBuilder_Integration_BuildMutatePrompt_NilSeed (0.00s)
=== RUN   TestBuilder_Integration_BuildMutatePrompt_EmptyTestCases
--- PASS: TestBuilder_Integration_BuildMutatePrompt_EmptyTestCases (0.00s)
=== RUN   TestBuilder_Integration_BuildAnalyzePrompt
--- PASS: TestBuilder_Integration_BuildAnalyzePrompt (0.00s)
=== RUN   TestBuilder_Integration_BuildAnalyzePrompt_NilSeed
--- PASS: TestBuilder_Integration_BuildAnalyzePrompt_NilSeed (0.00s)
=== RUN   TestBuilder_Integration_BuildAnalyzePrompt_EmptyFeedback
--- PASS: TestBuilder_Integration_BuildAnalyzePrompt_EmptyFeedback (0.00s)
=== RUN   TestBuilder_Integration_PromptChain
--- PASS: TestBuilder_Integration_PromptChain (0.00s)
=== RUN   TestBuilder_Integration_SpecialCharacters
--- PASS: TestBuilder_Integration_SpecialCharacters (0.00s)
=== RUN   TestBuilder_Integration_LargePrompt
--- PASS: TestBuilder_Integration_LargePrompt (0.00s)
=== RUN   TestReadFileOrDefault_Integration
--- PASS: TestReadFileOrDefault_Integration (0.00s)
PASS
ok  	github.com/zjy-dev/de-fuzz/internal/prompt	(cached)
=== RUN   TestMarkdownReporter_Integration_Save
--- PASS: TestMarkdownReporter_Integration_Save (0.00s)
=== RUN   TestMarkdownReporter_Integration_SaveMultipleBugs
--- PASS: TestMarkdownReporter_Integration_SaveMultipleBugs (0.00s)
=== RUN   TestMarkdownReporter_Integration_CreateDirectory
--- PASS: TestMarkdownReporter_Integration_CreateDirectory (0.00s)
=== RUN   TestMarkdownReporter_Integration_EmptyResults
--- PASS: TestMarkdownReporter_Integration_EmptyResults (0.00s)
=== RUN   TestMarkdownReporter_Integration_LargeReport
--- PASS: TestMarkdownReporter_Integration_LargeReport (0.01s)
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
--- PASS: TestLocalExecutor_Integration_Execute (0.10s)
=== RUN   TestLocalExecutor_Integration_ExitCode
--- PASS: TestLocalExecutor_Integration_ExitCode (0.11s)
=== RUN   TestLocalExecutor_Integration_Stderr
--- PASS: TestLocalExecutor_Integration_Stderr (0.10s)
=== RUN   TestLocalExecutor_Integration_Timeout
--- PASS: TestLocalExecutor_Integration_Timeout (1.12s)
=== RUN   TestLocalExecutor_Integration_StackSmashing
    executor_integration_test.go:243: Stack smashing test exit code: -1
--- PASS: TestLocalExecutor_Integration_StackSmashing (0.30s)
=== RUN   TestQEMUExecutor_Integration_AArch64
--- PASS: TestQEMUExecutor_Integration_AArch64 (0.12s)
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
--- PASS: TestExecutorInterface_Integration (0.07s)
=== RUN   TestLocalExecutor_Integration_MultipleTestCases
--- PASS: TestLocalExecutor_Integration_MultipleTestCases (0.08s)
=== RUN   TestLocalExecutor_Integration_NoTestCases
--- PASS: TestLocalExecutor_Integration_NoTestCases (0.10s)
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
