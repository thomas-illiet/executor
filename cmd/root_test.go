package cmd

import (
	"context"
	"errors"
	"io"
	"os"
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
				"  init",
			} {
				if !strings.Contains(got, fragment) {
					t.Fatalf("help output %q does not contain %q", got, fragment)
				}
			}
			for _, hidden := range []string{
				"  internal",
				"  term                 Open an SSH shell in the VM",
			} {
				if strings.Contains(got, hidden) {
					t.Fatalf("help output %q contains hidden command %q", got, hidden)
				}
			}
			if strings.Contains(got, "  download") {
				t.Fatalf("help output %q contains removed download command", got)
			}
			for _, hidden := range []string{
				"  serve                Keep the proxy container running without starting QEMU",
				"  serve --init         Boot QEMU, configure podman, then keep the container running",
				"  serve --init         Boot QEMU, configure Podman, then keep the container running",
			} {
				if strings.Contains(got, hidden) {
					t.Fatalf("help output %q contains removed serve command %q", got, hidden)
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
		{name: "help removed serve command", args: []string{"help", "serve"}},
		{name: "removed root term command", args: []string{"term"}},
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

// TestInternalTermOpensOneSSHShell verifies the VM shell only exists under internal.
func TestInternalTermOpensOneSSHShell(t *testing.T) {
	runner := &scriptedRunner{outputs: map[string]scriptedOutput{}}
	application := newTestApp(runner, io.Discard, io.Discard)

	if err := ExecuteContext(context.Background(), application, []string{"internal", "term"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.outputCalls) != 0 {
		t.Fatalf("internal term output calls = %#v, want none", runner.outputCalls)
	}
	if len(runner.runs) != 1 {
		t.Fatalf("internal term runs = %#v, want one SSH shell", runner.runs)
	}
	run := runner.runs[0]
	if run.name != "ssh" || len(run.args) < 2 || run.args[len(run.args)-2] != "coder@localhost" {
		t.Fatalf("internal term run = %#v, want an SSH shell for coder@localhost", run)
	}
	command := run.args[len(run.args)-1]
	for _, fragment := range []string{
		"'env'",
		"'XDG_RUNTIME_DIR=/run/user/1000'",
		"'REGISTRY_AUTH_FILE=/home/coder/.config/containers/auth.json'",
		"'TMPDIR=/run/user/1000'",
		"'/bin/sh' '-l'",
	} {
		if !strings.Contains(command, fragment) {
			t.Fatalf("internal term command = %q, want %q", command, fragment)
		}
	}
}

// TestInternalConsoleRefusesStoppedVM verifies console is local and requires QEMU.
func TestInternalConsoleRefusesStoppedVM(t *testing.T) {
	runner := &scriptedRunner{outputs: map[string]scriptedOutput{}}
	application := newTestApp(runner, io.Discard, io.Discard)
	input := &countingReader{}
	application.In = input

	err := ExecuteContext(context.Background(), application, []string{"internal", "console"})
	if err == nil || !strings.Contains(err.Error(), "VM is not running") {
		t.Fatalf("internal console error = %v, want stopped VM error", err)
	}
	if input.reads != 0 {
		t.Fatalf("internal console read stdin %d times, want 0", input.reads)
	}
	if len(runner.runs) != 0 || len(runner.outputCalls) != 0 {
		t.Fatalf("internal console ran unexpected commands: runs=%#v outputCalls=%#v", runner.runs, runner.outputCalls)
	}
}

// TestInternalHelpIsCobraLocal verifies internal command help is served locally.
func TestInternalHelpIsCobraLocal(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		fragments []string
	}{
		{
			name:      "top-level internal command",
			args:      []string{"help", "usage"},
			fragments: []string{"Show QEMU CPU and memory usage", "Usage:"},
		},
		{
			name:      "init flavor options",
			args:      []string{"help", "init"},
			fragments: []string{"--cpu", "--memory", "4096M or 4G"},
		},
		{
			name:      "internal namespace flag",
			args:      []string{"internal", "--help"},
			fragments: []string{"Internal executor commands", "console", "term"},
		},
		{
			name:      "internal console flag",
			args:      []string{"internal", "console", "--help"},
			fragments: []string{"Display the read-only VM console", "podman internal console"},
		},
		{
			name:      "internal term flag",
			args:      []string{"internal", "term", "--help"},
			fragments: []string{"Open an SSH shell in the VM", "podman internal term"},
		},
		{
			name:      "nested help command",
			args:      []string{"help", "internal", "term"},
			fragments: []string{"Open an SSH shell in the VM", "podman internal term"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &scriptedRunner{outputs: map[string]scriptedOutput{}}
			var out strings.Builder
			application := newTestApp(runner, &out, io.Discard)

			if err := ExecuteContext(context.Background(), application, tt.args); err != nil {
				t.Fatal(err)
			}
			for _, fragment := range tt.fragments {
				if !strings.Contains(out.String(), fragment) {
					t.Fatalf("help %q does not contain %q", out.String(), fragment)
				}
			}
			if len(runner.runs) != 0 || len(runner.outputCalls) != 0 {
				t.Fatalf("internal help ran commands: runs=%#v outputCalls=%#v", runner.runs, runner.outputCalls)
			}
		})
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

// TestInitRejectsInvalidFlavorBeforeWritingConfig verifies CLI validation is side-effect free.
func TestInitRejectsInvalidFlavorBeforeWritingConfig(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "zero CPU", args: []string{"init", "--cpu", "0"}},
		{name: "memory without unit", args: []string{"init", "--memory", "4096"}},
		{name: "decimal memory", args: []string{"init", "--memory", "1.5G"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executorDir := t.TempDir()
			runner := &scriptedRunner{outputs: map[string]scriptedOutput{}}
			application := newTestApp(runner, io.Discard, io.Discard)
			application.Config.ExecutorDir = executorDir

			if err := ExecuteContext(context.Background(), application, tt.args); err == nil {
				t.Fatalf("ExecuteContext(%v) error = nil, want validation error", tt.args)
			}
			if _, err := os.Stat(filepath.Join(executorDir, "config.yaml")); !os.IsNotExist(err) {
				t.Fatalf("config stat error = %v, want missing file", err)
			}
			if len(runner.runs) != 0 || len(runner.outputCalls) != 0 {
				t.Fatalf("invalid init ran commands: runs=%#v outputs=%#v", runner.runs, runner.outputCalls)
			}
		})
	}
}

// TestComposeUpCommandArgsExpandsShorthand verifies executor up proxies compose up.
func TestComposeUpCommandArgsExpandsShorthand(t *testing.T) {
	got := composeUpCommandArgs([]string{"-d", "--remove-orphans"})
	want := []string{"compose", "up", "-d", "--remove-orphans"}
	if len(got) != len(want) {
		t.Fatalf("composeUpCommandArgs() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("composeUpCommandArgs()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// newTestApp builds an App with test-safe defaults and injected streams.
func newTestApp(runner *scriptedRunner, out io.Writer, errOut io.Writer) app.App {
	return app.App{
		Config: config.Config{
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

type countingReader struct {
	reads int
}

// Read records any unexpected attempt to consume CLI input.
func (r *countingReader) Read(_ []byte) (int, error) {
	r.reads++
	return 0, io.EOF
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
