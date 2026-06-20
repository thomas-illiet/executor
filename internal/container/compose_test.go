package container

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadPortsSupportsStringAndMappingPorts verifies both compose port formats.
func TestLoadPortsSupportsStringAndMappingPorts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	content := []byte(`
services:
  web:
    image: nginx
    ports:
      - "8080:80"
      - target: 443
        published: "8443"
        host_ip: "127.0.0.1"
        protocol: tcp
  worker:
    image: busybox
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	mappings, warnings, err := LoadPorts(path, func() (int, error) { return 50000, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if len(mappings) != 2 {
		t.Fatalf("mappings = %#v", mappings)
	}
	if mappings[0].HostPort != 8080 || mappings[0].ContainerPort != 80 {
		t.Fatalf("first mapping = %#v", mappings[0])
	}
	if mappings[1].IP != "127.0.0.1" || mappings[1].HostPort != 8443 || mappings[1].ContainerPort != 443 {
		t.Fatalf("second mapping = %#v", mappings[1])
	}
}

// TestResolveFileFindsComposeFile verifies compose file discovery in a directory.
func TestResolveFileFindsComposeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveFile(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("ResolveFile() = %q", got)
	}
}

// TestLoadPortsRejectsLongSyntaxWithoutPublishedPort verifies ambiguous ports fail.
func TestLoadPortsRejectsLongSyntaxWithoutPublishedPort(t *testing.T) {
	path := writeCompose(t, `
services:
  web:
    image: nginx
    ports:
      - target: 80
`)
	_, _, err := LoadPorts(path, nil)
	if err == nil || !strings.Contains(err.Error(), "published port is required") {
		t.Fatalf("LoadPorts() error = %v, want missing published port error", err)
	}
}

// TestLoadPortsRejectsInvalidLongSyntaxProtocol verifies protocols are strict.
func TestLoadPortsRejectsInvalidLongSyntaxProtocol(t *testing.T) {
	path := writeCompose(t, `
services:
  web:
    image: nginx
    ports:
      - target: 80
        published: 8080
        protocol: sctp
`)
	_, _, err := LoadPorts(path, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported protocol") {
		t.Fatalf("LoadPorts() error = %v, want protocol error", err)
	}
}

// TestLoadPortsRejectsRanges verifies Compose range syntax is explicitly unsupported.
func TestLoadPortsRejectsRanges(t *testing.T) {
	path := writeCompose(t, `
services:
  web:
    image: nginx
    ports:
      - "8080-8082:80-82"
`)
	_, _, err := LoadPorts(path, nil)
	if err == nil || !strings.Contains(err.Error(), "port ranges are not supported") {
		t.Fatalf("LoadPorts() error = %v, want range rejection", err)
	}
}

func writeCompose(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
