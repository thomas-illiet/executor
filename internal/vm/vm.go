package vm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"executor/internal/config"
	"executor/internal/system"
)

type Manager struct {
	Config  config.Config
	Runner  system.Runner
	SSH     SSHClient
	Monitor MonitorClient
}

// NewManager creates a VM manager from the app configuration.
func NewManager(cfg config.Config, run system.Runner) Manager {
	return Manager{
		Config: cfg,
		Runner: run,
		SSH: SSHClient{
			SocketPath: cfg.SSHSocket,
			User:       cfg.SSHUser,
			KeyPath:    cfg.SSHKeyPath,
			Runner:     run,
		},
		Monitor: MonitorClient{
			SocketPath: cfg.MonitorSocket,
			Netdev:     "mynet0",
			Timeout:    10 * time.Second,
		},
	}
}

// Start launches QEMU with the configured VM arguments.
func (m Manager) Start(ctx context.Context, daemonize bool) error {
	if err := os.MkdirAll(filepath.Dir(m.Config.VMImage), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.Config.QEMUPIDFile), 0o755); err != nil {
		return err
	}
	if m.isRunning(ctx) {
		return nil
	}
	if err := m.ensurePodmanDisk(ctx); err != nil {
		return err
	}
	if err := m.prepareSockets(); err != nil {
		return err
	}
	if err := m.probeUnixHostForward(ctx); err != nil {
		return err
	}
	args := m.qemuArgs(daemonize)
	return m.Runner.Run(ctx, m.Config.QEMUBinary, args...)
}

// Stop asks QEMU to power down gracefully, then falls back to killing the configured PID.
func (m Manager) Stop(ctx context.Context) error {
	pid, err := m.readQEMUPID()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := m.validateQEMUProcess(ctx, pid); err != nil {
		return err
	}
	if err := m.powerdown(ctx, pid); err == nil {
		_ = os.Remove(m.Config.QEMUPIDFile)
		return nil
	}
	if err := m.Runner.Run(ctx, "kill", pid); err != nil {
		return err
	}
	_ = os.Remove(m.Config.QEMUPIDFile)
	return nil
}

// isRunning reports whether the configured QEMU process is already active.
func (m Manager) isRunning(ctx context.Context) bool {
	pid, err := m.readQEMUPID()
	if err != nil {
		return false
	}
	return m.validateQEMUProcess(ctx, pid) == nil
}

// ensurePodmanDisk creates the dedicated Podman disk image when configured.
func (m Manager) ensurePodmanDisk(ctx context.Context) error {
	if strings.TrimSpace(m.Config.PodmanDiskImage) == "" {
		return nil
	}
	if strings.TrimSpace(m.Config.PodmanDiskSize) == "" {
		return fmt.Errorf("podman disk size must be set")
	}
	if err := os.MkdirAll(filepath.Dir(m.Config.PodmanDiskImage), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(m.Config.PodmanDiskImage); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return m.Runner.Run(ctx, "qemu-img", "create", "-q", "-f", "qcow2", "-o", "preallocation=off", m.Config.PodmanDiskImage, m.Config.PodmanDiskSize)
}

// powerdown asks QEMU to exit through the monitor and waits for the PID to stop.
func (m Manager) powerdown(ctx context.Context, pid string) error {
	if _, err := m.Monitor.Execute(ctx, "system_powerdown"); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := m.Runner.Output(ctx, "ps", "-p", pid, "-o", "args="); err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("QEMU pid %s did not exit after system_powerdown", pid)
}
