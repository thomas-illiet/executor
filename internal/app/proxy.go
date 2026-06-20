package app

import (
	"context"
	"fmt"

	"executor/internal/container"
	"executor/internal/system"
	"executor/internal/vm"
)

// runContainer rewrites published ports before running a container.
func (a App) runContainer(ctx context.Context, manager vm.Manager, args []string) error {
	allocate := func() (int, error) { return container.AllocateFree(localForwardCheckHost) }
	rewritten, mappings, err := container.RewriteRunPublishArgs(args, allocate)
	if err != nil {
		return err
	}
	for _, mapping := range mappings {
		if err := a.openForward(ctx, manager, mapping); err != nil {
			return err
		}
	}
	return a.proxy(ctx, manager.SSH, rewritten)
}

// compose opens compose service ports before proxying compose commands.
func (a App) compose(ctx context.Context, manager vm.Manager, args []string) error {
	if contains(args, "-h") || contains(args, "--help") {
		return a.proxy(ctx, manager.SSH, args)
	}
	if container.HasUp(args) {
		path, err := container.ResolveFile(args, a.Config.WorkDir)
		if err != nil {
			return err
		}
		allocate := func() (int, error) { return container.AllocateFree(localForwardCheckHost) }
		mappings, warnings, err := container.LoadPorts(path, allocate)
		for _, warning := range warnings {
			fmt.Fprintln(a.Err, "warning:", warning)
		}
		if err != nil {
			return err
		}
		for _, mapping := range mappings {
			if err := a.openForward(ctx, manager, mapping); err != nil {
				return err
			}
		}
	}
	return a.proxy(ctx, manager.SSH, args)
}

// proxy runs a Podman-compatible command on the VM through SSH.
func (a App) proxy(ctx context.Context, ssh vm.SSHClient, args []string) error {
	workDir := a.remoteWorkDir()
	engineCommand := a.engineCommand()
	if command, ok := container.DetachedRunCommandWithPrefix(engineCommand, args); ok {
		return ssh.RunNoTTY(ctx, "cd "+system.Single(workDir)+" && "+command)
	}
	command := container.CommandWithPrefix(engineCommand, args)
	if container.WantsTTY(args) {
		return ssh.RunInDir(ctx, workDir, command)
	}
	if container.Detaches(args) {
		return ssh.RunInDirDetachedNoTTY(ctx, workDir, command)
	}
	return ssh.RunInDirNoTTY(ctx, workDir, command)
}

func (a App) remoteWorkDir() string {
	if a.Config.HostShare == "none" {
		return "/home/coder"
	}
	return a.Config.WorkDir
}

// openForward creates a local SSH forward for a published port.
func (a App) openForward(ctx context.Context, manager vm.Manager, mapping container.Mapping) error {
	if container.IsOpen(localForwardCheckHost, mapping.HostPort) {
		_ = a.Runner.Run(ctx, "pkill", "-f", fmt.Sprintf("0.0.0.0:%d:127.0.0.1:%d", mapping.HostPort, mapping.HostPort))
		if container.IsOpen(localForwardCheckHost, mapping.HostPort) {
			return fmt.Errorf("port %d is already used", mapping.HostPort)
		}
	}
	listenHost := "0.0.0.0"
	if mapping.IP != "" {
		listenHost = mapping.IP
	}
	fmt.Fprintf(a.Out, "Forwarding %s:%d -> VM:%d/%s\n", hostLabel(mapping.IP), mapping.HostPort, mapping.HostPort, mapping.Protocol)
	return manager.SSH.StartLocalForward(ctx, listenHost, mapping.HostPort, "127.0.0.1", mapping.HostPort)
}
