package vm

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// TestCommandInDirQuotesArguments verifies remote directory commands are quoted.
func TestCommandInDirQuotesArguments(t *testing.T) {
	got := CommandInDir("/workspace/my app", []string{"podman", "run", "hello world"})
	want := "cd '/workspace/my app' && 'podman' 'run' 'hello world'"
	if got != want {
		t.Fatalf("CommandInDir() = %q, want %q", got, want)
	}
}

// TestDetachedCommandInDirRunsInRemoteBackground verifies detached shell construction.
func TestDetachedCommandInDirRunsInRemoteBackground(t *testing.T) {
	got := DetachedCommandInDir("/tmp/executor-run", "/workspace/my app", []string{"podman", "run", "-d", "nginx"})
	for _, fragment := range []string{
		"run_dir='/tmp/executor-run'",
		"mkdir -p \"$run_dir\"",
		"trap '' HUP",
		"( cd '/workspace/my app' && 'podman' 'run' '-d' 'nginx' ) >\"$run_dir/out\" 2>\"$run_dir/err\"",
		"echo $? >\"$run_dir/status\"",
		"</dev/null >/dev/null 2>/dev/null &",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("command %q does not contain %q", got, fragment)
		}
	}
}

// TestFinishDetachedCommandReplaysOutputAndStatus verifies detached result cleanup.
func TestFinishDetachedCommandReplaysOutputAndStatus(t *testing.T) {
	got := FinishDetachedCommand("/tmp/executor-run", 7)
	want := "cat '/tmp/executor-run/out'; cat '/tmp/executor-run/err' >&2; rm -rf '/tmp/executor-run'; exit 7"
	if got != want {
		t.Fatalf("FinishDetachedCommand() = %q, want %q", got, want)
	}
}

// TestStartLocalForwardDetachesSSHStdio verifies SSH forwarding detaches stdio.
func TestStartLocalForwardDetachesSSHStdio(t *testing.T) {
	runner := &sshRecordingRunner{}
	client := SSHClient{
		SocketPath: "/tmp/executor.sock",
		User:       "coder",
		KeyPath:    "/tmp/key",
		Runner:     runner,
	}

	if err := client.StartLocalForward(context.Background(), "0.0.0.0", 8080, "127.0.0.1", 80, "/tmp/forward.sock"); err != nil {
		t.Fatal(err)
	}
	if runner.name != "sh" {
		t.Fatalf("command = %q, want sh", runner.name)
	}
	if len(runner.args) != 2 || runner.args[0] != "-c" {
		t.Fatalf("args = %#v, want sh -c", runner.args)
	}
	command := runner.args[1]
	for _, fragment := range []string{
		"'ssh'",
		"'-f'",
		"'-N'",
		"'-M'",
		"'-S' '/tmp/forward.sock'",
		"'-L' '0.0.0.0:8080:127.0.0.1:80'",
		"'coder@localhost'",
		"</dev/null >/dev/null 2>/dev/null",
	} {
		if !strings.Contains(command, fragment) {
			t.Fatalf("command %q does not contain %q", command, fragment)
		}
	}
}

// TestStopLocalForwardUsesControlSocket verifies only owned forwards are stopped.
func TestStopLocalForwardUsesControlSocket(t *testing.T) {
	runner := &sshRecordingRunner{}
	client := SSHClient{
		SocketPath: "/tmp/executor.sock",
		User:       "coder",
		KeyPath:    "/tmp/key",
		Runner:     runner,
	}

	if err := client.StopLocalForward(context.Background(), "/tmp/forward.sock"); err != nil {
		t.Fatal(err)
	}
	if runner.name != "ssh" {
		t.Fatalf("command = %q, want ssh", runner.name)
	}
	if !containsArgPair(runner.args, "-S", "/tmp/forward.sock") {
		t.Fatalf("args = %#v, want control socket", runner.args)
	}
	if !containsArgPair(runner.args, "-O", "exit") {
		t.Fatalf("args = %#v, want exit control command", runner.args)
	}
}

// TestRunNoTTYUsesUnixSocketProxyCommand verifies SSH can connect through a Unix socket.
func TestRunNoTTYUsesUnixSocketProxyCommand(t *testing.T) {
	runner := &sshRecordingRunner{}
	client := SSHClient{
		SocketPath: "/tmp/executor ssh.sock",
		User:       "coder",
		KeyPath:    "/tmp/key",
		Runner:     runner,
	}

	if err := client.RunNoTTY(context.Background(), "true"); err != nil {
		t.Fatal(err)
	}
	if runner.name != "ssh" {
		t.Fatalf("command = %q, want ssh", runner.name)
	}
	if slices.Contains(runner.args, "-p") {
		t.Fatalf("args = %#v, should not include TCP port in socket mode", runner.args)
	}
	if !containsArgPair(runner.args, "-o", "ProxyCommand=nc -U '/tmp/executor ssh.sock'") {
		t.Fatalf("args = %#v, want Unix socket ProxyCommand", runner.args)
	}
}

// TestShellWithEnvironmentExportsPodmanPaths verifies VM shells share proxy state.
func TestShellWithEnvironmentExportsPodmanPaths(t *testing.T) {
	runner := &sshRecordingRunner{}
	client := SSHClient{
		SocketPath: "/tmp/executor.sock",
		User:       "coder",
		KeyPath:    "/tmp/key",
		Runner:     runner,
	}
	environment := []string{
		"XDG_RUNTIME_DIR=/run/user/1000",
		"REGISTRY_AUTH_FILE=/home/coder/.config/containers/auth.json",
		"TMPDIR=/run/user/1000",
	}

	if err := client.ShellWithEnvironment(context.Background(), environment); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(runner.args, "-t") {
		t.Fatalf("args = %#v, want SSH TTY", runner.args)
	}
	if len(runner.args) < 2 || runner.args[len(runner.args)-2] != "coder@localhost" {
		t.Fatalf("args = %#v, want coder destination before command", runner.args)
	}
	command := runner.args[len(runner.args)-1]
	for _, fragment := range []string{
		"'XDG_RUNTIME_DIR=/run/user/1000'",
		"'REGISTRY_AUTH_FILE=/home/coder/.config/containers/auth.json'",
		"'TMPDIR=/run/user/1000'",
		"'/bin/sh' '-l'",
	} {
		if !strings.Contains(command, fragment) {
			t.Fatalf("command = %q, want %q", command, fragment)
		}
	}
}

type sshRecordingRunner struct {
	name string
	args []string
}

// Run records the command and arguments used by the SSH client.
func (r *sshRecordingRunner) Run(_ context.Context, name string, args ...string) error {
	r.name = name
	r.args = args
	return nil
}

// Output returns no output for tests that only inspect Run calls.
func (r *sshRecordingRunner) Output(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return nil, nil
}

// containsArgPair reports whether adjacent SSH arguments match a key and value.
func containsArgPair(args []string, key string, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}
