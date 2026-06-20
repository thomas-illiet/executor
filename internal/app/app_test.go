package app

import (
	"context"
	"errors"
	"io"
	"os"
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
	got, ok := container.DetachedRunCommand([]string{"run", "-d", "--name", "web", "-p", "18080:80", "nginx"})
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
			Home:        "/home/appuser",
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

// TestBootReportsMissingAssets verifies boot fails before QEMU when assets have not been downloaded.
func TestBootReportsMissingAssets(t *testing.T) {
	dir := t.TempDir()
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
	if err == nil || !strings.Contains(err.Error(), "run `executor download` first") {
		t.Fatalf("Boot() error = %v, want download guidance", err)
	}
}

func TestResetRemovesPodmanDiskImageAndKeepsVMImage(t *testing.T) {
	dir := t.TempDir()
	vmImage := dir + "/alpine-container.qcow2"
	podmanDisk := dir + "/podman-data.qcow2"
	pidfile := dir + "/missing-qemu.pid"
	for _, path := range []string{vmImage, podmanDisk} {
		if err := os.WriteFile(path, []byte("disk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runner := &recordingRunner{}
	cfg := config.Config{
		VMImage:         vmImage,
		PodmanDiskImage: podmanDisk,
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
	if err == nil || !strings.Contains(err.Error(), "run `executor download` first") {
		t.Fatalf("reset() error = %v, want missing asset guidance after disk removal", err)
	}
	if _, err := os.Stat(vmImage); err != nil {
		t.Fatalf("VM image was removed or stat failed: %v", err)
	}
	if _, err := os.Stat(podmanDisk); !os.IsNotExist(err) {
		t.Fatalf("Podman disk still exists or stat failed: %v", err)
	}
}

// TestFirstQEMUProcessSkipsDefunct verifies stale QEMU processes are ignored.
func TestFirstQEMUProcessSkipsDefunct(t *testing.T) {
	process, ok := firstQEMUProcess([]byte("42 [qemu-system-x86_64] <defunct>\n123 qemu-system-x86_64 -m 2048\n"))
	if !ok {
		t.Fatal("firstQEMUProcess() did not find running process")
	}
	if process.PID != "123" {
		t.Fatalf("process PID = %q, want 123", process.PID)
	}
	if process.Command != "qemu-system-x86_64 -m 2048" {
		t.Fatalf("process command = %q, want QEMU command", process.Command)
	}
}

// TestUsagePrintsQEMUUsage verifies the usage command displays CPU and memory usage.
func TestUsagePrintsQEMUUsage(t *testing.T) {
	runner := &scriptedRunner{
		outputs: map[string]scriptedOutput{
			commandKey("pgrep", "-af", "qemu-system-x86_64"): {
				output: []byte("123 qemu-system-x86_64 -m 2048\n"),
			},
			commandKey("ps", "-p", "123", "-o", "pid=", "-o", "pcpu=", "-o", "pmem=", "-o", "rss=", "-o", "vsz=", "-o", "comm="): {
				output: []byte("123 12.5 6.3 524288 1048576 qemu-system-x86_64\n"),
			},
		},
	}
	var out strings.Builder
	app := App{
		Config: config.Config{QEMUBinary: "qemu-system-x86_64"},
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
	runner := &scriptedRunner{
		outputs: map[string]scriptedOutput{
			commandKey("pgrep", "-af", "qemu-system-x86_64"): {
				err: errors.New("pgrep failed"),
			},
		},
	}
	app := App{
		Config: config.Config{QEMUBinary: "qemu-system-x86_64"},
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

func TestProxyUsesCoderHomeWhenHostShareDisabled(t *testing.T) {
	runner := &recordingRunner{}
	app := App{
		Config: config.Config{
			HostShare: "none",
			WorkDir:   "/home/appuser",
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

type recordedRun struct {
	name string
	args []string
}

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

func commandKey(name string, args ...string) string {
	values := append([]string{name}, args...)
	return strings.Join(values, "\x00")
}
