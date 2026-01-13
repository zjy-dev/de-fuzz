# DeFuzz Performance Benchmarks

Last updated: 2026-01-13

## Overview

This document contains performance benchmarks for DeFuzz components, including traditional fuzzing pipeline and LLM providers.

## Traditional Fuzzing Pipeline

### Per-Seed Processing Time Breakdown

Tested with GCC 15.2.0 aarch64 cross-compiler, canary strategy.

| Component | Time | Percentage |
|-----------|------|------------|
| **Oracle (binary search)** | **13.4s** | **89%** |
| Compile + Coverage | 1.6s | 11% |
| Record Coverage | 0.015s | <1% |
| **Total** | **~15s/seed** | 100% |

### Oracle Performance Analysis

The Canary Oracle uses binary search to find the minimum buffer size that triggers a crash:
- `max_buffer_size = 1024` → log2(1024) = 10 binary search iterations
- Each iteration runs QEMU aarch64 emulation (~1s per execution)
- Plus verification call = ~13 QEMU executions total

**Optimization Opportunities:**
1. Reduce `max_buffer_size` (e.g., 256 → 8 iterations)
2. Skip Oracle for initial seeds (known safe)
3. Parallel QEMU execution

### Stress Test Results

Run with: `./scripts/stress_test.sh 4`

```
Processed 4 initial seeds in 1m1.58s (avg: 15.39s/seed)
```

## LLM Provider Benchmarks

### Test Configuration

- 8 iterations per provider
- Unique prompts per request (no caching)
- Temperature: 0.1
- Task: Generate C function with buffer operations

### Results Summary

| Provider | Model | Avg Response | Min | Max | Tokens/req |
|----------|-------|--------------|-----|-----|------------|
| **DeepSeek** | deepseek-chat | **3.34s** | 2.21s | 5.34s | ~150 |
| **MiniMax** | MiniMax-M2.1 | **3.78s** | 2.14s | 5.75s | ~287 |

### Detailed Results

#### DeepSeek (deepseek-chat)

- **Iterations:** 8
- **Success Rate:** 100%
- **Total Time:** 26.71s
- **Average Response:** 3.34s
- **Min Response:** 2.21s
- **Max Response:** 5.34s
- **Avg Tokens:** ~150 tokens/request

#### MiniMax (MiniMax-M2.1)

- **Iterations:** 8
- **Success Rate:** 100%
- **Total Time:** 30.22s
- **Average Response:** 3.78s
- **Min Response:** 2.14s
- **Max Response:** 5.75s
- **Avg Tokens:** ~287 tokens/request

### Observations

1. **DeepSeek is ~12% faster** than MiniMax on average
2. **MiniMax generates more tokens** (~2x), which may indicate more verbose output
3. Both providers have similar minimum response times (~2.1-2.2s)
4. Response time variance is higher for MiniMax (2.14-5.75s vs 2.21-5.34s)

## Running Benchmarks

### Traditional Fuzzer Stress Test

```bash
./scripts/stress_test.sh [num_seeds]
```

### LLM Provider Stress Test

```bash
./scripts/llm_stress_test.sh <provider> [iterations]

# Examples:
./scripts/llm_stress_test.sh deepseek 8
./scripts/llm_stress_test.sh minimax 8
```

Results are saved to `docs/llm_stress_test_results.json`.

## Raw Data

See `docs/llm_stress_test_results.json` for detailed JSON results.
