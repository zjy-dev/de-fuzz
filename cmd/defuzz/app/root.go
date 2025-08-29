package app

import (
	"github.com/spf13/cobra"
)

// NewDefuzzCommand creates the root command for the defuzz tool.
func NewDefuzzCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "defuzz",
		Short: "A fuzzer for software defense strategies.",
		Long:  `DeFuzz is a command-line tool for fuzzing software defense strategies.`,
	}

	cmd.AddCommand(NewGenerateCommand())
	// Future commands like 'fuzz' will be added here.

	return cmd
}
