package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureAssetsAcceptsPresentFiles verifies complete asset sets pass validation.
func TestEnsureAssetsAcceptsPresentFiles(t *testing.T) {
	dir := t.TempDir()
	paths := AssetPaths{
		Image:  filepath.Join(dir, "alpine-podman.qcow2"),
		Kernel: filepath.Join(dir, "vmlinuz-virt"),
		Initrd: filepath.Join(dir, "initramfs-virt"),
		SSHKey: filepath.Join(dir, "id_ed25519"),
	}
	for _, path := range []string{paths.Image, paths.Kernel, paths.Initrd, paths.SSHKey, paths.publicKey()} {
		if err := os.WriteFile(path, []byte("asset"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := EnsureAssets(paths); err != nil {
		t.Fatalf("EnsureAssets() error = %v, want nil", err)
	}
}

// TestEnsureAssetsReportsMissingFiles verifies boot-time asset checks only report missing files.
func TestEnsureAssetsReportsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	err := EnsureAssets(AssetPaths{
		Image:  filepath.Join(dir, "alpine-podman.qcow2"),
		Kernel: filepath.Join(dir, "vmlinuz-virt"),
		Initrd: filepath.Join(dir, "initramfs-virt"),
		SSHKey: filepath.Join(dir, "id_ed25519"),
	})
	if err == nil || !strings.Contains(err.Error(), "generate and mount them before boot") {
		t.Fatalf("EnsureAssets() error = %v, want asset guidance", err)
	}
}
