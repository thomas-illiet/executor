package app

import (
	"path/filepath"
	"strings"
)

// contains reports whether target is present in values.
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// hostLabel returns a display label for a host binding.
func hostLabel(ip string) string {
	if ip == "" {
		return "localhost"
	}
	return ip
}

// reachableLabel returns a short status label for connectivity.
func reachableLabel(ok bool) string {
	if ok {
		return "reachable"
	}
	return "unreachable"
}

// remotePath quotes a path for use in the remote shell.
func remotePath(path string) string {
	if !filepath.IsAbs(path) {
		return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
	}
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}
