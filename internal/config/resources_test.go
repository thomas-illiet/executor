package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseMemoryMiB accepts the documented memory size forms.
func TestParseMemoryMiB(t *testing.T) {
	for _, value := range []string{"4096M", "4096MiB", "4G", "4GiB", "4gib"} {
		t.Run(value, func(t *testing.T) {
			got, err := ParseMemoryMiB(value)
			if err != nil {
				t.Fatal(err)
			}
			if got != 4096 {
				t.Fatalf("ParseMemoryMiB(%q) = %d, want 4096", value, got)
			}
		})
	}
}

// TestParseMemoryMiBRejectsInvalidValues rejects unsupported or unsafe sizes.
func TestParseMemoryMiBRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "4096", "0M", "-1G", "1.5G", "1K", "18446744073709551615G"} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseMemoryMiB(value); err == nil {
				t.Fatalf("ParseMemoryMiB(%q) error = nil, want error", value)
			}
		})
	}
}

// TestEnsureVMResourcesCreatesCanonicalConfig verifies init can create the full config.
func TestEnsureVMResourcesCreatesCanonicalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := Load("executor")
	if err != nil {
		t.Fatal(err)
	}
	cpus := 6
	memoryMiB := 8192

	updated, changed, err := EnsureVMResources(cfg, VMResourceOverrides{
		CPUs:      &cpus,
		MemoryMiB: &memoryMiB,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("EnsureVMResources() changed = false, want true")
	}
	if updated.CPUs != cpus || updated.MemoryMiB != memoryMiB {
		t.Fatalf("updated resources = %d/%d, want %d/%d", updated.CPUs, updated.MemoryMiB, cpus, memoryMiB)
	}

	configPath := filepath.Join(home, ".executor", configFileName)
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"qemu:",
		"  memory_mib: 8192",
		"  cpus: 6",
		"host_share: 9p",
		"podman:",
		"storage:",
		"timeouts:",
	} {
		if !strings.Contains(string(content), fragment) {
			t.Fatalf("created config %q does not contain %q", content, fragment)
		}
	}
	for _, removed := range []string{"binary:", "data_root:", "disk_image:"} {
		if strings.Contains(string(content), removed) {
			t.Fatalf("created config %q contains immutable path key %q", content, removed)
		}
	}

	reloaded, err := Load("executor")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CPUs != cpus || reloaded.MemoryMiB != memoryMiB {
		t.Fatalf("reloaded resources = %d/%d, want %d/%d", reloaded.CPUs, reloaded.MemoryMiB, cpus, memoryMiB)
	}
}

// TestEnsureVMResourcesCreatesDefaultsWithoutOverrides verifies plain init persists defaults.
func TestEnsureVMResourcesCreatesDefaultsWithoutOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := Load("executor")
	if err != nil {
		t.Fatal(err)
	}

	updated, changed, err := EnsureVMResources(cfg, VMResourceOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("EnsureVMResources() changed = true without overrides")
	}
	if updated.CPUs != defaultCPUs || updated.MemoryMiB != defaultMemoryMiB {
		t.Fatalf("updated resources = %d/%d, want defaults", updated.CPUs, updated.MemoryMiB)
	}
	configPath := filepath.Join(home, ".executor", configFileName)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("default config was not created: %v", err)
	}
}

// TestEnsureVMResourcesPreservesExistingYAML verifies targeted updates retain custom data.
func TestEnsureVMResourcesPreservesExistingYAML(t *testing.T) {
	executorDir := t.TempDir()
	configPath := filepath.Join(executorDir, configFileName)
	content := `# executor configuration
qemu:
  binary: custom-qemu
  memory_mib: 2048 # keep this memory
  cpus: 2
  extension: retained
custom:
  enabled: true # keep this extension
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cpus := 8
	cfg := Config{ExecutorDir: executorDir, CPUs: 2, MemoryMiB: 2048}

	updated, changed, err := EnsureVMResources(cfg, VMResourceOverrides{CPUs: &cpus})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || updated.CPUs != cpus || updated.MemoryMiB != 2048 {
		t.Fatalf("updated config = %+v changed=%t, want cpu-only change", updated, changed)
	}
	updatedContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"# executor configuration",
		"memory_mib: 2048 # keep this memory",
		"cpus: 8",
		"extension: retained",
		"enabled: true # keep this extension",
	} {
		if !strings.Contains(string(updatedContent), fragment) {
			t.Fatalf("updated config %q does not contain %q", updatedContent, fragment)
		}
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
}

// TestEnsureVMResourcesPopulatesEmptyQEMU verifies valid default-only YAML can be updated.
func TestEnsureVMResourcesPopulatesEmptyQEMU(t *testing.T) {
	executorDir := t.TempDir()
	configPath := filepath.Join(executorDir, configFileName)
	if err := os.WriteFile(configPath, []byte("qemu:\ncustom: retained\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cpus := 3

	if _, _, err := EnsureVMResources(
		Config{ExecutorDir: executorDir, CPUs: defaultCPUs, MemoryMiB: defaultMemoryMiB},
		VMResourceOverrides{CPUs: &cpus},
	); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"qemu:\n  cpus: 3", "custom: retained"} {
		if !strings.Contains(string(content), fragment) {
			t.Fatalf("updated config %q does not contain %q", content, fragment)
		}
	}
}

// TestEnsureVMResourcesRejectsInvalidCPUWithoutWriting verifies validation precedes persistence.
func TestEnsureVMResourcesRejectsInvalidCPUWithoutWriting(t *testing.T) {
	executorDir := t.TempDir()
	configPath := filepath.Join(executorDir, configFileName)
	original := []byte("qemu:\n  memory_mib: 2048\n  cpus: 2\n")
	if err := os.WriteFile(configPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	invalid := 0

	if _, _, err := EnsureVMResources(
		Config{ExecutorDir: executorDir, CPUs: 2, MemoryMiB: 2048},
		VMResourceOverrides{CPUs: &invalid},
	); err == nil {
		t.Fatal("EnsureVMResources() error = nil, want invalid CPU error")
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(original) {
		t.Fatalf("config changed to %q after invalid CPU", content)
	}
}
