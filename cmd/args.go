package cmd

// containerCommandArgs prefixes proxied arguments with a Podman subcommand name.
func containerCommandArgs(name string, args []string) []string {
	values := make([]string, 0, len(args)+1)
	values = append(values, name)
	values = append(values, args...)
	return values
}

// composeUpCommandArgs expands the executor shorthand into podman compose up.
func composeUpCommandArgs(args []string) []string {
	values := make([]string, 0, len(args)+2)
	values = append(values, "compose", "up")
	values = append(values, args...)
	return values
}
