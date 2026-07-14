package vm

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"executor/internal/config"
)

// TestQEMUArgsUseQCOW2DiskAnd9PByDefault verifies default QEMU disk and share args.
func TestQEMUArgsUseQCOW2DiskAnd9PByDefault(t *testing.T) {
	manager := Manager{Config: testConfig()}

	args := manager.qemuArgs(false)

	drive, ok := argValue(args, "-drive")
	if !ok {
		t.Fatal("missing -drive")
	}
	wantDrive := "format=qcow2,file=/tmp/system.qcow2,if=none,cache=unsafe,aio=threads,id=drive0"
	if drive != wantDrive {
		t.Fatalf("-drive = %q, want %q", drive, wantDrive)
	}
	if !argValueContains(args, "-device", "virtio-blk-pci,drive=drive0,iothread=io0,num-queues=4,queue-size=1024,write-cache=on") {
		t.Fatalf("args %#v do not attach virtio-blk to io0", args)
	}
	if !argValueContains(args, "-drive", "format=qcow2,file=/tmp/data.qcow2,if=none,cache=unsafe,aio=threads,id=drive1") {
		t.Fatalf("args %#v do not attach dedicated Podman data disk", args)
	}
	if !argValueContains(args, "-device", "virtio-blk-pci,drive=drive1,iothread=io0,num-queues=4,queue-size=1024,write-cache=on") {
		t.Fatalf("args %#v do not attach Podman data disk to io0", args)
	}
	if !argValueContains(args, "-virtfs", "local,path=/workspace,mount_tag=host0,security_model=none,id=host0") {
		t.Fatalf("args %#v do not include 9p share", args)
	}
	appendArgs := manager.kernelAppend("9p")
	if !strings.Contains(appendArgs, "executor.host_target="+GuestWorkDir) {
		t.Fatalf("kernel append = %q, want fixed guest workspace mount", appendArgs)
	}
	if argValueContains(args, "-virtfs", "mount_tag=podmandata") {
		t.Fatalf("args %#v should not include a dedicated Podman data share", args)
	}
	if !argValueContains(args, "-netdev", "user,id=mynet0,hostfwd=unix:/tmp/executorssh.sock-:22") {
		t.Fatalf("args %#v do not include SSH unix socket hostfwd", args)
	}
	monitor, ok := argValue(args, "-monitor")
	if !ok {
		t.Fatal("missing -monitor")
	}
	if monitor != "unix:/tmp/executormonitor.sock,server,nowait" {
		t.Fatalf("-monitor = %q, want unix monitor socket", monitor)
	}
	chardev, ok := argValue(args, "-chardev")
	if !ok {
		t.Fatal("missing -chardev")
	}
	if chardev != "stdio,id=vmconsole,logfile=/tmp/console.log,logappend=off" {
		t.Fatalf("-chardev = %q, want foreground console logging", chardev)
	}
	serial, ok := argValue(args, "-serial")
	if !ok || serial != "chardev:vmconsole" {
		t.Fatalf("-serial = %q, %v, want VM console chardev", serial, ok)
	}
	pidfile, ok := argValue(args, "-pidfile")
	if !ok {
		t.Fatal("missing -pidfile")
	}
	if pidfile != "/tmp/executorqemu.pid" {
		t.Fatalf("-pidfile = %q, want configured pidfile", pidfile)
	}
	if argValueContains(args, "-device", "vhost-user-fs-pci") {
		t.Fatalf("args %#v should not include VirtioFS device", args)
	}
	if argValueContains(args, "-chardev", "charfs0") {
		t.Fatalf("args %#v should not include VirtioFS chardev", args)
	}
	if argValueContains(args, "-loadvm", "") {
		t.Fatalf("args %#v should not load a QEMU snapshot", args)
	}
}

// TestPrepareSocketsRejectsHyphenatedSSHSocket verifies QEMU hostfwd paths fail clearly.
func TestPrepareSocketsRejectsHyphenatedSSHSocket(t *testing.T) {
	manager := Manager{Config: config.Config{
		SSHSocket:     "/tmp/executor-ssh.sock",
		MonitorSocket: "/tmp/executormonitor.sock",
	}}

	err := manager.prepareSockets()

	if err == nil {
		t.Fatal("prepareSockets() error = nil, want hyphenated socket path error")
	}
	if !strings.Contains(err.Error(), "cannot contain '-'") {
		t.Fatalf("prepareSockets() error = %v, want hyphen explanation", err)
	}
}

// testConfig returns a minimal config for VM argument tests.
func testConfig() config.Config {
	return config.Config{
		Home:            "/workspace",
		VMImage:         "/tmp/system.qcow2",
		PodmanDiskImage: "/tmp/data.qcow2",
		QEMUBinary:      "qemu-system-x86_64",
		QEMUPIDFile:     "/tmp/executorqemu.pid",
		QEMUAccel:       "tcg,thread=multi",
		QEMUIOProfile:   "max",
		DiskCache:       "unsafe",
		DiskAIO:         "threads",
		HostShare:       "9p",
		SSHSocket:       "/tmp/executorssh.sock",
		MonitorSocket:   "/tmp/executormonitor.sock",
		ConsoleLog:      "/tmp/console.log",
		MemoryMiB:       4096,
		CPUs:            4,
	}
}

// TestQEMUArgsUseOutputOnlyConsoleWhenDaemonized verifies init cannot inject input.
func TestQEMUArgsUseOutputOnlyConsoleWhenDaemonized(t *testing.T) {
	manager := Manager{Config: testConfig()}

	args := manager.qemuArgs(true)

	chardev, ok := argValue(args, "-chardev")
	if !ok {
		t.Fatal("missing -chardev")
	}
	want := "file,id=vmconsole,path=/tmp/console.log"
	if chardev != want {
		t.Fatalf("-chardev = %q, want %q", chardev, want)
	}
	if strings.Contains(chardev, "input-path") {
		t.Fatalf("-chardev = %q, must not expose console input", chardev)
	}
	if !argValueContains(args, "-serial", "chardev:vmconsole") {
		t.Fatalf("args %#v do not connect ttyS0 to the console log", args)
	}
	if !containsArg(args, "-daemonize") {
		t.Fatalf("args %#v do not daemonize QEMU", args)
	}
}

// TestEnsurePodmanDiskCreatesMissingImage verifies missing Podman disks are created.
func TestEnsurePodmanDiskCreatesMissingImage(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "nested", "data.qcow2")
	runner := &recordingRunner{}
	manager := Manager{
		Config: config.Config{
			PodmanDiskImage: image,
			PodmanDiskSize:  "10G",
			QEMUImgBinary:   filepath.Join(dir, "bin", "qemu-img"),
		},
		Runner: runner,
	}

	if err := manager.ensurePodmanDisk(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := commandKey(filepath.Join(dir, "bin", "qemu-img"), "create", "-q", "-f", "qcow2", "-o", "preallocation=off", image, "10G")
	if len(runner.runs) != 1 || runner.runs[0] != want {
		t.Fatalf("runs = %#v, want %q", runner.runs, want)
	}
	if _, err := os.Stat(filepath.Dir(image)); err != nil {
		t.Fatalf("Podman disk directory was not created: %v", err)
	}
}

// TestEnsurePodmanDiskKeepsExistingImage verifies existing Podman disks are reused.
func TestEnsurePodmanDiskKeepsExistingImage(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "data.qcow2")
	if err := os.WriteFile(image, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	manager := Manager{
		Config: config.Config{
			PodmanDiskImage: image,
			PodmanDiskSize:  "10G",
		},
		Runner: runner,
	}

	if err := manager.ensurePodmanDisk(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runs = %#v, want existing Podman disk reused", runner.runs)
	}
}

// TestStartKeepsSocketsWhenConfiguredQEMUAlreadyRuns verifies Start is idempotent.
func TestStartKeepsSocketsWhenConfiguredQEMUAlreadyRuns(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "executorstart")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	pidfile := filepath.Join(dir, "qemu.pid")
	sshSocket := filepath.Join(dir, "ssh.sock")
	monitorSocket := filepath.Join(dir, "monitor.sock")
	consoleLog := filepath.Join(dir, "console.log")
	if err := os.WriteFile(pidfile, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(consoleLog, []byte("current session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sshListener, err := net.Listen("unix", sshSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer sshListener.Close()
	monitorListener, err := net.Listen("unix", monitorSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer monitorListener.Close()

	runner := &recordingRunner{
		outputs: map[string][]byte{
			commandKey("ps", "-p", "123", "-o", "args="): []byte("qemu-system-x86_64 -pidfile " + pidfile + "\n"),
		},
	}
	manager := Manager{
		Config: config.Config{
			VMImage:       filepath.Join(dir, "system.qcow2"),
			QEMUBinary:    "qemu-system-x86_64",
			QEMUPIDFile:   pidfile,
			SSHSocket:     sshSocket,
			MonitorSocket: monitorSocket,
			ConsoleLog:    consoleLog,
		},
		Runner: runner,
	}

	if err := manager.Start(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runs = %#v, want no QEMU restart", runner.runs)
	}
	for _, path := range []string{sshSocket, monitorSocket} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("%s was removed: %v", path, err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			t.Fatalf("%s mode = %v, want socket", path, info.Mode())
		}
	}
	content, err := os.ReadFile(consoleLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "current session\n" {
		t.Fatalf("console log = %q, want existing running session preserved", got)
	}
}

// TestStopKillsOnlyConfiguredPID verifies QEMU shutdown targets the pidfile.
func TestStopKillsOnlyConfiguredPID(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "qemu.pid")
	if err := os.WriteFile(pidfile, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{
		outputs: map[string][]byte{
			commandKey("ps", "-p", "123", "-o", "args="): []byte("qemu-system-x86_64 -pidfile " + pidfile + "\n"),
		},
	}
	manager := Manager{
		Config: config.Config{QEMUBinary: "qemu-system-x86_64", QEMUPIDFile: pidfile},
		Runner: runner,
	}

	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.runs) != 1 || runner.runs[0] != commandKey("kill", "123") {
		t.Fatalf("runs = %#v, want targeted kill", runner.runs)
	}
	if _, err := os.Stat(pidfile); !os.IsNotExist(err) {
		t.Fatalf("pidfile still exists or stat failed: %v", err)
	}
}

// TestStopUsesMonitorPowerdownBeforeKill verifies graceful shutdown wins over kill.
func TestStopUsesMonitorPowerdownBeforeKill(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "executor-qemu-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	pidfile := filepath.Join(dir, "qemu.pid")
	monitorSocket := filepath.Join(dir, "monitor.sock")
	if err := os.WriteFile(pidfile, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", monitorSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	monitorCommand := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadString('\n')
		monitorCommand <- strings.TrimSpace(line)
		_, _ = conn.Write([]byte("(qemu) "))
	}()

	psKey := commandKey("ps", "-p", "123", "-o", "args=")
	runner := &recordingRunner{
		outputs: map[string][]byte{
			psKey: []byte("qemu-system-x86_64 -pidfile " + pidfile + "\n"),
		},
		outputErrorAfter: map[string]int{
			psKey: 2,
		},
	}
	manager := Manager{
		Config: config.Config{
			QEMUBinary:    "qemu-system-x86_64",
			QEMUPIDFile:   pidfile,
			MonitorSocket: monitorSocket,
		},
		Runner: runner,
		Monitor: MonitorClient{
			SocketPath: monitorSocket,
			Timeout:    2 * time.Second,
		},
	}

	if err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := <-monitorCommand; got != "system_powerdown" {
		t.Fatalf("monitor command = %q, want system_powerdown", got)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runs = %#v, want no kill after graceful powerdown", runner.runs)
	}
	if _, err := os.Stat(pidfile); !os.IsNotExist(err) {
		t.Fatalf("pidfile still exists or stat failed: %v", err)
	}
}

// TestStopRejectsPIDForAnotherProcess verifies shutdown does not kill unrelated PIDs.
func TestStopRejectsPIDForAnotherProcess(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "qemu.pid")
	if err := os.WriteFile(pidfile, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{
		outputs: map[string][]byte{
			commandKey("ps", "-p", "123", "-o", "args="): []byte("sleep 999\n"),
		},
	}
	manager := Manager{
		Config: config.Config{QEMUBinary: "qemu-system-x86_64", QEMUPIDFile: pidfile},
		Runner: runner,
	}

	err := manager.Stop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not a") {
		t.Fatalf("Stop() error = %v, want unrelated PID rejection", err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runs = %#v, want no kill", runner.runs)
	}
}

type recordingRunner struct {
	runs             []string
	outputs          map[string][]byte
	outputCounts     map[string]int
	outputErrorAfter map[string]int
}

// Run records a command invocation for assertions.
func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	r.runs = append(r.runs, commandKey(name, args...))
	return nil
}

// Output returns scripted command output and can simulate process exit.
func (r *recordingRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	key := commandKey(name, args...)
	if r.outputCounts == nil {
		r.outputCounts = map[string]int{}
	}
	r.outputCounts[key]++
	if after := r.outputErrorAfter[key]; after > 0 && r.outputCounts[key] >= after {
		return nil, errors.New("process exited")
	}
	return r.outputs[key], nil
}

// commandKey creates a stable map key for a command invocation.
func commandKey(name string, args ...string) string {
	values := append([]string{name}, args...)
	return strings.Join(values, "\x00")
}

// TestQEMUUnixHostForwardProbeClassifiesUnsupportedQEMU verifies the clear error.
func TestQEMUUnixHostForwardProbeClassifiesUnsupportedQEMU(t *testing.T) {
	err := qemuUnixHostForwardProbeError(fakeError("qemu-system-x86_64: -netdev user,id=executorprobe,hostfwd=unix:/tmp/executorprobe.sock-:22: Invalid host forwarding rule 'unix:/tmp/executorprobe.sock-:22' (Bad protocol name)"))

	if err == nil {
		t.Fatal("qemuUnixHostForwardProbeError() = nil, want error")
	}
	if !strings.Contains(err.Error(), "QEMU does not support Unix socket host forwarding") {
		t.Fatalf("qemuUnixHostForwardProbeError() = %v, want unsupported QEMU message", err)
	}
}

type fakeError string

// Error returns the fake QEMU error text.
func (e fakeError) Error() string {
	return string(e)
}

// argValue returns the value that follows a flag.
func argValue(args []string, flag string) (string, bool) {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1], true
		}
	}
	return "", false
}

// argValueContains reports whether a flag value contains a fragment.
func argValueContains(args []string, flag string, fragment string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && strings.Contains(args[i+1], fragment) {
			return true
		}
	}
	return false
}

// containsArg reports whether an exact argument is present.
func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
