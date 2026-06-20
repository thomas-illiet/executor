package app

import (
	"executor/internal/system"
	"executor/internal/vm"
)

// podmanCommand returns the remote Podman command with required environment.
func (a App) podmanCommand() []string {
	return append(podmanEnvCommand(), CommandName)
}

// podmanEnvCommand builds the env prefix for rootless Podman commands.
func podmanEnvCommand() []string {
	return []string{
		"env",
		"XDG_RUNTIME_DIR=" + vm.PodmanRuntimeDir,
		"REGISTRY_AUTH_FILE=" + vm.PodmanAuthFile,
		"TMPDIR=" + vm.PodmanRuntimeDir,
	}
}

// podmanShellEnv returns the shell form of the rootless Podman environment.
func podmanShellEnv() string {
	return "XDG_RUNTIME_DIR=" + system.Single(vm.PodmanRuntimeDir) +
		" REGISTRY_AUTH_FILE=" + system.Single(vm.PodmanAuthFile) +
		" TMPDIR=" + system.Single(vm.PodmanRuntimeDir)
}
