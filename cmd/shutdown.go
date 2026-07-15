package cmd

import (
	"executor/internal/app"

	"github.com/spf13/cobra"
)

// newShutdownCommand builds the command that stops Podman and QEMU.
func newShutdownCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "shutdown",
		Short: "Stop QEMU and Podman",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Shutdown(command.Context(), app.ShutdownOptions{})
		},
	}
}
