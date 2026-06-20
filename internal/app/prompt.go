package app

import (
	"bufio"
	"fmt"
	"strings"
)

// confirm asks the user for a yes or no answer.
func (a App) confirm(prompt string) bool {
	fmt.Fprint(a.Out, prompt)
	scanner := bufio.NewScanner(a.In)
	if !scanner.Scan() {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes"
}
