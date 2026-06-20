package cmd

import "github.com/spf13/cobra"

var internalHelpTargets = map[string]struct{}{
	"init":      {},
	"boot":      {},
	"shutdown":  {},
	"reset":     {},
	"term":      {},
	"add-certs": {},
	"status":    {},
	"usage":     {},
}

func newHelpCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:                "help [command]",
		Short:              "Help about any command",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, args []string) error {
			if len(args) == 0 {
				c.application.PrintHelp()
				return nil
			}
			if _, ok := internalHelpTargets[args[0]]; ok {
				target, _, err := command.Root().Find(args[:1])
				if err != nil {
					return err
				}
				return target.Help()
			}
			return c.application.ExecuteContainer(command.Context(), containerCommandArgs("help", args))
		},
	}
}
