package cmd

import "github.com/spf13/cobra"

// newUsageCommand builds the command that reports QEMU resource usage.
func newUsageCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "usage",
		Short: "Show QEMU CPU and memory usage",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Usage(command.Context())
		},
	}
}
