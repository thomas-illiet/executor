package vm

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDownloadAssetsDownloadsAllFiles verifies VM assets are downloaded and chmodded.
func TestDownloadAssetsDownloadsAllFiles(t *testing.T) {
	diskImage := append(bytes.Repeat([]byte{0}, 4096), []byte("podman-in-container")...)
	assets := map[string][]byte{
		"/" + imageAsset:        diskImage,
		"/" + kernelAsset:       []byte("kernel"),
		"/" + initrdAsset:       []byte("initrd"),
		"/" + sshKeyAsset:       []byte("private-key"),
		"/" + sshPublicKeyAsset: []byte("public-key"),
	}
	var authChecks int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "user" || password != "password" {
			t.Fatalf("BasicAuth() = %q/%q/%v, want user/password/true", username, password, ok)
		}
		authChecks++
		content, ok := assets[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(content)
	}))
	defer server.Close()

	dir := t.TempDir()
	paths := AssetPaths{
		Image:  filepath.Join(dir, "alpine-podman.qcow2"),
		Kernel: filepath.Join(dir, "vmlinuz-virt"),
		Initrd: filepath.Join(dir, "initramfs-virt"),
		SSHKey: filepath.Join(dir, "id_ed25519"),
	}
	if err := DownloadAssets(context.Background(), paths, DownloadOptions{Mirror: server.URL, BasicAuth: "user:password"}); err != nil {
		t.Fatal(err)
	}
	if authChecks != len(assets) {
		t.Fatalf("auth checks = %d, want %d", authChecks, len(assets))
	}

	assertFileContent(t, paths.Image, diskImage)
	assertFileContent(t, paths.Kernel, []byte("kernel"))
	assertFileContent(t, paths.Initrd, []byte("initrd"))
	assertFileContent(t, paths.SSHKey, []byte("private-key"))
	assertFileContent(t, filepath.Join(dir, "id_ed25519.pub"), []byte("public-key"))
	assertFileMode(t, paths.Image, 0o644)
	assertFileMode(t, paths.Kernel, 0o644)
	assertFileMode(t, paths.Initrd, 0o644)
	assertFileMode(t, paths.SSHKey, 0o600)
	assertFileMode(t, filepath.Join(dir, "id_ed25519.pub"), 0o644)
}

// TestDownloadAssetsReturnsHTTPError verifies failed downloads name the failed request.
func TestDownloadAssetsReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	dir := t.TempDir()
	err := DownloadAssets(context.Background(), AssetPaths{
		Image:  filepath.Join(dir, "alpine-podman.qcow2"),
		Kernel: filepath.Join(dir, "vmlinuz-virt"),
		Initrd: filepath.Join(dir, "initramfs-virt"),
		SSHKey: filepath.Join(dir, "id_ed25519"),
	}, DownloadOptions{Mirror: server.URL})
	if err == nil || !strings.Contains(err.Error(), "404 Not Found") {
		t.Fatalf("DownloadAssets() error = %v, want 404 error", err)
	}
}

// TestEnsureAssetsReportsMissingDownloadCommand verifies boot-time asset checks only report missing files.
func TestEnsureAssetsReportsMissingDownloadCommand(t *testing.T) {
	dir := t.TempDir()
	err := EnsureAssets(AssetPaths{
		Image:  filepath.Join(dir, "alpine-podman.qcow2"),
		Kernel: filepath.Join(dir, "vmlinuz-virt"),
		Initrd: filepath.Join(dir, "initramfs-virt"),
		SSHKey: filepath.Join(dir, "id_ed25519"),
	})
	if err == nil || !strings.Contains(err.Error(), "run `executor download` first") {
		t.Fatalf("EnsureAssets() error = %v, want download guidance", err)
	}
}

func assertFileContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s content = %q, want %q", path, got, want)
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %#o, want %#o", path, got, want)
	}
}
