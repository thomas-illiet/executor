package cmd

import (
	"context"
	"fmt"

	"executor/internal/app"

	"github.com/spf13/cobra"
)

type cli struct {
	application app.App
}

// ExecuteContext runs the Cobra command tree with explicit arguments.
func ExecuteContext(ctx context.Context, application app.App, args []string) error {
	root := New(application)
	if args == nil {
		args = []string{}
	}
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}

// New builds the executor command tree.
func New(application app.App) *cobra.Command {
	c := &cli{application: application}
	root := &cobra.Command{
		Use:                app.CommandName,
		Short:              "A self-sufficient runtime for containers",
		SilenceUsage:       true,
		SilenceErrors:      true,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE:               c.runRoot,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(application.Out)
	root.SetErr(application.Err)
	root.SetIn(application.In)
	root.SetHelpCommand(newHelpCommand(c))

	root.AddCommand(
		newInitCommand(c),
		newBootCommand(c),
		newShutdownCommand(c),
		newResetCommand(c),
		newTermCommand(c),
		newAddCertsCommand(c),
		newStatusCommand(c),
		newUsageCommand(c),
		newRunCommand(c),
		newComposeCommand(c),
		newUpCommand(c),
	)
	return root
}

// runRoot handles top-level help/version locally and proxies everything else.
func (c *cli) runRoot(command *cobra.Command, args []string) error {
	if len(args) == 0 {
		c.application.PrintHelp()
		return nil
	}
	switch args[0] {
	case "-h", "--help":
		c.application.PrintHelp()
		return nil
	case "-v", "--version":
		fmt.Fprintln(c.application.Out, app.Version)
		return nil
	default:
		return c.application.ExecuteContainer(command.Context(), args)
	}
}

// containerCommandArgs prefixes proxied arguments with a Podman subcommand name.
func containerCommandArgs(name string, args []string) []string {
	values := make([]string, 0, len(args)+1)
	values = append(values, name)
	values = append(values, args...)
	return values
}
