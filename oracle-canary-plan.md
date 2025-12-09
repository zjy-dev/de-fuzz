# Canary Oracle Implementation Plan

This plan outlines the implementation of the `CanaryOracle`, a specialized oracle for detecting stack canary bypasses using a binary search approach.

## 1. Interface Updates

To support active oracles that need to execute the binary with varying inputs, we need to update the `Oracle` interface.

### `internal/oracle/oracle.go`

Update `Analyze` to accept the binary path and the executor.

```go
type Oracle interface {
    // Analyze analyzes the seed.
    // binaryPath: path to the compiled binary
    // executor: interface to run the binary
    // results: initial execution results (optional usage)
    Analyze(s *seed.Seed, binaryPath string, exec executor.Executor, results []Result) (*Bug, error)
}
```

**Note**: This is a breaking change. We need to update:

- `LLMOracle`
- `CrashOracle`
- `Engine` (caller)

## 2. Canary Oracle Implementation

Create `internal/oracle/plugins/canary.go`.

### Struct

```go
type CanaryOracle struct {
    MaxBufferSize int // Default 4096
}
```

### Logic

Implement the binary search algorithm described in `oracle-canary.md`.

1.  **Input Generation**: Create a helper to generate 'A' strings of length `n`.
2.  **Execution**:
    - Clone the `Seed`.
    - Set `Seed.TestCases` to a single case with the generated input.
    - Call `executor.Execute(seed, binaryPath)`.
3.  **Binary Search**:
    - Range `[0, MaxBufferSize]`.
    - Find `min_crash_size`.
4.  **Verdict**:
    - If `min_crash_size` found:
      - Check exit code.
      - `139` (SIGSEGV) -> **BUG**.
      - `134` (SIGABRT) -> **SAFE**.
    - Else -> **SAFE** (or inconclusive).

## 3. Integration

### `internal/oracle/plugins/canary.go`

- Register "canary" in `init()`.
- Parse `max_buffer_size` from options.

### `internal/fuzz/engine.go`

- Update the `Analyze` call to pass `compileResult.BinaryPath` and `e.cfg.Executor`.

## 4. Testing Plan

### Unit Tests (`internal/oracle/plugins/canary_test.go`)

- **Mock Executor**: Create a mock executor that simulates crashes based on input length.
  - Scenario 1: Safe (Crash at 100 with SIGABRT).
  - Scenario 2: Bug (Crash at 100 with SIGSEGV).
  - Scenario 3: No Crash (up to N).
  - Scenario 4: Bug (Crash at 50 with SIGSEGV, then 100 with SIGABRT - simulating `ret -> canary`).
- **Test Logic**: Verify `CanaryOracle` correctly identifies bugs and safe cases using the mock.

### Integration Tests

- Since we don't have a real vulnerable binary easily available in unit tests, we rely on the Mock Executor for logic verification.
- Real integration would require compiling a C program with a known buffer overflow and running it. We can add a test case that compiles a simple C program:
  ```c
  #include <stdio.h>
  #include <string.h>
  void vuln() {
      char buf[10];
      gets(buf); // Vulnerable
  }
  int main() {
      vuln();
      return 0;
  }
  ```
  - Compile with `-fstack-protector-all`.
  - Run `CanaryOracle` against it.
  - Expect `SAFE` (SIGABRT) or `BUG` (SIGSEGV) depending on the compiler/arch.

## 5. Step-by-Step Implementation

1.  **Refactor Interface**: Update `Oracle` interface and existing implementations (`LLMOracle`, `CrashOracle`).
2.  **Update Engine**: Fix compilation errors in `Engine`.
3.  **Implement CanaryOracle**: Write the code in `internal/oracle/plugins/canary.go`.
4.  **Write Tests**: Create `internal/oracle/plugins/canary_test.go`.
