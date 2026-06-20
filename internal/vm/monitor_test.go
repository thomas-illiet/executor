package vm

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// TestExecuteUsesUnixSocket verifies the monitor dials Unix sockets when configured.
func TestExecuteUsesUnixSocket(t *testing.T) {
	socketPath := t.TempDir() + "/monitor.sock"
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serveOneMonitorCommand(t, listener, "info status")

	client := MonitorClient{SocketPath: socketPath, Timeout: time.Second}
	out, err := client.Execute(context.Background(), "info status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(qemu)") {
		t.Fatalf("output = %q, want monitor prompt", out)
	}
	if client.Endpoint() != socketPath {
		t.Fatalf("Endpoint() = %q, want socket path", client.Endpoint())
	}
}

// serveOneMonitorCommand accepts one monitor command and returns a prompt.
func serveOneMonitorCommand(t *testing.T, listener net.Listener, want string) {
	t.Helper()
	errs := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			errs <- err
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			errs <- err
			return
		}
		if strings.TrimSpace(line) != want {
			errs <- &unexpectedCommandError{got: strings.TrimSpace(line), want: want}
			return
		}
		_, err = conn.Write([]byte("running\n(qemu) "))
		errs <- err
	}()
	t.Cleanup(func() {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	})
}

type unexpectedCommandError struct {
	got  string
	want string
}

// Error formats the monitor command mismatch for test failures.
func (e *unexpectedCommandError) Error() string {
	return "monitor command = " + e.got + ", want " + e.want
}
