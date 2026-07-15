package cmd

import (
	"executor/internal/app"

	"github.com/spf13/cobra"
)

// newResetCommand builds the command that clears Podman state and reinitializes.
func newResetCommand(c *cli) *cobra.Command {
	var options app.ResetOptions
	command := &cobra.Command{
		Use:   "reset",
		Short: "Remove Podman state before a fresh init",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Reset(command.Context(), options)
		},
	}
	command.Flags().BoolVarP(&options.Force, "force", "f", false, "reset without prompting")
	return command
}
