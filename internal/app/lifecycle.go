package app

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"executor/internal/vm"
)

var downloadVMAssets = vm.DownloadAssets

// init prepares the VM assets, starts QEMU, and configures rootless Podman.
func (a App) init(ctx context.Context, manager vm.Manager, creds vm.Credentials) error {
	if err := a.ensureMemory(); err != nil {
		return err
	}
	if err := downloadVMAssets(ctx, a.assetPaths(), a.Out); err != nil {
		return err
	}
	if err := a.ensureVMAssets(); err != nil {
		return err
	}
	fmt.Fprintln(a.Out, "Initializing VM...")
	if err := manager.Start(ctx, true); err != nil {
		return err
	}
	if err := manager.WaitForSSH(ctx, a.Config.BootTimeout); err != nil {
		_ = manager.Stop(context.Background())
		return err
	}
	if err := manager.ConfigurePodman(ctx, creds); err != nil {
		return err
	}
	fmt.Fprintln(a.Out, "Ready.")
	return nil
}

// shutdown stops Podman containers before stopping QEMU.
func (a App) shutdown(ctx context.Context, manager vm.Manager, _ ShutdownOptions) error {
	_ = manager.StopPodman(ctx)
	_ = manager.Sync(ctx)
	fmt.Fprintln(a.Out, "Stopping...")
	return manager.Stop(ctx)
}

// reset removes Podman state and reinitializes the VM.
func (a App) reset(ctx context.Context, manager vm.Manager, creds vm.Credentials, options ResetOptions) error {
	if !options.Force && !a.confirm("Do you want to erase Podman state and restart (Y/N)? ") {
		return nil
	}
	_ = manager.StopPodman(ctx)
	_ = manager.Sync(ctx)
	_ = manager.Stop(ctx)
	if a.Config.PodmanDiskImage != "" {
		if err := os.Remove(a.Config.PodmanDiskImage); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return a.init(ctx, manager, creds)
}

// addCerts copies local certificates into the VM and refreshes trust.
func (a App) addCerts(ctx context.Context, ssh vm.SSHClient, certPath string) error {
	command := "cd " + remotePath(a.Config.WorkDir) + " && cp " + remotePath(certPath) + "/* /etc/ssl/certs && update-ca-certificates"
	return ssh.RunNoTTY(ctx, command)
}

// ensureMemory checks that the container memory limit can host the VM.
func (a App) ensureMemory() error {
	maxBytes, ok := readMemoryMax()
	if !ok {
		return nil
	}
	required := int64(a.Config.MemoryMiB) * 1024 * 1024
	if maxBytes <= required {
		return fmt.Errorf("Podman VM needs more than %d MiB of container memory", a.Config.MemoryMiB)
	}
	return nil
}

// ensureVMAssets verifies the VM assets are available before booting.
func (a App) ensureVMAssets() error {
	return vm.EnsureAssets(a.assetPaths())
}

// assetPaths returns the configured local paths for required VM assets.
func (a App) assetPaths() vm.AssetPaths {
	return vm.AssetPaths{
		Image:  a.Config.VMImage,
		Kernel: a.Config.KernelImage,
		Initrd: a.Config.InitrdImage,
		SSHKey: a.Config.SSHKeyPath,
	}
}

// readMemoryMax reads the cgroup memory limit when one is set.
func readMemoryMax() (int64, bool) {
	content, err := os.ReadFile("/sys/fs/cgroup/memory.max")
	if err != nil {
		return 0, false
	}
	value := strings.TrimSpace(string(content))
	if value == "" || value == "max" {
		return 0, false
	}
	max, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return max, true
}
