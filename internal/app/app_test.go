package app

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"executor/internal/config"
	"executor/internal/container"
	"executor/internal/vm"
)

// TestContainerWantsTTY verifies TTY detection for container-style arguments.
func TestContainerWantsTTY(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "detached run does not need tty", args: []string{"run", "-d", "nginx"}, want: false},
		{name: "interactive run with short flags needs tty", args: []string{"run", "-it", "alpine", "sh"}, want: true},
		{name: "exec with long tty flag needs tty", args: []string{"exec", "--tty", "container", "sh"}, want: true},
		{name: "build tag is not a run tty", args: []string{"build", "-t", "image", "."}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := container.WantsTTY(tt.args); got != tt.want {
				t.Fatalf("container.WantsTTY(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

// TestContainerDetaches verifies detach detection for run and compose commands.
func TestContainerDetaches(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "run detached short flag", args: []string{"run", "-d", "nginx"}, want: true},
		{name: "run detached combined short flags", args: []string{"run", "-itd", "alpine", "sh"}, want: true},
		{name: "run foreground", args: []string{"run", "nginx"}, want: false},
		{name: "compose up detached", args: []string{"compose", "up", "-d"}, want: true},
		{name: "up detached", args: []string{"up", "-d"}, want: true},
		{name: "explicit detach false", args: []string{"run", "--detach=false", "nginx"}, want: false},
		{name: "build tag is not detach", args: []string{"build", "-d", "."}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := container.Detaches(tt.args); got != tt.want {
				t.Fatalf("container.Detaches(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

// TestRewriteRunPublishArgsStopsAtImageCommand verifies publish parsing stops at the image.
func TestRewriteRunPublishArgsStopsAtImageCommand(t *testing.T) {
	nextPort := 10037
	allocate := func() (int, error) { return nextPort, nil }

	gotArgs, mappings, err := container.RewriteRunPublishArgs(
		[]string{"run", "-d", "--name", "executor-http", "-p", "18080:80", "busybox:latest", "httpd", "-f", "-p", "80"},
		allocate,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"run", "-d", "--name", "executor-http", "-p", "18080:80", "busybox:latest", "httpd", "-f", "-p", "80"}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("rewritten args length = %d, want %d: %v", len(gotArgs), len(wantArgs), gotArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Fatalf("rewritten args[%d] = %q, want %q: %v", i, gotArgs[i], wantArgs[i], gotArgs)
		}
	}
	if len(mappings) != 1 {
		t.Fatalf("mappings = %v, want one mapping", mappings)
	}
	if mappings[0].HostPort != 18080 || mappings[0].ContainerPort != 80 {
		t.Fatalf("mapping = %+v, want 18080:80", mappings[0])
	}
}

// TestRewriteRunPublishArgsAllocatesContainerOnlyPublish verifies host port allocation.
func TestRewriteRunPublishArgsAllocatesContainerOnlyPublish(t *testing.T) {
	allocate := func() (int, error) { return 10037, nil }

	gotArgs, mappings, err := container.RewriteRunPublishArgs([]string{"run", "-p", "80", "nginx"}, allocate)
	if err != nil {
		t.Fatal(err)
	}
	if gotArgs[2] != "10037:80" {
		t.Fatalf("rewritten publish = %q, want 10037:80", gotArgs[2])
	}
	if len(mappings) != 1 || mappings[0].HostPort != 10037 || mappings[0].ContainerPort != 80 {
		t.Fatalf("mappings = %+v, want 10037:80", mappings)
	}
}

// TestDetachedRunCommandUsesCreateAndStart verifies detached run command rewriting.
func TestDetachedRunCommandUsesCreateAndStart(t *testing.T) {
	got, ok := container.DetachedRunCommandWithPrefix([]string{"podman"}, []string{"run", "-d", "--name", "web", "-p", "18080:80", "nginx"})
	if !ok {
		t.Fatal("container.DetachedRunCommand() did not match detached run")
	}
	for _, fragment := range []string{
		"id=$('podman' 'create' '--name' 'web' '-p' '18080:80' 'nginx')",
		"'podman' 'start' \"$id\" >/dev/null",
		"printf '%s\\n' \"$id\"",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("command %q does not contain %q", got, fragment)
		}
	}
	if strings.Contains(got, "'-d'") {
		t.Fatalf("command %q should not pass -d to podman create", got)
	}
}

// TestShutdownStopsPodmanBeforeQEMU verifies Podman is stopped before QEMU exits.
func TestShutdownStopsPodmanBeforeQEMU(t *testing.T) {
	dir := t.TempDir()
	pidfile := dir + "/qemu.pid"
	if err := os.WriteFile(pidfile, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{
		outputs: map[string]scriptedOutput{
			commandKey("ps", "-p", "123", "-o", "args="): {
				output: []byte("qemu-system-x86_64 -pidfile " + pidfile + "\n"),
			},
		},
	}
	app := App{
		Config: config.Config{
			Home:        "/home/coder",
			QEMUBinary:  "qemu-system-x86_64",
			QEMUPIDFile: pidfile,
			SSHSocket:   "/tmp/executorssh.sock",
			SSHUser:     "coder",
		},
		Runner: runner,
		Out:    io.Discard,
		Err:    io.Discard,
		In:     strings.NewReader(""),
	}

	if err := app.Shutdown(context.Background(), ShutdownOptions{}); err != nil {
		t.Fatal(err)
	}
	stopIndex := recordedRunIndexContaining(runner.runs, "podman stop --all --ignore")
	if stopIndex == -1 {
		t.Fatalf("runs = %#v, want Podman stop command", runner.runs)
	}
	syncIndex := recordedRunIndexContaining(runner.runs, "sync")
	if syncIndex == -1 {
		t.Fatalf("runs = %#v, want sync command", runner.runs)
	}
	killIndex := recordedRunIndex(runner.runs, "kill", "123")
	if killIndex == -1 {
		t.Fatalf("runs = %#v, want QEMU kill command", runner.runs)
	}
	if stopIndex > syncIndex || syncIndex > killIndex {
		t.Fatalf("Podman stop/sync ran after QEMU kill: runs = %#v", runner.runs)
	}
}

// TestBootReportsMissingAssets verifies boot fails before QEMU when assets are missing.
func TestBootReportsMissingAssets(t *testing.T) {
	dir := t.TempDir()
	downloadCalls := 0
	withDownloadVMAssets(t, func(_ context.Context, _ vm.AssetPaths, _ io.Writer) error {
		downloadCalls++
		return nil
	})
	app := App{
		Config: config.Config{
			VMImage:     dir + "/alpine-container.qcow2",
			KernelImage: dir + "/vmlinuz-virt",
			InitrdImage: dir + "/initramfs-virt",
			SSHKeyPath:  dir + "/id_ed25519",
		},
		Runner: &recordingRunner{},
		Out:    io.Discard,
		Err:    io.Discard,
		In:     strings.NewReader(""),
	}

	err := app.Boot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "generate and mount them before boot") {
		t.Fatalf("Boot() error = %v, want asset guidance", err)
	}
	if downloadCalls != 0 {
		t.Fatalf("Boot() download calls = %d, want 0", downloadCalls)
	}
}

// TestInitDownloadFailureDoesNotStartQEMU verifies failed asset downloads stop init before QEMU.
func TestInitDownloadFailureDoesNotStartQEMU(t *testing.T) {
	dir := t.TempDir()
	downloadErr := errors.New("download VM asset system.qcow2: boom")
	withDownloadVMAssets(t, func(_ context.Context, _ vm.AssetPaths, _ io.Writer) error {
		return downloadErr
	})
	runner := &recordingRunner{}
	cfg := config.Config{
		VMImage:     filepath.Join(dir, "system.qcow2"),
		KernelImage: filepath.Join(dir, "vmlinuz-virt"),
		InitrdImage: filepath.Join(dir, "initramfs-virt"),
		SSHKeyPath:  filepath.Join(dir, "id_ed25519"),
	}
	app := App{
		Config: cfg,
		Runner: runner,
		Out:    io.Discard,
		Err:    io.Discard,
		In:     strings.NewReader(""),
	}

	err := app.init(context.Background(), vm.NewManager(cfg, runner), vm.Credentials{})
	if !errors.Is(err, downloadErr) {
		t.Fatalf("init() error = %v, want download error", err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runs = %#v, want no QEMU commands after download failure", runner.runs)
	}
}

// TestInitSkipsDownloadWhenAssetsExist verifies local generated assets are reused.
func TestInitSkipsDownloadWhenAssetsExist(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		VMImage:       filepath.Join(dir, "system.qcow2"),
		KernelImage:   filepath.Join(dir, "vmlinuz-virt"),
		InitrdImage:   filepath.Join(dir, "initramfs-virt"),
		SSHKeyPath:    filepath.Join(dir, "id_ed25519"),
		QEMUBinary:    "qemu-system-x86_64",
		QEMUPIDFile:   filepath.Join(dir, "qemu.pid"),
		SSHSocket:     filepath.Join(dir, "executorssh.sock"),
		MonitorSocket: filepath.Join(dir, "monitorssh.sock"),
	}
	if err := writeTestAssets(vm.AssetPaths{
		Image:  cfg.VMImage,
		Kernel: cfg.KernelImage,
		Initrd: cfg.InitrdImage,
		SSHKey: cfg.SSHKeyPath,
	}); err != nil {
		t.Fatal(err)
	}
	downloadCalls := 0
	withDownloadVMAssets(t, func(_ context.Context, _ vm.AssetPaths, _ io.Writer) error {
		downloadCalls++
		return errors.New("download should not run")
	})
	runner := &recordingRunner{}
	app := App{
		Config: cfg,
		Runner: runner,
		Out:    io.Discard,
		Err:    io.Discard,
		In:     strings.NewReader(""),
	}

	err := app.init(context.Background(), vm.NewManager(cfg, runner), vm.Credentials{})
	if err == nil || !strings.Contains(err.Error(), "QEMU Unix socket host forwarding probe did not write pidfile") {
		t.Fatalf("init() error = %v, want QEMU probe error after asset reuse", err)
	}
	if downloadCalls != 0 {
		t.Fatalf("download calls = %d, want 0", downloadCalls)
	}
}

// TestResetDownloadsAssetsAndRemovesPodmanDisk verifies reset refreshes assets through init.
func TestResetDownloadsAssetsAndRemovesPodmanDisk(t *testing.T) {
	dir := t.TempDir()
	vmImage := dir + "/alpine-container.qcow2"
	podmanDisk := dir + "/data.qcow2"
	pidfile := dir + "/missing-qemu.pid"
	for _, path := range []string{vmImage, podmanDisk} {
		if err := os.WriteFile(path, []byte("disk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	downloadCalls := 0
	withDownloadVMAssets(t, func(_ context.Context, paths vm.AssetPaths, _ io.Writer) error {
		downloadCalls++
		return writeTestAssets(paths)
	})
	runner := &recordingRunner{}
	cfg := config.Config{
		VMImage:         vmImage,
		PodmanDiskImage: podmanDisk,
		PodmanDiskSize:  "10G",
		KernelImage:     dir + "/vmlinuz-virt",
		InitrdImage:     dir + "/initramfs-virt",
		SSHKeyPath:      dir + "/id_ed25519",
		QEMUPIDFile:     pidfile,
	}
	app := App{
		Config: cfg,
		Runner: runner,
		Out:    io.Discard,
		Err:    io.Discard,
		In:     strings.NewReader(""),
	}

	err := app.reset(context.Background(), vm.NewManager(cfg, runner), vm.Credentials{}, ResetOptions{Force: true})
	if err == nil || !strings.Contains(err.Error(), "ssh socket path must be set") {
		t.Fatalf("reset() error = %v, want QEMU start validation after asset download", err)
	}
	if downloadCalls != 1 {
		t.Fatalf("download calls = %d, want 1", downloadCalls)
	}
	if _, err := os.Stat(vmImage); err != nil {
		t.Fatalf("VM image was removed or stat failed: %v", err)
	}
	if _, err := os.Stat(podmanDisk); !os.IsNotExist(err) {
		t.Fatalf("Podman disk still exists or stat failed: %v", err)
	}
}

// TestUsagePrintsQEMUUsage verifies the usage command displays CPU and memory usage.
func TestUsagePrintsQEMUUsage(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "qemu.pid")
	if err := os.WriteFile(pidfile, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{
		outputs: map[string]scriptedOutput{
			commandKey("ps", "-p", "123", "-o", "args="): {
				output: []byte("qemu-system-x86_64 -pidfile " + pidfile + " -m 2048\n"),
			},
			commandKey("ps", "-p", "123", "-o", "pid=", "-o", "pcpu=", "-o", "pmem=", "-o", "rss=", "-o", "vsz=", "-o", "comm="): {
				output: []byte("123 12.5 6.3 524288 1048576 qemu-system-x86_64\n"),
			},
		},
	}
	var out strings.Builder
	app := App{
		Config: config.Config{QEMUBinary: "qemu-system-x86_64", QEMUPIDFile: pidfile},
		Runner: runner,
		Out:    &out,
		Err:    io.Discard,
		In:     strings.NewReader(""),
	}

	if err := app.Usage(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"QEMU usage:",
		"PID",
		"CPU%",
		"MEM%",
		"123",
		"12.5",
		"6.3",
		"512.0",
		"1024.0",
		"qemu-system-x86_64",
	} {
		if !strings.Contains(out.String(), fragment) {
			t.Fatalf("usage output %q does not contain %q", out.String(), fragment)
		}
	}
}

// TestUsageErrorsWhenQEMUIsStopped verifies usage reports a missing QEMU process.
func TestUsageErrorsWhenQEMUIsStopped(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "qemu.pid")
	if err := os.WriteFile(pidfile, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{
		outputs: map[string]scriptedOutput{
			commandKey("ps", "-p", "123", "-o", "args="): {
				err: errors.New("ps failed"),
			},
		},
	}
	app := App{
		Config: config.Config{QEMUBinary: "qemu-system-x86_64", QEMUPIDFile: pidfile},
		Runner: runner,
		Out:    io.Discard,
		Err:    io.Discard,
		In:     strings.NewReader(""),
	}

	err := app.Usage(context.Background())
	if err == nil || !strings.Contains(err.Error(), "QEMU is not running") {
		t.Fatalf("usage error = %v, want QEMU is not running", err)
	}
}

// TestUsageRejectsUnrelatedConfiguredPID verifies usage does not pick another QEMU.
func TestUsageRejectsUnrelatedConfiguredPID(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "qemu.pid")
	if err := os.WriteFile(pidfile, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{
		outputs: map[string]scriptedOutput{
			commandKey("ps", "-p", "123", "-o", "args="): {
				output: []byte("sleep 999\n"),
			},
		},
	}
	app := App{
		Config: config.Config{QEMUBinary: "qemu-system-x86_64", QEMUPIDFile: pidfile},
		Runner: runner,
		Out:    io.Discard,
		Err:    io.Discard,
		In:     strings.NewReader(""),
	}

	err := app.Usage(context.Background())
	if err == nil || !strings.Contains(err.Error(), "QEMU is not running") {
		t.Fatalf("usage error = %v, want QEMU is not running", err)
	}
	if len(runner.outputCalls) != 1 {
		t.Fatalf("output calls = %#v, want only pidfile ps validation", runner.outputCalls)
	}
}

// TestProxyUsesCoderHomeWhenHostShareDisabled verifies proxy commands avoid disabled shares.
func TestProxyUsesCoderHomeWhenHostShareDisabled(t *testing.T) {
	runner := &recordingRunner{}
	app := App{
		Config: config.Config{
			HostShare: "none",
			WorkDir:   "/home/coder",
		},
	}
	ssh := vm.SSHClient{
		SocketPath: "/tmp/executor.sock",
		User:       "coder",
		Runner:     runner,
	}

	if err := app.proxy(context.Background(), ssh, []string{"ps"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.runs) != 1 {
		t.Fatalf("proxy runs = %#v, want one SSH command", runner.runs)
	}
	args := runner.runs[0].args
	if len(args) == 0 {
		t.Fatal("proxy SSH args are empty")
	}
	command := args[len(args)-1]
	want := "cd '/home/coder' && 'env' 'XDG_RUNTIME_DIR=/run/user/1000' 'REGISTRY_AUTH_FILE=/home/coder/.config/containers/auth.json' 'TMPDIR=/run/user/1000' 'podman' 'ps'"
	if command != want {
		t.Fatalf("proxy command = %q, want %q", command, want)
	}
}

// TestOpenForwardRejectsUDP verifies unsupported UDP mappings fail clearly.
func TestOpenForwardRejectsUDP(t *testing.T) {
	dir := t.TempDir()
	runner := &recordingRunner{}
	app := App{
		Config: config.Config{SSHSocket: filepath.Join(dir, "executorssh.sock")},
		Runner: runner,
		Out:    io.Discard,
	}
	manager := vm.NewManager(app.Config, runner)

	err := app.openForward(context.Background(), manager, container.Mapping{HostPort: 5353, Protocol: "udp"})
	if err == nil || !strings.Contains(err.Error(), "SSH local forwarding supports TCP only") {
		t.Fatalf("openForward() error = %v, want UDP rejection", err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runs = %#v, want no SSH command", runner.runs)
	}
}

// TestOpenForwardDoesNotKillUnownedOccupiedPort verifies arbitrary listeners are left alone.
func TestOpenForwardDoesNotKillUnownedOccupiedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	dir := t.TempDir()
	port := listener.Addr().(*net.TCPAddr).Port
	runner := &recordingRunner{}
	app := App{
		Config: config.Config{SSHSocket: filepath.Join(dir, "executorssh.sock")},
		Runner: runner,
		Out:    io.Discard,
	}
	manager := vm.NewManager(app.Config, runner)

	err = app.openForward(context.Background(), manager, container.Mapping{HostPort: port, Protocol: "tcp"})
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("openForward() error = %v, want occupied port error", err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runs = %#v, want no cleanup command for unowned port", runner.runs)
	}
}

// TestOpenForwardStopsOwnedForwardBeforeReusingPort verifies owned control sockets are used.
func TestOpenForwardStopsOwnedForwardBeforeReusingPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	dir := t.TempDir()
	port := listener.Addr().(*net.TCPAddr).Port
	runner := &recordingRunner{}
	app := App{
		Config: config.Config{SSHSocket: filepath.Join(dir, "executorssh.sock"), SSHUser: "coder"},
		Runner: runner,
		Out:    io.Discard,
	}
	mapping := container.Mapping{HostPort: port, Protocol: "tcp"}
	controlPath := app.forwardControlPath(mapping)
	if err := os.WriteFile(controlPath, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := vm.NewManager(app.Config, runner)

	err = app.openForward(context.Background(), manager, mapping)
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("openForward() error = %v, want occupied port error after owned cleanup", err)
	}
	stopIndex := recordedRunIndexContaining(runner.runs, "-O")
	if stopIndex == -1 || recordedRunIndexContaining(runner.runs, "exit") == -1 {
		t.Fatalf("runs = %#v, want SSH control socket exit", runner.runs)
	}
	if recordedRunIndex(runner.runs, "pkill") != -1 {
		t.Fatalf("runs = %#v, should not run pkill", runner.runs)
	}
}

type recordedRun struct {
	name string
	args []string
}

// recordedRunIndex returns the index of an exact recorded command match.
func recordedRunIndex(runs []recordedRun, name string, args ...string) int {
	for i, run := range runs {
		if run.name != name || len(run.args) != len(args) {
			continue
		}
		match := true
		for j := range args {
			if run.args[j] != args[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// recordedRunIndexContaining returns the first command containing a fragment.
func recordedRunIndexContaining(runs []recordedRun, fragment string) int {
	for i, run := range runs {
		if strings.Contains(run.name, fragment) {
			return i
		}
		for _, arg := range run.args {
			if strings.Contains(arg, fragment) {
				return i
			}
		}
	}
	return -1
}

type recordingRunner struct {
	runs []recordedRun
}

// Run records a local command invocation for assertions.
func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	r.runs = append(r.runs, recordedRun{name: name, args: append([]string(nil), args...)})
	return nil
}

// Output returns no output for tests that only inspect Run calls.
func (r *recordingRunner) Output(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return nil, nil
}

type scriptedOutput struct {
	output []byte
	err    error
}

type scriptedRunner struct {
	runs        []recordedRun
	outputCalls []recordedRun
	outputs     map[string]scriptedOutput
}

// Run records a local command invocation for assertions.
func (r *scriptedRunner) Run(_ context.Context, name string, args ...string) error {
	r.runs = append(r.runs, recordedRun{name: name, args: append([]string(nil), args...)})
	return nil
}

// Output returns scripted command output for assertions.
func (r *scriptedRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	r.outputCalls = append(r.outputCalls, recordedRun{name: name, args: append([]string(nil), args...)})
	output, ok := r.outputs[commandKey(name, args...)]
	if !ok {
		return nil, errors.New("unexpected command: " + commandKey(name, args...))
	}
	return output.output, output.err
}

// commandKey creates a stable map key for a command invocation.
func commandKey(name string, args ...string) string {
	values := append([]string{name}, args...)
	return strings.Join(values, "\x00")
}

func withDownloadVMAssets(t *testing.T, fn func(context.Context, vm.AssetPaths, io.Writer) error) {
	t.Helper()
	previous := downloadVMAssets
	downloadVMAssets = fn
	t.Cleanup(func() {
		downloadVMAssets = previous
	})
}

func writeTestAssets(paths vm.AssetPaths) error {
	publicKey := paths.SSHPublicKey
	if publicKey == "" {
		publicKey = filepath.Join(filepath.Dir(paths.SSHKey), "id_ed25519.pub")
	}
	for _, asset := range []struct {
		path string
		mode os.FileMode
	}{
		{path: paths.Image, mode: 0o644},
		{path: paths.Kernel, mode: 0o644},
		{path: paths.Initrd, mode: 0o644},
		{path: paths.SSHKey, mode: 0o600},
		{path: publicKey, mode: 0o644},
	} {
		if err := os.MkdirAll(filepath.Dir(asset.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(asset.path, []byte("downloaded"), asset.mode); err != nil {
			return err
		}
	}
	return nil
}
