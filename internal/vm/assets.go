package vm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	imageAsset        = "system.qcow2"
	kernelAsset       = "vmlinuz-virt"
	initrdAsset       = "initramfs-virt"
	sshKeyAsset       = "id_ed25519"
	sshPublicKeyAsset = "id_ed25519.pub"
)

var assetBaseURLBytes = []byte{
	104, 116, 116, 112, 115, 58, 47, 47, 101, 120, 97, 109, 112, 108, 101, 46,
	105, 110, 118, 97, 108, 105, 100, 47, 101, 120, 101, 99, 117, 116, 111,
	114, 45, 118, 109, 45, 97, 115, 115, 101, 116, 115,
}

type AssetPaths struct {
	Image        string
	Kernel       string
	Initrd       string
	SSHKey       string
	SSHPublicKey string
}

type assetFile struct {
	name string
	path string
	mode os.FileMode
}

type assetDownloadGroup struct {
	label string
	files []assetFile
}

// DownloadAssets downloads and replaces all required VM assets from the built-in base URL.
func DownloadAssets(ctx context.Context, paths AssetPaths, out io.Writer) error {
	return downloadAssetsFromBaseURL(ctx, string(assetBaseURLBytes), paths, out)
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

// missingAssets returns the required VM asset names whose files are absent.
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

// downloadAssetsFromBaseURL downloads every VM asset into temporary files, then replaces the local set.
func downloadAssetsFromBaseURL(ctx context.Context, baseURL string, paths AssetPaths, out io.Writer) error {
	groups := assetDownloadGroups(paths)
	if out != nil {
		fmt.Fprintln(out, "Downloading VM assets...")
	}
	for _, group := range groups {
		if out != nil {
			fmt.Fprintf(out, "Downloading %s...\n", group.label)
		}
		for _, asset := range group.files {
			if err := downloadAsset(ctx, baseURL, asset); err != nil {
				return err
			}
		}
	}
	if out != nil {
		fmt.Fprintln(out, "VM assets ready.")
	}
	return nil
}

// assetDownloadGroups returns the required assets with the concise labels shown during init.
func assetDownloadGroups(paths AssetPaths) []assetDownloadGroup {
	return []assetDownloadGroup{
		{
			label: imageAsset,
			files: []assetFile{{name: imageAsset, path: paths.Image, mode: 0o644}},
		},
		{
			label: kernelAsset,
			files: []assetFile{{name: kernelAsset, path: paths.Kernel, mode: 0o644}},
		},
		{
			label: initrdAsset,
			files: []assetFile{{name: initrdAsset, path: paths.Initrd, mode: 0o644}},
		},
		{
			label: "SSH keys",
			files: []assetFile{
				{name: sshKeyAsset, path: paths.SSHKey, mode: 0o600},
				{name: sshPublicKeyAsset, path: paths.publicKey(), mode: 0o644},
			},
		},
	}
}

// downloadAsset downloads one asset and atomically replaces the target file.
func downloadAsset(ctx context.Context, baseURL string, asset assetFile) error {
	sourceURL, err := buildAssetURL(baseURL, asset.name)
	if err != nil {
		return fmt.Errorf("download VM asset %s: %w", asset.name, err)
	}
	if err := os.MkdirAll(filepath.Dir(asset.path), 0o755); err != nil {
		return fmt.Errorf("download VM asset %s: %w", asset.name, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return fmt.Errorf("download VM asset %s: %w", asset.name, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download VM asset %s: %w", asset.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download VM asset %s: HTTP %d from %s", asset.name, resp.StatusCode, sourceURL)
	}

	temp, err := os.CreateTemp(filepath.Dir(asset.path), "."+asset.name+".*")
	if err != nil {
		return fmt.Errorf("download VM asset %s: %w", asset.name, err)
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()

	written, copyErr := io.Copy(temp, resp.Body)
	closeErr := temp.Close()
	if copyErr != nil {
		return fmt.Errorf("download VM asset %s: %w", asset.name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("download VM asset %s: %w", asset.name, closeErr)
	}
	if written == 0 {
		return fmt.Errorf("download VM asset %s: empty response from %s", asset.name, sourceURL)
	}
	if err := os.Chmod(tempPath, asset.mode); err != nil {
		return fmt.Errorf("download VM asset %s: %w", asset.name, err)
	}
	if err := os.Rename(tempPath, asset.path); err != nil {
		return fmt.Errorf("download VM asset %s: %w", asset.name, err)
	}
	if err := os.Chmod(asset.path, asset.mode); err != nil {
		return fmt.Errorf("download VM asset %s: %w", asset.name, err)
	}
	removeTemp = false
	return nil
}

// buildAssetURL joins the built-in base URL with an asset filename.
func buildAssetURL(baseURL, name string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return "", fmt.Errorf("asset base URL is empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("asset base URL must use http or https")
	}
	return trimmed + "/" + url.PathEscape(name), nil
}

// exists reports whether the path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
