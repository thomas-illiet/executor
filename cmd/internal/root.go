package internal

import (
	"executor/internal/app"

	"github.com/spf13/cobra"
)

// New builds the hidden namespace for executor-only commands.
func New(application app.App) *cobra.Command {
	command := &cobra.Command{
		Use:    "internal",
		Short:  "Internal executor commands",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	command.SetHelpFunc(func(*cobra.Command, []string) {})
	command.AddCommand(
		newTermCommand(application),
		newConsoleCommand(application),
	)
	return command
}
