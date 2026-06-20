package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAlpineUnlocksCoderForPublicKeySSH(t *testing.T) {
	scriptPath := filepath.Join("..", "..", "scripts", "build-alpine.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	for _, fragment := range []string{
		"OpenSSH refuses public-key logins",
		"coder:*:",
		"chmod 4755",
		"newuidmap",
		"newgidmap",
		"chmod 1777",
		"/var/tmp",
		"/dev/net/tun",
		"mknod /dev/net/tun c 10 200",
	} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("%s does not contain %q", scriptPath, fragment)
		}
	}
}
