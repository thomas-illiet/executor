package container

import (
	"fmt"
	"strings"

	"executor/internal/system"
)

// CommandWithPrefix builds the Podman command with an optional environment prefix.
func CommandWithPrefix(podmanCommand []string, args []string) []string {
	command := make([]string, 0, len(podmanCommand)+len(args)+1)
	command = append(command, podmanCommand...)
	command = append(command, args...)
	if commandName(podmanCommand) == "podman" && len(args) > 0 && args[0] == "build" {
		hasLayers := false
		for _, arg := range args {
			if strings.HasPrefix(arg, "--layers") {
				hasLayers = true
				break
			}
		}
		if !hasLayers {
			command = append(command, "--layers=false")
		}
	}
	return command
}

// WantsTTY reports whether the container arguments request a TTY.
func WantsTTY(args []string) bool {
	if len(args) == 0 || (args[0] != "run" && args[0] != "exec") {
		return false
	}
	for _, arg := range args[1:] {
		if arg == "-t" || arg == "--tty" {
			return true
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg[1:], "t") {
			return true
		}
	}
	return false
}

// DetachedRunCommandWithPrefix builds a safer command for detached Podman runs.
func DetachedRunCommandWithPrefix(podmanCommand []string, args []string) (string, bool) {
	if len(args) == 0 || args[0] != "run" || !hasDetachFlag(args[1:]) || WantsTTY(args) {
		return "", false
	}
	createCommand := CommandWithPrefix(podmanCommand, detachedRunCreateArgs(args))
	create := system.Join(createCommand)
	startCommand := append(append([]string(nil), podmanCommand...), "start")
	start := system.Join(startCommand)
	return "id=$(" + create + "); " +
		"status=$?; " +
		"if [ \"$status\" -ne 0 ]; then exit \"$status\"; fi; " +
		start + " \"$id\" >/dev/null; " +
		"status=$?; " +
		"if [ \"$status\" -eq 0 ]; then printf '%s\\n' \"$id\"; fi; " +
		"exit \"$status\"", true
}

// commandName returns the final executable component of a command prefix.
func commandName(command []string) string {
	if len(command) == 0 {
		return ""
	}
	return command[len(command)-1]
}

// RewriteRunPublishArgs normalizes publish args and returns port mappings.
func RewriteRunPublishArgs(args []string, allocate func() (int, error)) ([]string, []Mapping, error) {
	rewritten := append([]string(nil), args...)
	var mappings []Mapping

	for i := 1; i < len(rewritten); i++ {
		arg := rewritten[i]
		if arg == "--" {
			break
		}
		if arg == "-p" || arg == "--publish" {
			if i+1 >= len(rewritten) {
				return nil, nil, fmt.Errorf("%s requires a port mapping", arg)
			}
			mapping, publishArg, err := ParsePublish(rewritten[i+1], allocate)
			if err != nil {
				return nil, nil, err
			}
			rewritten[i+1] = publishArg
			mappings = append(mappings, mapping)
			i++
			continue
		}
		if value, ok := strings.CutPrefix(arg, "--publish="); ok {
			mapping, publishArg, err := ParsePublish(value, allocate)
			if err != nil {
				return nil, nil, err
			}
			rewritten[i] = "--publish=" + publishArg
			mappings = append(mappings, mapping)
			continue
		}
		if value, ok := shortPublishValue(arg); ok {
			mapping, publishArg, err := ParsePublish(value, allocate)
			if err != nil {
				return nil, nil, err
			}
			rewritten[i] = "-p" + publishArg
			mappings = append(mappings, mapping)
			continue
		}
		if runOptionConsumesNext(arg) {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		break
	}
	return rewritten, mappings, nil
}

// Detaches reports whether the command starts work in detached mode.
func Detaches(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "run", "up":
		return hasDetachFlag(args[1:])
	case "compose":
		return HasUp(args) && hasDetachFlag(args[1:])
	default:
		return false
	}
}

// detachedRunCreateArgs converts detached run arguments into create arguments.
func detachedRunCreateArgs(args []string) []string {
	createArgs := []string{"create"}
	for _, arg := range args[1:] {
		if arg == "-d" || arg == "--detach" || arg == "--detach=true" {
			continue
		}
		if strings.HasPrefix(arg, "--detach=") {
			continue
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg[1:], "d") {
			flags := strings.ReplaceAll(arg[1:], "d", "")
			if flags != "" {
				createArgs = append(createArgs, "-"+flags)
			}
			continue
		}
		createArgs = append(createArgs, arg)
	}
	return createArgs
}

// shortPublishValue extracts the value from compact -p syntax.
func shortPublishValue(arg string) (string, bool) {
	if strings.HasPrefix(arg, "--") || !strings.HasPrefix(arg, "-p") || len(arg) <= 2 {
		return "", false
	}
	return strings.TrimPrefix(arg[2:], "="), true
}

// runOptionConsumesNext reports whether a run option has a next value.
func runOptionConsumesNext(arg string) bool {
	if strings.Contains(arg, "=") {
		return false
	}
	switch arg {
	case "-a", "--attach",
		"--add-host",
		"--annotation",
		"--blkio-weight",
		"--cap-add",
		"--cap-drop",
		"--cidfile",
		"--cgroup-parent",
		"--cpuset-cpus",
		"--cpus",
		"--device",
		"--dns",
		"--dns-option",
		"--dns-search",
		"-e", "--env",
		"--env-file",
		"--entrypoint",
		"--expose",
		"--group-add",
		"-h", "--hostname",
		"--ip",
		"--ip6",
		"-l", "--label",
		"--label-file",
		"--log-driver",
		"--log-opt",
		"--mac-address",
		"-m", "--memory",
		"--memory-reservation",
		"--memory-swap",
		"--mount",
		"--name",
		"--network",
		"--network-alias",
		"--platform",
		"--pull",
		"--restart",
		"--security-opt",
		"--shm-size",
		"--stop-signal",
		"--stop-timeout",
		"--storage-opt",
		"--sysctl",
		"--tmpfs",
		"-u", "--user",
		"--ulimit",
		"-v", "--volume",
		"--volume-driver",
		"--volumes-from",
		"-w", "--workdir":
		return true
	default:
		return false
	}
}

// hasDetachFlag checks the argument list for a true detach flag.
func hasDetachFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "-d" || arg == "--detach" || arg == "--detach=true" {
			return true
		}
		if strings.HasPrefix(arg, "--detach=") {
			return false
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg[1:], "d") {
			return true
		}
	}
	return false
}
