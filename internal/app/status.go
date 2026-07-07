package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"executor/internal/vm"
)

// status prints the current QEMU, monitor, SSH, and Podman status.
func (a App) status(ctx context.Context, manager vm.Manager) error {
	qemuRunning, qemuDetail := a.qemuStatus(ctx, manager)
	monitorReachable := a.monitorReachable(ctx, manager)
	sshReachable := a.sshReachable(ctx, manager)
	podmanRunning, podmanDetail := a.podmanStatus(ctx, manager, sshReachable)

	overall := "stopped"
	if qemuRunning || sshReachable || podmanRunning {
		overall = "degraded"
	}
	if qemuRunning && sshReachable && podmanRunning {
		overall = "ready"
	}

	fmt.Fprintf(a.Out, "Overall: %s\n", overall)
	fmt.Fprintf(a.Out, "QEMU:    %s\n", qemuDetail)
	fmt.Fprintf(a.Out, "Monitor: %s (%s)\n", reachableLabel(monitorReachable), manager.Monitor.Endpoint())
	fmt.Fprintf(a.Out, "SSH:     %s (%s)\n", reachableLabel(sshReachable), manager.SSH.Endpoint())
	fmt.Fprintf(a.Out, "Podman:  %s\n", podmanDetail)
	return nil
}

// usage prints a CPU and memory usage snapshot for the QEMU process.
func (a App) usage(ctx context.Context) error {
	process, ok := a.qemuProcess(ctx, a.manager())
	if !ok {
		return fmt.Errorf("QEMU is not running")
	}
	usage, err := a.readQEMUUsage(ctx, process.PID)
	if err != nil {
		return err
	}

	command := usage.Command
	if command == "" {
		command = process.Command
	}

	fmt.Fprintln(a.Out, "QEMU usage:")
	fmt.Fprintf(a.Out, "%-8s %6s %6s %10s %10s %s\n", "PID", "CPU%", "MEM%", "RSS MiB", "VSZ MiB", "COMMAND")
	fmt.Fprintf(a.Out, "%-8s %6s %6s %10s %10s %s\n",
		usage.PID,
		usage.CPUPercent,
		usage.MemPercent,
		formatKiBAsMiB(usage.RSSKiB),
		formatKiBAsMiB(usage.VSZKiB),
		command,
	)
	return nil
}

// monitorReachable checks whether the QEMU monitor accepts commands.
func (a App) monitorReachable(ctx context.Context, manager vm.Manager) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := manager.Monitor.Execute(checkCtx, "info status")
	return err == nil
}

// sshReachable checks whether the VM accepts SSH commands.
func (a App) sshReachable(ctx context.Context, manager vm.Manager) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := manager.SSH.Output(checkCtx, "true")
	return err == nil
}

// qemuStatus checks whether the configured QEMU process is running.
func (a App) qemuStatus(ctx context.Context, manager vm.Manager) (bool, string) {
	process, ok := a.qemuProcess(ctx, manager)
	if !ok {
		return false, "stopped"
	}
	return true, "running (pid " + process.PID + ")"
}

// qemuProcess returns the configured QEMU process from the pidfile.
func (a App) qemuProcess(ctx context.Context, manager vm.Manager) (qemuProcess, bool) {
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	pid, command, err := manager.QEMUProcess(checkCtx)
	if err != nil {
		return qemuProcess{}, false
	}
	return qemuProcess{PID: pid, Command: command}, true
}

// readQEMUUsage reads process CPU and memory usage from ps.
func (a App) readQEMUUsage(ctx context.Context, pid string) (qemuUsageStats, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	output, err := a.Runner.Output(checkCtx, "ps",
		"-p", pid,
		"-o", "pid=",
		"-o", "pcpu=",
		"-o", "pmem=",
		"-o", "rss=",
		"-o", "vsz=",
		"-o", "comm=",
	)
	if err != nil {
		return qemuUsageStats{}, fmt.Errorf("failed to read QEMU usage: %w", err)
	}
	usage, err := parseQEMUUsage(output)
	if err != nil {
		return qemuUsageStats{}, err
	}
	return usage, nil
}

type qemuProcess struct {
	PID     string
	Command string
}

type qemuUsageStats struct {
	PID        string
	CPUPercent string
	MemPercent string
	RSSKiB     int64
	VSZKiB     int64
	Command    string
}

// parseQEMUUsage parses ps output into QEMU usage statistics.
func parseQEMUUsage(output []byte) (qemuUsageStats, error) {
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) < 6 {
		return qemuUsageStats{}, fmt.Errorf("failed to parse QEMU usage")
	}
	rssKiB, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return qemuUsageStats{}, fmt.Errorf("failed to parse QEMU RSS: %w", err)
	}
	vszKiB, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return qemuUsageStats{}, fmt.Errorf("failed to parse QEMU VSZ: %w", err)
	}
	return qemuUsageStats{
		PID:        fields[0],
		CPUPercent: fields[1],
		MemPercent: fields[2],
		RSSKiB:     rssKiB,
		VSZKiB:     vszKiB,
		Command:    strings.Join(fields[5:], " "),
	}, nil
}

// formatKiBAsMiB formats a KiB value as MiB with one decimal place.
func formatKiBAsMiB(kib int64) string {
	return fmt.Sprintf("%.1f", float64(kib)/1024)
}

// podmanStatus checks Podman availability through SSH.
func (a App) podmanStatus(ctx context.Context, manager vm.Manager, sshReachable bool) (bool, string) {
	if !sshReachable {
		return false, "unreachable (SSH is down)"
	}

	timeout := a.Config.CommandTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	output, err := manager.SSH.Output(checkCtx, podmanShellEnv()+" podman --version")
	if err != nil {
		return false, "unavailable (" + strings.TrimSpace(err.Error()) + ")"
	}
	version := strings.TrimSpace(string(output))
	if version == "" {
		return true, "running"
	}
	return true, "running (" + version + ")"
}
