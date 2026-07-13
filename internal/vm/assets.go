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
	"sort"
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

// extractAssetArchive expands regular files and directories while rejecting unsafe tar entries.
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
	directoryModes := make(map[string]os.FileMode)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return applyDirectoryModes(directoryModes)
		}
		if err != nil {
			return fmt.Errorf("extract VM assets archive: %w", err)
		}
		name, err := safeAssetPath(header.Name)
		if err != nil {
			return fmt.Errorf("extract VM assets archive: %w", err)
		}

		target := filepath.Join(stageDir, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if name == "." {
				continue
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("extract VM assets archive %s: %w", name, err)
			}
			directoryModes[target] = archiveMode(header.FileInfo().Mode().Perm(), 0o755)
		case tar.TypeReg, tar.TypeRegA:
			if name == "." {
				return fmt.Errorf("extract VM assets archive: unsupported entry %q", header.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("extract VM assets archive %s: %w", name, err)
			}
			if err := extractAssetFile(tarReader, target, header.FileInfo().Mode().Perm()); err != nil {
				return fmt.Errorf("extract VM assets archive %s: %w", name, err)
			}
		default:
			return fmt.Errorf("extract VM assets archive: unsupported entry %q", header.Name)
		}
	}
}

// safeAssetPath accepts relative tar paths, including the conventional leading "./".
func safeAssetPath(name string) (string, error) {
	if name == "" || path.IsAbs(name) || filepath.IsAbs(filepath.FromSlash(name)) || strings.Contains(name, `\`) {
		return "", fmt.Errorf("unsafe archive entry %q", name)
	}

	parts := strings.Split(name, "/")
	for len(parts) > 0 && parts[0] == "." {
		parts = parts[1:]
	}
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return ".", nil
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("unsafe archive entry %q", name)
		}
	}
	return path.Join(parts...), nil
}

// archiveMode supplies a usable default when an archive omits permission bits.
func archiveMode(mode, fallback os.FileMode) os.FileMode {
	if mode == 0 {
		return fallback
	}
	return mode
}

// applyDirectoryModes restores directory permissions after all children are extracted.
func applyDirectoryModes(modes map[string]os.FileMode) error {
	paths := make([]string, 0, len(modes))
	for path := range modes {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		return strings.Count(paths[i], string(os.PathSeparator)) > strings.Count(paths[j], string(os.PathSeparator))
	})
	for _, path := range paths {
		if err := os.Chmod(path, modes[path]); err != nil {
			return err
		}
	}
	return nil
}

// extractAssetFile writes one staged archive member with ordinary permission bits only.
func extractAssetFile(reader io.Reader, target string, mode os.FileMode) error {
	mode = archiveMode(mode, 0o644)
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

	directoryModes := make(map[string]os.FileMode)
	err := filepath.WalkDir(stageDir, func(source string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(stageDir, source)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(executorDir, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if err := ensureInstallDirectory(executorDir, relative); err != nil {
				return err
			}
			directoryModes[target] = archiveMode(info.Mode().Perm(), 0o755)
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("prepared asset %q is not a regular file", relative)
		}
		if err := ensureInstallDirectory(executorDir, filepath.Dir(relative)); err != nil {
			return err
		}
		return copyPreparedAsset(source, target, filepath.Dir(target))
	})
	if err != nil {
		return err
	}
	return applyDirectoryModes(directoryModes)
}

// ensureInstallDirectory creates a nested destination without following existing symlinks.
func ensureInstallDirectory(executorDir, relative string) error {
	if relative == "." || relative == "" {
		return nil
	}
	current := executorDir
	for _, part := range strings.Split(filepath.Clean(relative), string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("prepared asset directory %q conflicts with an existing non-directory", relative)
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

// exists reports whether the path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
