### `llm` Package Plan

1.  **Purpose**: This package is responsible for abstracting all communication with the Large Language Model (LLM). It provides a standardized interface that the rest of the application can use, decoupling the core fuzzing logic from the specific LLM provider being used (e.g., Gemini, OpenAI, etc.).

2.  **Core Components**:
    *   **`LLM` Interface**: This will be the central abstraction. It will define a set of methods that represent the key interactions with the LLM as required by the fuzzing algorithm.
        *   `UnderstandPrompt(prompt string) (string, error)`: Sends the initial, detailed prompt (containing environment, defense strategy, etc.) to the LLM and returns a unique identifier for the resulting "understanding" or conversation context.
        *   `GenerateInitialSeeds(ctxID string, n int) ([]seed.Seed, error)`: Using the context from a previous prompt, asks the LLM to generate `n` initial seeds.
        *   `AnalyzeFeedback(ctxID string, s seed.Seed, feedback string) (*Analysis, error)`: Provides the LLM with a seed and its execution feedback, asking for an analysis of the outcome.
        *   `MutateSeed(ctxID string, s seed.Seed) (*seed.Seed, error)`: Asks the LLM to mutate an existing seed to create a new variant for the seed pool.

    *   **`Analysis` Struct**: A struct to hold the structured response from the `AnalyzeFeedback` method. It will contain fields like:
        *   `IsBug`: A boolean indicating if the LLM identified a bug.
        *   `Description`: A detailed explanation of the bug if one was found.
        *   `ShouldDiscard`: A boolean indicating whether the LLM suggests discarding the seed as unproductive.

    *   **Concrete Implementations**: In the future, concrete types that implement the `LLM` interface will be created (e.g., `geminiLLM`, `openAILLM`). These structs will handle the provider-specific details of API requests, authentication, and response parsing.

3.  **Dependencies**:
    *   This package will depend on the `internal/seed` package for the `Seed` type.

4.  **Configuration**:
    *   Configuration details (API keys, model names, endpoints) will be managed externally (e.g., in the `configs` directory) and passed into the constructor functions for the concrete LLM implementations.
