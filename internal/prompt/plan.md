# Code Plan for `internal/prompt`

This plan outlines the enhancements for the `BuildUnderstandPrompt` function to create more detailed and context-rich prompts for the LLM.

## 1. Helper Function: `readFileOrDefault`

A private helper function will be added to the `prompt` package to safely read auxiliary context files.

-   **Function Signature:** `readFileOrDefault(path string) (string, error)`
-   **Behavior:**
    -   Attempts to read the file at the given `path`.
    -   If the file does not exist (`os.IsNotExist`), it returns the default string `"Not available for now"` and a `nil` error.
    -   If any other error occurs during reading, it returns an empty string and the error.
    -   On successful read, it returns the file's content as a string and a `nil` error.

## 2. `BuildUnderstandPrompt` Enhancement

The `BuildUnderstandPrompt` function will be updated to incorporate the additional context.

-   **New Signature:** `BuildUnderstandPrompt(isa, strategy, basePath string) (string, error)`
-   **Logic:**
    1.  It will construct the paths to the context files within the `basePath` directory (e.g., `initial_seeds/<isa>/<defense_strategy>/`).
        -   `stackLayoutPath := filepath.Join(basePath, "stack_layout.md")`
        -   `sourceCodePath := filepath.Join(basePath, "defense_strategy.c")`
    2.  It will call the `readFileOrDefault` helper for each of these paths to get their content.
    3.  The `fmt.Sprintf` template for the prompt will be expanded to include new sections:
        -   `[ISA Stack Layout]`
        -   `[Defense Strategy Source Code]`
    4.  These new sections will be populated with the content read from the files.

This change will produce a much more detailed initial prompt, enabling the LLM to generate a higher quality "understanding" and, subsequently, better seeds.