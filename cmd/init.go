package cmd

import (
	"executor/internal/app"
	"executor/internal/config"

	"github.com/spf13/cobra"
)

// newInitCommand builds the command that boots and configures the VM.
func newInitCommand(c *cli) *cobra.Command {
	var cpu int
	var memory string
	command := &cobra.Command{
		Use:   "init",
		Short: "Boot QEMU and configure Podman",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			var options app.InitOptions
			if command.Flags().Changed("cpu") {
				options.CPUs = &cpu
			}
			if command.Flags().Changed("memory") {
				memoryMiB, err := config.ParseMemoryMiB(memory)
				if err != nil {
					return err
				}
				options.MemoryMiB = &memoryMiB
			}
			return c.application.Init(command.Context(), options)
		},
	}
	command.Flags().IntVar(&cpu, "cpu", 0, "number of virtual CPUs")
	command.Flags().StringVar(&memory, "memory", "", "VM memory (for example 4096M or 4G)")
	return command
}
