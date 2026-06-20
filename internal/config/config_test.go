package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesDefaultsWithoutConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load("executor")
	if err != nil {
		t.Fatal(err)
	}

	executorDir := filepath.Join(home, ".executor")
	runtimeDir := filepath.Join(home, ".executor_runtime")
	if cfg.ExecutorDir != executorDir {
		t.Fatalf("ExecutorDir = %q, want %q", cfg.ExecutorDir, executorDir)
	}
	if cfg.VMImage != filepath.Join(executorDir, "alpine-podman.qcow2") {
		t.Fatalf("VMImage = %q, want derived qcow2 image path", cfg.VMImage)
	}
	if cfg.QEMUPIDFile != filepath.Join(runtimeDir, "qemu.pid") {
		t.Fatalf("QEMUPIDFile = %q, want default pidfile", cfg.QEMUPIDFile)
	}
	if cfg.PodmanDataDir != defaultPodmanDataRoot {
		t.Fatalf("PodmanDataDir = %q, want default Podman data root", cfg.PodmanDataDir)
	}
	if cfg.PodmanDiskImage != defaultPodmanDiskImage {
		t.Fatalf("PodmanDiskImage = %q, want default Podman disk image", cfg.PodmanDiskImage)
	}
	if cfg.PodmanDiskSize != defaultPodmanDiskSize {
		t.Fatalf("PodmanDiskSize = %q, want default Podman disk size", cfg.PodmanDiskSize)
	}
	if cfg.PodmanStorageDriver != defaultPodmanStorageDriver {
		t.Fatalf("PodmanStorageDriver = %q, want default Podman storage driver", cfg.PodmanStorageDriver)
	}
	if cfg.PodmanRegistryMirror != defaultPodmanRegistryMirror {
		t.Fatalf("PodmanRegistryMirror = %q, want default Podman registry mirror", cfg.PodmanRegistryMirror)
	}
	if cfg.QEMUIOProfile != "max" || cfg.DiskCache != "unsafe" || cfg.DiskAIO != "threads" {
		t.Fatalf("I/O options = profile:%q cache:%q aio:%q, want max/unsafe/threads", cfg.QEMUIOProfile, cfg.DiskCache, cfg.DiskAIO)
	}
	if cfg.HostShare != "9p" {
		t.Fatalf("HostShare = %q, want 9p", cfg.HostShare)
	}
	if cfg.SSHSocket != filepath.Join(runtimeDir, "ssh.sock") {
		t.Fatalf("SSHSocket = %q, want derived socket path", cfg.SSHSocket)
	}
	if cfg.SSHUser != defaultGuestUser {
		t.Fatalf("SSHUser = %q, want %q", cfg.SSHUser, defaultGuestUser)
	}
	if cfg.MonitorSocket != filepath.Join(runtimeDir, "monitor.sock") {
		t.Fatalf("MonitorSocket = %q, want derived socket path", cfg.MonitorSocket)
	}
	if cfg.AssetMirror != defaultAssetMirror {
		t.Fatalf("AssetMirror = %q, want default mirror", cfg.AssetMirror)
	}
	if _, err := os.Stat(filepath.Join(executorDir, configFileName)); !os.IsNotExist(err) {
		t.Fatalf("config file stat error = %v, want not exist", err)
	}
}

func TestLoadReadsConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	executorDir := filepath.Join(home, ".executor")
	if err := os.MkdirAll(executorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(executorDir, configFileName)
	content := `qemu:
  binary: tools/qemu-system-x86_64
  accel: tcg,thread=multi
  io_profile: balanced
  disk_cache: none
  disk_aio: native
  memory_mib: 2048
  cpus: 2
host_share: none
guest_arch: amd64
podman:
  registry_mirror: https://mirror.example.invalid
  data_root: /data/podman
  disk_image: disks/podman-data.qcow2
  disk_size: 25G
  storage_driver: vfs
asset_mirror: https://example.invalid/assets
timeouts:
  command: 30s
  boot: 15m
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("executor")
	if err != nil {
		t.Fatal(err)
	}

	if cfg.QEMUBinary != filepath.Join(executorDir, "tools/qemu-system-x86_64") {
		t.Fatalf("QEMUBinary = %q, want relative path resolved under executor dir", cfg.QEMUBinary)
	}
	if cfg.QEMUAccel != "tcg,thread=multi" {
		t.Fatalf("QEMUAccel = %q, want override", cfg.QEMUAccel)
	}
	if cfg.QEMUIOProfile != "balanced" || cfg.DiskCache != "none" || cfg.DiskAIO != "native" {
		t.Fatalf("I/O options = profile:%q cache:%q aio:%q, want balanced/none/native", cfg.QEMUIOProfile, cfg.DiskCache, cfg.DiskAIO)
	}
	if cfg.MemoryMiB != 2048 || cfg.CPUs != 2 {
		t.Fatalf("resources = memory:%d cpus:%d, want 2048/2", cfg.MemoryMiB, cfg.CPUs)
	}
	if cfg.HostShare != "none" {
		t.Fatalf("HostShare = %q, want none", cfg.HostShare)
	}
	if cfg.PodmanRegistryMirror != "https://mirror.example.invalid" {
		t.Fatalf("PodmanRegistryMirror = %q, want mirror override", cfg.PodmanRegistryMirror)
	}
	if cfg.PodmanDataDir != "/data/podman" {
		t.Fatalf("PodmanDataDir = %q, want config override", cfg.PodmanDataDir)
	}
	if cfg.PodmanDiskImage != filepath.Join(executorDir, "disks/podman-data.qcow2") {
		t.Fatalf("PodmanDiskImage = %q, want relative path resolved under executor dir", cfg.PodmanDiskImage)
	}
	if cfg.PodmanDiskSize != "25G" {
		t.Fatalf("PodmanDiskSize = %q, want config override", cfg.PodmanDiskSize)
	}
	if cfg.PodmanStorageDriver != "vfs" {
		t.Fatalf("PodmanStorageDriver = %q, want config override", cfg.PodmanStorageDriver)
	}
	if cfg.CommandTimeout != 30*time.Second || cfg.BootTimeout != 15*time.Minute {
		t.Fatalf("timeouts = command:%s boot:%s, want 30s/15m", cfg.CommandTimeout, cfg.BootTimeout)
	}
}

func TestLoadIgnoresExecutorEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("EXECUTOR_CPUS", "99")
	t.Setenv("EXECUTOR_QEMU_IO_PROFILE", "safe")
	t.Setenv("EXECUTOR_PODMAN_REGISTRY_MIRROR", "https://ignored.example.invalid")
	t.Setenv("EXECUTOR_DIR", "/ignored")

	cfg, err := Load("executor")
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ExecutorDir != filepath.Join(home, ".executor") {
		t.Fatalf("ExecutorDir = %q, want HOME-derived path", cfg.ExecutorDir)
	}
	if cfg.CPUs != defaultCPUs {
		t.Fatalf("CPUs = %d, want default", cfg.CPUs)
	}
	if cfg.QEMUIOProfile != "max" {
		t.Fatalf("QEMUIOProfile = %q, want default", cfg.QEMUIOProfile)
	}
	if cfg.PodmanDataDir != defaultPodmanDataRoot {
		t.Fatalf("PodmanDataDir = %q, want default", cfg.PodmanDataDir)
	}
	if cfg.PodmanRegistryMirror != defaultPodmanRegistryMirror {
		t.Fatalf("PodmanRegistryMirror = %q, want default", cfg.PodmanRegistryMirror)
	}
}

func TestLoadIgnoresLegacyEngineAndDockerKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	executorDir := filepath.Join(home, ".executor")
	if err := os.MkdirAll(executorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `engine: docker
docker:
  data_root: /var/lib/docker
podman:
  data_root: /data/podman
  disk_image: disks/podman-data.qcow2
  disk_size: 25G
  storage_driver: vfs
`
	if err := os.WriteFile(filepath.Join(executorDir, configFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("executor")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PodmanDataDir != "/data/podman" {
		t.Fatalf("PodmanDataDir = %q, want podman config to be used", cfg.PodmanDataDir)
	}
	if cfg.PodmanDiskImage != filepath.Join(executorDir, "disks/podman-data.qcow2") {
		t.Fatalf("PodmanDiskImage = %q, want relative Podman disk path resolved", cfg.PodmanDiskImage)
	}
	if cfg.PodmanDiskSize != "25G" {
		t.Fatalf("PodmanDiskSize = %q, want podman config to be used", cfg.PodmanDiskSize)
	}
	if cfg.PodmanStorageDriver != "vfs" {
		t.Fatalf("PodmanStorageDriver = %q, want podman config to be used", cfg.PodmanStorageDriver)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	executorDir := filepath.Join(home, ".executor")
	if err := os.MkdirAll(executorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(executorDir, configFileName), []byte("qemu:\n  memory_mib: 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load("executor")
	if err == nil {
		t.Fatal("Load() error = nil, want memory validation error")
	}
	if !strings.Contains(err.Error(), "qemu.memory_mib must be positive") {
		t.Fatalf("Load() error = %v, want YAML key validation error", err)
	}
}
