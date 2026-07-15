package cmd

import "github.com/spf13/cobra"

// newRunCommand builds the Podman run proxy command.
func newRunCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:                "run",
		Short:              "Proxy podman run into the VM",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, args []string) error {
			return c.application.ExecuteContainer(command.Context(), containerCommandArgs("run", args))
		},
	}
}
