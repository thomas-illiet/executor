package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"executor/internal/app"
	"executor/internal/config"
)

// TestTopLevelHelpIsPodmanLikeAndLocal verifies top-level help is static and does not touch SSH.
func TestTopLevelHelpIsPodmanLikeAndLocal(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no args", args: nil},
		{name: "long help", args: []string{"--help"}},
		{name: "help command", args: []string{"help"}},
	}

	var baseline string
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &scriptedRunner{outputs: map[string]scriptedOutput{}}
			var out strings.Builder
			application := newTestApp(runner, &out, io.Discard)

			if err := ExecuteContext(context.Background(), application, tt.args); err != nil {
				t.Fatal(err)
			}
			if len(runner.runs) != 0 || len(runner.outputCalls) != 0 {
				t.Fatalf("help ran commands: runs=%#v outputCalls=%#v", runner.runs, runner.outputCalls)
			}

			got := out.String()
			if baseline == "" {
				baseline = got
			} else if got != baseline {
				t.Fatalf("help output = %q, want %q", got, baseline)
			}
			for _, fragment := range []string{
				"Usage: podman [OPTIONS] COMMAND",
				"Common Commands:",
				"  run         Create and run a new container from an image",
				"  ps          List containers",
				"Executor Commands:",
				"  download",
			} {
				if !strings.Contains(got, fragment) {
					t.Fatalf("help output %q does not contain %q", got, fragment)
				}
			}
		})
	}
}

// TestVersionIsLocal verifies version flags do not touch SSH.
func TestVersionIsLocal(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-v"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			runner := &scriptedRunner{outputs: map[string]scriptedOutput{}}
			var out strings.Builder
			application := newTestApp(runner, &out, io.Discard)

			if err := ExecuteContext(context.Background(), application, args); err != nil {
				t.Fatal(err)
			}
			if got, want := out.String(), app.Version+"\n"; got != want {
				t.Fatalf("version output = %q, want %q", got, want)
			}
			if len(runner.runs) != 0 || len(runner.outputCalls) != 0 {
				t.Fatalf("version ran commands: runs=%#v outputCalls=%#v", runner.runs, runner.outputCalls)
			}
		})
	}
}

// TestForwardedCommandsRequireInit verifies Podman commands fail clearly before the VM is ready.
func TestForwardedCommandsRequireInit(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "podman ps with flags", args: []string{"ps", "-a"}},
		{name: "podman top", args: []string{"top", "container"}},
		{name: "run subcommand help", args: []string{"run", "--help"}},
		{name: "help podman subcommand", args: []string{"help", "run"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &scriptedRunner{outputs: map[string]scriptedOutput{}}
			application := newTestApp(runner, io.Discard, io.Discard)

			err := ExecuteContext(context.Background(), application, tt.args)
			want := "podman init has not been run: run 'podman init' first"
			if err == nil || err.Error() != want {
				t.Fatalf("ExecuteContext(%v) error = %v, want %q", tt.args, err, want)
			}
			if len(runner.runs) != 0 {
				t.Fatalf("ExecuteContext(%v) ran commands %#v, want none", tt.args, runner.runs)
			}
			if len(runner.outputCalls) != 1 {
				t.Fatalf("ExecuteContext(%v) output calls = %#v, want one SSH readiness check", tt.args, runner.outputCalls)
			}
		})
	}
}

// TestInternalHelpIsCobraLocal verifies internal command help is served locally.
func TestInternalHelpIsCobraLocal(t *testing.T) {
	runner := &scriptedRunner{outputs: map[string]scriptedOutput{}}
	var out strings.Builder
	application := newTestApp(runner, &out, io.Discard)

	if err := ExecuteContext(context.Background(), application, []string{"help", "usage"}); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"Show QEMU CPU and memory usage", "Usage:"} {
		if !strings.Contains(out.String(), fragment) {
			t.Fatalf("usage help %q does not contain %q", out.String(), fragment)
		}
	}
	if len(runner.runs) != 0 || len(runner.outputCalls) != 0 {
		t.Fatalf("internal help ran commands: runs=%#v outputCalls=%#v", runner.runs, runner.outputCalls)
	}
}

// TestInternalCommandsRejectUnknownFlags verifies Cobra parses internal commands strictly.
func TestInternalCommandsRejectUnknownFlags(t *testing.T) {
	tests := []string{"--bogus", "--save", "--no-save"}

	for _, flag := range tests {
		t.Run(flag, func(t *testing.T) {
			runner := &scriptedRunner{outputs: map[string]scriptedOutput{}}
			application := newTestApp(runner, io.Discard, io.Discard)

			err := ExecuteContext(context.Background(), application, []string{"shutdown", flag})
			want := "unknown flag: " + flag
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("shutdown error = %v, want %q", err, want)
			}
			if len(runner.runs) != 0 || len(runner.outputCalls) != 0 {
				t.Fatalf("strict parsing ran commands: runs=%#v outputCalls=%#v", runner.runs, runner.outputCalls)
			}
		})
	}
}

// TestDownloadCommandDownloadsAssets verifies Cobra parses download flags and invokes the app path.
func TestDownloadCommandDownloadsAssets(t *testing.T) {
	required := map[string][]byte{
		"/alpine-podman.qcow2": []byte("disk-image"),
		"/vmlinuz-virt":        []byte("kernel"),
		"/initramfs-virt":      []byte("initrd"),
		"/id_ed25519":          []byte("private"),
		"/id_ed25519.pub":      []byte("public"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "cli" || password != "secret" {
			t.Fatalf("BasicAuth() = %q/%q/%v, want cli/secret/true", username, password, ok)
		}
		content, ok := required[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()
	var out strings.Builder
	application := app.App{
		Config: config.Config{
			Engine:      "podman",
			AssetMirror: "https://example.invalid/assets",
			VMImage:     filepath.Join(dir, "alpine-podman.qcow2"),
			KernelImage: filepath.Join(dir, "vmlinuz-virt"),
			InitrdImage: filepath.Join(dir, "initramfs-virt"),
			SSHKeyPath:  filepath.Join(dir, "id_ed25519"),
		},
		Runner: &scriptedRunner{outputs: map[string]scriptedOutput{}},
		Out:    &out,
		Err:    io.Discard,
		In:     strings.NewReader(""),
	}

	err := ExecuteContext(context.Background(), application, []string{"download", "--mirror", server.URL, "--basic-auth", "cli:secret"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "VM assets downloaded.") {
		t.Fatalf("download output = %q, want success message", out.String())
	}
}

func newTestApp(runner *scriptedRunner, out io.Writer, errOut io.Writer) app.App {
	return app.App{
		Config: config.Config{
			Engine:    "podman",
			SSHSocket: "/tmp/executor-ssh.sock",
			SSHUser:   "coder",
		},
		Runner: runner,
		Out:    out,
		Err:    errOut,
		In:     strings.NewReader(""),
	}
}

type recordedRun struct {
	name string
	args []string
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
