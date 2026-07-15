package vm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"executor/internal/config"
)

// TestConfigurePodmanWritesRootlessConfig verifies rootless Podman files and auth.
func TestConfigurePodmanWritesRootlessConfig(t *testing.T) {
	runner := &recordingRunner{}
	manager := Manager{
		Config: config.Config{
			PodmanDataDir:        "/home/coder/.local/share/containers",
			PodmanStorageDriver:  "overlay",
			PodmanRegistryMirror: "https://mirror.example.invalid/",
			SSHSocket:            "/tmp/executorssh.sock",
			SSHUser:              "coder",
		},
		SSH: SSHClient{
			SocketPath: "/tmp/executorssh.sock",
			User:       "coder",
			Runner:     runner,
		},
	}

	if err := manager.ConfigurePodman(context.Background()); err != nil {
		t.Fatal(err)
	}

	mkdir := runContaining(runner.runs, "mkdir -p")
	for _, fragment := range []string{
		"'/home/coder/.config/containers'",
		"'/home/coder/.local/share/containers'",
	} {
		if !strings.Contains(mkdir, fragment) {
			t.Fatalf("mkdir command %q does not contain %q", mkdir, fragment)
		}
	}

	storageWrite := runContaining(runner.runs, "/home/coder/.config/containers/storage.conf")
	for _, fragment := range []string{
		`driver = "overlay"`,
		`graphroot = "/home/coder/.local/share/containers"`,
		`runroot = "/run/user/1000/containers"`,
		`mount_program = "/usr/bin/fuse-overlayfs"`,
	} {
		if !strings.Contains(storageWrite, fragment) {
			t.Fatalf("storage write %q does not contain %q", storageWrite, fragment)
		}
	}

	containersWrite := runContaining(runner.runs, "/home/coder/.config/containers/containers.conf")
	for _, fragment := range []string{
		`compose_providers = ["/usr/bin/podman-compose"]`,
		`compose_warning_logs = false`,
		`runtime = "crun"`,
		`default_rootless_network_cmd = "slirp4netns"`,
	} {
		if !strings.Contains(containersWrite, fragment) {
			t.Fatalf("containers write %q does not contain %q", containersWrite, fragment)
		}
	}

	registriesWrite := runContaining(runner.runs, "/home/coder/.config/containers/registries.conf")
	for _, fragment := range []string{
		`unqualified-search-registries = ["docker.io"]`,
		`prefix = "docker.io"`,
		`location = "mirror.example.invalid"`,
	} {
		if !strings.Contains(registriesWrite, fragment) {
			t.Fatalf("registries write %q does not contain %q", registriesWrite, fragment)
		}
	}

	if authWrite := runContaining(runner.runs, "auth.json.executor.tmp"); authWrite != "" {
		t.Fatalf("ConfigurePodman unexpectedly managed registry auth: %q", authWrite)
	}

	waitPodman := runContaining(runner.runs, "podman info")
	for _, fragment := range []string{
		"XDG_RUNTIME_DIR='/run/user/1000'",
		"REGISTRY_AUTH_FILE='/home/coder/.config/containers/auth.json'",
		"TMPDIR='/run/user/1000'",
		"podman info",
	} {
		if !strings.Contains(waitPodman, fragment) {
			t.Fatalf("wait command %q does not contain %q", waitPodman, fragment)
		}
	}
	if runContaining(runner.runs, "rc-service docker") != "" {
		t.Fatalf("runs %#v should not start Docker", runner.runs)
	}
}

// TestWaitForSSHReturnsAuthenticationFailureImmediately verifies auth errors fail fast.
func TestWaitForSSHReturnsAuthenticationFailureImmediately(t *testing.T) {
	runner := &authFailureRunner{}
	manager := Manager{
		SSH: SSHClient{
			SocketPath: "/tmp/executorssh.sock",
			User:       "coder",
			KeyPath:    "/tmp/id_ed25519",
			Runner:     runner,
		},
	}

	err := manager.WaitForSSH(context.Background(), time.Minute)

	if err == nil {
		t.Fatal("WaitForSSH() error = nil, want authentication failure")
	}
	for _, fragment := range []string{
		"authentication failed",
		"/tmp/executorssh.sock",
		"coder",
		"/tmp/id_ed25519",
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("WaitForSSH() error = %v, want %q", err, fragment)
		}
	}
	if runner.outputCalls != 1 {
		t.Fatalf("Output calls = %d, want one fast-fail attempt", runner.outputCalls)
	}
}

// runContaining returns the first recorded command containing a fragment.
func runContaining(runs []string, fragment string) string {
	for _, run := range runs {
		if strings.Contains(run, fragment) {
			return run
		}
	}
	return ""
}

type authFailureRunner struct {
	outputCalls int
}

// Run satisfies system.Runner for tests that only exercise Output.
func (r *authFailureRunner) Run(_ context.Context, _ string, _ ...string) error {
	return nil
}

// Output returns an SSH authentication failure and counts attempts.
func (r *authFailureRunner) Output(_ context.Context, _ string, _ ...string) ([]byte, error) {
	r.outputCalls++
	return nil, errors.New("ssh failed: exit status 255: coder@localhost: Permission denied (publickey,keyboard-interactive).")
}
