package cmd

import (
	"executor/internal/app"

	"github.com/spf13/cobra"
)

func newInitCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Boot QEMU and configure the container engine",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Init(command.Context())
		},
	}
}

func newServeCommand(c *cli) *cobra.Command {
	var initFirst bool
	command := &cobra.Command{
		Use:   "serve",
		Short: "Keep the proxy container running",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Serve(command.Context(), initFirst)
		},
	}
	command.Flags().BoolVar(&initFirst, "init", false, "boot QEMU and configure the container engine before serving")
	return command
}

func newBootCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "boot",
		Short: "Start QEMU without configuring the container engine",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Boot(command.Context())
		},
	}
}

func newDownloadCommand(c *cli) *cobra.Command {
	var options app.DownloadOptions
	command := &cobra.Command{
		Use:   "download",
		Short: "Download the Alpine VM assets",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Download(command.Context(), options)
		},
	}
	command.Flags().StringVar(&options.Mirror, "mirror", "", "asset mirror base URL")
	command.Flags().StringVar(&options.BasicAuth, "basic-auth", "", "basic authentication credentials as user:password")
	return command
}

func newShutdownCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "shutdown",
		Short: "Stop QEMU and the container engine",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Shutdown(command.Context(), app.ShutdownOptions{})
		},
	}
}

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

func newStatusCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show QEMU, SSH, and container engine status",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return c.application.Status(command.Context())
		},
	}
}

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

func newUpCommand(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:                "up",
		Short:              "Proxy podman compose up into the VM",
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, args []string) error {
			return c.application.ExecuteContainer(command.Context(), containerCommandArgs("up", args))
		},
	}
}
