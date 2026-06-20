package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"executor/internal/vm"
)

// status prints the current QEMU, monitor, SSH, and Podman status.
func (a App) status(ctx context.Context, manager vm.Manager) error {
	qemuRunning, qemuDetail := a.qemuStatus(ctx)
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
	process, ok := a.qemuProcess(ctx)
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
func (a App) qemuStatus(ctx context.Context) (bool, string) {
	process, ok := a.qemuProcess(ctx)
	if !ok {
		return false, "stopped"
	}
	return true, "running (pid " + process.PID + ")"
}

// qemuProcess returns the first running QEMU process matching the configured binary.
func (a App) qemuProcess(ctx context.Context) (qemuProcess, bool) {
	name := filepath.Base(a.Config.QEMUBinary)
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	output, err := a.Runner.Output(checkCtx, "pgrep", "-af", name)
	if err != nil {
		return qemuProcess{}, false
	}
	return firstQEMUProcess(output)
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

func firstQEMUProcess(output []byte) (qemuProcess, bool) {
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "<defunct>") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		return qemuProcess{
			PID:     fields[0],
			Command: strings.Join(fields[1:], " "),
		}, true
	}
	return qemuProcess{}, false
}

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
