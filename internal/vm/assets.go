package vm

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	imageAsset        = "system.qcow2"
	kernelAsset       = "vmlinuz-virt"
	initrdAsset       = "initramfs-virt"
	sshKeyAsset       = "id_ed25519"
	sshPublicKeyAsset = "id_ed25519.pub"
	assetArchiveName  = "executor-vm-assets.tar.gz"
	configAsset       = "config.yaml"
	podmanDiskAsset   = "data.qcow2"
)

// AssetInstallMode controls whether archive files overlay or replace local state.
type AssetInstallMode int

const (
	AssetInstallOverlay AssetInstallMode = iota
	AssetInstallClean
)

// AssetStorage identifies the remote folder containing the VM asset archive.
type AssetStorage struct {
	URL    string
	Folder string
}

type AssetPaths struct {
	Image        string
	Kernel       string
	Initrd       string
	SSHKey       string
	SSHPublicKey string
}

// DownloadAssets downloads, prepares, and installs the VM asset archive.
func DownloadAssets(ctx context.Context, storage AssetStorage, executorDir string, mode AssetInstallMode, out io.Writer) error {
	if out != nil {
		fmt.Fprintln(out, "Downloading VM assets...")
	}
	stageDir, err := prepareAssetArchive(ctx, storage, executorDir, out)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageDir)

	if err := installPreparedAssets(stageDir, executorDir, mode); err != nil {
		return fmt.Errorf("install VM assets: %w", err)
	}
	if out != nil {
		fmt.Fprintln(out, "VM assets ready.")
	}
	return nil
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

// prepareAssetArchive downloads and safely expands the archive into a staging directory.
func prepareAssetArchive(ctx context.Context, storage AssetStorage, executorDir string, out io.Writer) (string, error) {
	archiveURL, err := buildAssetArchiveURL(storage)
	if err != nil {
		return "", fmt.Errorf("download VM assets archive: %w", err)
	}
	parentDir := filepath.Dir(executorDir)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return "", fmt.Errorf("download VM assets archive: %w", err)
	}

	archive, err := os.CreateTemp(parentDir, ".executor-vm-assets-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("download VM assets archive: %w", err)
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		_ = archive.Close()
		return "", fmt.Errorf("download VM assets archive: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = archive.Close()
		return "", fmt.Errorf("download VM assets archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = archive.Close()
		return "", fmt.Errorf("download VM assets archive: HTTP %d from %s", resp.StatusCode, archiveURL)
	}
	if _, err := io.Copy(archive, resp.Body); err != nil {
		_ = archive.Close()
		return "", fmt.Errorf("download VM assets archive: %w", err)
	}
	if err := archive.Close(); err != nil {
		return "", fmt.Errorf("download VM assets archive: %w", err)
	}
	if out != nil {
		fmt.Fprintln(out, "Extracting VM assets...")
	}

	stageDir, err := os.MkdirTemp(parentDir, ".executor-vm-assets-stage-*")
	if err != nil {
		return "", fmt.Errorf("extract VM assets archive: %w", err)
	}
	if err := extractAssetArchive(archivePath, stageDir); err != nil {
		os.RemoveAll(stageDir)
		return "", err
	}
	return stageDir, nil
}

// extractAssetArchive expands regular root files while rejecting unsafe tar entries.
func extractAssetArchive(archivePath, stageDir string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("extract VM assets archive: %w", err)
	}
	defer archive.Close()

	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("extract VM assets archive: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("extract VM assets archive: %w", err)
		}
		name, err := safeRootAssetName(header.Name)
		if err != nil {
			return fmt.Errorf("extract VM assets archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return fmt.Errorf("extract VM assets archive: unsupported entry %q", header.Name)
		}
		if isProtectedAsset(name) {
			continue
		}
		if err := extractAssetFile(tarReader, filepath.Join(stageDir, name), header.FileInfo().Mode().Perm()); err != nil {
			return fmt.Errorf("extract VM assets archive %s: %w", name, err)
		}
	}
}

// safeRootAssetName accepts only plain filenames at the archive root.
func safeRootAssetName(name string) (string, error) {
	if name == "" || name == "." || name == ".." || path.IsAbs(name) || strings.Contains(name, "/") {
		return "", fmt.Errorf("unsafe archive entry %q", name)
	}
	return name, nil
}

// extractAssetFile writes one staged archive member with ordinary permission bits only.
func extractAssetFile(reader io.Reader, target string, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(target, mode)
}

// installPreparedAssets applies staged files over local state or after a clean reset.
func installPreparedAssets(stageDir, executorDir string, mode AssetInstallMode) error {
	if err := os.MkdirAll(executorDir, 0o755); err != nil {
		return err
	}
	if mode == AssetInstallClean {
		if err := cleanExecutorDir(executorDir); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("prepared asset %q is not a regular file", entry.Name())
		}
		if err := copyPreparedAsset(filepath.Join(stageDir, entry.Name()), filepath.Join(executorDir, entry.Name()), executorDir); err != nil {
			return err
		}
	}
	return nil
}

// cleanExecutorDir removes all resettable state while preserving the user config.
func cleanExecutorDir(executorDir string) error {
	entries, err := os.ReadDir(executorDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == configAsset {
			continue
		}
		if err := os.RemoveAll(filepath.Join(executorDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copyPreparedAsset replaces one destination through a temporary file on its filesystem.
func copyPreparedAsset(source, target, executorDir string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(executorDir, ".asset-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(temp, input); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Rename(tempPath, target); err != nil {
		return err
	}
	return nil
}

// buildAssetArchiveURL joins the configured server, remote folder, and archive name.
func buildAssetArchiveURL(storage AssetStorage) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(storage.URL), "/")
	folder := strings.Trim(strings.TrimSpace(storage.Folder), "/")
	if base == "" {
		return "", fmt.Errorf("storage.url must be set")
	}
	if folder == "" {
		return "", fmt.Errorf("storage.folder must be set")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("storage.url must use http or https")
	}
	segments := strings.Split(folder, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("storage.folder must be a relative remote folder")
		}
	}
	return parsed.JoinPath(append(segments, assetArchiveName)...).String(), nil
}

// isProtectedAsset reports files that an archive must never install.
func isProtectedAsset(name string) bool {
	return name == configAsset || name == podmanDiskAsset
}

// exists reports whether the path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
