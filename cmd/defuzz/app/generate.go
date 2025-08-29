package app

import (
	"fmt"
	"path/filepath"

	"defuzz/internal/config"
	"defuzz/internal/llm"
	"defuzz/internal/prompt"
	"defuzz/internal/seed"

	"github.com/spf13/cobra"
)

// NewGenerateCommand creates the "generate" subcommand.
func NewGenerateCommand() *cobra.Command {
	var (
		isa      string
		strategy string
		output   string
		count    int
		seedType string
	)

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate initial seeds for a specific ISA and defense strategy.",
		Long: `This command generates an initial seed pool for a given target.
It initializes the LLM's understanding of the target and creates a set of starting seeds.

Examples:
  defuzz generate --isa x86_64 --strategy stackguard --count 5 --type c
  defuzz generate --isa arm64 --strategy aslr --output ./my_seeds --type asm`,
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

				understandPrompt, err := promptBuilder.BuildUnderstandPrompt(isa, strategy)
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

			// 6. Validate seed type
			var seedTypeEnum seed.SeedType
			switch seedType {
			case "c":
				seedTypeEnum = seed.SeedTypeC
			case "c-asm":
				seedTypeEnum = seed.SeedTypeCAsm
			case "asm":
				seedTypeEnum = seed.SeedTypeAsm
			default:
				return fmt.Errorf("invalid seed type: %s (valid types: c, c-asm, asm)", seedType)
			}

			// 7. Generate seeds
			fmt.Printf("Generating %d seeds of type '%s'...\n", count, seedType)
			for i := 0; i < count; i++ {
				generatePrompt, err := promptBuilder.BuildGeneratePrompt(understanding, seedType)
				if err != nil {
					return fmt.Errorf("failed to build generate prompt: %w", err)
				}

				newSeed, err := llmClient.Generate(generatePrompt, seedTypeEnum)
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
			fmt.Printf("Seeds can be found in the following directories:\n")

			// List generated seed directories
			pool, err := seed.LoadSeeds(basePath)
			if err != nil {
				fmt.Printf("Warning: Could not load seeds to display summary: %v\n", err)
			} else {
				for {
					s := pool.Next()
					if s == nil {
						break
					}
					fmt.Printf("  - %s_%s/\n", s.ID, s.Type)
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
	cmd.Flags().StringVarP(&seedType, "type", "t", "c", "Type of seed to generate (c, c-asm, asm)")

	return cmd
}
