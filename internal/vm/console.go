package vm

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	consoleOutputPollInterval  = 100 * time.Millisecond
	consoleProcessPollInterval = time.Second
)

// prepareConsoleLog creates a private log directory and truncates the current log.
func (m Manager) prepareConsoleLog() error {
	if strings.TrimSpace(m.Config.ConsoleLog) == "" {
		return fmt.Errorf("console log path must be set")
	}
	dir := filepath.Dir(m.Config.ConsoleLog)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(m.Config.ConsoleLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// Console copies the current serial log and follows it until QEMU stops.
func (m Manager) Console(ctx context.Context, out io.Writer) error {
	pid, _, err := m.QEMUProcess(ctx)
	if err != nil {
		return fmt.Errorf("VM is not running: %w", err)
	}
	file, err := os.Open(m.Config.ConsoleLog)
	if err != nil {
		return fmt.Errorf("open VM console log: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(out, file); err != nil {
		return fmt.Errorf("display VM console: %w", err)
	}

	outputTicker := time.NewTicker(consoleOutputPollInterval)
	defer outputTicker.Stop()
	processTicker := time.NewTicker(consoleProcessPollInterval)
	defer processTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-outputTicker.C:
			if _, err := io.Copy(out, file); err != nil {
				return fmt.Errorf("display VM console: %w", err)
			}
		case <-processTicker.C:
			if err := m.validateQEMUProcess(ctx, pid); err != nil {
				if _, copyErr := io.Copy(out, file); copyErr != nil {
					return fmt.Errorf("display VM console: %w", copyErr)
				}
				return nil
			}
		}
	}
}
