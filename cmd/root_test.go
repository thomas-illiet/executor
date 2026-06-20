package cmd

import (
	"context"
	"errors"
	"io"
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
			if strings.Contains(got, "  term                 Open an SSH shell in the VM") {
				t.Fatalf("help output %q contains hidden term command", got)
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
