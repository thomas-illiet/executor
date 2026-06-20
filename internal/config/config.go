package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const (
	defaultMemoryMiB            = 4096
	defaultCPUs                 = 4
	defaultAssetMirror          = "https://github.com/thomas-illiet/executor/releases/latest/download"
	defaultPodmanDataRoot       = "/home/coder/.local/share/containers"
	defaultPodmanDiskImage      = "/home/appuser/.executor/podman-data.qcow2"
	defaultPodmanDiskSize       = "10G"
	defaultPodmanStorageDriver  = "overlay"
	defaultPodmanRegistryMirror = ""
	defaultGuestUser            = "coder"
	configFileName              = "config.yaml"
)

type Config struct {
	Home                 string
	ExecutorDir          string
	PodmanDataDir        string
	PodmanDiskImage      string
	PodmanDiskSize       string
	PodmanStorageDriver  string
	PodmanRegistryMirror string
	VMImage              string
	KernelImage          string
	InitrdImage          string
	QEMUBinary           string
	QEMUPIDFile          string
	QEMUAccel            string
	QEMUIOProfile        string
	DiskCache            string
	DiskAIO              string
	HostShare            string
	GuestArch            string
	SSHSocket            string
	SSHUser              string
	SSHKeyPath           string
	MonitorSocket        string
	MemoryMiB            int
	CPUs                 int
	BootFile             string
	AssetMirror          string
	WorkDir              string
	CommandTimeout       time.Duration
	BootTimeout          time.Duration
}

type fileConfig struct {
	QEMU        qemuConfig     `yaml:"qemu" mapstructure:"qemu"`
	HostShare   string         `yaml:"host_share" mapstructure:"host_share"`
	GuestArch   string         `yaml:"guest_arch" mapstructure:"guest_arch"`
	Podman      podmanConfig   `yaml:"podman" mapstructure:"podman"`
	AssetMirror string         `yaml:"asset_mirror" mapstructure:"asset_mirror"`
	Timeouts    timeoutsConfig `yaml:"timeouts" mapstructure:"timeouts"`
}

type qemuConfig struct {
	Binary    string `yaml:"binary" mapstructure:"binary"`
	Accel     string `yaml:"accel" mapstructure:"accel"`
	IOProfile string `yaml:"io_profile" mapstructure:"io_profile"`
	DiskCache string `yaml:"disk_cache,omitempty" mapstructure:"disk_cache"`
	DiskAIO   string `yaml:"disk_aio,omitempty" mapstructure:"disk_aio"`
	MemoryMiB int    `yaml:"memory_mib" mapstructure:"memory_mib"`
	CPUs      int    `yaml:"cpus" mapstructure:"cpus"`
}

type podmanConfig struct {
	RegistryMirror string `yaml:"registry_mirror" mapstructure:"registry_mirror"`
	DataRoot       string `yaml:"data_root" mapstructure:"data_root"`
	DiskImage      string `yaml:"disk_image" mapstructure:"disk_image"`
	DiskSize       string `yaml:"disk_size" mapstructure:"disk_size"`
	StorageDriver  string `yaml:"storage_driver" mapstructure:"storage_driver"`
}

type timeoutsConfig struct {
	Command string `yaml:"command" mapstructure:"command"`
	Boot    string `yaml:"boot" mapstructure:"boot"`
}

// Load builds the app configuration from $HOME/.executor/config.yaml.
func Load(_ string) (Config, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return Config{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("resolve home directory: %w", err)
	}
	executorDir := filepath.Join(home, ".executor")
	configPath := filepath.Join(executorDir, configFileName)
	if err := ensureConfig(configPath, defaultFileConfig()); err != nil {
		return Config{}, err
	}
	return loadFile(workDir, home, executorDir, configPath)
}

// FromEnvironment loads $HOME/.executor/config.yaml; EXECUTOR_* values are ignored.
func FromEnvironment(argv0 string) (Config, error) {
	return Load(argv0)
}

func loadFile(workDir, home, executorDir, configPath string) (Config, error) {
	reader := viper.New()
	reader.SetConfigFile(configPath)
	reader.SetConfigType("yaml")
	setDefaults(reader, defaultFileConfig())
	if err := reader.ReadInConfig(); err != nil {
		return Config{}, err
	}

	ioProfile := strings.ToLower(strings.TrimSpace(reader.GetString("qemu.io_profile")))
	profileDefaults, err := ioDefaults(ioProfile)
	if err != nil {
		return Config{}, err
	}
	diskCache := strings.ToLower(strings.TrimSpace(reader.GetString("qemu.disk_cache")))
	if diskCache == "" {
		diskCache = profileDefaults.diskCache
	}
	diskAIO := strings.ToLower(strings.TrimSpace(reader.GetString("qemu.disk_aio")))
	if diskAIO == "" {
		diskAIO = profileDefaults.diskAIO
	}

	commandTimeout, err := parseDuration("timeouts.command", reader.GetString("timeouts.command"))
	if err != nil {
		return Config{}, err
	}
	bootTimeout, err := parseDuration("timeouts.boot", reader.GetString("timeouts.boot"))
	if err != nil {
		return Config{}, err
	}
	runtimeDir := filepath.Join(home, ".executor_runtime")

	cfg := Config{
		Home:                 home,
		ExecutorDir:          executorDir,
		PodmanDataDir:        strings.TrimSpace(reader.GetString("podman.data_root")),
		PodmanDiskImage:      resolveConfigPath(executorDir, strings.TrimSpace(reader.GetString("podman.disk_image"))),
		PodmanDiskSize:       strings.TrimSpace(reader.GetString("podman.disk_size")),
		PodmanStorageDriver:  strings.ToLower(strings.TrimSpace(reader.GetString("podman.storage_driver"))),
		PodmanRegistryMirror: strings.TrimSpace(reader.GetString("podman.registry_mirror")),
		VMImage:              filepath.Join(executorDir, "alpine-podman.qcow2"),
		KernelImage:          filepath.Join(executorDir, "vmlinuz-virt"),
		InitrdImage:          filepath.Join(executorDir, "initramfs-virt"),
		QEMUBinary:           resolveConfigPath(executorDir, reader.GetString("qemu.binary")),
		QEMUPIDFile:          filepath.Join(runtimeDir, "qemu.pid"),
		QEMUAccel:            strings.ToLower(strings.TrimSpace(reader.GetString("qemu.accel"))),
		QEMUIOProfile:        ioProfile,
		DiskCache:            diskCache,
		DiskAIO:              diskAIO,
		HostShare:            strings.ToLower(strings.TrimSpace(reader.GetString("host_share"))),
		GuestArch:            reader.GetString("guest_arch"),
		SSHSocket:            filepath.Join(runtimeDir, "ssh.sock"),
		SSHUser:              defaultGuestUser,
		SSHKeyPath:           filepath.Join(executorDir, "id_ed25519"),
		MonitorSocket:        filepath.Join(runtimeDir, "monitor.sock"),
		MemoryMiB:            reader.GetInt("qemu.memory_mib"),
		CPUs:                 reader.GetInt("qemu.cpus"),
		BootFile:             filepath.Join(home, ".boot"),
		AssetMirror:          reader.GetString("asset_mirror"),
		WorkDir:              workDir,
		CommandTimeout:       commandTimeout,
		BootTimeout:          bootTimeout,
	}
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func ensureConfig(path string, cfg fileConfig) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func defaultFileConfig() fileConfig {
	return fileConfig{
		QEMU: qemuConfig{
			Binary:    "qemu-system-x86_64",
			Accel:     "auto",
			IOProfile: "max",
			MemoryMiB: defaultMemoryMiB,
			CPUs:      defaultCPUs,
		},
		HostShare: "9p",
		GuestArch: "amd64",
		Podman: podmanConfig{
			RegistryMirror: defaultPodmanRegistryMirror,
			DataRoot:       defaultPodmanDataRoot,
			DiskImage:      defaultPodmanDiskImage,
			DiskSize:       defaultPodmanDiskSize,
			StorageDriver:  defaultPodmanStorageDriver,
		},
		AssetMirror: defaultAssetMirror,
		Timeouts: timeoutsConfig{
			Command: "2m",
			Boot:    "8m",
		},
	}
}

func setDefaults(reader *viper.Viper, cfg fileConfig) {
	reader.SetDefault("qemu.binary", cfg.QEMU.Binary)
	reader.SetDefault("qemu.accel", cfg.QEMU.Accel)
	reader.SetDefault("qemu.io_profile", cfg.QEMU.IOProfile)
	reader.SetDefault("qemu.memory_mib", cfg.QEMU.MemoryMiB)
	reader.SetDefault("qemu.cpus", cfg.QEMU.CPUs)
	reader.SetDefault("host_share", cfg.HostShare)
	reader.SetDefault("guest_arch", cfg.GuestArch)
	reader.SetDefault("podman.registry_mirror", cfg.Podman.RegistryMirror)
	reader.SetDefault("podman.data_root", cfg.Podman.DataRoot)
	reader.SetDefault("podman.disk_image", cfg.Podman.DiskImage)
	reader.SetDefault("podman.disk_size", cfg.Podman.DiskSize)
	reader.SetDefault("podman.storage_driver", cfg.Podman.StorageDriver)
	reader.SetDefault("asset_mirror", cfg.AssetMirror)
	reader.SetDefault("timeouts.command", cfg.Timeouts.Command)
	reader.SetDefault("timeouts.boot", cfg.Timeouts.Boot)
}

type ioProfileDefaults struct {
	diskCache string
	diskAIO   string
	hostShare string
}

// ioDefaults returns disk and sharing defaults for an I/O profile.
func ioDefaults(profile string) (ioProfileDefaults, error) {
	switch profile {
	case "max":
		return ioProfileDefaults{diskCache: "unsafe", diskAIO: "threads", hostShare: "9p"}, nil
	case "balanced":
		return ioProfileDefaults{diskCache: "writeback", diskAIO: "threads", hostShare: "9p"}, nil
	case "safe":
		return ioProfileDefaults{diskCache: "none", diskAIO: "threads", hostShare: "9p"}, nil
	default:
		return ioProfileDefaults{}, fmt.Errorf("qemu.io_profile must be one of max, balanced, safe")
	}
}

func parseDuration(key, value string) (time.Duration, error) {
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return parsed, nil
}

func resolveConfigPath(baseDir, value string) string {
	if value == "" || filepath.IsAbs(value) || !strings.ContainsAny(value, `/\`) {
		return value
	}
	return filepath.Join(baseDir, value)
}

func validate(cfg Config) error {
	if cfg.MemoryMiB <= 0 {
		return fmt.Errorf("qemu.memory_mib must be positive")
	}
	if cfg.CPUs <= 0 {
		return fmt.Errorf("qemu.cpus must be positive")
	}
	if err := validateOneOf("qemu.disk_cache", cfg.DiskCache, "unsafe", "writeback", "none", "directsync", "writethrough"); err != nil {
		return err
	}
	if err := validateOneOf("qemu.disk_aio", cfg.DiskAIO, "threads", "native", "io_uring"); err != nil {
		return err
	}
	if err := validateOneOf("host_share", cfg.HostShare, "9p", "none"); err != nil {
		return err
	}
	if cfg.PodmanDataDir == "" {
		return fmt.Errorf("podman.data_root must be set")
	}
	if cfg.PodmanDiskImage == "" {
		return fmt.Errorf("podman.disk_image must be set")
	}
	if cfg.PodmanDiskSize == "" {
		return fmt.Errorf("podman.disk_size must be set")
	}
	if cfg.PodmanStorageDriver == "" {
		return fmt.Errorf("podman.storage_driver must be set")
	}
	if cfg.SSHSocket == "" {
		return fmt.Errorf("ssh socket path must be set")
	}
	if cfg.MonitorSocket == "" {
		return fmt.Errorf("monitor socket path must be set")
	}
	return nil
}

// validateOneOf checks that a value is in the allowed list.
func validateOneOf(key, value string, allowed ...string) error {
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s", key, strings.Join(allowed, ", "))
}
