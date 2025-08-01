## Plan for `internal/analysis` Module

### 1. Objective

To provide a concrete implementation of the `Analyzer` interface. This module's primary responsibility is to determine if the execution of a seed has resulted in a bug.

### 2. Core Component: `LLMAnalyzer`

- An `LLMAnalyzer` struct will implement the `Analyzer` interface.
- This implementation will be stateless and will orchestrate the analysis by calling the `prompt` and `llm` modules.

### 3. Implementation (`analysis.go`)

- **`NewLLMAnalyzer()`**: A constructor that returns a new `LLMAnalyzer`.
- **`AnalyzeResult(s *seed.Seed, result *vm.ExecutionResult, l llm.LLM, pb *prompt.Builder, ctx string)`**:
  - This method will be the core of the module.
  - It will first call `pb.BuildAnalyzePrompt()` with the seed, result, and context to generate a detailed prompt for the LLM.
  - It will then need to call a method on the LLM to get the analysis. The existing `llm.Analyze` method is not suitable as it's designed to be abstract. A more direct method is needed.
  - **Proposed Change**: I will add a `GetCompletion(prompt string) (string, error)` method to the `llm.LLM` interface. This will be a general-purpose method for getting a direct response from the LLM. The existing `Understand`, `Generate`, and `Mutate` methods will be refactored to use this new method internally.
  - The `AnalyzeResult` method will call `l.GetCompletion()` with the analysis prompt.
  - It will parse the response from the LLM. If the response, after trimming whitespace, is exactly "BUG", it will construct and return an `analysis.Bug` struct.
  - If the response is anything else, it will return `nil` (no bug found).

### 4. Testing (`analysis_test.go`)

- A `mockLLM` will be created that implements the `llm.LLM` interface (including the new `GetCompletion` method).
- Tests will inject the mock LLM and a real `prompt.Builder`.
- Tests will verify:
  - That `AnalyzeResult` correctly identifies a bug when the mock LLM returns "BUG".
  - That `AnalyzeResult` returns `nil` when the mock LLM returns "NO_BUG" or any other string.
  - That errors from the LLM are propagated correctly.
