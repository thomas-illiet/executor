package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"executor/internal/config"
	"executor/internal/vm"
)

// TestInitRestartsRunningVMWithChangedFlavor verifies new resources reach QEMU immediately.
func TestInitRestartsRunningVMWithChangedFlavor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := config.Load("executor")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PodmanDiskImage = filepath.Join(cfg.ExecutorDir, "data.qcow2")
	if err := writeTestAssets(vm.AssetPaths{
		Image:   cfg.VMImage,
		Kernel:  cfg.KernelImage,
		Initrd:  cfg.InitrdImage,
		SSHKey:  cfg.SSHKeyPath,
		QEMU:    cfg.QEMUBinary,
		QEMUImg: cfg.QEMUImgBinary,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.QEMUPIDFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.QEMUPIDFile, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	startErr := errors.New("stop after QEMU start assertion")
	runner := &flavorRunner{cfg: cfg, startErr: startErr}
	application := App{Config: cfg, Runner: runner, Out: io.Discard, Err: io.Discard}
	cpus := 2
	memoryMiB := 1

	err = application.Init(context.Background(), InitOptions{CPUs: &cpus, MemoryMiB: &memoryMiB})
	if !errors.Is(err, startErr) {
		t.Fatalf("Init() error = %v, want %v", err, startErr)
	}
	killIndex := recordedRunIndex(runner.runs, "kill", "123")
	qemuIndex := flavorQEMURunIndex(runner.runs, cfg.QEMUBinary)
	if killIndex == -1 || qemuIndex == -1 || killIndex > qemuIndex {
		t.Fatalf("runs = %#v, want old QEMU killed before new QEMU starts", runner.runs)
	}
	qemuRun := runner.runs[qemuIndex]
	if !runContainsPair(qemuRun, "-m", "1M") || !runContainsPair(qemuRun, "-smp", "2") {
		t.Fatalf("QEMU run = %#v, want updated memory and CPU arguments", qemuRun)
	}

	reloaded, err := config.Load("executor")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CPUs != cpus || reloaded.MemoryMiB != memoryMiB {
		t.Fatalf("persisted resources = %d/%d, want %d/%d", reloaded.CPUs, reloaded.MemoryMiB, cpus, memoryMiB)
	}
}

// TestInitKeepsRunningVMForIdenticalFlavor verifies unchanged resources remain idempotent.
func TestInitKeepsRunningVMForIdenticalFlavor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := config.Load("executor")
	if err != nil {
		t.Fatal(err)
	}
	cpus := 2
	memoryMiB := 1
	if _, _, err := config.EnsureVMResources(cfg, config.VMResourceOverrides{
		CPUs:      &cpus,
		MemoryMiB: &memoryMiB,
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load("executor")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTestAssets(vm.AssetPaths{
		Image:   cfg.VMImage,
		Kernel:  cfg.KernelImage,
		Initrd:  cfg.InitrdImage,
		SSHKey:  cfg.SSHKeyPath,
		QEMU:    cfg.QEMUBinary,
		QEMUImg: cfg.QEMUImgBinary,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.QEMUPIDFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.QEMUPIDFile, []byte("123\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &flavorRunner{cfg: cfg, startErr: errors.New("QEMU should not restart")}
	application := App{Config: cfg, Runner: runner, Out: io.Discard, Err: io.Discard}
	if err := application.Init(context.Background(), InitOptions{
		CPUs:      &cpus,
		MemoryMiB: &memoryMiB,
	}); err != nil {
		t.Fatal(err)
	}
	if index := recordedRunIndex(runner.runs, "kill", "123"); index != -1 {
		t.Fatalf("runs = %#v, identical flavor killed QEMU", runner.runs)
	}
	if index := flavorQEMURunIndex(runner.runs, cfg.QEMUBinary); index != -1 {
		t.Fatalf("runs = %#v, identical flavor started another QEMU", runner.runs)
	}
}

type flavorRunner struct {
	cfg      config.Config
	startErr error
	runs     []recordedRun
	outputs  []recordedRun
}

// Run records commands and stops tests when a new QEMU process is launched.
func (r *flavorRunner) Run(_ context.Context, name string, args ...string) error {
	run := recordedRun{name: name, args: append([]string(nil), args...)}
	r.runs = append(r.runs, run)
	if name == r.cfg.QEMUBinary {
		return r.startErr
	}
	return nil
}

// Output reports the old QEMU process and emulates the Unix-forwarding probe.
func (r *flavorRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	r.outputs = append(r.outputs, recordedRun{name: name, args: append([]string(nil), args...)})
	if name == "ps" {
		return []byte(fmt.Sprintf("%s -pidfile %s\n", r.cfg.QEMUBinary, r.cfg.QEMUPIDFile)), nil
	}
	if name == r.cfg.QEMUBinary {
		pidfile := argumentValue(args, "-pidfile")
		if pidfile == "" {
			return nil, errors.New("QEMU probe did not include a pidfile")
		}
		if err := os.WriteFile(pidfile, []byte("456\n"), 0o644); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// flavorQEMURunIndex finds a launched QEMU command.
func flavorQEMURunIndex(runs []recordedRun, binary string) int {
	for index, run := range runs {
		if run.name == binary {
			return index
		}
	}
	return -1
}

// runContainsPair reports whether adjacent command arguments match.
func runContainsPair(run recordedRun, key, value string) bool {
	for index := 0; index+1 < len(run.args); index++ {
		if run.args[index] == key && run.args[index+1] == value {
			return true
		}
	}
	return false
}

// argumentValue returns the value following a command argument.
func argumentValue(args []string, key string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == key {
			return args[index+1]
		}
	}
	return ""
}
