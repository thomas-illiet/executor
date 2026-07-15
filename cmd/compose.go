package cmd

import "github.com/spf13/cobra"

// newComposeCommand builds the Podman compose proxy command.
func newComposeCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:                "compose",
		Short:              "Proxy podman compose into the VM",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, args []string) error {
			return c.application.ExecuteContainer(command.Context(), containerCommandArgs("compose", args))
		},
	}
}
