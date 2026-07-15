package internal

import (
	"executor/internal/app"

	"github.com/spf13/cobra"
)

// newTermCommand builds the internal command that opens a VM shell.
func newTermCommand(application app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "term",
		Short: "Open an SSH shell in the VM",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return application.Term(command.Context())
		},
	}
}
