package vm

import "testing"

// TestParseLine verifies boot file credential parsing.
func TestParseLine(t *testing.T) {
	got := ParseLine("ARTIFACTORY_API_KEY secret-key user-id")
	if got.APIKey != "secret-key" {
		t.Fatalf("APIKey = %q", got.APIKey)
	}
	if got.UID != "user-id" {
		t.Fatalf("UID = %q", got.UID)
	}
}

// TestRegistryAuth verifies registry auth token encoding.
func TestRegistryAuth(t *testing.T) {
	got := (Credentials{UID: "alice", APIKey: "secret"}).RegistryAuth()
	if got != "YWxpY2U6c2VjcmV0" {
		t.Fatalf("RegistryAuth() = %q", got)
	}
}
