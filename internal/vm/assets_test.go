package vm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
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
	paths := testAssetPaths(dir)
	for _, path := range []string{paths.Image, paths.Kernel, paths.Initrd, paths.SSHKey, paths.publicKey()} {
		if err := os.WriteFile(path, []byte("asset"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := EnsureAssets(paths); err != nil {
		t.Fatalf("EnsureAssets() error = %v, want nil", err)
	}
}

// TestEnsureAssetsReportsMissingFiles verifies boot-time checks report missing files.
func TestEnsureAssetsReportsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	err := EnsureAssets(testAssetPaths(dir))
	if err == nil || !strings.Contains(err.Error(), "generate and mount them before boot") {
		t.Fatalf("EnsureAssets() error = %v, want asset guidance", err)
	}
}

// TestDownloadAssetsOverlaysArchiveFiles verifies unknown files are accepted without clearing local state.
func TestDownloadAssetsOverlaysArchiveFiles(t *testing.T) {
	executorDir := t.TempDir()
	writeFile(t, filepath.Join(executorDir, configAsset), "local-config", 0o600)
	writeFile(t, filepath.Join(executorDir, podmanDiskAsset), "local-data", 0o600)
	writeFile(t, filepath.Join(executorDir, "local-only"), "keep", 0o644)

	archive := testArchive(t,
		tarEntry{name: imageAsset, content: "image", mode: 0o644},
		tarEntry{name: kernelAsset, content: "kernel", mode: 0o644},
		tarEntry{name: initrdAsset, content: "initrd", mode: 0o644},
		tarEntry{name: sshKeyAsset, content: "private", mode: 0o600},
		tarEntry{name: sshPublicKeyAsset, content: "public", mode: 0o644},
		tarEntry{name: "future-asset", content: "", mode: 0o640},
		tarEntry{name: configAsset, content: "archive-config", mode: 0o644},
		tarEntry{name: podmanDiskAsset, content: "archive-data", mode: 0o644},
	)
	requestedPath := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	var out bytes.Buffer
	err := DownloadAssets(context.Background(), AssetStorage{URL: server.URL + "/", Folder: "/releases/v1/"}, executorDir, AssetInstallOverlay, &out)
	if err != nil {
		t.Fatal(err)
	}
	if requestedPath != "/releases/v1/"+assetArchiveName {
		t.Fatalf("request path = %q, want archive path", requestedPath)
	}
	assertFileContent(t, filepath.Join(executorDir, imageAsset), "image")
	assertFileContent(t, filepath.Join(executorDir, "future-asset"), "")
	assertFileContent(t, filepath.Join(executorDir, "local-only"), "keep")
	assertFileContent(t, filepath.Join(executorDir, configAsset), "archive-config")
	assertFileContent(t, filepath.Join(executorDir, podmanDiskAsset), "archive-data")
	info, err := os.Stat(filepath.Join(executorDir, sshKeyAsset))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode = %v, want 0600", info.Mode().Perm())
	}
	for _, fragment := range []string{"Downloading VM assets...", "Extracting VM assets...", "VM assets ready."} {
		if !strings.Contains(out.String(), fragment) {
			t.Fatalf("output %q does not contain %q", out.String(), fragment)
		}
	}
}

// TestDownloadAssetsAcceptsRootMarkerAndDirectories verifies tar -C dir . archives are supported.
func TestDownloadAssetsAcceptsRootMarkerAndDirectories(t *testing.T) {
	executorDir := t.TempDir()
	archive := testArchive(t,
		tarEntry{name: "./", mode: 0o755, typeflag: tar.TypeDir},
		tarEntry{name: "./" + imageAsset, content: "image", mode: 0o644},
		tarEntry{name: "./qemu/", mode: 0o755, typeflag: tar.TypeDir},
		tarEntry{name: "./qemu/bin/", mode: 0o755, typeflag: tar.TypeDir},
		tarEntry{name: "./qemu/bin/qemu-system-x86_64", content: "qemu", mode: 0o755},
	)
	server := archiveServer(archive)
	defer server.Close()

	if err := DownloadAssets(context.Background(), AssetStorage{URL: server.URL, Folder: "assets"}, executorDir, AssetInstallOverlay, nil); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(executorDir, imageAsset), "image")
	qemu := filepath.Join(executorDir, "qemu", "bin", "qemu-system-x86_64")
	assertFileContent(t, qemu, "qemu")
	info, err := os.Stat(qemu)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("QEMU mode = %v, want 0755", info.Mode().Perm())
	}
}

// TestDownloadAssetsRejectsExistingSymlinkDirectory verifies nested installs stay inside the executor directory.
func TestDownloadAssetsRejectsExistingSymlinkDirectory(t *testing.T) {
	executorDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(executorDir, "qemu")); err != nil {
		t.Fatal(err)
	}
	archive := testArchive(t,
		tarEntry{name: "qemu/bin/", mode: 0o755, typeflag: tar.TypeDir},
		tarEntry{name: "qemu/bin/qemu-system-x86_64", content: "qemu", mode: 0o755},
	)
	server := archiveServer(archive)
	defer server.Close()

	err := DownloadAssets(context.Background(), AssetStorage{URL: server.URL, Folder: "assets"}, executorDir, AssetInstallOverlay, nil)
	if err == nil || !strings.Contains(err.Error(), "existing non-directory") {
		t.Fatalf("DownloadAssets() error = %v, want symlink conflict", err)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "bin", "qemu-system-x86_64")); !os.IsNotExist(err) {
		t.Fatalf("outside file stat error = %v, want no file", err)
	}
}

// TestDownloadAssetsCleanRemovesOldState verifies reset mode preserves only config before install.
func TestDownloadAssetsCleanRemovesOldState(t *testing.T) {
	executorDir := t.TempDir()
	writeFile(t, filepath.Join(executorDir, configAsset), "local-config", 0o600)
	writeFile(t, filepath.Join(executorDir, podmanDiskAsset), "old-data", 0o600)
	writeFile(t, filepath.Join(executorDir, "stale"), "remove", 0o644)
	if err := os.Mkdir(filepath.Join(executorDir, "old-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := testArchive(t,
		tarEntry{name: imageAsset, content: "new-image", mode: 0o644},
		tarEntry{name: configAsset, content: "ignored", mode: 0o644},
		tarEntry{name: podmanDiskAsset, content: "ignored", mode: 0o644},
		tarEntry{name: "future", content: "new", mode: 0o644},
	)
	server := archiveServer(archive)
	defer server.Close()

	if err := DownloadAssets(context.Background(), AssetStorage{URL: server.URL, Folder: "assets"}, executorDir, AssetInstallClean, nil); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(executorDir, configAsset), "ignored")
	assertFileContent(t, filepath.Join(executorDir, podmanDiskAsset), "ignored")
	assertFileContent(t, filepath.Join(executorDir, imageAsset), "new-image")
	assertFileContent(t, filepath.Join(executorDir, "future"), "new")
	for _, removed := range []string{"stale", "old-dir"} {
		if _, err := os.Stat(filepath.Join(executorDir, removed)); !os.IsNotExist(err) {
			t.Fatalf("%s stat error = %v, want removed", removed, err)
		}
	}
}

// TestDownloadAssetsFailureLeavesCleanTargetUntouched verifies preparation precedes reset cleanup.
func TestDownloadAssetsFailureLeavesCleanTargetUntouched(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler http.Handler
	}{
		{name: "http", handler: http.NotFoundHandler()},
		{name: "invalid gzip", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not gzip")) })},
	} {
		t.Run(test.name, func(t *testing.T) {
			executorDir := t.TempDir()
			writeFile(t, filepath.Join(executorDir, "existing"), "keep", 0o644)
			server := httptest.NewServer(test.handler)
			defer server.Close()

			err := DownloadAssets(context.Background(), AssetStorage{URL: server.URL, Folder: "assets"}, executorDir, AssetInstallClean, nil)
			if err == nil {
				t.Fatal("DownloadAssets() error = nil, want preparation error")
			}
			assertFileContent(t, filepath.Join(executorDir, "existing"), "keep")
		})
	}
}

// TestDownloadAssetsAcceptsUncheckedPaths verifies archive member paths are not validated.
func TestDownloadAssetsAcceptsUncheckedPaths(t *testing.T) {
	executorDir := t.TempDir()
	archive := testArchive(t, tarEntry{name: "nested/../future-asset", content: "asset", mode: 0o644})
	server := archiveServer(archive)
	defer server.Close()

	if err := DownloadAssets(context.Background(), AssetStorage{URL: server.URL, Folder: "assets"}, executorDir, AssetInstallOverlay, nil); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(executorDir, "future-asset"), "asset")
}

// TestDownloadAssetsInstallsSymbolicLinks verifies archive symlinks remain symlinks after installation.
func TestDownloadAssetsInstallsSymbolicLinks(t *testing.T) {
	executorDir := t.TempDir()
	archive := testArchive(t,
		tarEntry{name: "lib/target.so.1", content: "library", mode: 0o644},
		tarEntry{name: "lib/target.so", mode: 0o777, typeflag: tar.TypeSymlink, linkname: "target.so.1"},
	)
	server := archiveServer(archive)
	defer server.Close()

	if err := DownloadAssets(context.Background(), AssetStorage{URL: server.URL, Folder: "assets"}, executorDir, AssetInstallOverlay, nil); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(executorDir, "lib", "target.so")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s mode = %v, want symbolic link", link, info.Mode())
	}
	linkTarget, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if linkTarget != "target.so.1" {
		t.Fatalf("%s target = %q, want %q", link, linkTarget, "target.so.1")
	}
	assertFileContent(t, link, "library")
}

// TestDownloadAssetsRejectsUnsupportedEntries verifies other special tar entries stay unsupported.
func TestDownloadAssetsRejectsUnsupportedEntries(t *testing.T) {
	executorDir := t.TempDir()
	archive := testArchive(t, tarEntry{name: "pipe", mode: 0o644, typeflag: tar.TypeFifo})
	server := archiveServer(archive)
	defer server.Close()

	err := DownloadAssets(context.Background(), AssetStorage{URL: server.URL, Folder: "assets"}, executorDir, AssetInstallOverlay, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported entry") {
		t.Fatalf("DownloadAssets() error = %v, want unsupported entry", err)
	}
}

// TestBuildAssetArchiveURLValidatesStorage verifies URL construction and required fields.
func TestBuildAssetArchiveURLValidatesStorage(t *testing.T) {
	got, err := buildAssetArchiveURL(AssetStorage{URL: "https://storage.example/base/", Folder: "/releases/current/"})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://storage.example/base/releases/current/" + assetArchiveName
	if got != want {
		t.Fatalf("buildAssetArchiveURL() = %q, want %q", got, want)
	}
	for _, storage := range []AssetStorage{
		{Folder: "assets"},
		{URL: "https://storage.example"},
		{URL: "file:///tmp", Folder: "assets"},
		{URL: "https://storage.example", Folder: "../assets"},
	} {
		if _, err := buildAssetArchiveURL(storage); err == nil {
			t.Fatalf("buildAssetArchiveURL(%+v) error = nil, want validation error", storage)
		}
	}
}

type tarEntry struct {
	name     string
	content  string
	mode     int64
	typeflag byte
	linkname string
}

func testArchive(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Size:     int64(len(entry.content)),
			Typeflag: typeflag,
			Linkname: entry.linkname,
		}
		if typeflag != tar.TypeReg && typeflag != tar.TypeRegA {
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := tarWriter.Write([]byte(entry.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func archiveServer(archive []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
}

func testAssetPaths(dir string) AssetPaths {
	return AssetPaths{
		Image:  filepath.Join(dir, imageAsset),
		Kernel: filepath.Join(dir, kernelAsset),
		Initrd: filepath.Join(dir, initrdAsset),
		SSHKey: filepath.Join(dir, sshKeyAsset),
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("%s content = %q, want %q", path, content, want)
	}
}
