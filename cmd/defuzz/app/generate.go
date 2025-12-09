package app

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/zjy-dev/de-fuzz/internal/config"
	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// NewGenerateCommand creates the "generate" subcommand.
func NewGenerateCommand() *cobra.Command {
	var (
		output     string
		count      int
		maxRetries int
	)

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate initial seeds for the configured ISA and defense strategy.",
		Long: `This command generates an initial seed pool for the configured target.
It initializes the LLM's understanding of the target and creates a set of starting seeds.

The ISA and strategy are read from the config.yaml file under the 'config' section.

Each seed consists of:
  - C source code (source.c)
  - Optional test cases (testcases.json)

The seeds are saved in a directory-based format with metadata encoded in the directory name.

Output directory structure:
  {output}/{isa}/{strategy}/
    ├── understanding.md     # LLM's understanding of the target
    └── {seed-name}/         # Each seed is a directory
        ├── source.c         # C source code
        └── testcases.json   # Optional test cases

Examples:
  # Generate 5 seeds using config from config.yaml
  defuzz generate --count 5

  # Generate seeds to a custom directory
  defuzz generate --output ./my_seeds --count 3

  # Generate single seed (default)
  defuzz generate`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// 1. Load configuration
			cfg, err := config.LoadConfig()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Get ISA and strategy from config
			isa := cfg.ISA
			strategy := cfg.Strategy

			if isa == "" || strategy == "" {
				return fmt.Errorf("ISA and strategy must be configured in config.yaml")
			}

			fmt.Printf("[Generate] Target: %s / %s\n", isa, strategy)

			// 2. Create LLM client
			llmClient, err := llm.New(cfg)
			if err != nil {
				return fmt.Errorf("failed to create LLM client: %w", err)
			}

			// 3. Create prompt builder with configuration
			promptBuilder := prompt.NewBuilder(cfg.Compiler.Fuzz.MaxTestCases, cfg.Compiler.Fuzz.FunctionTemplate)

			// Log mode
			if promptBuilder.IsFunctionTemplateMode() {
				fmt.Printf("[Generate] Mode: Function Template (template: %s)\n", cfg.Compiler.Fuzz.FunctionTemplate)
			} else if promptBuilder.RequiresTestCases() {
				fmt.Printf("[Generate] Mode: Standard (with test cases, max: %d)\n", cfg.Compiler.Fuzz.MaxTestCases)
			} else {
				fmt.Printf("[Generate] Mode: Code Only (no test cases)\n")
			}

			// 4. Define base path for seeds
			basePath := filepath.Join(output, isa, strategy)
			fmt.Printf("[Generate] Output directory: %s\n", basePath)

			// 5. Load or generate understanding
			var understanding string
			understanding, err = seed.LoadUnderstanding(basePath)
			if err != nil {
				// If understanding.md doesn't exist, generate it
				fmt.Printf("[Generate] Understanding not found, generating LLM understanding...\n")

				understandPrompt, promptErr := promptBuilder.BuildUnderstandPrompt(isa, strategy, basePath)
				if promptErr != nil {
					return fmt.Errorf("failed to build understand prompt: %w", promptErr)
				}

				fmt.Printf("[Generate] Sending understand prompt to LLM...\n")

				understanding, err = llmClient.Understand(understandPrompt)
				if err != nil {
					return fmt.Errorf("failed to get understanding from LLM: %w", err)
				}

				if err := seed.SaveUnderstanding(basePath, understanding); err != nil {
					return fmt.Errorf("failed to save understanding: %w", err)
				}

				fmt.Printf("[Generate] Understanding saved to %s\n", seed.GetUnderstandingPath(basePath))
			} else {
				fmt.Printf("[Generate] Using existing understanding from %s\n", seed.GetUnderstandingPath(basePath))
			}

			// 6. Create naming strategy for seeds
			namer := seed.NewDefaultNamingStrategy()

			// 7. Generate seeds with retry logic
			fmt.Printf("[Generate] Generating %d seeds (max %d retries per seed)...\n", count, maxRetries)
			successCount := 0

			for i := 0; i < count; i++ {
				var newSeed *seed.Seed
				var lastErr error

				// Retry loop for each seed
				for attempt := 0; attempt <= maxRetries; attempt++ {
					if attempt > 0 {
						fmt.Printf("  [%d/%d] Retry %d/%d...\n", i+1, count, attempt, maxRetries)
					}

					generatePrompt, promptErr := promptBuilder.BuildGeneratePrompt(basePath)
					if promptErr != nil {
						lastErr = fmt.Errorf("failed to build generate prompt: %w", promptErr)
						continue
					}

					// Get raw LLM response
					response, llmErr := llmClient.GetCompletionWithSystem(understanding, generatePrompt)
					if llmErr != nil {
						fmt.Printf("  [%d/%d] LLM request failed: %v\n", i+1, count, llmErr)
						lastErr = llmErr
						continue
					}

					// Parse response using prompt builder (handles different modes)
					newSeed, lastErr = promptBuilder.ParseLLMResponse(response)
					if lastErr != nil {
						fmt.Printf("  [%d/%d] Parse failed: %v\n", i+1, count, lastErr)
						continue
					}

					// Success - break retry loop
					lastErr = nil
					break
				}

				if lastErr != nil {
					fmt.Printf("  [%d/%d] Failed after %d retries: %v\n", i+1, count, maxRetries, lastErr)
					continue
				}

				// Set metadata for the seed
				newSeed.Meta.ID = uint64(i + 1)
				newSeed.Meta.ParentID = 0 // Initial seeds have no parent
				newSeed.Meta.Depth = 0
				newSeed.Meta.State = seed.SeedStatePending

				// Save using the new metadata-based format
				filename, saveErr := seed.SaveSeedWithMetadata(basePath, newSeed, namer)
				if saveErr != nil {
					fmt.Printf("  [%d/%d] Failed to save: %v\n", i+1, count, saveErr)
					continue
				}

				successCount++
				fmt.Printf("  [%d/%d] Generated seed: %s\n", i+1, count, filename)
			}

			if successCount == 0 {
				return fmt.Errorf("failed to generate any seeds")
			}

			fmt.Printf("\n[Generate] Successfully generated %d/%d seeds in %s\n", successCount, count, basePath)

			// List generated seed files
			seeds, err := seed.LoadSeedsWithMetadata(basePath, namer)
			if err != nil {
				fmt.Printf("Warning: Could not load seeds to display summary: %v\n", err)
			} else {
				fmt.Printf("\n[Generate] Seed files:\n")
				for _, s := range seeds {
					fmt.Printf("  - %s (ID=%d, Depth=%d)\n", s.Meta.FilePath, s.Meta.ID, s.Meta.Depth)
				}
			}

			return nil
		},
	}

	// Optional flags - ISA and strategy now come from config
	cmd.Flags().StringVarP(&output, "output", "o", "initial_seeds", "Base output directory for seeds")
	cmd.Flags().IntVarP(&count, "count", "c", 1, "Number of seeds to generate")
	cmd.Flags().IntVar(&maxRetries, "max-retries", 3, "Maximum retries for failed seed generation")

	return cmd
}
