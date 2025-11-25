package app

import (
	"fmt"
	"path/filepath"

	"github.com/zjy-dev/de-fuzz/internal/config"
	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
	"github.com/zjy-dev/de-fuzz/internal/seed"

	"github.com/spf13/cobra"
)

// NewGenerateCommand creates the "generate" subcommand.
func NewGenerateCommand() *cobra.Command {
	var (
		isa      string
		strategy string
		output   string
		count    int
	)

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate initial seeds for a specific ISA and defense strategy.",
		Long: `This command generates an initial seed pool for a given target.
It initializes the LLM's understanding of the target and creates a set of starting seeds.

Each seed consists of:
  - C source code (source.c)
  - Makefile for compilation
  - Run script (run.sh) for execution

The generated seeds are ready for compilation and fuzzing on the host machine.

Examples:
  # Generate 5 seeds for x86_64 with stack guard protection
  defuzz generate --isa x86_64 --strategy stackguard --count 5

  # Generate ARM64 seeds with ASLR and save to custom directory
  defuzz generate --isa arm64 --strategy aslr --output ./my_seeds --count 3

  # Generate single seed for RISC-V with CFI
  defuzz generate --isa riscv64 --strategy cfi`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// 1. Load configuration
			cfg, err := config.LoadConfig()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// 2. Create LLM client
			llmClient, err := llm.New(cfg)
			if err != nil {
				return fmt.Errorf("failed to create LLM client: %w", err)
			}

			// 3. Create prompt builder
			promptBuilder := prompt.NewBuilder()

			// 4. Define base path for seeds
			basePath := filepath.Join(output, isa, strategy)

			// 5. Load or generate understanding
			var understanding string
			understanding, err = seed.LoadUnderstanding(basePath)
			if err != nil {
				// If understanding.md doesn't exist, generate it
				fmt.Printf("Understanding not found, generating LLM understanding for %s/%s...\n", isa, strategy)

				understandPrompt, err := promptBuilder.BuildUnderstandPrompt(isa, strategy, basePath)

				fmt.Printf("Understand Prompt:\n%s\n", understandPrompt)

				if err != nil {
					return fmt.Errorf("failed to build understand prompt: %w", err)
				}

				understanding, err = llmClient.Understand(understandPrompt)
				if err != nil {
					return fmt.Errorf("failed to get understanding from LLM: %w", err)
				}

				if err := seed.SaveUnderstanding(basePath, understanding); err != nil {
					return fmt.Errorf("failed to save understanding: %w", err)
				}

				fmt.Printf("Understanding saved to %s\n", seed.GetUnderstandingPath(basePath))
			} else {
				fmt.Printf("Using existing understanding from %s\n", seed.GetUnderstandingPath(basePath))
			}

			// 6. Generate seeds
			fmt.Printf("Generating %d seeds...\n", count)
			for i := 0; i < count; i++ {
				generatePrompt, err := promptBuilder.BuildGeneratePrompt(basePath)
				if err != nil {
					return fmt.Errorf("failed to build generate prompt: %w", err)
				}

				newSeed, err := llmClient.Generate(understanding, generatePrompt)
				if err != nil {
					return fmt.Errorf("failed to generate seed %d from LLM: %w", i+1, err)
				}

				// Set a unique ID for the seed
				newSeed.ID = fmt.Sprintf("%s_%s_gen_%03d", isa, strategy, i+1)

				if err := seed.SaveSeed(basePath, newSeed); err != nil {
					return fmt.Errorf("failed to save seed %d: %w", i+1, err)
				}

				fmt.Printf("  Generated seed %d: %s\n", i+1, newSeed.ID)
			}

			fmt.Printf("\nSuccessfully generated %d seeds in %s\n", count, basePath)
			fmt.Printf("Seeds can be found in the following files:\n")

			// List generated seed files
			seeds, err := seed.LoadSeedsWithMetadata(basePath, seed.NewDefaultNamingStrategy())
			if err != nil {
				fmt.Printf("Warning: Could not load seeds to display summary: %v\n", err)
			} else {
				for _, s := range seeds {
					fmt.Printf("  - %s\n", s.Meta.FilePath)
				}
			}

			return nil
		},
	}

	// Required flags
	cmd.Flags().StringVar(&isa, "isa", "", "Target ISA (e.g., x86_64, arm64)")
	cmd.Flags().StringVar(&strategy, "strategy", "", "Defense strategy (e.g., stackguard, aslr)")
	_ = cmd.MarkFlagRequired("isa")
	_ = cmd.MarkFlagRequired("strategy")

	// Optional flags
	cmd.Flags().StringVarP(&output, "output", "o", "initial_seeds", "Output directory for seeds")
	cmd.Flags().IntVarP(&count, "count", "c", 1, "Number of seeds to generate")

	return cmd
}
