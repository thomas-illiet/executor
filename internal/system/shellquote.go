package system

import "strings"

// Single returns a single-quoted shell argument.
func Single(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// Join quotes and joins shell arguments with spaces.
func Join(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, Single(arg))
	}
	return strings.Join(quoted, " ")
}
