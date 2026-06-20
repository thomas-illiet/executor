package app

import (
	"context"
	"fmt"
	"io"

	"executor/internal/config"
	"executor/internal/system"
	"executor/internal/vm"
)

const (
	CommandName = "podman"
	Version     = "0.1.0"
)

const localForwardCheckHost = "127.0.0.1"

type App struct {
	Config config.Config
	Runner system.Runner
	Out    io.Writer
	Err    io.Writer
	In     io.Reader
}

type ShutdownOptions struct{}

type ResetOptions struct {
	Force bool
}

// New creates an App with its configuration, runner, and I/O streams.
func New(cfg config.Config, run system.Runner, out io.Writer, err io.Writer, in io.Reader) App {
	return App{Config: cfg, Runner: run, Out: out, Err: err, In: in}
}

// PrintHelp writes the Podman command help text.
func (a App) PrintHelp() {
	a.printHelp()
}

// Init prepares the VM assets, starts QEMU, and configures rootless Podman.
func (a App) Init(ctx context.Context) error {
	creds, err := vm.LoadCredentials(a.Config.BootFile)
	if err != nil {
		return err
	}
	return a.init(ctx, a.manager(), creds)
}

// Boot starts QEMU without configuring Podman.
func (a App) Boot(ctx context.Context) error {
	if err := a.ensureVMAssets(); err != nil {
		return err
	}
	return a.manager().Start(ctx, false)
}

// Shutdown stops Podman and the VM.
func (a App) Shutdown(ctx context.Context, options ShutdownOptions) error {
	return a.shutdown(ctx, a.manager(), options)
}

// Reset removes Podman state and reinitializes the VM.
func (a App) Reset(ctx context.Context, options ResetOptions) error {
	creds, err := vm.LoadCredentials(a.Config.BootFile)
	if err != nil {
		return err
	}
	return a.reset(ctx, a.manager(), creds, options)
}

// Term opens an SSH shell in the VM.
func (a App) Term(ctx context.Context) error {
	return a.manager().SSH.Shell(ctx)
}

// AddCerts copies local certificates into the VM and refreshes trust.
func (a App) AddCerts(ctx context.Context, certPath string) error {
	return a.addCerts(ctx, a.manager().SSH, certPath)
}

// Status prints the current QEMU, monitor, SSH, and Podman status.
func (a App) Status(ctx context.Context) error {
	return a.status(ctx, a.manager())
}

// Usage prints a CPU and memory usage snapshot for the QEMU process.
func (a App) Usage(ctx context.Context) error {
	return a.usage(ctx)
}

// ExecuteContainer proxies Podman-compatible arguments to the VM.
func (a App) ExecuteContainer(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.printHelp()
		return nil
	}
	manager := a.manager()
	if !a.sshReachable(ctx, manager) {
		return fmt.Errorf("%s init has not been run: run '%s init' first", CommandName, CommandName)
	}

	switch args[0] {
	case "run":
		return a.runContainer(ctx, manager, args)
	case "compose", "up":
		return a.compose(ctx, manager, args)
	default:
		return a.proxy(ctx, manager.SSH, args)
	}
}

// manager builds a VM manager from the App configuration and runner.
func (a App) manager() vm.Manager {
	return vm.NewManager(a.Config, a.Runner)
}
