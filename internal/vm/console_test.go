package vm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"executor/internal/config"
)

// TestPrepareConsoleLogCreatesPrivateTruncatedFile verifies log lifecycle and modes.
func TestPrepareConsoleLogCreatesPrivateTruncatedFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "console.log")
	if err := os.WriteFile(path, []byte("old console"), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := Manager{Config: config.Config{ConsoleLog: path}}
	if err := manager.prepareConsoleLog(); err != nil {
		t.Fatal(err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("log directory mode = %o, want 700", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("console log mode = %o, want 600", got)
	}
	if info.Size() != 0 {
		t.Fatalf("console log size = %d, want truncated file", info.Size())
	}
}

// TestConsoleRefusesStoppedVM verifies stale console logs are not displayed.
func TestConsoleRefusesStoppedVM(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "qemu.pid")
	logfile := filepath.Join(dir, "console.log")
	if err := os.WriteFile(pidfile, []byte("123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logfile, []byte("stale console\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &consoleRunner{}
	manager := Manager{
		Config: config.Config{
			QEMUBinary:  "qemu-system-x86_64",
			QEMUPIDFile: pidfile,
			ConsoleLog:  logfile,
		},
		Runner: runner,
	}
	var out bytes.Buffer

	err := manager.Console(context.Background(), &out)
	if err == nil || !strings.Contains(err.Error(), "VM is not running") {
		t.Fatalf("Console() error = %v, want stopped VM error", err)
	}
	if out.Len() != 0 {
		t.Fatalf("Console() output = %q, want stale log hidden", out.String())
	}
}

// TestConsoleHonorsContextCancellation verifies callers can stop a live reader.
func TestConsoleHonorsContextCancellation(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "qemu.pid")
	logfile := filepath.Join(dir, "console.log")
	if err := os.WriteFile(pidfile, []byte("123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logfile, []byte("boot history\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &consoleRunner{running: true, command: "qemu-system-x86_64 -pidfile " + pidfile}
	manager := Manager{
		Config: config.Config{
			QEMUBinary:  "qemu-system-x86_64",
			QEMUPIDFile: pidfile,
			ConsoleLog:  logfile,
		},
		Runner: runner,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := manager.Console(ctx, &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Console() error = %v, want context cancellation", err)
	}
}

// TestConsoleSupportsConcurrentReaders verifies history and live output are shared.
func TestConsoleSupportsConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "qemu.pid")
	logfile := filepath.Join(dir, "console.log")
	if err := os.WriteFile(pidfile, []byte("123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logfile, []byte("boot history\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &consoleRunner{running: true, command: "qemu-system-x86_64 -pidfile " + pidfile}
	manager := Manager{
		Config: config.Config{
			QEMUBinary:  "qemu-system-x86_64",
			QEMUPIDFile: pidfile,
			ConsoleLog:  logfile,
		},
		Runner: runner,
	}

	readers := []*observedWriter{newObservedWriter(), newObservedWriter()}
	errs := make(chan error, len(readers))
	for _, reader := range readers {
		go func(out *observedWriter) {
			errs <- manager.Console(context.Background(), out)
		}(reader)
	}
	for _, reader := range readers {
		reader.waitFor(t, "boot history\n")
	}

	file, err := os.OpenFile(logfile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("live output\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, reader := range readers {
		reader.waitFor(t, "live output\n")
	}

	runner.setRunning(false)
	for range readers {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("Console() error = %v, want clean exit", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Console() did not exit after QEMU stopped")
		}
	}
}

type consoleRunner struct {
	mu      sync.Mutex
	running bool
	command string
}

func (r *consoleRunner) Run(context.Context, string, ...string) error {
	return errors.New("unexpected Run call")
}

func (r *consoleRunner) Output(_ context.Context, _ string, _ ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return nil, errors.New("process exited")
	}
	return []byte(r.command + "\n"), nil
}

func (r *consoleRunner) setRunning(running bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running = running
}

type observedWriter struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	changed chan struct{}
}

func newObservedWriter() *observedWriter {
	return &observedWriter{changed: make(chan struct{}, 1)}
}

func (w *observedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buffer.Write(data)
	w.mu.Unlock()
	select {
	case w.changed <- struct{}{}:
	default:
	}
	return n, err
}

func (w *observedWriter) waitFor(t *testing.T, fragment string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		w.mu.Lock()
		found := strings.Contains(w.buffer.String(), fragment)
		w.mu.Unlock()
		if found {
			return
		}
		select {
		case <-w.changed:
		case <-deadline.C:
			t.Fatalf("console output did not contain %q", fragment)
		}
	}
}
