package cmd

import "github.com/spf13/cobra"

// newUpCommand builds the shorthand command for podman compose up.
func newUpCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:                "up",
		Short:              "Proxy podman compose up into the VM",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, args []string) error {
			return c.application.ExecuteContainer(command.Context(), composeUpCommandArgs(args))
		},
	}
}
