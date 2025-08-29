# Code Plan for `cmd/defuzz`

This plan details the necessary updates to the `defuzz generate` command to support the enhanced `BuildUnderstandPrompt` function.

## 1. Context

The `BuildUnderstandPrompt` function in `internal/prompt/prompt.go` is being updated to accept a `basePath` argument. This allows it to read additional context files (like `stack_layout.md` and `defense_strategy.c`) located within the `initial_seeds/<isa>/<defense_strategy>/` directory.

## 2. `generate.go` Updates

The `RunE` function within `cmd/defuzz/app/generate.go` needs to be modified to provide this `basePath`.

-   **File to Modify:** `cmd/defuzz/app/generate.go`
-   **Action:**
    1.  Locate the section where the `understanding` is generated (inside the `if err != nil` block after `seed.LoadUnderstanding(basePath)`).
    2.  Find the line that calls `promptBuilder.BuildUnderstandPrompt(isa, strategy)`.
    3.  Update this call to pass the `basePath` variable, which is already defined and available in that scope.
    4.  The modified call will be: `promptBuilder.BuildUnderstandPrompt(isa, strategy, basePath)`.

This change ensures that when the `generate` command creates a new `understanding.md`, it provides the prompt builder with the necessary path to find and include the richer context in the prompt sent to the LLM.
