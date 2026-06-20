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

type DownloadOptions struct {
	Mirror    string
	BasicAuth string
}

// EnsureAssets verifies that all required VM assets are already available locally.
func EnsureAssets(paths AssetPaths) error {
	missing := missingAssets(paths)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("VM assets are missing (%s); run `executor download` first", strings.Join(missing, ", "))
}

// DownloadAssets downloads all required VM assets from the configured mirror.
func DownloadAssets(ctx context.Context, paths AssetPaths, options DownloadOptions) error {
	if strings.TrimSpace(options.Mirror) == "" {
		return fmt.Errorf("asset mirror must be set")
	}
	client := http.DefaultClient
	assets := []struct {
		name        string
		destination string
		mode        os.FileMode
	}{
		{name: imageAsset, destination: paths.Image, mode: 0o644},
		{name: kernelAsset, destination: paths.Kernel, mode: 0o644},
		{name: initrdAsset, destination: paths.Initrd, mode: 0o644},
		{name: sshKeyAsset, destination: paths.SSHKey, mode: 0o600},
		{name: sshPublicKeyAsset, destination: paths.publicKey(), mode: 0o644},
	}
	for _, asset := range assets {
		assetURL, err := url.JoinPath(options.Mirror, asset.name)
		if err != nil {
			return fmt.Errorf("build asset URL for %s: %w", asset.name, err)
		}
		if err := downloadFile(ctx, client, assetURL, asset.destination, asset.mode, options.BasicAuth); err != nil {
			return err
		}
	}
	return nil
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

func downloadFile(ctx context.Context, client *http.Client, sourceURL, destination string, mode os.FileMode, basicAuth string) error {
	response, err := assetRequest(ctx, client, sourceURL, basicAuth)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	return writeDestination(destination, mode, func(output *os.File) error {
		_, err := io.Copy(output, response.Body)
		return err
	})
}

func assetRequest(ctx context.Context, client *http.Client, sourceURL, basicAuth string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	if basicAuth != "" {
		username, password, ok := strings.Cut(basicAuth, ":")
		if !ok {
			return nil, fmt.Errorf("--basic-auth must use user:password")
		}
		request.SetBasicAuth(username, password)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		_ = response.Body.Close()
		return nil, fmt.Errorf("download %s failed: %s", sourceURL, response.Status)
	}
	return response, nil
}

func writeDestination(destination string, mode os.FileMode, write func(*os.File) error) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if err := write(temp); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

// exists reports whether the path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
