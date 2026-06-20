package vm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// qemuArgs builds QEMU arguments for the configured share mode.
func (m Manager) qemuArgs(daemonize bool) []string {
	return m.qemuArgsWithShare(daemonize, m.Config.HostShare)
}

// qemuArgsWithShare builds QEMU arguments for a specific share mode.
func (m Manager) qemuArgsWithShare(daemonize bool, share string) []string {
	netdev := m.netdevArg()
	drive := fmt.Sprintf("format=qcow2,file=%s,if=none,cache=%s,aio=%s,id=drive0", m.Config.VMImage, m.Config.DiskCache, m.Config.DiskAIO)
	blk := fmt.Sprintf("virtio-blk-pci,drive=drive0,iothread=io0,num-queues=%d,queue-size=1024,write-cache=on", m.Config.CPUs)
	args := []string{
		"-m", strconv.Itoa(m.Config.MemoryMiB) + "M",
		"-smp", strconv.Itoa(m.Config.CPUs),
		"-rtc", "base=utc,clock=host,driftfix=none",
		"-monitor", m.monitorArg(),
		"-device", "virtio-net-pci,netdev=mynet0",
		"-netdev", netdev,
		"-display", "none",
		"-drive", drive,
		"-object", "iothread,id=io0",
		"-device", blk,
		"-cpu", "max",
		"-accel", m.resolvedAccel(),
		"-pidfile", m.Config.QEMUPIDFile,
	}
	if sharePath := m.hostSharePath(share); sharePath != "" {
		args = append(args, "-virtfs", fmt.Sprintf("local,path=%s,mount_tag=host0,security_model=none,id=host0", sharePath))
	}
	if m.Config.PodmanDiskImage != "" {
		podmanDrive := fmt.Sprintf("format=qcow2,file=%s,if=none,cache=%s,aio=%s,id=drive1", m.Config.PodmanDiskImage, m.Config.DiskCache, m.Config.DiskAIO)
		podmanBlk := fmt.Sprintf("virtio-blk-pci,drive=drive1,iothread=io0,num-queues=%d,queue-size=1024,write-cache=on", m.Config.CPUs)
		args = append(args,
			"-drive", podmanDrive,
			"-device", podmanBlk,
		)
	}
	if _, err := os.Stat(m.Config.KernelImage); err == nil {
		args = append(args,
			"-kernel", m.Config.KernelImage,
			"-append", m.kernelAppend(share),
		)
	}
	if _, err := os.Stat(m.Config.InitrdImage); err == nil {
		args = append(args, "-initrd", m.Config.InitrdImage)
	}
	if daemonize {
		args = append(args, "-daemonize")
	} else {
		args = append(args, "-nographic")
	}
	return args
}

func (m Manager) hostSharePath(share string) string {
	if share != "9p" {
		return ""
	}
	if m.Config.WorkDir != "" {
		return m.Config.WorkDir
	}
	return m.Config.Home
}

func (m Manager) kernelAppend(share string) string {
	values := []string{"root=/dev/vda", "rootfstype=ext4", "rw", "console=ttyS0", "modules=virtio_blk,virtio_net,ext4,fuse", "quiet"}
	if sharePath := m.hostSharePath(share); sharePath != "" {
		values = append(values, "executor.host_target="+sharePath)
	}
	return strings.Join(values, " ")
}

func (m Manager) prepareSockets() error {
	if m.Config.SSHSocket == "" {
		return fmt.Errorf("ssh socket path must be set; SSH over TCP fallback is not supported")
	}
	if m.Config.MonitorSocket == "" {
		return fmt.Errorf("monitor socket path must be set; QEMU monitor over TCP fallback is not supported")
	}
	for _, path := range []string{m.Config.SSHSocket, m.Config.MonitorSocket} {
		if path == "" {
			continue
		}
		if path == m.Config.SSHSocket && strings.Contains(path, "-") {
			return fmt.Errorf("ssh socket path cannot contain '-' because QEMU hostfwd=unix uses '-' as a separator: %s", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err == nil {
			if info.Mode()&os.ModeSocket == 0 {
				return fmt.Errorf("%s exists and is not a socket", path)
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (m Manager) netdevArg() string {
	return fmt.Sprintf("user,id=mynet0,hostfwd=unix:%s-:22", m.Config.SSHSocket)
}

func (m Manager) monitorArg() string {
	return fmt.Sprintf("unix:%s,server,nowait", m.Config.MonitorSocket)
}

func (m Manager) readQEMUPID() (string, error) {
	content, err := os.ReadFile(m.Config.QEMUPIDFile)
	if err != nil {
		return "", err
	}
	pid := strings.TrimSpace(string(content))
	if pid == "" {
		return "", fmt.Errorf("QEMU pidfile is empty: %s", m.Config.QEMUPIDFile)
	}
	if _, err := strconv.Atoi(pid); err != nil {
		return "", fmt.Errorf("QEMU pidfile contains invalid PID %q: %w", pid, err)
	}
	return pid, nil
}

func (m Manager) validateQEMUProcess(ctx context.Context, pid string) error {
	output, err := m.Runner.Output(ctx, "ps", "-p", pid, "-o", "args=")
	if err != nil {
		return fmt.Errorf("QEMU pid %s is not running: %w", pid, err)
	}
	command := strings.TrimSpace(string(output))
	want := filepath.Base(m.Config.QEMUBinary)
	if !strings.Contains(command, want) {
		return fmt.Errorf("pid %s is %q, not a %q process", pid, command, want)
	}
	if m.Config.QEMUPIDFile != "" && !strings.Contains(command, m.Config.QEMUPIDFile) {
		return fmt.Errorf("pid %s is %q, not the configured QEMU runtime", pid, command)
	}
	return nil
}

func (m Manager) probeUnixHostForward(ctx context.Context) error {
	probeDir, err := os.MkdirTemp("", "executorqemuprobe")
	if err != nil {
		return err
	}
	defer os.RemoveAll(probeDir)

	socketPath := filepath.Join(probeDir, "ssh.sock")
	pidPath := filepath.Join(probeDir, "qemu.pid")
	args := qemuUnixHostForwardProbeArgs(socketPath, pidPath)
	if _, err := m.Runner.Output(ctx, m.Config.QEMUBinary, args...); err != nil {
		return qemuUnixHostForwardProbeError(err)
	}

	pid, err := os.ReadFile(pidPath)
	if err != nil {
		return fmt.Errorf("QEMU Unix socket host forwarding probe did not write pidfile: %w", err)
	}
	value := strings.TrimSpace(string(pid))
	if value == "" {
		return fmt.Errorf("QEMU Unix socket host forwarding probe wrote an empty pidfile")
	}
	_ = m.Runner.Run(context.Background(), "kill", value)
	return nil
}

func qemuUnixHostForwardProbeArgs(socketPath string, pidPath string) []string {
	return []string{
		"-nodefaults",
		"-display", "none",
		"-S",
		"-netdev", fmt.Sprintf("user,id=executorprobe,hostfwd=unix:%s-:22", socketPath),
		"-daemonize",
		"-pidfile", pidPath,
	}
}

func qemuUnixHostForwardProbeError(err error) error {
	message := err.Error()
	if strings.Contains(message, "Bad protocol name") || strings.Contains(message, "Invalid host forwarding rule") {
		return fmt.Errorf("QEMU does not support Unix socket host forwarding; use an image with QEMU/libslirp support for hostfwd=unix: %w", err)
	}
	return fmt.Errorf("failed to probe QEMU Unix socket host forwarding: %w", err)
}

// resolvedAccel chooses the QEMU accelerator to use.
func (m Manager) resolvedAccel() string {
	if m.Config.QEMUAccel != "auto" {
		return m.Config.QEMUAccel
	}
	if _, err := os.Stat("/dev/kvm"); err == nil {
		return "kvm"
	}
	return "tcg,thread=multi"
}
