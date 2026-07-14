package cmd

import (
	"executor/internal/app"

	"github.com/spf13/cobra"
)

// newInitCommand builds the command that boots and configures the VM.
func newInitCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Boot QEMU and configure Podman",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Init(command.Context())
		},
	}
}

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

// newResetCommand builds the command that clears Podman state and reinitializes.
func newResetCommand(c *cli) *cobra.Command {
	var options app.ResetOptions
	command := &cobra.Command{
		Use:   "reset",
		Short: "Remove Podman state before a fresh init",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Reset(command.Context(), options)
		},
	}
	command.Flags().BoolVarP(&options.Force, "force", "f", false, "reset without prompting")
	return command
}

// newInternalCommand builds the hidden namespace for executor-only commands.
func newInternalCommand(c *cli) *cobra.Command {
	command := &cobra.Command{
		Use:    "internal",
		Short:  "Internal executor commands",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	command.AddCommand(
		newTermCommand(c),
		newConsoleCommand(c),
	)
	return command
}

// newTermCommand builds the internal command that opens a VM shell.
func newTermCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "term",
		Short: "Open an SSH shell in the VM",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Term(command.Context())
		},
	}
}

// newConsoleCommand builds the internal read-only VM console command.
func newConsoleCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "console",
		Short: "Display the read-only VM console",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Console(command.Context())
		},
	}
}

// newAddCertsCommand builds the command that installs CA certificates in the VM.
func newAddCertsCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "add-certs <cert-directory>",
		Short: "Copy CA certificates into the VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			return c.application.AddCerts(command.Context(), args[0])
		},
	}
}

// newStatusCommand builds the command that reports runtime health.
func newStatusCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show QEMU, SSH, and Podman status",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Status(command.Context())
		},
	}
}

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

// composeUpCommandArgs expands the executor shorthand into podman compose up.
func composeUpCommandArgs(args []string) []string {
	values := make([]string, 0, len(args)+2)
	values = append(values, "compose", "up")
	values = append(values, args...)
	return values
}
