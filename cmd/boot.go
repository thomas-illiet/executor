package cmd

import "github.com/spf13/cobra"

// newBootCommand builds the command that starts QEMU without setup.
func newBootCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "boot",
		Short: "Start QEMU without configuring Podman",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Boot(command.Context())
		},
	}
}
