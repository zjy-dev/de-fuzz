## Plan for `internal/llm` Module

### 1. Objective

This module will abstract all interactions with Large Language Models (LLMs). It will provide a consistent interface for the rest of the application to perform key tasks like understanding fuzzing targets, generating seeds, analyzing execution feedback, and mutating seeds, regardless of the specific LLM provider being used.

### 2. Core Components

#### a. `llm.go`: The Core Interface and Factory

-   **`LLM` Interface**: Define a generic interface that all LLM clients must implement. This ensures that different LLMs can be used interchangeably.
    ```go
    package llm

    import "defuzz/internal/seed"

    // LLM defines the interface for interacting with a Large Language Model.
    type LLM interface {
        // GetCompletion sends a raw prompt to the LLM and gets a direct response.
        // This is the foundational method that other, more specific methods will use.
        GetCompletion(prompt string) (string, error)

        // Understand processes the initial prompt and returns the LLM's summary.
        Understand(prompt string) (string, error)

        // Generate creates a new seed based on the provided context.
        Generate(ctx, seedType string) (*seed.Seed, error)

        // Analyze interprets the feedback from a seed execution.
        Analyze(ctx string, s *seed.Seed, feedback string) (string, error)

        // Mutate modifies an existing seed to create a new variant.
        Mutate(ctx string, s *seed.Seed) (*seed.Seed, error)
    }
    ```
-   **`New()` Factory Function**: A constructor that reads configuration (via the `config` module) to determine which LLM implementation to instantiate (e.g., "deepseek", "openai"). It will return an object satisfying the `LLM` interface.

#### b. `deepseek.go`: A Concrete Implementation

-   **`DeepSeekClient` Struct**: This struct will hold the necessary configuration for the DeepSeek API, such as the API key and an `http.Client`.
-   **Implementation of `LLM`**: The `DeepSeekClient` will implement all methods of the `LLM` interface.
    -   The `GetCompletion` method will handle the direct API call and response parsing.
    -   The other methods (`Understand`, `Generate`, `Analyze`, `Mutate`) will be responsible for constructing their specific prompts and then calling `GetCompletion` to get the result. They will then parse the raw string response from `GetCompletion` into the required return type (e.g., `*seed.Seed`).

### 3. Configuration

-   The `New()` factory will depend on the `config` module to fetch LLM settings from `configs/llm.yaml`. The configuration will specify the provider, model, and API key.
    ```yaml
    # configs/llm.yaml
    provider: "deepseek"
    model: "deepseek-coder"
    api_key: "YOUR_API_KEY_HERE"
    ```

### 4. Testing (`llm_test.go`)

-   **Factory Test**: Test the `New()` function to ensure it returns the correct client type based on mock configuration data.
-   **Client Mocks**: Use an HTTP client mock (like `net/http/httptest`) to test the `DeepSeekClient` without making real network calls.
-   **Interface Method Tests**: For each method, write a test to verify:
    -   The correct, task-specific prompt is constructed.
    -   The `GetCompletion` method is called with the right prompt.
    -   The client correctly parses a successful response from `GetCompletion`.
    -   The client handles errors gracefully.