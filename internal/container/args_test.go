package container

import "testing"

// TestCommandAddsPodmanBuildLayersFlag verifies Podman build disables layers.
func TestCommandAddsPodmanBuildLayersFlag(t *testing.T) {
	got := Command([]string{"build", "."})
	if got[len(got)-1] != "--layers=false" {
		t.Fatalf("Command() = %#v", got)
	}
}

// TestCommandWithPrefixPreservesRootlessEnvironment verifies env prefixes are kept.
func TestCommandWithPrefixPreservesRootlessEnvironment(t *testing.T) {
	got := CommandWithPrefix([]string{"env", "XDG_RUNTIME_DIR=/run/user/1000", "podman"}, []string{"ps"})
	want := []string{"env", "XDG_RUNTIME_DIR=/run/user/1000", "podman", "ps"}
	if len(got) != len(want) {
		t.Fatalf("Command() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Command() = %#v, want %#v", got, want)
		}
	}
}
