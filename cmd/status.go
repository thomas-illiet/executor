package cmd

import "github.com/spf13/cobra"

// newStatusCommand builds the command that reports runtime health.
func newStatusCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show QEMU, SSH, and Podman status",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Status(command.Context())
		},
	}
}
