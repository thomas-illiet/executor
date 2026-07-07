package vm

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureAssetsAcceptsPresentFiles verifies complete asset sets pass validation.
func TestEnsureAssetsAcceptsPresentFiles(t *testing.T) {
	dir := t.TempDir()
	paths := AssetPaths{
		Image:  filepath.Join(dir, "system.qcow2"),
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
		Image:  filepath.Join(dir, "system.qcow2"),
		Kernel: filepath.Join(dir, "vmlinuz-virt"),
		Initrd: filepath.Join(dir, "initramfs-virt"),
		SSHKey: filepath.Join(dir, "id_ed25519"),
	})
	if err == nil || !strings.Contains(err.Error(), "generate and mount them before boot") {
		t.Fatalf("EnsureAssets() error = %v, want asset guidance", err)
	}
}

// TestDownloadAssetsFromBaseURLReplacesAssets verifies init-time asset downloads replace the full local set.
func TestDownloadAssetsFromBaseURLReplacesAssets(t *testing.T) {
	dir := t.TempDir()
	paths := testAssetPaths(dir)
	for _, path := range []string{paths.Image, paths.Kernel, paths.Initrd, paths.SSHKey, paths.publicKey()} {
		if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		requests[name]++
		_, _ = w.Write([]byte("new-" + name))
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := downloadAssetsFromBaseURL(context.Background(), server.URL, paths, &out); err != nil {
		t.Fatal(err)
	}

	for _, asset := range []struct {
		name string
		path string
		want string
	}{
		{name: imageAsset, path: paths.Image, want: "new-" + imageAsset},
		{name: kernelAsset, path: paths.Kernel, want: "new-" + kernelAsset},
		{name: initrdAsset, path: paths.Initrd, want: "new-" + initrdAsset},
		{name: sshKeyAsset, path: paths.SSHKey, want: "new-" + sshKeyAsset},
		{name: sshPublicKeyAsset, path: paths.publicKey(), want: "new-" + sshPublicKeyAsset},
	} {
		content, err := os.ReadFile(asset.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != asset.want {
			t.Fatalf("%s content = %q, want %q", asset.name, content, asset.want)
		}
		if requests[asset.name] != 1 {
			t.Fatalf("%s requests = %d, want 1", asset.name, requests[asset.name])
		}
	}
	info, err := os.Stat(paths.SSHKey)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode = %v, want 0600", info.Mode().Perm())
	}
	output := out.String()
	for _, fragment := range []string{
		"Downloading VM assets...",
		"Downloading system.qcow2...",
		"Downloading vmlinuz-virt...",
		"Downloading initramfs-virt...",
		"Downloading SSH keys...",
		"VM assets ready.",
	} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("output %q does not contain %q", output, fragment)
		}
	}
}

// TestDownloadAssetsFromBaseURLReportsHTTPError verifies HTTP failures include the asset name and status.
func TestDownloadAssetsFromBaseURLReportsHTTPError(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	err := downloadAssetsFromBaseURL(context.Background(), server.URL, testAssetPaths(dir), nil)
	if err == nil || !strings.Contains(err.Error(), "download VM asset system.qcow2: HTTP 404") {
		t.Fatalf("downloadAssetsFromBaseURL() error = %v, want HTTP 404 asset error", err)
	}
}

// TestDownloadAssetsFromBaseURLRejectsEmptyResponse verifies empty assets fail before replacement.
func TestDownloadAssetsFromBaseURLRejectsEmptyResponse(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := downloadAssetsFromBaseURL(context.Background(), server.URL, testAssetPaths(dir), nil)
	if err == nil || !strings.Contains(err.Error(), "download VM asset system.qcow2: empty response") {
		t.Fatalf("downloadAssetsFromBaseURL() error = %v, want empty response error", err)
	}
}

// TestDownloadAssetsFromBaseURLReportsNetworkError verifies transport failures are surfaced clearly.
func TestDownloadAssetsFromBaseURLReportsNetworkError(t *testing.T) {
	dir := t.TempDir()
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})}
	t.Cleanup(func() {
		http.DefaultClient = previous
	})

	err := downloadAssetsFromBaseURL(context.Background(), "https://example.invalid", testAssetPaths(dir), nil)
	if err == nil || !strings.Contains(err.Error(), "download VM asset system.qcow2:") || !strings.Contains(err.Error(), "network down") {
		t.Fatalf("downloadAssetsFromBaseURL() error = %v, want network asset error", err)
	}
}

func testAssetPaths(dir string) AssetPaths {
	return AssetPaths{
		Image:  filepath.Join(dir, imageAsset),
		Kernel: filepath.Join(dir, kernelAsset),
		Initrd: filepath.Join(dir, initrdAsset),
		SSHKey: filepath.Join(dir, sshKeyAsset),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
