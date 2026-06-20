package container

import (
	"strings"
	"testing"
)

// TestParsePublishContainerOnlyAllocatesHostPort verifies host port allocation.
func TestParsePublishContainerOnlyAllocatesHostPort(t *testing.T) {
	allocate := func() (int, error) { return 49152, nil }
	mapping, publishArg, err := ParsePublish("80", allocate)
	if err != nil {
		t.Fatal(err)
	}
	if mapping.HostPort != 49152 || mapping.ContainerPort != 80 {
		t.Fatalf("mapping = %#v", mapping)
	}
	if publishArg != "49152:80" {
		t.Fatalf("publish arg = %q", publishArg)
	}
}

// TestParsePublishHostContainer verifies host, container, and protocol parsing.
func TestParsePublishHostContainer(t *testing.T) {
	mapping, publishArg, err := ParsePublish("127.0.0.1:8080:80/udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if mapping.IP != "127.0.0.1" || mapping.HostPort != 8080 || mapping.ContainerPort != 80 || mapping.Protocol != "udp" {
		t.Fatalf("mapping = %#v", mapping)
	}
	if publishArg != "127.0.0.1:8080:80/udp" {
		t.Fatalf("publish arg = %q", publishArg)
	}
}

// TestParsePublishRejectsRanges verifies unsupported ranges return an error.
func TestParsePublishRejectsRanges(t *testing.T) {
	_, _, err := ParsePublish("8080-8082:80", nil)
	if err == nil || !strings.Contains(err.Error(), "port ranges are not supported") {
		t.Fatalf("ParsePublish() error = %v, want range rejection", err)
	}
}

// TestParseComposePortRequiresPublishedHostPort verifies Compose ports are strict.
func TestParseComposePortRequiresPublishedHostPort(t *testing.T) {
	_, err := ParseComposePort("80", nil)
	if err == nil || !strings.Contains(err.Error(), "explicit published host port") {
		t.Fatalf("ParseComposePort() error = %v, want explicit published port error", err)
	}
}

// TestParseComposePortRejectsRanges verifies Compose ranges fail clearly.
func TestParseComposePortRejectsRanges(t *testing.T) {
	_, err := ParseComposePort("8080-8082:80-82", nil)
	if err == nil || !strings.Contains(err.Error(), "port ranges are not supported") {
		t.Fatalf("ParseComposePort() error = %v, want range rejection", err)
	}
}
