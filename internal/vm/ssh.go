package vm

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"executor/internal/system"
)

type SSHClient struct {
	SocketPath string
	User       string
	KeyPath    string
	Runner     system.Runner
}

// RunNoTTY executes a remote command without a TTY.
func (c SSHClient) RunNoTTY(ctx context.Context, command string) error {
	return c.RunWithTTY(ctx, command, false)
}

// RunWithTTY executes a remote command with explicit TTY behavior.
func (c SSHClient) RunWithTTY(ctx context.Context, command string, tty bool) error {
	args := c.baseArgs(tty)
	args = append(args, c.destination(), command)
	return c.Runner.Run(ctx, "ssh", args...)
}

// Shell opens an interactive SSH shell.
func (c SSHClient) Shell(ctx context.Context) error {
	args := c.baseArgs(true)
	args = append(args, c.destination())
	return c.Runner.Run(ctx, "ssh", args...)
}

// RunInDir executes a command in a remote directory with a TTY.
func (c SSHClient) RunInDir(ctx context.Context, dir string, command []string) error {
	return c.RunInDirWithTTY(ctx, dir, command, true)
}

// RunInDirNoTTY executes a command in a remote directory without a TTY.
func (c SSHClient) RunInDirNoTTY(ctx context.Context, dir string, command []string) error {
	return c.RunInDirWithTTY(ctx, dir, command, false)
}

// RunInDirWithTTY executes a command in a remote directory with explicit TTY behavior.
func (c SSHClient) RunInDirWithTTY(ctx context.Context, dir string, command []string, tty bool) error {
	return c.RunWithTTY(ctx, CommandInDir(dir, command), tty)
}

// RunInDirDetachedNoTTY runs a remote command detached and replays its result.
func (c SSHClient) RunInDirDetachedNoTTY(ctx context.Context, dir string, command []string) error {
	runDir := fmt.Sprintf("/tmp/executor-%d-%d", os.Getpid(), time.Now().UnixNano())
	if err := c.RunNoTTY(ctx, DetachedCommandInDir(runDir, dir, command)); err != nil {
		return err
	}
	status, err := c.waitForDetachedStatus(ctx, runDir)
	if err != nil {
		return err
	}
	return c.RunNoTTY(ctx, FinishDetachedCommand(runDir, status))
}

// Output runs a remote command and returns its stdout.
func (c SSHClient) Output(ctx context.Context, command string) ([]byte, error) {
	args := c.baseArgs(false)
	args = append(args, c.destination(), command)
	return c.Runner.Output(ctx, "ssh", args...)
}

// StartLocalForward starts an SSH local port forward in the background.
func (c SSHClient) StartLocalForward(ctx context.Context, listenHost string, listenPort int, targetHost string, targetPort int) error {
	args := c.baseArgs(false)
	args = append(args,
		"-f",
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-L", fmt.Sprintf("%s:%d:%s:%d", listenHost, listenPort, targetHost, targetPort),
		c.destination(),
	)
	return c.Runner.Run(ctx, "sh", "-c", system.Join(append([]string{"ssh"}, args...))+" </dev/null >/dev/null 2>/dev/null")
}

// Endpoint returns a display label for the SSH transport.
func (c SSHClient) Endpoint() string {
	return c.SocketPath
}

// CommandInDir builds a shell command that runs from a remote directory.
func CommandInDir(dir string, command []string) string {
	if dir == "" {
		return system.Join(command)
	}
	return "cd " + system.Single(dir) + " && " + system.Join(command)
}

// DetachedCommandInDir builds a remote background command with captured output.
func DetachedCommandInDir(runDir string, dir string, command []string) string {
	return "run_dir=" + system.Single(runDir) + "; " +
		"mkdir -p \"$run_dir\" && " +
		"( trap '' HUP; ( " + CommandInDir(dir, command) + " ) >\"$run_dir/out\" 2>\"$run_dir/err\"; " +
		"echo $? >\"$run_dir/status\" ) </dev/null >/dev/null 2>/dev/null &"
}

// FinishDetachedCommand builds a command that replays detached output and exits.
func FinishDetachedCommand(runDir string, status int) string {
	return "cat " + system.Single(runDir+"/out") + "; " +
		"cat " + system.Single(runDir+"/err") + " >&2; " +
		"rm -rf " + system.Single(runDir) + "; " +
		"exit " + strconv.Itoa(status)
}

// waitForDetachedStatus waits for a remote detached command status file.
func (c SSHClient) waitForDetachedStatus(ctx context.Context, runDir string) (int, error) {
	statusPath := system.Single(runDir + "/status")
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		out, err := c.Output(attemptCtx, "if [ -f "+statusPath+" ]; then cat "+statusPath+"; else echo pending; fi")
		cancel()
		if err == nil {
			value := strings.TrimSpace(string(out))
			if value != "pending" {
				status, parseErr := strconv.Atoi(value)
				if parseErr != nil {
					return 1, fmt.Errorf("invalid remote status for %s: %w", runDir, parseErr)
				}
				return status, nil
			}
		}
		select {
		case <-ctx.Done():
			return 1, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// baseArgs returns the common SSH arguments for this client.
func (c SSHClient) baseArgs(tty bool) []string {
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-q",
		"-o", "ProxyCommand=nc -U " + system.Single(c.SocketPath),
	}
	if tty {
		args = append(args, "-t")
	} else {
		args = append(args, "-T")
	}
	if c.KeyPath != "" {
		args = append(args, "-i", c.KeyPath)
	}
	return args
}

// destination returns the SSH user and host target.
func (c SSHClient) destination() string {
	return fmt.Sprintf("%s@localhost", c.User)
}
