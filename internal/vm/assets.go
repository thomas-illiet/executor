package vm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	imageAsset        = "alpine-podman.qcow2"
	kernelAsset       = "vmlinuz-virt"
	initrdAsset       = "initramfs-virt"
	sshKeyAsset       = "id_ed25519"
	sshPublicKeyAsset = "id_ed25519.pub"
)

type AssetPaths struct {
	Image        string
	Kernel       string
	Initrd       string
	SSHKey       string
	SSHPublicKey string
}

// EnsureAssets verifies that all required VM assets are already available locally.
func EnsureAssets(paths AssetPaths) error {
	missing := missingAssets(paths)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("VM assets are missing (%s); generate and mount them before boot", strings.Join(missing, ", "))
}

// publicKey returns the SSH public key path next to the configured private key.
func (paths AssetPaths) publicKey() string {
	if paths.SSHPublicKey != "" {
		return paths.SSHPublicKey
	}
	return filepath.Join(filepath.Dir(paths.SSHKey), sshPublicKeyAsset)
}

func missingAssets(paths AssetPaths) []string {
	required := []struct {
		name string
		path string
	}{
		{name: imageAsset, path: paths.Image},
		{name: kernelAsset, path: paths.Kernel},
		{name: initrdAsset, path: paths.Initrd},
		{name: sshKeyAsset, path: paths.SSHKey},
		{name: sshPublicKeyAsset, path: paths.publicKey()},
	}
	var missing []string
	for _, asset := range required {
		if !exists(asset.path) {
			missing = append(missing, asset.name)
		}
	}
	return missing
}

// exists reports whether the path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
