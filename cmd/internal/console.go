package internal

import (
	"executor/internal/app"

	"github.com/spf13/cobra"
)

// newConsoleCommand builds the internal read-only VM console command.
func newConsoleCommand(application app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "console",
		Short: "Display the read-only VM console",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return application.Console(command.Context())
		},
	}
}
